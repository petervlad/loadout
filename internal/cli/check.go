package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/mail"
)

// LoginReport says which login parts a configuration turns on.
type LoginReport struct {
	// Mail is the mailer's kind: "smtp", "log", "test" or "off".
	Mail string
	// Google is whether Google sign-in is configured.
	Google bool
}

// runServeCheck is `serve --check`: it reads the configuration the way
// serve does, builds the login parts, and prints one line per part. It
// prints a variable's name when it is unset, unknown or wrong (an unknown
// one is printed, then fails the check), and never a
// secret's value: the SMTP password, the Google secret and the Hetzner token
// are only ever "on" or "off".
func runServeCheck(ctx context.Context, d Deps, out io.Writer, envFile, dataDir string) error {
	if d.CheckLogin == nil {
		return errors.New("this build cannot check the configuration")
	}
	cfg, env, err := d.checkedConfig(envFile)
	if err != nil {
		return err
	}
	report := func(name, value string) { fmt.Fprintf(out, "%-12s%s\n", name+":", value) }
	if envFile != "" {
		report("env file", envFile)
	}
	login := cfg.Login
	if login.PublicURL == "" {
		report("login", "off (set LOADOUT_PUBLIC_URL to turn on web and CLI login)")
	} else {
		parts, err := d.CheckLogin(login)
		if err != nil {
			return err
		}
		report("public URL", login.PublicURL)
		report("login", loginMode(login))
		report("email", mailLine(login, parts.Mail))
		report("google", enabled(parts.Google))
		report("docs", docsLine(login))
	}
	line, err := d.firstAdminLine(ctx, cfg.Login, dataDir)
	if err != nil {
		return err
	}
	if line != "" {
		// The name is as wide as the column, so the space is added by hand.
		fmt.Fprintf(out, "first admin: %s\n", line)
	}
	report("proxies", proxiesLine(cfg.Proxies))
	report("server", serverLine(cfg.Stats))
	unknown := config.Unknown(env)
	if len(unknown) == 0 {
		report("unknown", "none")
		return nil
	}
	report("unknown", strings.Join(unknown, ", ")+" (loadout does not read these; check the spelling)")
	// A typo silently turns a feature off, so it fails the check: deploy.sh
	// stops before restarting the service.
	return fmt.Errorf("loadout does not read %s: fix the spelling or remove the line", strings.Join(unknown, ", "))
}

// checkedConfig is the configuration to check and the variables it came
// from: the named file (with only HOME taken from the process), or the
// process environment.
func (d Deps) checkedConfig(envFile string) (config.Config, map[string]string, error) {
	if envFile == "" {
		env := map[string]string{}
		if d.Environ != nil {
			for _, kv := range d.Environ() {
				if k, v, ok := strings.Cut(kv, "="); ok {
					env[k] = v
				}
			}
		}
		return d.Config, env, nil
	}
	f, err := os.Open(envFile)
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("read the environment file: %w", err)
	}
	defer f.Close()
	env, err := config.ParseEnvFile(f)
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("%s: %w", envFile, err)
	}
	cfg, err := config.Load(func(k string) string {
		if k == "HOME" {
			return d.Home
		}
		return env[k]
	})
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("%s: %w", envFile, err)
	}
	return cfg, env, nil
}

// firstAdminLine says whether serve would create the first admin, from a
// read-only look at the database; it never opens it for writing and never
// shows a password. Empty means this build does not say.
func (d Deps) firstAdminLine(ctx context.Context, l config.Login, dataDir string) (string, error) {
	if d.HasAccounts == nil {
		return "", nil
	}
	if !l.Simple || l.PublicURL == "" {
		return "off (only in LOADOUT_LOGIN=simple; otherwise use loadout admin grant)", nil
	}
	has, err := d.HasAccounts(ctx, dataDir)
	if err != nil {
		return "", fmt.Errorf("check the first admin: %w", err)
	}
	if has {
		return "exists", nil
	}
	return "will be created (empty database)", nil
}

func enabled(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func loginMode(l config.Login) string {
	if l.Demo {
		return "demo (one fixed password, fake data, localhost only; no Google, no sign-up, mail only logged)"
	}
	if l.Simple {
		return "simple (email and password only; no Google, no sign-up, no mail)"
	}
	return "email, Google and sign-up as configured below"
}

func mailLine(l config.Login, kind string) string {
	switch {
	case l.Simple:
		return "off (simple login sends no mail)"
	case kind == "smtp":
		port := l.SMTPPort
		if port == 0 {
			port = mail.DefaultPort
		}
		return "smtp via " + l.SMTPHost + ":" + strconv.Itoa(port)
	case kind == "log":
		return "log only (LOADOUT_DEV=1: mails are logged, never sent)"
	case kind == "off":
		return "off (set LOADOUT_SMTP_HOST, LOADOUT_SMTP_USER, LOADOUT_SMTP_PASSWORD and LOADOUT_MAIL_FROM to turn on)"
	}
	return kind
}

func docsLine(l config.Login) string {
	if l.DocsURL == "" {
		return "the project's site (LOADOUT_DOCS_URL is not set)"
	}
	return l.DocsURL
}

// serverLine states what the admins' server page will show. The Hetzner
// token is a secret, so only whether it is set is said.
func serverLine(s config.Stats) string {
	var facts []string
	for _, f := range []struct{ name, v string }{
		{"provider", s.Host.Provider},
		{"region", s.Host.Region},
		{"size", s.Host.Size},
		{"contact", s.Host.Contact},
		{"console", s.Host.Console},
	} {
		if f.v != "" {
			facts = append(facts, f.name)
		}
	}
	listed := "none"
	if len(facts) > 0 {
		listed = strings.Join(facts, ", ")
	}
	backups := "not set"
	if s.BackupDir != "" {
		backups = s.BackupDir
	}
	return fmt.Sprintf("Hetzner %s; host facts: %s; backup folder: %s", enabled(s.HetznerToken != ""), listed, backups)
}

// proxiesLine says where client addresses come from. The ranges are the
// operator's own settings, not secrets.
func proxiesLine(p config.Proxies) string {
	if p.Header == "" {
		return "a proxy on this machine through X-Real-IP (LOADOUT_TRUSTED_PROXIES is not set)"
	}
	ranges := make([]string, len(p.Trusted))
	for i, t := range p.Trusted {
		ranges[i] = t.String()
	}
	return "header " + p.Header + " from " + strings.Join(ranges, ", ")
}
