package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/server"
)

// errNoRegistry is returned when the program was wired without a registry.
var errNoRegistry = errors.New("this build cannot open a registry database")

// withRegistry opens the registry database in dataDir, runs fn and closes
// the database, reporting a failed close too: it can lose writes.
func withRegistry(ctx context.Context, d Deps, dataDir string, fn func(Registry) error) (err error) {
	if d.OpenRegistry == nil {
		return errNoRegistry
	}
	reg, closer, err := d.OpenRegistry(ctx, dataDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closer.Close()) }()
	return fn(reg)
}

func (d Deps) defaultDataDir() string { return filepath.Join(d.StateDir, "server") }

// firstAdminActive says whether serve may create the first admin: the
// address cannot receive mail, so only simple login (no mailed code) can
// sign it in.
func (d Deps) firstAdminActive() bool {
	return d.Config.Login.Simple && d.Config.Login.PublicURL != ""
}

// createFirstAdmin makes the first admin of an empty simple-login registry
// and shows a generated password once on stderr. The block is written
// straight to out, never through the logger, so it stays out of the log
// file and the access log. (Under systemd stderr is the journal: the
// operator is told to change the password and how.)
func (d Deps) createFirstAdmin(ctx context.Context, reg Registry, out io.Writer) error {
	if !d.firstAdminActive() || reg.FirstAdmin == nil {
		return nil
	}
	fa, err := reg.FirstAdmin(ctx, d.Config.Login.AdminPassword)
	if err != nil || !fa.Created {
		return err
	}
	d.Log.Info("first admin created", "email", fa.Email, "generated_password", fa.Password != "")
	if fa.Password == "" {
		return nil
	}
	fmt.Fprintf(out, `
==================================================================
 loadout: first admin created. Shown once, never stored in plain text.
   sign in:  %s
   password: %s
 Sign in at %s/auth/login, then change the password on this host:
   loadout user password --email %s
 (a service manager may keep this block in its log; the change makes it useless)
==================================================================

`, fa.Email, fa.Password, d.Config.Login.PublicURL, fa.Email)
	return nil
}

func newServeCmd(d Deps) *cobra.Command {
	var dataDir, addr, tlsCert, tlsKey, envFile string
	var check bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a registry your team can publish to and install from",
		Long: `Run a registry. On a LAN, start it on any always-on machine and give
teammates its URL; in the cloud, run it behind a TLS proxy (see deploy/).

With --check nothing is served: loadout reads the configuration the way serve
would, builds the login parts without the network, prints one line per part
and exits 0, or names the variable that is wrong or that loadout does not read. It never prints a secret.
Name an environment file with --env-file to check it before a restart.`,
		Example: `  loadout serve --addr :7420
  loadout serve --check --env-file /etc/loadout/loadout.env
  loadout token create --team payments --publish`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if check {
				return runServeCheck(cmd.Context(), d, cmd.OutOrStdout(), envFile, dataDir)
			}
			if envFile != "" {
				return errors.New("--env-file is only for --check; serve reads its environment")
			}
			if (tlsCert == "") != (tlsKey == "") {
				return fmt.Errorf("--tls-cert and --tls-key must be given together")
			}
			// A server should show its access log.
			if d.LogLevel != nil && d.LogLevel.Level() > slog.LevelInfo {
				d.LogLevel.Set(slog.LevelInfo)
			}
			if tlsCert == "" && !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "localhost:") {
				d.Log.Warn("serving plain HTTP: tokens travel unencrypted, use only on a trusted network or put TLS in front", "addr", addr)
			}
			return withRegistry(cmd.Context(), d, dataDir, func(reg Registry) error {
				d.Log.Info("registry data", "dir", dataDir) // the server logs "listening" once bound
				if err := d.createFirstAdmin(cmd.Context(), reg, cmd.ErrOrStderr()); err != nil {
					return err
				}
				h, err := reg.NewHandler()
				if err != nil {
					return err
				}
				return server.Run(cmd.Context(), addr, h, d.Log, tlsCert, tlsKey)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&dataDir, "data", d.defaultDataDir(), "folder for the registry database")
	f.StringVar(&addr, "addr", ":7420", "address to listen on")
	f.StringVar(&tlsCert, "tls-cert", "", "TLS certificate file (PEM)")
	f.StringVar(&tlsKey, "tls-key", "", "TLS private key file (PEM)")
	f.BoolVar(&check, "check", false, "check the configuration and exit, without serving")
	f.StringVar(&envFile, "env-file", "", "with --check, read this systemd environment file instead of the environment")
	return quiet(cmd)
}
