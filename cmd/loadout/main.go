// Command loadout shares a team's AI agents across every AI tool.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"syscall"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/builtin"
	"github.com/petervlad/loadout/internal/cli"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/cliupdate"
	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/detect"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/hook"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/logging"
	"github.com/petervlad/loadout/internal/mcpbridge"
	"github.com/petervlad/loadout/internal/mcpsetup"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/notice"
	"github.com/petervlad/loadout/internal/notify/pushover"
	"github.com/petervlad/loadout/internal/rebase"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/release"
	"github.com/petervlad/loadout/internal/remote"
	"github.com/petervlad/loadout/internal/review"
	"github.com/petervlad/loadout/internal/reviewplan"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/server"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
	"github.com/petervlad/loadout/internal/update"
	"github.com/petervlad/loadout/internal/wiki"
)

// version is set at build time with -ldflags "-X main.version=...";
// buildinfo.Current falls back to what Go stamped into the binary.
var version = "dev"

// packaged is "true" in the release pipeline's builds (.goreleaser.yaml),
// which are updated by downloading the next release, not from a clone.
var packaged = ""

// installMethod is "registry" in the CLIs deploy.sh builds for a registry to
// hand out (-X main.installMethod=registry), so they update from it.
var installMethod = ""

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv, workDir())
	stop()
	os.Exit(code)
}

// externals are the parts of the program that reach outside this machine
// or wait on a person. Tests replace them; nil fields use the real thing.
type externals struct {
	// runners start the reviewer's own AI tools for `review compare`,
	// keyed by target name.
	runners map[string]review.Runner
	// mailer, if set, sends the registry's login mails instead of SMTP.
	mailer account.Mailer
	// browser, if set, opens the page of `loadout login`.
	browser cli.Browser
	// sleep, if set, waits between the polls of `loadout login`.
	sleep func(context.Context, time.Duration) error
	// executable and lookPath, if set, say where the running loadout is
	// and what the PATH finds, for the command the session hooks run.
	executable func() (string, error)
	lookPath   func(string) (string, error)
	// version, if set, is the CLI's version instead of the one this binary
	// was built with, and Go's build information is ignored.
	version string
	// environ, if set, lists the process environment for `serve --check`.
	environ func() []string
	// stderrTerminal, if set, says whether stderr is a terminal, which
	// decides who reads the line about a newer loadout.
	stderrTerminal func() bool
}

// run wires the program with the real AI tool runners and executes one
// command.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, wd string) int {
	return runWith(ctx, args, stdin, stdout, stderr, getenv, wd, realExternals())
}

// realExternals are the real AI tool runners; everything else is the
// real thing by default.
func realExternals() externals {
	env := os.Environ()
	return externals{
		runners: map[string]review.Runner{
			claudecode.Name: review.NewClaudeCode("claude", env),
			copilot.Name:    review.NewCopilotCLI("copilot", env),
		},
		environ: os.Environ,
	}
}

// runWith wires the program and executes one command. It is the only place
// that turns an error into an exit code.
func runWith(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, wd string, ext externals) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	log, level, logFile := logging.New(stderr, cfg.LogLevel, cfg.LogPath())
	defer logFile.Close()

	build := currentBuild(ext)
	start := time.Now()
	log.Debug("run started", "version", build.Raw(), "args", args, "workdir", wd)

	// Without a channel the sync table on the terminal is the notification,
	// and the notifier stays nil so a channel set up later hears the news.
	var (
		notifier update.Notifier
		pusher   cliupdate.Pusher
	)
	if cfg.PushoverToken != "" || cfg.PushoverUser != "" {
		n, err := pushover.New(cfg.PushoverToken, cfg.PushoverUser)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		notifier, pusher = n, n
	}

	claudeAdapter, copilotAdapter := claudecode.New(), copilot.New()
	installer := install.New(lock.NewStore(cfg.LockPath(), cfg.Home), log, claudeAdapter, copilotAdapter)
	var tools []review.Tool
	for _, t := range []review.Tool{
		{Name: claudecode.Name, Renderer: claudeAdapter},
		{Name: copilot.Name, Renderer: copilotAdapter},
	} {
		if runner, ok := ext.runners[t.Name]; ok {
			t.Runner = runner
			tools = append(tools, t)
		}
	}
	// What sync learned between runs, for status and the session hooks.
	memory := syncstate.NewStore(filepath.Join(cfg.StateDir, "syncstate.yaml"), cfg.Home)
	releases := release.New(installer)
	detector := detect.New(claudeAdapter, copilotAdapter)
	builtins, err := builtin.New(installer, detector, builtin.Options{
		Version:   build.Raw(),
		StatePath: filepath.Join(cfg.StateDir, "builtin.yaml"),
		Home:      cfg.Home,
		Memory:    memory,
		Log:       log,
	})
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	executable, lookPath := os.Executable, exec.LookPath
	if ext.executable != nil {
		executable = ext.executable
	}
	if ext.lookPath != nil {
		lookPath = ext.lookPath
	}
	hooks, err := hook.New(hook.Options{
		Home:       cfg.Home,
		WishPath:   filepath.Join(cfg.StateDir, "hooks.yaml"),
		Executable: executable,
		LookPath:   lookPath,
		Windows:    runtime.GOOS == "windows",
		Log:        log,
	}, claudeAdapter, copilotAdapter)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	remotes := remote.NewStore(cfg.StateDir, getenv)
	memorySetup, err := mcpsetup.New(mcpsetup.Options{
		Home:       cfg.Home,
		WishPath:   filepath.Join(cfg.StateDir, "mcp.yaml"),
		Executable: executable,
		LookPath:   lookPath,
		Tokens: memoryTokens{
			remotes: remotes,
			newClient: func(r remote.Remote, loginToken string) (*client.Client, error) {
				opts := []client.Option{client.WithUserAgent("loadout/" + build.Raw())}
				if r.CAFile != "" {
					opts = append(opts, client.WithCAFile(r.CAFile))
				}
				return client.New(r.URL, loginToken, opts...)
			},
			label: cli.DefaultTokenLabel(),
			now:   time.Now,
		},
		Log: log,
	}, claudeAdapter, copilotAdapter)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	rebaseRecords := rebase.NewRecords(cfg.StateDir)
	// What registries say about loadout releases, kept between runs; built
	// from a file and this build only, so it reaches no registry itself.
	updates := cliupdate.New(cliupdate.Options{
		Store:  cliupdate.NewStore(filepath.Join(cfg.StateDir, "cliupdate.yaml")),
		Build:  build,
		Method: buildinfo.MethodOf(buildinfo.Install{Build: build, Executable: resolvedExecutable(executable), Packaged: packaged == "true", Channel: installMethod}),
		Log:    log,
		Quiet:  cfg.QuietUpdates,
	})
	deps := cli.Deps{
		Installer: installer,
		Releases:  releases,
		Detector:  detector,
		Builtins:  builtins,
		Remotes:   remotes,
		Log:       log,
		StateDir:  cfg.StateDir,
		Home:      cfg.Home,
		WorkDir:   wd,
		Version:   build.Raw(),
		Build:     build,
		LogLevel:  level,
		Comparer:  review.NewComparer(log, time.Now, "", tools...),
		NewPlanner: func(reg reviewplan.Registry) cli.Planner {
			return reviewplan.NewPlanner(reg, releases)
		},
		NewSyncer: func(connect update.Connector) cli.Syncer {
			return update.New(installer, connect, notifier, memory, log)
		},
		Bridge: func(c mcpbridge.Config) (cli.BridgeRunner, error) {
			b, err := mcpbridge.New(c)
			if err != nil {
				return nil, err
			}
			return b, nil
		},
		OpenRegistry: registryOpener(log, cfg.Login, cfg.Stats, cfg.Proxies, build, ext.mailer),
		Config:       cfg,
		Environ:      ext.environ,
		NewMailer:    mailerFor(log, ext.mailer),
		CheckLogin:   checkLoginFor(ext.mailer),
		HasAccounts: func(ctx context.Context, dataDir string) (bool, error) {
			return sqlitestore.HasAccounts(ctx, filepath.Join(dataDir, dbFile))
		},
		Browser: browserFor(ext, log),
		Sleep:   ext.sleep,
		Wiki:    wiki.NewSyncer(wiki.NewCheckouts(cfg.StateDir)),
		// Built from local files only: notices never reach a registry.
		Notices:     notice.New(memory, installer),
		Hooks:       hooks,
		MemorySetup: memorySetup,
		Updates:     updates,
		BackupDir:   cfg.Stats.BackupDir,
		Pusher:      pusher,
		NewRebaser: func(reg rebase.Registry, url string) cli.Rebaser {
			return rebase.New(reg, rebaseRecords, url, time.Now)
		},
	}
	root := cli.NewRoot(deps)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(stdin)

	cmd, err := root.ExecuteContextC(ctx)
	// The error is shown to the user below; the file gets the record at
	// debug level so it is not printed twice on the terminal.
	log.Debug("run finished", "ok", err == nil, "err", err, "duration_ms", time.Since(start).Milliseconds())
	code := 0
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "interrupted")
			return 130 // the shell convention for Ctrl-C
		}
		fmt.Fprintln(stderr, "error:", err)
		code = exitCode(err)
	}
	// Last, after an error too: "this loadout is too old" explains one.
	cli.AfterRun(ctx, deps, cmd, stderr, cliupdate.Detect(getenv, isTerminal(stderr, ext)))
	return code
}

// isTerminal reports whether stderr is a terminal, or what a test says.
// A character device is a terminal, the Windows console included.
func isTerminal(stderr io.Writer, ext externals) bool {
	if ext.stderrTerminal != nil {
		return ext.stderrTerminal()
	}
	f, ok := stderr.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// exitCode is 1 for an error, or the code the error asks for: sync exits
// 2 while an update is held back, so cron can tell it from a failure.
func exitCode(err error) int {
	if ec, ok := errors.AsType[interface {
		error
		ExitCode() int
	}](err); ok {
		return ec.ExitCode()
	}
	return 1
}

// registryOpener returns the cli's way to open the registry database in a
// data folder: SQLite, with the registry and account services on top, and
// the HTTP handler built on demand for serve.
func registryOpener(log *slog.Logger, login config.Login, stats config.Stats, proxies config.Proxies, build buildinfo.Build, testMailer account.Mailer) func(context.Context, string) (cli.Registry, io.Closer, error) {
	return func(ctx context.Context, dataDir string) (cli.Registry, io.Closer, error) {
		store, err := sqlitestore.Open(ctx, filepath.Join(dataDir, dbFile), log)
		if err != nil {
			return cli.Registry{}, nil, err
		}
		closers := &closeList{store}
		// Team memory has its own file, so a bug or a migration there cannot
		// damage packages.
		mem, err := sqlitestore.OpenMemory(ctx, filepath.Join(dataDir, memoryDBFile), log)
		if err != nil {
			return cli.Registry{}, nil, errors.Join(err, store.Close())
		}
		closers.add(mem)
		// Roles answer, on every request with a login token, what its user
		// may do now, and who owns a namespace (owners approve their own
		// proposals). They answer team memory's rights too.
		roles := account.NewRoles(store, store)
		mems, err := newMemoryServices(mem, store, roles, log)
		if err != nil {
			return cli.Registry{}, nil, errors.Join(err, closers.Close())
		}
		svc := registry.NewService(store, store, store, time.Now, log, registry.WithMemberships(roles), registry.WithOwnerHints(roles))
		props := registry.NewProposalService(store, store, time.Now, log, registry.WithAccess(svc), registry.WithOwners(roles))
		wikis := registry.NewWikiService(store, store, time.Now, log)
		// The host's own deletes (`loadout package delete`) leave a record
		// like every other delete, and need no copy: the deploy skill backs
		// up by hand.
		hostDeletions := registry.NewDeletionService(store, store, time.Now, log)
		// The admin commands on the host need no mail or device flow; the
		// public URL goes into the sign-in link of member invite.
		admin := account.NewService(store, svc, roles, device.New(time.Now), account.Options{PublicURL: login.PublicURL, Wiki: wikis, MemoryTokens: mems.Tokens, MemoryRemover: mems.Service, Deletions: hostDeletions, Now: time.Now, Log: log})
		newHandler := func() (http.Handler, error) {
			// Approving a deletion saves a copy of the database first, so
			// it needs the backup folder; without one approvals are refused
			// with a sentence that says so.
			snaps := webSnapshots(log, stats.BackupDir, store)
			dopts := []registry.DeletionOption{registry.WithDeletionAccess(svc), registry.WithDeletionOwners(roles)}
			if snaps != nil {
				dopts = append(dopts, registry.WithDeletionSnapshots(snaps))
			}
			dels := registry.NewDeletionService(store, store, time.Now, log, dopts...)
			opts := []server.Option{server.WithProposals(props), server.WithDeletions(dels), server.WithCLI(advertised(build), server.MinimumCLI())}
			more, err := loginOptions(log, login, statsSetup{Stats: stats, DataDir: dataDir, Build: build}, testMailer, store, svc, props, dels, snaps, wikis, roles, mems, closers)
			if err != nil {
				return nil, err
			}
			rule, err := proxies.Rule()
			if err != nil {
				return nil, err
			}
			if rule.Header() != "" {
				log.Info("client addresses come from a proxy", "header", rule.Header(), "trusted", len(rule.Trusted()))
			}
			// Outside the server, so its access log and every limit see the
			// client's address and not the proxy's.
			return rule.Wrap(server.New(svc, log, build.Raw(), append(opts, more...)...)), nil
		}
		firstAdmin := func(ctx context.Context, configured string) (account.FirstAdmin, error) {
			return admin.EnsureFirstAdmin(ctx, store, configured)
		}
		return cli.Registry{Service: svc, Accounts: admin, Memory: mems.Service, NewHandler: newHandler, FirstAdmin: firstAdmin}, closers, nil
	}
}

// memoryServices are the team memory parts the registry's other services
// share: the rules, and the tokens that sign a person in to the MCP server.
type memoryServices struct {
	Service *memory.Service
	Tokens  *memory.Tokens
	// Stats counts memory.db for the server page.
	Stats *sqlitestore.MemoryStore
}

// newMemoryServices builds them over memory.db. roles answer a person's
// rights, store tells whether a token's person still exists.
func newMemoryServices(mem *sqlitestore.MemoryStore, store *sqlitestore.Store, roles *account.Roles, log *slog.Logger) (memoryServices, error) {
	svc, err := memory.NewService(mem, mem.Switches(), roles, time.Now, log)
	if err != nil {
		return memoryServices{}, err
	}
	tokens, err := memory.NewTokens(mem, account.NewMemoryUsers(store), time.Now, log)
	if err != nil {
		return memoryServices{}, err
	}
	return memoryServices{Service: svc, Tokens: tokens, Stats: mem}, nil
}

// resolvedExecutable is the running binary's path with links resolved,
// which says how it was installed; empty when it cannot be found.
func resolvedExecutable(executable func() (string, error)) string {
	p, err := executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// currentBuild is this binary: the version a test chose, else the ldflags
// version or what Go stamped into it.
func currentBuild(ext externals) buildinfo.Build {
	if ext.version != "" {
		return buildinfo.Current(ext.version, nil, false)
	}
	info, ok := debug.ReadBuildInfo()
	return buildinfo.Current(version, info, ok)
}

// advertised is the CLI release a registry built as b tells loadout CLIs
// about: its own release, only when it is exactly that release (a clean
// tag), else none. Deploying from a tag is how a release reaches people.
func advertised(b buildinfo.Build) semver.Version {
	if v, ok := b.Release(); ok && b.Exact() {
		return v
	}
	return semver.Version{}
}

// closeList closes its members last first, joining their errors.
type closeList []io.Closer

func (c *closeList) add(cl io.Closer) { *c = append(*c, cl) }

func (c *closeList) Close() error {
	var errs []error
	for _, cl := range slices.Backward(*c) {
		errs = append(errs, cl.Close())
	}
	return errors.Join(errs...)
}

func workDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
