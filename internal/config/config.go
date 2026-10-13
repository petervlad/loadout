// Package config resolves loadout's settings from the environment once, at
// startup, into a typed Config that is passed down explicitly.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/petervlad/loadout/internal/ratelimit"
)

// Config holds resolved settings.
type Config struct {
	// Home is the user's home directory, the root for user-scope installs.
	Home string
	// StateDir holds loadout's own files, such as the lock.
	StateDir string
	LogLevel slog.Level
	// PushoverToken and PushoverUser enable Pushover notifications for
	// updates sync finds. Both come only from the environment.
	PushoverToken string
	PushoverUser  string
	// QuietUpdates is LOADOUT_UPDATE_NOTICE=off: say that a newer loadout
	// is out only in status, notices and version. A registry that needs a
	// newer loadout is still said after every command.
	QuietUpdates bool
	// Login configures web and CLI login on a registry (serve only).
	Login Login
	// Stats configures the server page only admins see (serve only).
	Stats Stats
	// Proxies says which peers may name the client's address (serve only).
	Proxies Proxies
}

// Proxies is LOADOUT_TRUSTED_PROXIES and LOADOUT_CLIENT_IP_HEADER: for a
// proxy that is not on the registry's machine (a container platform, a
// tunnel), the ranges it connects from and the header it puts the client's
// address in. Both empty is the default: only a proxy on this machine, through
// X-Real-IP.
type Proxies struct {
	Header  string
	Trusted []netip.Prefix
}

// Rule is the proxy rule these settings describe. Load has already checked
// it, so an error here means a Proxies built by hand.
func (p Proxies) Rule() (ratelimit.Proxies, error) { return ratelimit.NewProxies(p.Header, p.Trusted) }

// Stats configures the admins' server page. Every part is optional.
type Stats struct {
	// HetznerToken is a Hetzner Cloud API token of the server's project;
	// read-only is enough. Empty leaves the Hetzner part out. It comes
	// only from the environment and is never logged.
	HetznerToken string
	// HetznerServer is the server's id or name; empty finds it by this
	// machine's public IPv4.
	HetznerServer string
	// HetznerAPI replaces the API URL for tests and previews; only plain
	// http on a loopback address is accepted.
	HetznerAPI string
	// BackupDir is where loadout-backup.sh writes its archives.
	BackupDir string
	// Host is what the operator states about the machine.
	Host Host
}

// Host describes where the registry runs, as the operator states it, for
// the admins' server page. Every field is optional plain text.
type Host struct {
	Provider string // LOADOUT_HOST_PROVIDER, for example "Acme Cloud"
	Region   string // LOADOUT_HOST_REGION, a region, project or data centre
	Size     string // LOADOUT_HOST_SIZE, for example "4 vCPU, 16 GB memory"
	Contact  string // LOADOUT_HOST_CONTACT, a team or an address
	Console  string // LOADOUT_HOST_CONSOLE, an https link to the host's console
}

// Login holds the registry's login settings. Every part is optional: without
// PublicURL there is no web login at all, without Google settings no
// Google button, without SMTP no email sign-up.
type Login struct {
	// PublicURL is the registry's address in a browser, for example
	// https://registry.example.com.
	PublicURL          string
	GoogleClientID     string
	GoogleClientSecret string
	SMTPHost           string
	// SMTPPort is 587 unless set.
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	// MailFrom is the sender, for example "loadout <login@example.com>".
	MailFrom string
	// Dev logs that a mail was sent instead of sending it when no SMTP
	// server is set.
	Dev bool
	// Simple is LOADOUT_LOGIN=simple: email and password only, a right
	// password starts the session, no Google, no sign-up, no mail.
	Simple bool
	// Demo is LOADOUT_DEMO=1: a registry with fake data and one shared,
	// public password. It implies Simple and Dev, and Load refuses any
	// address but a localhost one, Google and SMTP.
	Demo bool
	// AdminPassword is LOADOUT_ADMIN_PASSWORD: the password of the first
	// admin that serve creates in an empty simple-login registry. Empty
	// means serve generates one and prints it once. It is a secret.
	AdminPassword string
	// DocsURL is LOADOUT_DOCS_URL: where the pages' Docs and What's new
	// links go, always ending in a slash. Empty is the project's site.
	DocsURL string
}

// Environment variables loadout reads.
const (
	EnvStateDir      = "LOADOUT_STATE_DIR"
	EnvLog           = "LOADOUT_LOG"
	EnvPushoverToken = "LOADOUT_PUSHOVER_TOKEN"
	EnvPushoverUser  = "LOADOUT_PUSHOVER_USER"
	EnvUpdateNotice  = "LOADOUT_UPDATE_NOTICE"

	EnvPublicURL          = "LOADOUT_PUBLIC_URL"
	EnvGoogleClientID     = "LOADOUT_GOOGLE_CLIENT_ID"
	EnvGoogleClientSecret = "LOADOUT_GOOGLE_CLIENT_SECRET"
	EnvSMTPHost           = "LOADOUT_SMTP_HOST"
	EnvSMTPPort           = "LOADOUT_SMTP_PORT"
	EnvSMTPUser           = "LOADOUT_SMTP_USER"
	EnvSMTPPassword       = "LOADOUT_SMTP_PASSWORD" //nolint:gosec // G101: a variable name, not a password
	EnvMailFrom           = "LOADOUT_MAIL_FROM"
	EnvDev                = "LOADOUT_DEV"
	EnvLogin              = "LOADOUT_LOGIN"
	EnvDemo               = "LOADOUT_DEMO"
	EnvDocsURL            = "LOADOUT_DOCS_URL"
	EnvAdminPassword      = "LOADOUT_ADMIN_PASSWORD" //nolint:gosec // G101: a variable name, not a password
	EnvTrustedProxies     = "LOADOUT_TRUSTED_PROXIES"
	EnvClientIPHeader     = "LOADOUT_CLIENT_IP_HEADER"

	EnvHetznerToken  = "LOADOUT_HETZNER_TOKEN" //nolint:gosec // G101: a variable name, not a token
	EnvHetznerServer = "LOADOUT_HETZNER_SERVER"
	EnvHetznerAPI    = "LOADOUT_HETZNER_API"
	EnvBackupDir     = "LOADOUT_BACKUP_DIR"

	EnvHostProvider = "LOADOUT_HOST_PROVIDER"
	EnvHostRegion   = "LOADOUT_HOST_REGION"
	EnvHostSize     = "LOADOUT_HOST_SIZE"
	EnvHostContact  = "LOADOUT_HOST_CONTACT"
	EnvHostConsole  = "LOADOUT_HOST_CONSOLE"
)

// Load builds a Config from environment lookups. getenv is os.Getenv in
// production and a map lookup in tests.
func Load(getenv func(string) string) (Config, error) {
	home := getenv("HOME")
	if home == "" {
		home = getenv("USERPROFILE") // Windows
	}
	if home == "" {
		return Config{}, errors.New("cannot find the home directory: HOME is not set")
	}
	if !filepath.IsAbs(home) {
		return Config{}, fmt.Errorf("home directory %q is not an absolute path", home)
	}

	state := getenv(EnvStateDir)
	if state == "" {
		state = filepath.Join(home, ".loadout")
	}

	level := slog.LevelWarn
	if v := getenv(EnvLog); v != "" {
		if err := level.UnmarshalText([]byte(strings.ToUpper(v))); err != nil {
			return Config{}, fmt.Errorf("%s=%q: want debug, info, warn or error", EnvLog, v)
		}
	}
	login, err := loadLogin(getenv)
	if err != nil {
		return Config{}, err
	}
	host, err := loadHost(getenv)
	if err != nil {
		return Config{}, err
	}
	proxies, err := loadProxies(getenv)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Home:          home,
		StateDir:      state,
		LogLevel:      level,
		PushoverToken: getenv(EnvPushoverToken),
		PushoverUser:  getenv(EnvPushoverUser),
		QuietUpdates:  strings.EqualFold(strings.TrimSpace(getenv(EnvUpdateNotice)), "off"),
		Login:         login,
		Proxies:       proxies,
		Stats: Stats{
			HetznerToken:  strings.TrimSpace(getenv(EnvHetznerToken)),
			HetznerServer: strings.TrimSpace(getenv(EnvHetznerServer)),
			HetznerAPI:    strings.TrimSpace(getenv(EnvHetznerAPI)),
			BackupDir:     strings.TrimSpace(getenv(EnvBackupDir)),
			Host:          host,
		},
	}, nil
}

// loadProxies reads the trusted proxies and the header they set, and checks
// them with the same rule serve applies, so a mistake stops the start (and
// serve --check) instead of letting clients pick their rate-limit key.
func loadProxies(getenv func(string) string) (Proxies, error) {
	p := Proxies{Header: strings.TrimSpace(getenv(EnvClientIPHeader))}
	if raw := strings.TrimSpace(getenv(EnvTrustedProxies)); raw != "" {
		for item := range strings.SplitSeq(raw, ",") {
			prefix, err := parseProxy(strings.TrimSpace(item))
			if err != nil {
				return Proxies{}, fmt.Errorf("%s: %w", EnvTrustedProxies, err)
			}
			p.Trusted = append(p.Trusted, prefix)
		}
	}
	if _, err := p.Rule(); err != nil {
		return Proxies{}, fmt.Errorf("%s and %s: %w", EnvTrustedProxies, EnvClientIPHeader, err)
	}
	return p, nil
}

// parseProxy reads one address or range; an address alone is a range of one.
func parseProxy(item string) (netip.Prefix, error) {
	if item == "" {
		return netip.Prefix{}, errors.New("an empty entry: separate ranges with one comma, for example 203.0.113.0/24,2001:db8::/32")
	}
	if p, err := netip.ParsePrefix(item); err == nil {
		return p, nil
	}
	a, err := netip.ParseAddr(item)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q is not an address or a range such as 203.0.113.0/24", item)
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// maxHostValue bounds each stated host fact; the page prints it as is.
const maxHostValue = 200

// loadHost reads and checks the stated host facts, so a typo stops serve
// at startup and never reaches the page.
func loadHost(getenv func(string) string) (Host, error) {
	var h Host
	for _, f := range []struct {
		env string
		dst *string
	}{
		{EnvHostProvider, &h.Provider},
		{EnvHostRegion, &h.Region},
		{EnvHostSize, &h.Size},
		{EnvHostContact, &h.Contact},
		{EnvHostConsole, &h.Console},
	} {
		v := strings.TrimSpace(getenv(f.env))
		if len(v) > maxHostValue {
			return Host{}, fmt.Errorf("%s is longer than %d bytes: shorten it", f.env, maxHostValue)
		}
		if strings.ContainsFunc(v, unicode.IsControl) {
			return Host{}, fmt.Errorf("%s has a control character: write plain text on one line", f.env)
		}
		*f.dst = v
	}
	if h.Console != "" {
		u, err := url.Parse(h.Console)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return Host{}, fmt.Errorf("%s=%q: want an absolute https link", EnvHostConsole, h.Console)
		}
	}
	return h, nil
}

func loadLogin(getenv func(string) string) (Login, error) {
	l := Login{
		PublicURL:          strings.TrimRight(strings.TrimSpace(getenv(EnvPublicURL)), "/"),
		GoogleClientID:     strings.TrimSpace(getenv(EnvGoogleClientID)),
		GoogleClientSecret: strings.TrimSpace(getenv(EnvGoogleClientSecret)),
		SMTPHost:           strings.TrimSpace(getenv(EnvSMTPHost)),
		SMTPUser:           getenv(EnvSMTPUser),
		SMTPPassword:       getenv(EnvSMTPPassword),
		MailFrom:           strings.TrimSpace(getenv(EnvMailFrom)),
		Dev:                getenv(EnvDev) == "1",
		AdminPassword:      getenv(EnvAdminPassword),
	}
	docs, err := parseDocsURL(getenv(EnvDocsURL))
	if err != nil {
		return Login{}, err
	}
	l.DocsURL = docs
	switch v := strings.ToLower(strings.TrimSpace(getenv(EnvLogin))); v {
	case "":
	case "simple":
		l.Simple = true
	default:
		return Login{}, fmt.Errorf("%s=%q: want \"simple\" or unset", EnvLogin, v)
	}
	if v := strings.TrimSpace(getenv(EnvSMTPPort)); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port < 1 || port > 65535 {
			return Login{}, fmt.Errorf("%s=%q: want a port number", EnvSMTPPort, v)
		}
		l.SMTPPort = port
	}
	if getenv(EnvDemo) == "1" {
		if err := l.demo(); err != nil {
			return Login{}, err
		}
	}
	return l, nil
}

// demo turns the demo on. Its password is printed on the sign-in page, so
// it must never meet a registry anyone else can reach: only a localhost
// address is accepted, and nothing that signs in another way or sends mail.
func (l *Login) demo() error {
	u, err := url.Parse(l.PublicURL)
	if err != nil || u.Scheme != "http" || !isLocalhost(u.Hostname()) {
		return fmt.Errorf("%s=1 prints its password on the sign-in page, so %s must be a localhost address such as http://localhost:8080", EnvDemo, EnvPublicURL)
	}
	if l.GoogleClientID != "" || l.GoogleClientSecret != "" || l.SMTPHost != "" {
		return fmt.Errorf("%s=1 signs in with its own password and only logs mail: unset the Google and SMTP settings", EnvDemo)
	}
	l.Demo, l.Simple, l.Dev = true, true, true
	return nil
}

func isLocalhost(host string) bool {
	if host == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsLoopback()
}

// parseDocsURL checks LOADOUT_DOCS_URL: an absolute http or https address,
// with no query or fragment (pages add their own), made to end in a slash so
// a page name can follow. Empty stays empty.
func parseDocsURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%s: want an absolute http or https address without a query or fragment, for example https://docs.example.com/loadout/", EnvDocsURL)
	}
	return strings.TrimRight(raw, "/") + "/", nil
}

// LogPath is where the JSON debug log lives.
func (c Config) LogPath() string { return filepath.Join(c.StateDir, "loadout.log") }

// LockPath is where the install lock lives.
func (c Config) LockPath() string { return filepath.Join(c.StateDir, "lock.yaml") }
