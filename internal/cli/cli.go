// Package cli defines loadout's commands. Commands only parse flags, call a
// service and print; the work happens in the packages they call.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/builtin"
	"github.com/petervlad/loadout/internal/cliupdate"
	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/detect"
	"github.com/petervlad/loadout/internal/hook"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/mcpbridge"
	"github.com/petervlad/loadout/internal/mcpsetup"
	"github.com/petervlad/loadout/internal/notice"
	"github.com/petervlad/loadout/internal/rebase"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/release"
	"github.com/petervlad/loadout/internal/remote"
	"github.com/petervlad/loadout/internal/review"
	"github.com/petervlad/loadout/internal/reviewplan"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/update"
	"github.com/petervlad/loadout/internal/wiki"
)

// Deps are the services and settings the commands need.
type Deps struct {
	Installer *install.Installer
	// Releases answers which installed version to diff from or roll back.
	Releases *release.Service
	// Detector finds the AI tools the person has, for installs without
	// --target.
	Detector *detect.Detector
	Remotes  *remote.Store
	Log      *slog.Logger
	// StateDir holds loadout's own files; the default registry data lives
	// under it.
	StateDir string
	// Home is the user's home directory (user-scope root).
	Home string
	// WorkDir is the directory loadout was started in.
	WorkDir string
	// Version is this CLI's version as machines read it, for the
	// User-Agent; Build is the same build for people.
	Version string
	Build   buildinfo.Build
	// LogLevel is the terminal log level; -v lowers it to debug.
	LogLevel *slog.LevelVar
	// NewSyncer returns the service `sync` runs, reaching each install's
	// registry through connect.
	NewSyncer func(connect update.Connector) Syncer
	// OpenRegistry opens the registry database in dataDir, for serve and the
	// token and namespace commands. The closer releases the database.
	OpenRegistry func(ctx context.Context, dataDir string) (Registry, io.Closer, error)
	// Comparer runs `review PKG` and `review compare` in the reviewer's
	// own AI tools. Nil, like a nil NewPlanner, disables both.
	Comparer *review.Comparer
	// NewPlanner returns what picks the versions of a review, reading reg,
	// a client working in the view of the reviewed package's team.
	NewPlanner func(reg reviewplan.Registry) Planner
	// Browser opens the sign-in page for `loadout login`. Nil only prints
	// the address.
	Browser Browser
	// Sleep waits between the polls of `loadout login`; nil uses a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// Builtins installs loadout's own skills: after login, on sync, and
	// on `setup --skills`. Nil leaves them out.
	Builtins *builtin.Service
	// NewRebaser returns what locates and rebases package folders against
	// the registry at url, read through reg. Nil disables rebase, and
	// propose and publish then skip the check that a folder is behind.
	NewRebaser func(reg rebase.Registry, url string) Rebaser
	// Wiki pulls and pushes wiki pages, remembering what each file was
	// pulled at. Nil disables wiki pull and push.
	Wiki *wiki.Syncer
	// Notices says, offline, what the last sync found that still needs
	// the person: for status, notices and the session hooks.
	Notices *notice.Service
	// Updates remembers what registries say about loadout releases, from
	// every client newClient builds. Nil leaves it out.
	Updates *cliupdate.Service
	// Pusher, when a notification channel is set up, hears from sync that
	// a newer loadout is out, once per version.
	Pusher cliupdate.Pusher
	// Config is the settings `serve --check` reports on when no env file is
	// named, and the login settings `mail test` sends with.
	Config config.Config
	// Environ lists the process environment as KEY=VALUE, so `serve
	// --check` can name LOADOUT_ variables loadout does not read. Nil
	// lists none.
	Environ func() []string
	// NewMailer builds the mailer the registry would use for login and
	// names its kind: "smtp", "log", "test" or "off". It connects to
	// nothing. Nil disables `mail test`.
	NewMailer func(config.Login) (MailSender, string, error)
	// CheckLogin builds the login parts exactly as `serve` would, without
	// the network, and reports which are on. Nil disables `serve --check`.
	CheckLogin func(config.Login) (LoginReport, error)
	// HasAccounts says, read-only, whether the registry database in
	// dataDir holds any account, for `serve --check`. Nil leaves the
	// first-admin line out.
	HasAccounts func(ctx context.Context, dataDir string) (bool, error)
	// Hooks sets up and removes the session hooks in the AI tools: after
	// login and on setup --hooks or --no-hooks. Nil leaves them out.
	Hooks *hook.Service
	// MemorySetup adds and removes the team memory servers in the AI
	// tools: after install and sync, and on setup --memory or --no-memory.
	// Nil leaves them out.
	MemorySetup *mcpsetup.Service
	// BackupDir is where the nightly backups are kept (LOADOUT_BACKUP_DIR),
	// for `memory purge` to name the ones that still hold an entry.
	BackupDir string
	// Terminal says whether a command's input is a person at a keyboard.
	// Nil asks the file itself. Tests set it.
	Terminal func(in io.Reader) bool
	// Bridge builds what `loadout mcp` runs. Nil leaves the command failing
	// with a sentence.
	Bridge func(cfg mcpbridge.Config) (BridgeRunner, error)
	// RunShell runs the command of `update`; nil runs it with sh. Tests set it.
	RunShell func(ctx context.Context, command string, stdout, stderr io.Writer) error
}

// Browser opens a web page for the person at the terminal.
type Browser interface {
	Open(ctx context.Context, url string) error
}

// Syncer brings installed packages up to date; *update.Service is one.
type Syncer interface {
	Sync(ctx context.Context) ([]update.Result, error)
}

var _ Syncer = (*update.Service)(nil)

// Registry is a registry database opened on this machine.
type Registry struct {
	// Service manages tokens and namespaces.
	Service *registry.Service
	// Accounts manages users and their memberships.
	Accounts *account.Service
	// Memory is the team memory in this data folder, for the operator's
	// commands. Nil leaves them out.
	Memory MemoryHost
	// NewHandler builds what `serve` serves: the HTTP API, and the sign-in
	// pages when login is configured.
	NewHandler func() (http.Handler, error)
	// FirstAdmin creates the first admin when the registry has no account
	// at all (see account.Service.EnsureFirstAdmin). Nil does nothing.
	FirstAdmin func(ctx context.Context, configured string) (account.FirstAdmin, error)
}

// NewRoot returns the loadout command tree.
func NewRoot(d Deps) *cobra.Command {
	var verbose bool
	root := &cobra.Command{
		Use:           "loadout",
		Short:         "Share your team's AI agents, skills and knowledge bases across every AI tool",
		SilenceUsage:  true,
		SilenceErrors: true, // main prints the error once
		PersistentPreRun: func(*cobra.Command, []string) {
			if verbose && d.LogLevel != nil {
				d.LogLevel.Set(slog.LevelDebug)
			}
		},
	}
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "show debug logs (always written to ~/.loadout/loadout.log)")
	root.AddCommand(
		newNewCmd(d),
		newImportCmd(d),
		newInstallCmd(d),
		newCleanupCmd(d),
		newStatusCmd(d),
		newPolicyCmd(d),
		newUninstallCmd(d),
		newTeamCmd(d),
		newRemoteCmd(d),
		newPublishCmd(d),
		newListCmd(d),
		newSyncCmd(d),
		newDiffCmd(d),
		newRollbackCmd(d),
		newServeCmd(d),
		newTokenCmd(d),
		newNamespaceCmd(d),
		newMemberCmd(d),
		newUserCmd(d),
		newMailCmd(d),
		newPackageCmd(d),
		newAdminCmd(d),
		newLoginCmd(d),
		newLogoutCmd(d),
		newLayersCmd(d),
		newProposeCmd(d),
		newRebaseCmd(d),
		newReviewCmd(d),
		newWikiCmd(d),
		newMemoryCmd(d),
		newMcpCmd(d),
		newSetupCmd(d),
		newNoticesCmd(d),
		newVersionCmd(d),
		newUpdateCmd(d),
	)
	return root
}

// errNoProject is returned for --scope project outside a git repository.
var errNoProject = errors.New("--scope project needs a git repository; cd into one, or use --scope user")

// projectRoot returns the nearest folder at or above dir that contains .git
// (a folder, or a file in a worktree).
func projectRoot(dir string) (string, error) {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", errNoProject
		}
		d = parent
	}
}

// scopeFor resolves a --scope value to a scope.
func (d Deps) scopeFor(kind string) (target.Scope, error) {
	switch target.ScopeKind(kind) {
	case target.ScopeUser:
		return target.Scope{Kind: target.ScopeUser, Root: d.Home}, nil
	case target.ScopeProject:
		root, err := projectRoot(d.WorkDir)
		if err != nil {
			return target.Scope{}, err
		}
		return target.Scope{Kind: target.ScopeProject, Root: root}, nil
	}
	return target.Scope{}, fmt.Errorf("--scope %q: want user or project", kind)
}

// scopeKindFilter validates an optional --scope filter; empty means any.
func scopeKindFilter(kind string) (target.ScopeKind, error) {
	k := target.ScopeKind(kind)
	if k != "" && !k.Valid() {
		return "", fmt.Errorf("--scope %q: want user or project", kind)
	}
	return k, nil
}

// abs resolves a path relative to the working directory.
func (d Deps) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(d.WorkDir, p)
}
