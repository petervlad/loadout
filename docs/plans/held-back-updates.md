# Updates held back by local edits

Status: built and deployed (2026-10-04). The hook output was verified by hand only in Claude Code. Copilot CLI, VS Code and Windows were not.

## Goal

A person edits an installed agent, skill or knowledge base. Later `loadout sync` (by
hand or from their cron) cannot update it, because loadout never overwrites an edit.
Today they are told once, in a table they may never see, and then nothing remembers
it: `loadout status` says "modified" and nothing else, so they believe they have the
latest version and they do not.

Four changes, the first three shippable before the fourth:

1. **Remember it.** When sync holds an update back (policy `auto` and edited files, and
   policy `notify` when an update is out and the files are edited, so `install` would
   be held too), loadout records the version held back, the files in the way and when,
   in a state file next to the lock. The record clears itself when the install catches
   up, when the edits are reverted, or after `install --force`.
2. **Show it.** `loadout status` says "1.3.0 held back: you edited SKILL.md" without a
   registry call. The sync table gives the right next steps (today it says "use
   --force", a flag sync does not have). Pushover fires once per held-back version, not
   every run. Sync exits non-zero when an `auto` update is held back, so cron notices.
3. **Tell it inside the tools.** A session-start hook in Claude Code and Copilot runs a
   fast, offline `loadout notices` that prints one short line, or nothing.
4. **A way out that keeps the edits.** `loadout install TEAM/NAME --merge` three-way
   merges the person's edited files onto the new release, on the rendered output for
   each tool, writes conflict markers where both changed the same lines, and records the
   new version so the merged files count as the person's edits on it.

The one rule holds. Nothing here calls a model or needs a key. The hook runs the
loadout binary on the person's machine, reads two local files and prints text. What
Claude Code or Copilot does with that text is the person's own tool at work. The merge
is the deterministic `textdiff.Merge3` that `loadout rebase` already uses, run in the
CLI. The registry learns nothing new and changes in no way.

## Decided

The user settled these (2026-10-04):

1. Remember held-back updates persistently, including `notify` installs whose files
   are edited. Clear the record on update, revert or force.
2. Show them in `status` with no registry call, give correct next steps in `sync`, notify
   once per held-back version, and decide on a non-zero exit for cron (recommended
   below).
3. Offer a merge that keeps the edits, as later steps. `install --force` stays the way to
   discard them.
4. Tell the person at session start in Claude Code and Copilot, with an offline
   command. Research what Copilot supports.

The three decisions that were open at the end of this plan, settled as recommended
(2026-10-04):

5. **Hooks on by default.** `loadout login` sets up the session hooks and says so in one
   line. `loadout setup --no-hooks` removes them and records that the person declined.
   Existing users get them with `loadout setup --hooks`, and sync suggests that in one
   tip line when something is held back and hooks are neither wanted nor declined.
6. **Exit code 2.** `sync` exits 2 while an `auto` update is held back, on every run
   until it is resolved. A failed registry check stays 1 (and wins over 2). `notify`
   Installs with edits exit 0. `loadout policy TEAM/NAME pin` is the quiet way out.
7. **Pushover once per version**, for held-back updates and for plain "update available"
   alike.

## What exists today

- **No trigger.** Sync runs by hand or from a cron the person writes:
  `internal/cli/sync.go:18-22` ("Safe to run from cron"), GUIDE "4. Stay up to date"
  (`docs/GUIDE.md:125-136`).
- **Policy `notify`** is the default (`internal/lock/lock.go:30`). For a newer release,
  `syncOne` reports `OutcomeAvailable` and calls the notifier without looking at the
  files (`internal/update/update.go:189-195`). A person who edited the files learns of
  1.3.0, runs `loadout install`, and only then hits the conflict.
- **Policy `auto`.** `apply` installs without `Force` (`update.go:241-254`).
  `findConflicts` turns a hash that differs from the lock into `ReasonModified`
  (`internal/install/conflict.go:102-103`), `installLocked` refuses the whole install
  (`internal/install/install.go:211-217`), and sync reports `OutcomeBlocked`, "blocked by
  local edits" (`update.go:81`, `247-249`). The same path runs when only the team's model
  settings changed (`update.go:228-231`).
- **Wrong hint.** `ConflictError.Error` ends with "use --force to overwrite"
  (`conflict.go:47-60`), sync prints that error verbatim (`sync.go:69-73`), and `sync`
  has no `--force`.
- **Quiet for cron.** Only `OutcomeFailed` counts toward the error (`sync.go:62-65`,
  `79-81`), so a held-back update exits 0. `runWith` turns every error into exit 1
  (`cmd/loadout/main.go:159-171`). The notifier is only called for available updates and
  model changes (`update.go:191-194`, `221-224`), never for a held-back one, and
  Pushover's text says "Update with: loadout install" (`internal/notify/pushover/pushover.go:63-68`),
  which fails for an edited install. It pushes on every sync, so a cron sync repeats the
  same message daily (DESIGN, Notifications).
- **Nothing persists.** `lock.Entry` has no field for it (`lock.go:51-76`). `status`
  (`internal/cli/status.go:26-43`, `internal/install/status.go:78-115`) reads only the
  lock and the disk and shows "modified". It never knows that a newer version exists.
- **The lock is read strictly.** `lock.Store.Load` decodes with `strictyaml`
  (`internal/lock/store.go:56`), so a new field in the lock would make every older
  loadout call the lock damaged. The built-in skills keep their state apart for that
  reason (`internal/builtin/state.go:19-20`).
- **Built-in skills** take their own path: `Ensure` meets the conflict and reports
  `ActionKept` (`internal/builtin/service.go:241-243`), printed as "kept your own ...
  skill" (`internal/cli/setup.go:76`) on every sync, and remembered nowhere.
- **Rendered files.** An installed file is not a package file: `target.Render`
  (`internal/target/render.go:88-116`) gives each tool its own frontmatter, tool names
  and models, and adds `ManagedNote` with the version and digest
  (`internal/target/markdown.go:57-60`, whose text already says "Local edits pause
  updates"). The installer renders every tool and writes a path two tools share once
  (`install.go:248-269`). The lock keeps the SHA-256 of each rendered file and the digest
  of the model table it rendered with (`lock.Entry.Models`), not the table.
- **Merging.** `textdiff.Merge3` merges three texts with diff3 markers and refuses over
  5,000 lines (`internal/textdiff/merge.go:28-31`). `internal/rebase` merges package
  folders with a table of per-file cases (`mergePath`, `internal/rebase/merge.go:257`)
  and `whole` (`merge.go:286`) for files too long to merge by line. Nothing merges
  installed files.
- **Tests that pin today's behaviour**: e2e `TestTeamSharesAnAgentThroughALANRegistry`
  expects "blocked by local edits" and exit 0 (`cmd/loadout/registry_test.go:148`).
  `internal/update/update_test.go:156` expects `OutcomeBlocked` for a conflict.

### What the tools offer at session start (researched 2026-10-04)

- **Claude Code** reads hooks from `~/.claude/settings.json` (user),
  `.claude/settings.json` and `.claude/settings.local.json` (project). A `SessionStart`
  hook has matchers `startup`, `resume`, `clear`, `compact` and `fork`. Its input JSON
  has `cwd` and `source`. On exit 0, plain stdout is added to Claude's context. JSON
  output may carry `systemMessage` (shown to the person) and
  `hookSpecificOutput.additionalContext` (for Claude). SessionStart cannot block. A
  non-zero exit shows a "hook error" notice with stderr. The command timeout defaults to
  600 s. Hooks from several settings files merge, and the same handler in two files
  runs once. There is no hooks folder for user hooks, so a user hook means editing
  `settings.json`. Source: code.claude.com/docs/en/hooks.
- **Copilot CLI** loads every `*.json` in `~/.copilot/hooks/` (user) and
  `.github/hooks/*.json` (repository): `{"version":1,"hooks":{"sessionStart":[{"type":"command","bash":...,"powershell":...,"timeoutSec":30}]}}`.
  The input has `cwd` and `source` (`startup`, `resume`, `new`). Only JSON output is
  read, and for `sessionStart` only a top-level `additionalContext`, which goes to the
  model. Nothing is shown to the person directly. Timeouts fail open. Source:
  docs.github.com/en/copilot/reference/hooks-reference.
- **VS Code** (agent hooks, preview, on by default through `chat.useHooks`) reads
  `~/.copilot/hooks/*.json` and `.github/hooks/*.json`, and `~/.claude/settings.json`
  only with `chat.useClaudeHooks`. It ignores matchers, and `SessionStart` fires on the
  first prompt of a session. Output is `hookSpecificOutput.additionalContext`. Source:
  code.visualstudio.com/docs/agent-customization/hooks.

So both tools have a user-level session-start hook. Claude Code can show a line to the
person. Copilot can only hand text to its model, which then mentions it. A file of
loadout's own in `~/.copilot/hooks/` serves Copilot CLI and VS Code without touching
anything of the person's.

## Design

### Where the record lives: a state file, not the lock

`~/.loadout/syncstate.yaml` (strict YAML, 0600, written atomically, format version 1),
read and written by a new package `internal/syncstate`. Reasons:

- Older loadouts read the lock strictly. A new lock field would break them on a
  downgrade or a second, older binary. A separate file is invisible to them, as
  `builtin.yaml` is.
- The lock records what loadout wrote. This records what the last sync learned from the
  registries. Different writers (install versus sync), different lifetimes: the state
  file can be deleted at any time and costs only notices until the next sync.
- No migration: a missing file is the empty state.

The record is a hint, never the truth. Everything it says is checked again against the
lock and the disk when read (see `notice` below), so it clears itself: an install that
caught up, edits that were reverted, a forced install and an uninstalled package stop
showing without anyone writing to the file. Each sync rewrites the records of the
installs it checked.

```go
// Package syncstate keeps what sync learned from the registries between runs,
// so status and the session hooks can say it without a network call.
package syncstate

// Key names one install: a package in a scope (user scope stored without root,
// as in the lock).
type Key struct {
	ID    pack.ID
	Scope target.Scope
}

// Blocker is one file that keeps an update out.
type Blocker struct {
	Path   string `yaml:"path"`   // slash-separated, relative to the scope root
	Reason string `yaml:"reason"` // install.Reason text: "edited since loadout wrote it", ...
}

// Behind is an install the last sync found behind its registry.
type Behind struct {
	ID      pack.ID        `yaml:"id"`
	Scope   target.Scope   `yaml:"scope"`
	Version semver.Version `yaml:"version"` // installed when checked
	Latest  semver.Version `yaml:"latest"`  // Version when only the model settings changed
	Models  string         `yaml:"models,omitempty"` // the table digest, for a model change
	Held    []Blocker      `yaml:"held,omitempty"`   // empty: available, nothing in the way
	Since   time.Time      `yaml:"since"`   // the first sync that saw this Latest
	Checked time.Time      `yaml:"checked"` // the last sync that checked it
	Told    bool           `yaml:"told,omitempty"` // the notifier was told about Latest, held or not
}

// Store reads and changes the state under a cross-process lock.
type Store struct{ /* path, home */ }

func NewStore(path, home string) *Store
func (s *Store) Load() (State, error)
func (s *Store) Update(ctx context.Context, fn func(*State) error) error
```

`State` is a value with `Find(Key)`, `Put(Behind)`, `Drop(Key)` and `All()`, copied on
the way out like `lock.Lock`. The file lock that keeps two loadouts from losing each
other's changes moves out of `internal/lock` (`flock.go`, `flock_unix.go`,
`flock_windows.go`) into a small package `internal/flock` with an exported `Acquire`,
used by both stores. A damaged state file is reported with the remedy "remove it. The
next sync writes it again", and `status` and `notices` treat it as empty rather than
fail.

### update: decide, then remember and tell

`update.Service` gets two consumer-owned additions and tells the notifier after the
loop, not inside it, so one place decides what to say.

```go
// Installer gains:
	// Edited returns the files of e that exist and differ from what loadout
	// wrote (slash paths, relative to the scope root); only checks the paths
	// in only when it is not empty.
	Edited(ctx context.Context, e lock.Entry, only []string) ([]string, error)

// Memory keeps what sync learned between runs; *syncstate.Store is one.
type Memory interface {
	Load() (syncstate.State, error)
	Update(ctx context.Context, fn func(*syncstate.State) error) error
}
```

- `Result` gains `Held []syncstate.Blocker`. For `auto` it comes from the
  `*install.ConflictError` (every conflict, with its reason, paths made relative). For
  `notify` with a newer release, sync asks `Installer.Edited(e, nil)` (hashes only, no
  fetch) and fills `Held` with the edited files, keeping `OutcomeAvailable`. Shadowed
  and pinned installs are not checked.
- `OutcomeBlocked` keeps its name and becomes "held back by your edits". A model-only
  hold reads the same, and the CLI words it as model settings (`Latest == Version`).
- `Notification` gains `Held []string` (relative paths). Pushover sends a count, not
  the paths ("You edited 1 file, so loadout kept 1.2.0. Run loadout status."), so no
  file names leave the machine. The title is "payments/planner 1.3.0 held back".
- **Once per version.** Before the loop sync loads the state. After it, for each
  result that is behind, it notifies only when the previous record of that install has
  `Told` false, another `Latest`, or another held state (an available update that
  became held is news). It then writes every checked install in one `Memory.Update`:
  `Put` for behind, `Drop` for up to date, updated and re-rendered. A failed or skipped
  source keeps its old record, so a registry outage does not erase what is known. A
  notification that fails leaves `Told` false and is tried again next run. A failed
  state write is logged as a warning and never fails the sync.
- `New(installer, connect, notifier, memory, log)`. `cmd/loadout` passes
  `syncstate.NewStore(filepath.Join(cfg.StateDir, "syncstate.yaml"), cfg.Home)`.
- **Built-in skills.** `builtin.Options` gains an optional `Memory` (the same small
  interface, owned by `builtin`). `Ensure` records an `ActionKept` skill as held, with
  the CLI version as `Latest`, and drops the record when the skill is in step. Its next
  step is `loadout setup --skills --force`. There is no merge for them (the old text is
  not in the new binary).

### notice: what to tell, checked against the lock and the disk

A new package `internal/notice` turns the state, the lock and the files into what to
say. It owns its interfaces and never opens a network connection: it is built in
`cmd/loadout` from the state store and the installer only, so the offline promise is a
property of its constructor, not of discipline.

```go
// Files is what notice needs from the installer.
type Files interface {
	Entries(ctx context.Context) ([]lock.Entry, error)
	Edited(ctx context.Context, e lock.Entry, only []string) ([]string, error)
}

// Kind says what a notice is about.
type Kind int

const (
	Available Kind = iota + 1 // a newer release, nothing in the way
	Held                      // a newer release (or model settings) your edits keep out
	Unresolved                // install --merge left conflict markers (step 6)
)

// Notice is one thing to tell, about one install.
type Notice struct {
	Kind      Kind
	ID        pack.ID
	Scope     target.Scope
	Installed semver.Version
	Latest    semver.Version
	Models    bool
	Files     []string // still edited, or still conflicted
	Checked   time.Time
}

// Sentence is the line status and the hooks print, e.g.
// "1.3.0 held back: you edited SKILL.md".
func (n Notice) Sentence() string

// List returns the notices for installs that match f, worst first.
func (s *Service) List(ctx context.Context, f Filter) ([]Notice, error)

// Filter narrows the notices: kinds, and the folder a session runs in (user
// scope always matches; a project scope matches when Dir is inside its root).
type Filter struct {
	Kinds []Kind
	Dir   string
}
```

A record shows only while it is still true: the lock has the install. Its version is
below `Latest` (or, for models, `Entry.Models` differs from the recorded digest), and
for `Held`, `Edited(e, paths)` still finds at least one of the recorded paths edited.
Re-checking only the recorded paths keeps a knowledge base of thousands of files cheap.
Reverted edits turn a `Held` record into `Available`. An `auto` install then updates at
the next sync.

### target: one optional interface for session hooks

Each adapter knows its tool's hook file and output format, so a third tool is a third
adapter, not a switch. Adapters stay pure: they turn bytes into bytes, and
`internal/hook` does the file work.

```go
// SessionHook is an Adapter whose tool runs a command when a session starts.
type SessionHook interface {
	// HookFile is the file, relative to the home folder, that holds the hook.
	HookFile() string
	// WithHook returns cur (nil when the file does not exist) with loadout's
	// hook running cmd, or cur unchanged when it is already there.
	WithHook(cur []byte, cmd HookCommand) ([]byte, error)
	// WithoutHook returns cur without loadout's hook; nil means the file
	// held nothing else and is deleted.
	WithoutHook(cur []byte) ([]byte, error)
	// SessionOutput is what the hook prints for msg; nil prints nothing.
	SessionOutput(msg SessionMessage) ([]byte, error)
}

// HookCommand is the command line loadout's hook runs, per shell.
type HookCommand struct{ POSIX, PowerShell string }

// SessionMessage is what to tell at session start: a line for the person,
// and a line for the agent.
type SessionMessage struct{ Person, Agent string }
```

- **Claude Code** (`claudecode`): `HookFile` is `.claude/settings.json`. `WithHook` adds,
  under `hooks.SessionStart`, one matcher group
  `{"matcher":"startup","hooks":[{"type":"command","command":"loadout notices --hook claude 2>/dev/null || true","timeout":5}]}`.
  It recognises its own handler by a command matching `loadout notices --hook claude`
  (with or without a path to the binary) and replaces only that one. The file is read
  with `encoding/json/jsontext` into an ordered list of members. Every value loadout does
  not touch is copied as its raw `jsontext.Value`, so keys keep their order and numbers
  and strings their exact text, and the result is written with two-space indents, as
  Claude Code writes it. Invalid JSON, or `hooks` or `SessionStart` of the wrong type,
  is refused, never "fixed". `WithoutHook` removes the handler, then a matcher group it
  emptied, then `SessionStart` and `hooks` only if they became empty. `SessionOutput`
  prints `{"systemMessage": Person, "hookSpecificOutput": {"hookEventName":
  "SessionStart", "additionalContext": Agent}}`.
- **Copilot** (`copilot`): `HookFile` is `.copilot/hooks/loadout.json`, a file loadout
  owns whole: `{"version":1,"hooks":{"sessionStart":[{"type":"command","bash":...,"powershell":...,"timeoutSec":5}]}}`.
  `WithoutHook` returns nil. `SessionOutput` prints `{"additionalContext": ...}` and the
  same under `hookSpecificOutput` for VS Code. The text asks the agent to mention it to
  the person in one sentence, since Copilot shows nothing directly.

The texts, built by `notice` from the held notices, at most three names and "and N
more":

- Person: "loadout: 2 updates are held back by your edits (payments/planner 1.3.0,
  payments/runbooks 2.0.0). Run loadout status."
- Agent: "loadout could not update payments/planner to 1.3.0 and payments/runbooks to
  2.0.0 because the user edited the installed files. Mention it once, in one sentence,
  and suggest loadout status. The loadout-setup skill explains the choices. Never run
  loadout install --force without asking." (Copilot gets "Mention it to the user in
  your first reply" since only the model sees it.)

The `|| true` and `2>/dev/null` keep an older or missing loadout from showing a hook
error in every session. The PowerShell form checks `Get-Command loadout` first.

### hook: install and remove the hooks safely

A new package `internal/hook`, built in `cmd/loadout`, reached through
`cli.Deps.Hooks`.

```go
// Tool is an AI tool whose user-level session hook loadout manages.
type Tool interface {
	target.Adapter
	target.SessionHook
}

type Service struct{ /* home, tools, wish file, command, now, log */ }

// Setup writes or updates the hook of every tool found (MarkerDir at user
// scope) or named, and records the wish. Remove takes them out and records
// that the person declined.
func (s *Service) Setup(ctx context.Context, tools []string) (Report, error)
func (s *Service) Remove(ctx context.Context) (Report, error)
func (s *Service) Wanted() (bool, error)
```

- **User scope only.** A project's `.claude/settings.json` is committed: a teammate
  without loadout would get a broken hook, and a hook in a repository is code everyone
  who clones it runs. The user-level hook already covers project installs, since
  `notices` matches project scopes by the session's `cwd`.
- **Safe writes.** Through `os.Root` on the home folder, with `atomicfile.WriteIn`.
  Refused, with the snippet printed to paste by hand, when `settings.json` or
  `~/.claude` (or `~/.copilot/hooks`) is a symbolic link (dotfiles repositories do
  this. Renaming over the link would replace it), when the JSON does not parse, or when
  the file changed between read and write (read again just before the rename, retried
  once). Before loadout's first change to `settings.json` it writes
  `settings.json.loadout.bak` if no such file exists. Nothing is written when
  `WithHook` returns the same bytes, so running setup again is a no-op.
- **Folders.** A tool's hook is set up only when its marker folder exists (as for the
  built-in skills). `~/.copilot/hooks/` is created inside an existing `~/.copilot`.
- **The command.** Bare `loadout` when `exec.LookPath("loadout")` is the running
  binary. Otherwise the absolute path of `os.Executable()`, quoted, so a binary outside
  the PATH a tool starts with still works. `LookPath` and the executable path are
  injected (`externals` in `cmd/loadout`) so e2e tests stay hermetic.
- **Wish.** `~/.loadout/hooks.yaml` (strict YAML, 0600: `wanted`, `declined`), apart
  from `builtin.yaml` so older loadouts still read that file strictly. `login` installs
  the hooks when not declined (decision 1) and prints one line. `setup --hooks` installs
  and clears `declined`. `setup --no-hooks` removes them and records `declined`. `sync`
  never edits a settings file: it runs from cron, unattended. When something is held
  back and hooks are neither wanted nor declined, sync prints one tip line.

### CLI

`internal/cli` parses, calls and prints, as now.

- **`sync`** (`sync.go`). RESULT reads "held back by your edits" (auto),
  "update available, you edited 2 files" (notify), "model settings held back by your
  edits". The raw `ConflictError` is no longer printed for these. Instead, per install:

  ```text
  payments/planner 1.3.0 is held back. Edited since loadout wrote it:
    ~/.claude/agents/planner.md
  Keep your edits and take 1.3.0:   loadout install payments/planner --merge
  Replace your edits with 1.3.0:    loadout install payments/planner --force
  Keep your version, stop updates:  loadout policy payments/planner pin
  ```

  A project install adds `--scope project` and names the folder to run it in. Files in
  the way for other reasons (not written by loadout, another package) are listed with
  their reason, and `--force` is offered only when it can help (`Conflict.blocking`).
  Until step 7 the first line is "See what changed: loadout diff payments/planner".
  **Exit code** (decision 2): 2 when at least one `auto` install is held back and
  nothing failed, 1 when a check failed, else 0. `runWith` learns
  `interface{ ExitCode() int }` through `errors.As`, the one place that maps errors to
  codes. The error text is "1 update is held back by your edits. See above".
- **`status`** (`status.go`). The table stays as it is. Below it, from
  `notice.List(all kinds)`, offline:

  ```text
  As of the last sync (4 Oct 09:00):
    payments/planner   1.3.0 held back: you edited planner.md (claude)
    payments/runbooks  2.1.0 available: loadout install payments/runbooks
  ```

  Nothing is printed when the state is empty or nothing is behind. `status` never calls
  a registry.
- **`notices [--hook claude|copilot]`** (new, `notices.go`). Without `--hook` it prints
  the held and unresolved sentences as plain lines, for a terminal or a shell prompt.
  With `--hook` it reads at most 64 KiB of hook input from stdin, takes `cwd` and
  `source`, stays silent for `resume`, `clear`, `compact` and `fork` (VS Code ignores
  matchers), lists held and unresolved notices for that folder, and prints the adapter's
  `SessionOutput`. With `--hook` every error prints nothing and exits 0 (logged at
  debug), and a 1 second deadline bounds the work. It prints nothing when there is
  nothing to say.
- **`setup --hooks`, `setup --no-hooks`** beside `--skills` (`setup.go`). `login` calls
  `Hooks.Setup` after the skills unless declined.
- **`install --merge`** (step 7), see below.

### install --merge: three-way merge on rendered files (steps 6 and 7)

Feasible because rendering is deterministic: the same pack, adapter, scope, stamp and
model table give the same bytes, and the lock holds the SHA-256 of every file loadout
wrote. So the base of the merge is the installed release rendered again, and its hash
proves it is exactly what loadout wrote.

```go
// MergeResult says what Merge did to each file.
type MergeResult struct {
	Entry lock.Entry
	Files []MergedFile // path (absolute), what happened, conflict count
	// Kept are edited files the new release no longer has: left in place,
	// no longer managed (as an upgrade does today).
	Kept []string
}

// Merge installs next over base, keeping the person's edits to base's files:
// each edited file is merged three ways (rendered base, the file on disk,
// rendered next) and conflicts are marked in it. Files loadout did not write
// still block, as in Install.
func (in *Installer) Merge(ctx context.Context, base, next *pack.Pack, opts Options) (MergeResult, error)
```

- **Sides, per tool.** `render` (install.go:248) runs for base and for next with the
  same renderers (`opts.Targets` from `Entry.Targets()`), scope and table, so each tool's
  file merges with its own base and new version, and a path two tools share is merged
  once. Base is stamped with the installed version, so the `ManagedNote` line changes
  only on the release's side and merges cleanly.
- **Base check.** Each rendered base file must hash to the lock's `SHA256` for its
  path. The lock keeps only the digest of the model table, so the base renders first
  with the current table and, if `Entry.Models` is empty and that differs, with the
  empty table. A file whose base still does not match (the table changed since, or a
  newer loadout renders the file differently) is merged whole: one conflict holding
  yours and the release's, with no guessed base. Only agent files carry models, so
  skills always find their base.
- **Cases per path** (a table in `install`, its own because installed files differ from
  folders): not edited takes the release (what `Install` does). Edited and unchanged in
  the release keeps yours. Both changed is `textdiff.Merge3` with labels
  `yours (edited 1.2.0)`, `installed 1.2.0`, `release 1.3.0`. Over 5,000 lines is
  `textdiff.Whole` (moved from `rebase.whole` and exported, rebase calls it). A file you
  deleted comes back from the release (an install restores missing files today). A file
  the release dropped that you edited is kept and unmanaged, as `removeFile` does.
- **Refusals.** `findConflicts` runs first. Any conflict but `ReasonModified` (a file
  not written by loadout, a symlink, another package, a linked folder) refuses the merge
  with the install's error. `--merge` with `--force` is refused. On a folder install
  (no registry base) and on `loadout/...` built-in skills it says what to do instead.
  Shadowed files are left alone.
- **Write and record.** Under `locks.Update`, every file is written through the install
  `tree` (atomic, `os.Root`), then the entry is put with next's version and the hashes
  of next's rendered files. The merged files therefore show "modified" against 1.3.0,
  the next sync finds the install up to date and records nothing, and a later 1.4.0 is
  held back again and merged from 1.3.0. If a write fails half way the lock still names
  1.2.0. Running the merge again is safe, because a change already applied is the same
  on both sides and `Merge3` applies it once.
- **Conflicts left.** The paths with markers and the labels go into the state as a
  `Merged` record (`syncstate`), shown by `status` and `notices` as `Unresolved`
  ("1.3.0 merged: resolve the conflicts in planner.md") while a marker line with those
  labels remains, like `rebase.Unresolved`. Until then the tool reads a file with
  markers in it, and the CLI says so.
- **CLI** (`install.go`): `--merge` needs TEAM/NAME and an installed entry in the scope
  (`release.Service.Installed`). It fetches the installed version and the requested one
  (default latest) and the team's table, as install does now, and calls
  `Installer.Merge`. It prints one line per file: "merged", "kept yours", "took the
  release", "N conflicts". `sync` then names `--merge` first in its next steps, and
  Pushover and the hook mention it.

## Data and API changes

- New files in the state folder: `syncstate.yaml` (+ `syncstate.yaml.lock`) and
  `hooks.yaml`. The lock, `builtin.yaml` and `rebase.yaml` do not change.
- Files outside loadout's folder, only with the person's consent (decision 1):
  `~/.claude/settings.json` (one hook entry added, backup once) and
  `~/.copilot/hooks/loadout.json`.
- `update.Result.Held`, `update.Notification.Held`, `update.New` takes a `Memory`.
  `install.Installer.Edited`, `Installer.Merge`. `target.SessionHook`,
  `HookCommand`, `SessionMessage`. `textdiff.Whole`. `builtin.Options.Memory`.
  `cli.Deps.Notices` and `Deps.Hooks`.
- Outcome text "blocked by local edits" becomes "held back by your edits". Sync's exit
  code becomes 2 for held-back `auto` updates.
- No registry, database, API or web change.

## Migration

None in the database. Old lock files are read as they are. A missing `syncstate.yaml`
is the empty state, so after the upgrade nothing is shown until the next sync, which
then records everything behind. An older loadout ignores the new files. After a
downgrade the hooks still call `loadout notices`, which the older binary does not have:
the `2>/dev/null || true` guard keeps that silent. Pushover users get each current
held-back or available version once more after the upgrade (no record says it was
told), then once per version.

## Order of work

Each step passes `make check` and can ship on its own. Steps 1 to 5 are the notice
parts. The merge (6, 7) comes after.

1. **State store.** Move the file lock to `internal/flock` (no behaviour change).
   `internal/syncstate` with `Store`, `State`, `Behind`, `Blocker`, `Key`. Tests. Nothing
   uses it yet.
2. **Sync remembers and says the right thing.** `Installer.Edited`. `update.Memory`,
   `Result.Held`, the notify-edited check, the outcome texts, notify-once with `Told`,
   the state written after the loop. `Notification.Held` and the Pushover texts.
   Built-in skills recorded through `builtin.Options.Memory`. Wiring in `cmd/loadout`.
   `sync` prints the held files and next steps (no `--merge` yet) and exits 2.
   `runWith` honours `ExitCode()`. Update `registry_test.go:148` and
   `update_test.go:156`. GUIDE (Updates and policies, Troubleshooting "held back by your
   edits", cron note on exit 2), DESIGN (Notifications, Rules that protect users),
   `loadout-setup` skill and `references/troubleshooting.md` (the row at line 18), and
   `docs/CHANGES.md`.
3. **Status and notices.** `internal/notice` with `List` and `Sentence`.
   `status` footer. `loadout notices` with plain output. `--hook` with
   `SessionOutput` on both adapters (`target.SessionHook` lands here with its output
   half. `WithHook` and `WithoutHook` follow in step 4). Docs: GUIDE (See what you
   have, command reference), DESIGN packages table.
4. **Hooks.** `WithHook`, `WithoutHook`, `HookFile` on both adapters. `internal/hook`
   With the wish file. `setup --hooks`, `--no-hooks`. `login` integration (decision 1).
   The sync tip. Before coding, check by hand with a current Claude Code, Copilot CLI and
   VS Code that each shows the output as researched (record the versions in this plan),
   including VS Code reading the Copilot-format file and both output shapes, and how
   Claude Code runs hook commands on Windows. Docs: GUIDE (a "Messages in Claude Code
   and Copilot" part of "Set up Claude Code and Copilot", how to remove them),
   DESIGN, the `loadout-setup` skill (what the line means and the choices), CHANGES.
5. **Ship the notices.** Rebuild the guide (`docs/guide/build.sh`), `go install
   ./cmd/loadout`, release. No database migration, so no backup is needed for the
   registry, which does not change.
6. **Merge in the installer.** `textdiff.Whole` (moved from `rebase.whole`, rebase
   unchanged in behaviour). `Installer.Merge` with the base check, the case table and
   the lock entry. The `Merged` record in `syncstate` and `Unresolved` in `notice`.
   Tests only. No command yet.
7. **`install --merge`.** The flag and its refusals. Sync's next steps, Pushover and the
   hook text name it first. `status` and `notices` show unresolved conflicts. Docs:
   GUIDE (Updates and policies: keep your edits, Troubleshooting for markers), DESIGN
   (Rules that protect users), the `loadout-setup` skill (offer `--merge` before
   `--force`, help resolve markers), CHANGES.

## Tests

Table-driven, with the fakes the packages already use.

- **syncstate**: load and save round trip. Missing and empty file. Unknown field and
  wrong version refused with the remedy. User scope saved without its root and read under
  the current home. `Put` replaces by key. Two concurrent `Update`s both land.
- **install.Edited**: intact, edited, missing (not edited), symlink (edited), `only`
  restricting the check, shadowed files ignored.
- **update** (extend `update_test.go`): notify with edits gives `OutcomeAvailable` and
  `Held`. Auto with a conflict gives `OutcomeBlocked` and `Held` with reasons. A
  model-only hold. Two syncs with the same latest notify once, a new latest notifies
  again, available turning held notifies again, a failed notification is retried.
  Up-to-date and updated drop the record. A failed source keeps it. A failed state write
  does not fail the sync.
- **notice**: a table of lock, state and disk: caught up, edits reverted (held becomes
  available), entry uninstalled, model digest applied, a forced install, project scope
  matched and not matched by `Dir`, a damaged state file read as empty. `Sentence` for
  each kind. A benchmark with 50 installs and a 5,000-file knowledge base, which must
  stay under 20 ms.
- **adapters** (`claudecode`, `copilot`): `WithHook` and `WithoutHook` tables and golden
  files: nil file, `{}`, a file with the person's own SessionStart and other hooks (kept
  in order), other top-level keys kept in order with exact numbers, already installed
  (same bytes), an older loadout command replaced, invalid JSON and wrong types refused,
  removal leaving the person's hooks. A fuzz test that `WithoutHook(WithHook(x))` keeps
  every member of any valid `x`. `SessionOutput` golden output for both tools.
- **hook**: symlinked `settings.json` and symlinked `~/.claude` refused with the
  snippet. No `~/.copilot` creates nothing. The backup written once. A file changed
  between read and write. The wish file. The command chosen from the injected
  `LookPath`.
- **Merge** (step 6, `install`): untouched file takes the release. Edited and unchanged
  in the release kept. Both changed merged clean. Both changed with markers and labels.
  The base check falling back to the empty table, then to a whole-file conflict. Over
  5,000 lines. You deleted, release deleted. An unmanaged file in the way refuses.
  Shadowed files untouched. A project skill shared by both tools merged once. A Copilot
  agent at `.github/agents`. The lock entry after merge (next version, next hashes) makes
  `Status` say modified and `Rerenders` false. A second merge after a simulated partial
  write gives the same result.
- **cli**: status footer lines. Sync next-step lines for user and project scope and for
  each conflict reason. The exit code mapping. `notices --hook` silent for `resume` and
  on any error. The built-in skills honesty test (`internal/cli/builtin_skills_test.go`)
  checks every new command and flag the skills name.
- **e2e** (`cmd/loadout`, `person` and `newSession`):
  - Update `TestTeamSharesAnAgentThroughALANRegistry`: Bob's edited sync exits 2 and says
    "held back". The file is untouched.
  - `TestHeldBackUpdateIsRemembered`: Alice publishes 0.2.0. Bob (auto) edited. `sync`
    Exits 2 with the next steps. `status` says "0.2.0 held back: you edited planner.md".
    The server is stopped and `notices --hook claude` with `{"cwd":...,"source":"startup"}`
    still prints the JSON line. Bob restores the file and `notices` prints nothing.
    Bob edits again, `install lime/planner --force`, and `status` shows nothing behind.
  - `TestNotifyWarnsAboutEdits`: notify install, edited, `sync` says "update available,
    you edited 1 file" and exits 0.
  - `TestSessionHooks`: a `settings.json` with the person's hooks. `setup --hooks` keeps
    them and adds one. Again changes nothing. `setup --no-hooks` restores the original
    members. Copilot's file appears only with `~/.copilot`.
  - `TestInstallMerge` (step 7): Bob edits a line, Alice changes another, `install
    --merge` keeps both; a second case with the same line gives markers, `status` says
    unresolved, removing the markers clears it, the next `sync` is up to date.

No page changes, so no UI review.

## Risks

- **Breaking a person's `settings.json`.** The worst outcome of this plan. Mitigated by
  refusing anything that does not parse, never writing through a link, copying untouched
  values raw in their order, a one-time backup, the read-again check before the rename,
  golden and fuzz tests, and consent (decision 1). Claude Code may write the file itself
  at the same moment. The window is small and the check catches most of it.
- **Noise.** One line per new session while something is held could annoy. It names a
  real problem, says one command, and stops once the person merges, forces, pins, or
  runs `setup --no-hooks`. Claude's matcher is `startup` only, and `notices` ignores
  other sources for the tools that ignore matchers. VS Code with `chat.useClaudeHooks`
  reads both files and may say it twice.
- **Copilot shows nothing directly.** Its model gets the text and is asked to mention
  it. A model may not. Status, sync and Pushover remain the reliable channels.
- **Stale state.** A sync from weeks ago says nothing about today's releases. `status`
  prints when the state was checked. Every line is re-checked against the lock and the
  disk, so the state never claims something already fixed.
- **Exit code 2 every run.** A cron mailing on failure mails daily while an update is
  held. That is the point for `auto`. `policy pin` is the quiet "keep mine". Scripts
  that treated any non-zero as a registry failure need to learn 2. The GUIDE says so.
- **Conflict markers in a live file.** After `--merge` with conflicts, Claude Code and
  Copilot read the markers until the person resolves them. The CLI, status and the hook
  say so. A person who wants to look first runs `loadout diff` and can still use
  `--force` or reinstall the old version.
- **Wrong base.** Guarded by the hash check. A mismatch becomes a whole-file conflict,
  never a silent wrong merge.
- **Memory.** A merge holds the base, the next release and the disk files: for a 64 MB
  knowledge base about 200 MB at worst. Only edited files are merged line by line.
  Acceptable for a one-off command.
- **Privacy.** Pushover gets a count, not file names. The state file lists paths and is
  0600 like the lock.
- **PATH in hooks.** A tool started from the Dock may not see `~/go/bin`. The absolute
  path fallback handles it, and the guard keeps a moved binary silent rather than noisy.

## Decisions for the user

Settled as recommended. See Decided, items 5 to 7.

1. **Hooks on by default.** Recommended: `login` sets up the hooks (and says so in one
   line, with `loadout setup --no-hooks` to remove them), and existing users get them
   with `setup --hooks`, suggested once by sync when something is held. The person who
   most needs the message is the one who never opts in. The alternative is opt-in only,
   with `setup --hooks`.
2. **Exit code for a held-back `auto` update.** Recommended: exit 2, every run until it
   is resolved (failures stay 1), so cron and scripts notice and can tell it from a
   registry outage. `notify` installs with edits stay 0.
3. **Pushover once per version for every update, not only held ones.** Recommended: yes.
   The same record makes it free, and a daily repeat of "1.3.0 available" from cron is
   the same noise this plan removes for held updates. The alternative keeps today's
   push on every sync for plain available updates.

The merge is `install --merge`, not a new `update` command: `install` is already how a
person takes a version (Pushover says "Update with: loadout install"), and it already
has `--force` beside it.

## Not in this step

- Merging during `sync`. Sync runs unattended. Writing conflict markers into live agent
  files from cron is not something it should do.
- Merging built-in skills: the old text is not in the new binary. `setup --skills --force`
  stays their way out.
- Hooks at project scope (committed settings that teammates would run), and hooks for
  tools without an adapter.
- Telling the registry or the team that someone holds an update back: the registry never
  learns what a person edited.
- More notification channels (Slack, Teams, email), still one package each.
- A desktop notification from cron (macOS `osascript`, Linux `notify-send`).

## Notes from the implementation

### Step 1

- `syncstate.Store.Update` starts from the empty state when the file is damaged (or has
  another format version) instead of failing, so the next sync repairs it. `Load` still
  reports the damage with the remedy. The state is only hints, so losing it costs at most
  one repeated notification.

### Step 2

- `syncstate.Blocker` gained `Owner` (the package a file belongs to, for "another package
  of the same layer"), so the next steps can name it.
- The conversion from `*install.ConflictError` to blockers lives in `syncstate.Blockers`,
  shared by `update` and `builtin`. `syncstate` therefore imports `install`.
- `builtin` owns a one-method `Memory` (only `Update`). It reads nothing back.
- A `notify` install whose model settings changed is checked for edits too ("model settings
  changed, you edited 1 file"), as an install would stop there as well.
- `pin` and fully shadowed installs drop their record: pin is the quiet way out.
- When no file in the way is the person's edit (only files loadout did not write), RESULT
  reads "held back by files in the way" and the force line "Replace these files with V".
- `docs/CHANGES.md` is left to the release, as asked.

### Step 3

- `notice.Kind` has `Available` and `Held` only. `Unresolved` comes with the merge (step 6).
  `Notice` gained `Edits` (the files are the person's edits, not files loadout did not
  write), `Builtin` (the way out is `setup --skills`), `Command` and `Name`.
- Files in the way for another reason than an edit (loadout did not write them) cannot be
  checked against the lock. They keep a record held until the next sync.
- One agent text for both tools: "Mention it to the user once, in one sentence, in your
  first reply, and suggest loadout status ...". Claude Code also shows the person's line.
- `loadout notices` without `--hook` lists every held install, not only the current folder:
  it is for a person at a terminal.
- Benchmark (`internal/notice/bench_test.go`, M1 Pro): 50 installs, one a 5,000-file
  knowledge base, all held: about 33 ms per `List`, over the 20 ms budget. Almost all of it
  is yaml.v3 decoding the lock's 5,000 file records, which every loadout command pays. The
  notice work itself is a few milliseconds. The hook has a 1 second deadline and a 5 second
  tool timeout, so this is left as it is. When nothing is held the hook does not read the
  lock at all.

### Step 4

Checked on 2026-10-04 against the current docs (code.claude.com/docs/en/hooks,
docs.github.com/en/copilot/reference/hooks-reference,
code.visualstudio.com/docs/agent-customization/hooks and
code.visualstudio.com/docs/agents/reference/hooks-reference) and by hand with a temporary
HOME only. This Mac's own `~/.claude` and `~/.copilot` were not touched.

- **Claude Code 2.1.287, verified by hand.** With `HOME` and `CLAUDE_CONFIG_DIR` in a
  temporary folder and no key in the environment, `loadout setup --hooks` wrote the hook,
  and `claude -p` ran it as `SessionStart:startup`: the transcript holds a
  `hook_system_message` with the person's line and a `hook_additional_context` with the
  agent's, before stopping at "Not logged in", so no model was called. The docs confirm the
  matchers, `systemMessage`, `hookSpecificOutput.additionalContext`, the 600 s default
  timeout, that the same handler in two settings files runs once, and on Windows: shell form
  runs in Git Bash, or PowerShell when Git Bash is missing, and a handler's `"shell":
  "powershell"` picks PowerShell. loadout therefore writes the PowerShell form with that
  field on Windows (`HookCommand.Windows`). Windows itself was not tried.
- **Copilot CLI 1.0.90, not verified by hand.** It stops at "No authentication information
  found" before it loads hooks, and signing it in would use this Mac's credentials. The docs
  confirm `~/.copilot/hooks/*.json`, the `bash`/`powershell`/`timeoutSec` fields, the input
  (`cwd`, `source` of `startup`, `resume` or `new`) and that only `additionalContext` is read
  for `sessionStart`. They also document `{"type":"progress","message":...}` lines that
  Copilot CLI shows to the person, not used, because VS Code reads the same file and parses
  stdout as one JSON object.
- **VS Code 1.138.0, not run** (no headless way to start an agent session). The docs confirm
  it reads `~/.copilot/hooks/*.json` in Copilot's format (mapping `bash` and `powershell` to
  its own fields), sends its own input (`source` is always `"new"`), ignores matchers, and
  reads `hookSpecificOutput.additionalContext`. New since the research: a top-level
  `systemMessage` is shown to the person for every event. Copilot's output now carries the
  person's line as `systemMessage` too (Copilot CLI ignores it).
- The hook commands were run through `sh -c` with each tool's documented input: about 20 ms,
  the JSON line for `startup` and `new`, nothing for `resume`, a missing binary, an older
  loadout or input that is not JSON, always exit 0.
- **Deviation: no reformatting.** The plan had settings.json rewritten with two-space
  indents. `internal/jsonedit` instead splices loadout's entry in at the siblings'
  indentation and leaves every other byte as it was, so a person's own formatting survives
  and removing the hook gives back their exact file (fuzz tested, about 6.5 million inputs).
  When removing the hook empties `SessionStart` or `hooks`, they go too (a person's own
  empty `"SessionStart": []` included), and a file left with nothing is deleted.
- `hook.Tool` adds `Product()` (from `target.ModelGuide`) for the messages. `Wanted()` is
  `Wish()` returning both `wanted` and `declined`, which the sync tip needs. `setup --target`
  narrows the tools for hooks too, but never creates a tool's folder for a hook.

### Step 5

- Not released, as asked: no deploy, push, merge or `go install`. `docs/CHANGES.md` is left
  for the release. The guide was rebuilt with `docs/guide/build.sh` in each step that
  changed it. README now names `notices`, `setup --hooks`, the status footer and sync's exit
  code 2. The registry does not change, so a release needs no database backup.

### Step 6

- `textdiff.Whole(base, ours, theirs []byte, l)` (base nil leaves its section empty, for a
  file with no known base) and `textdiff.Marked(text, l)`, which `rebase.Unresolved`,
  `Installer.Marked` and the notices share.
- The base table is chosen as the plan says (the current table, then the empty one for an
  entry recorded without one), and each base file is used only when it hashes to the lock.
- `Installer.Merge` refuses a base other than the installed version, and reports dropped
  files the person edited in `MergeResult.Kept`. A file you deleted counts as not edited and
  comes back from the release.
- The `Merged` record is written by `notice.Service.RecordMerge` (so `notice.Memory` gained
  `Update`). `Installer.Marked` checks markers, so notice still reads no file itself. A merge
  without conflicts drops an earlier record. `merged:` is a new list in `syncstate.yaml`
  under the same format version: a loadout from steps 2 to 5 would read such a file as
  damaged, treat it as empty and start it afresh on its next sync, which costs one repeated
  notification.
- Not tested separately: shadowed files during a merge (`splitShadowed` keeps them out of
  the write list exactly as in `Install`).

### Step 7

- `install --merge` prints one line per file it merged, kept or conflicted, and one count
  for the files that took the release ("took the release 2 files you had not edited"), so
  a knowledge base does not print thousands of lines. Exit 0 with conflicts left: they are
  reported, and `status`, `notices` and the hooks repeat them.
- `--merge` also refuses: no install in the scope, an install from a folder (the lock has
  no remote), a release equal to the installed version, and `--all`. New model settings
  over edits (a model-only hold) have no release to merge, so sync offers `--force` and
  `pin` for them, not `--merge`, nor is `--merge` offered when a file in the way is not an
  edit, since the merge would refuse it.
- sync's next steps: `--merge`, `--force`, `pin`, then `diff` last (the plan dropped
  `diff`. It stays as the way to look first).
- The merged files are recorded with the time of the merge (`syncstate.Merged.At`), which
  `status` shows in its "As of" line when only a merge is left to resolve.

### After testing

- A second `install --merge` while the first one's markers were still in a file reported
  "merged" and dropped the record, so status, notices and the hooks went quiet. Now, as
  `loadout rebase` does, `--merge` refuses while an earlier merge of that install is
  unresolved (`notice.Service.Unresolved`), naming the files and the ways out (resolve the
  markers, or `install --force`). A `Merged` record shows while its markers are in the
  files, whatever version is installed since, and `RecordMerge` never drops a record whose
  markers remain.
- Without a notification channel the notifier is nil and nothing is marked told, so
  setting up Pushover later still sends each pending update once.
