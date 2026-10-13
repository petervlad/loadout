package main

import (
	"errors"
	"log/slog"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/browser"
	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/builtin"
	"github.com/petervlad/loadout/internal/cli"
	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/ratelimit"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/server"
	"github.com/petervlad/loadout/internal/snapshot"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
	"github.com/petervlad/loadout/internal/web"
)

// Rate limits of the login endpoints, per client address and per email
// typed into a form.
const (
	webIPBurst    = 20
	webIPEvery    = 15 * time.Second
	webEmailBurst = 5
	webEmailEvery = 2 * time.Minute
	deviceIPBurst = 30
	deviceIPEvery = 2 * time.Second
	smtpTimeout   = 30 * time.Second
)

// loginOptions returns the server options for web and CLI login, or none
// when LOADOUT_PUBLIC_URL is not set: then the registry serves exactly the
// token API. It logs which parts are on, never a secret. A mail queue it
// starts is added to closers. The pages include the admins' server page.
func loginOptions(log *slog.Logger, login config.Login, stats statsSetup, testMailer account.Mailer, store *sqlitestore.Store, reg *registry.Service, props *registry.ProposalService, dels *registry.DeletionService, snaps account.Snapshots, wikis *registry.WikiService, roles *account.Roles, closers *closeList) ([]server.Option, error) {
	if login.PublicURL == "" {
		log.Info("login is off; set LOADOUT_PUBLIC_URL to turn on web and CLI login")
		return nil, nil
	}
	parts, err := buildLoginParts(log, login, testMailer)
	if err != nil {
		return nil, err
	}
	mailer, kind, google := parts.mailer, parts.kind, parts.google
	var (
		queued account.Mailer
		queue  *mail.Queue
	)
	if mailer != nil {
		queue = mail.NewQueue(mailer, log)
		closers.add(queue)
		queued = queue
	}
	collector, err := serverStats(log, stats.Stats, login.PublicURL, stats.DataDir, stats.Build.Raw(), store, queue)
	if err != nil {
		return nil, err
	}
	// The skills pages show the text this build installs, from the same
	// embedded files.
	skills, err := builtin.Packs(stats.Build.Raw())
	if err != nil {
		return nil, err
	}
	accounts := account.NewService(store, reg, roles, device.New(time.Now), account.Options{
		PublicURL: login.PublicURL,
		Mailer:    queued,
		Wiki:      wikis,
		Snapshots: snaps,
		Deletions: dels,
		Now:       time.Now,
		Log:       log,
	})
	// Deleting on the web, directly or through a request, needs the backup
	// folder: both routes take a copy first. Without one the pages leave
	// the whole feature out.
	var (
		deletes  web.Deletes
		requests web.DeleteRequests
	)
	if snaps != nil {
		deletes, requests = accounts, dels
	}
	pages, err := web.New(web.Config{
		PublicURL: login.PublicURL,
		DocsURL:   login.DocsURL,
		Simple:    login.Simple,
		Google:    google,
		PerIP:     ratelimit.New(webIPBurst, webIPEvery, time.Now),
		PerEmail:  ratelimit.New(webEmailBurst, webEmailEvery, time.Now),
		Log:       log,
		UI: &web.UI{
			Registry:  reg,
			Proposals: props,
			Places:    roles,
			Teams:     accounts,
			Tools:     []web.ModelTool{claudecode.New(), copilot.New()},
			Server:    collector,
			Now:       time.Now,
			Skills:    &web.BuiltinSkills{Packs: skills, Version: stats.Build.String()},
			Wiki:      accounts,
			Deletes:   deletes,
			Requests:  requests,
		},
	}, accounts)
	if err != nil {
		return nil, err
	}
	if login.Simple {
		log.Info("simple login is on: sign-in form only, no Google, no sign-up, no mail", "public_url", login.PublicURL)
	}
	log.Info("login is on", "public_url", login.PublicURL, "google", google != nil, "email", kind)
	return []server.Option{
		server.WithDeviceFlow(accounts, ratelimit.New(deviceIPBurst, deviceIPEvery, time.Now)),
		server.WithPages(pages),
		server.WithManagement(accounts),
		server.WithWiki(accounts),
	}, nil
}

// webSnapshots returns the copier that comes before a package is deleted on
// the web, or nil, which turns web deletes off, when LOADOUT_BACKUP_DIR is
// not set or is not a usable folder. The nil is a plain nil interface, not a
// nil *snapshot.Dir inside one.
func webSnapshots(log *slog.Logger, dir string, store *sqlitestore.Store) account.Snapshots {
	if dir == "" {
		log.Info("deleting packages on the web is off; set LOADOUT_BACKUP_DIR to turn it on")
		return nil
	}
	d, err := snapshot.New(dir, store, time.Now)
	if err != nil {
		log.Warn("deleting packages on the web is off", "error", err)
		return nil
	}
	log.Info("deleting packages on the web is on", "backups", dir)
	return d
}

// statsSetup is what the server page and the skills pages need beyond the
// stores.
type statsSetup struct {
	config.Stats
	DataDir string
	// Build is this binary, whose version the pages show.
	Build buildinfo.Build
}

// loginParts are the pieces of login that depend on configuration alone.
type loginParts struct {
	mailer account.Mailer // nil when email is off
	kind   string         // "smtp", "log", "test" or "off"
	google *web.Google    // nil when Google is not configured
}

// buildLoginParts checks the public URL and builds the mailer and Google
// client of one configuration, without the network. `serve`, `serve
// --check` and `mail test` all come here, so the check cannot disagree with
// the server.
func buildLoginParts(log *slog.Logger, login config.Login, testMailer account.Mailer) (loginParts, error) {
	if _, _, err := web.ParsePublicURL(login.PublicURL); err != nil {
		return loginParts{}, err
	}
	// Simple login sends no mail and offers no Google, whatever else is set.
	parts := loginParts{kind: "off"}
	if login.Simple {
		return parts, nil
	}
	var err error
	if parts.mailer, parts.kind, err = loginMailer(log, login, testMailer); err != nil {
		return loginParts{}, err
	}
	if parts.google, err = loginGoogle(login); err != nil {
		return loginParts{}, err
	}
	return parts, nil
}

// mailerFor is the cli's way to build the registry's mailer for `mail test`.
func mailerFor(log *slog.Logger, testMailer account.Mailer) func(config.Login) (cli.MailSender, string, error) {
	return func(login config.Login) (cli.MailSender, string, error) {
		parts, err := buildLoginParts(log, login, testMailer)
		if err != nil || parts.mailer == nil {
			return nil, parts.kind, err
		}
		return parts.mailer, parts.kind, nil
	}
}

// checkLoginFor is the cli's way to build the login parts for `serve
// --check`. Its log is silent: the check prints its own lines.
func checkLoginFor(testMailer account.Mailer) func(config.Login) (cli.LoginReport, error) {
	log := slog.New(slog.DiscardHandler)
	return func(login config.Login) (cli.LoginReport, error) {
		parts, err := buildLoginParts(log, login, testMailer)
		if err != nil {
			return cli.LoginReport{}, err
		}
		return cli.LoginReport{Mail: parts.kind, Google: parts.google != nil}, nil
	}
}

// loginMailer picks how login mails go out and names it for the log: a
// test mailer, SMTP, the log mailer with LOADOUT_DEV=1, or none (email
// sign-up and sign-in off).
func loginMailer(log *slog.Logger, login config.Login, testMailer account.Mailer) (account.Mailer, string, error) {
	switch {
	case testMailer != nil:
		return testMailer, "test", nil
	case login.SMTPHost != "":
		if login.MailFrom == "" {
			return nil, "", errors.New(`LOADOUT_SMTP_HOST is set but LOADOUT_MAIL_FROM is not; set it, for example to "loadout <login@example.com>"`)
		}
		m, err := mail.NewSMTP(mail.SMTPConfig{
			Host:     login.SMTPHost,
			Port:     login.SMTPPort,
			Username: login.SMTPUser,
			Password: login.SMTPPassword,
			From:     login.MailFrom,
			Timeout:  smtpTimeout,
		})
		if err != nil {
			return nil, "", err
		}
		return m, "smtp", nil
	case login.Dev:
		log.Warn("LOADOUT_DEV=1: login mails are only logged, never sent")
		return mail.NewLog(log), "log", nil
	}
	log.Info("email sign-up and sign-in are off; set LOADOUT_SMTP_HOST, LOADOUT_SMTP_USER, LOADOUT_SMTP_PASSWORD and LOADOUT_MAIL_FROM to turn them on")
	return nil, "off", nil
}

// loginGoogle returns Google sign-in, or nil when it is not configured.
func loginGoogle(login config.Login) (*web.Google, error) {
	if login.GoogleClientID == "" && login.GoogleClientSecret == "" {
		return nil, nil
	}
	if login.GoogleClientID == "" || login.GoogleClientSecret == "" {
		return nil, errors.New("set both LOADOUT_GOOGLE_CLIENT_ID and LOADOUT_GOOGLE_CLIENT_SECRET, or neither")
	}
	return web.NewGoogle(web.GoogleConfig{
		ClientID:     login.GoogleClientID,
		ClientSecret: login.GoogleClientSecret,
		RedirectURL:  login.PublicURL + "/auth/google/callback",
	})
}

// browserFor returns the test browser, or the system one.
func browserFor(ext externals, log *slog.Logger) cli.Browser {
	if ext.browser != nil {
		return ext.browser
	}
	return browser.New(log)
}
