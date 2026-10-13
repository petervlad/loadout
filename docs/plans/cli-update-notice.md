# Tell people when a newer loadout CLI is out

Status: built and deployed (2026-10-04, tag v0.1.0). The Homebrew cask upload stays off until downloads are public.

## Decided

The user settled the three decisions as recommended (2026-10-04):

1. **Release tags.** Tag `v0.1.0` and deploy from tags, so the registry advertises the
   release it was built from. Keep `release.yml`, with the Homebrew cask's
   `skip_upload: true` so a tag does not fail without `HOMEBREW_TAP_TOKEN`, until downloads
   are public. The tag is created and pushed by the release step, with the user's yes,
   never by an implementation step.
2. **How often.** Once a day per version for a person, for scripts and cron, and for
   agents (one clock shared with the session hook). "too old for this registry" every
   time. `LOADOUT_UPDATE_NOTICE=off` silences all but "too old". Pushover once per version,
   from `sync` only.
3. **No self-update** in this plan.

## Goal

When a newer loadout CLI is available, the person using an older one hears about it, in
one short line, without loadout adding a network call or slowing any command down. When
an agent (Claude Code, Copilot, any program) runs the CLI, the line is written for the
agent and asks it to pass the news to the person. A registry can also say that a CLI is
too old for it, which is said every time, since commands may fail.

The one rule holds. Nothing here calls a model or needs a key. The registry adds two
response headers with version numbers. The CLI compares them with its own version and
prints text. What an agent does with that text is the person's own tool at work, and the
text never contains anything the registry chose except two version numbers.

## What exists today

- **The CLI's version.** `var version = "dev"`, set only by `-ldflags "-X main.version=..."`
  (`cmd/loadout/main.go:47-48`). `loadout version` prints `loadout <Deps.Version>`
  (`internal/cli/version.go:14-16`). The same string goes to the log (`main.go:98`), the
  built-in skills (`main.go:129`), `cli.Deps.Version` (`main.go:169`) and the server's
  health answer (`main.go:247`).
- **How people get it.** Built from a clone: `go install ./cmd/loadout`
  (`docs/GUIDE.md:83-93`, "Ready-made downloads come later", the `loadout-setup` skill says
  the same at `internal/builtin/skills/loadout-setup/SKILL.md:21`). `go install` sets no
  ldflags, so every colleague's `loadout version` says `dev`. Go 1.24 and later do stamp
  the module version from git into the binary: this worktree's build carries
  `v0.0.0-20261004192155-b42ddb9b6aba` with `vcs.revision`, `vcs.time` and
  `vcs.modified=false` (checked with `go version -m`), which loadout never reads.
- **Release builds exist on paper only.** `make build` stamps `git describe --tags --always
  --dirty` (`Makefile:2-3`). `.goreleaser.yaml:20-21` stamps the tag, builds archives and a
  `checksums.txt` (`:37-39`), and pushes a Homebrew cask to `petervlad/homebrew-tap`
  (`:62-85`) with `HOMEBREW_TAP_TOKEN`. `.github/workflows/release.yml:3-5` runs it on a
  `v*` tag. `scripts/install.sh` finds the latest release through GitHub's redirect
  (`:51-61`) and checks the archive against `checksums.txt` (`:76-82`). None of it has run:
  the repository has no tags (`git tag` is empty), and `github.com/petervlad/loadout`
  answers 404 to anyone signed out, so the install script and release downloads would not
  work for colleagues today.
- **The registry's version.** `deploy/deploy.sh:46,55` stamps `git describe --tags --always
  --dirty`, which without tags is a bare commit hash (`b42ddb9`, or `b42ddb9-dirty`).
  `GET /v1/health` returns it only to a caller with a valid token, "so anonymous scanners
  cannot pin the exact code running" (`internal/server/server.go:116-125`,
  `api.HealthResponse` at `internal/api/api.go:90-95`). Neither the registry nor
  `loadout.lat` serves binaries or a "latest version" file (`deploy/Caddyfile:64-77` serves
  the static site only).
- **What the client sends.** Every request carries `User-Agent: loadout/<version>`
  (`internal/cli/connect.go:79`, `internal/client/client.go:377`). The server never reads it.
  Every client the commands use is built in one place, `Deps.newClient`
  (`connect.go:78-89`), and every response passes through `Client.send`
  (`client.go:364-390`). Every authenticated route passes `server.auth`
  (`internal/server/middleware.go:114-150`).
- **Compatibility today is per feature.** The client retries without fields an older
  registry refuses (`internal/client/proposals.go:52-63`), treats a 404 as "older registry"
  (`internal/cli/team.go:198`, `internal/client/manage.go:42`), and the wiki says "it needs
  a newer loadout on the registry" (`internal/cli/wiki.go:317`). Nothing says the other
  direction: "your CLI is older than this registry wants".
- **Built-in skills version.** `builtin.Version` turns `v1.4.0` and `1.4.0-3-gabc` into
  1.4.0, anything else into 0.0.1 (`internal/builtin/builtin.go:89-102`).
- **No JSON output.** No command has `--json`. Scripts parse tables on stdout. Only two
  places write to stderr on purpose (`internal/cli/reviewrun.go:148`, `wiki.go:77`), and
  `runWith` prints `error: ...` there (`main.go:203`).
- **Error codes and the end of a run.** `runWith` executes the root command and maps the
  error to an exit code in one place (`main.go:194-219`). Cobra skips post-run hooks after
  an error, so nothing runs after every command today.
- **Agent signals (checked 2026-10-04).** Inside Claude Code 2.1.287 a command's
  environment has `CLAUDECODE=1`, `CLAUDE_CODE_ENTRYPOINT=cli`, `CLAUDE_CODE_SESSION_ID`
  and `AI_AGENT=claude-code_2-1-287_agent`, and neither stdout nor stderr is a terminal
  (seen from this session). Copilot CLI documents `COPILOT_AGENT_SESSION_ID` in every shell
  command it runs. `AI_AGENT` is a convention several tools and detectors use
  (`@vercel/detect-agent`, `expo/agent-cli-detector`). VS Code's agent mode runs commands in
  an integrated terminal, so it looks like a person at a terminal and sets nothing of its
  own beyond `TERM_PROGRAM=vscode`, which a person's terminal has too.

## Design

Four parts, each in the package that owns it:

1. `internal/buildinfo`: what this binary is (its version as a comparable value, and how it
   was installed). Pure.
2. The registry advertises the latest CLI release it knows and an optional minimum, in two
   headers on authenticated responses (`internal/api`, `internal/server`).
3. The client hands those headers to a callback (`internal/client`). `internal/cliupdate`
   Remembers them in `~/.loadout/cliupdate.yaml` and decides what to say, to whom and how
   often.
4. `internal/cli` prints: one line on stderr after a command, a footer in `status`, a line
   in `notices`, a part of the session hook's message, and `loadout version`.

### Source of truth: the registry's own build

The latest CLI is the **last release tag of the registry's own build**. A registry built
at `v0.4.0` or `v0.4.0-3-gabc` advertises 0.4.0. Reasons:

- The registry is the one thing every person already talks to, with a token, on every
  `sync`, `install`, `login`, `review` and `propose`. No new host, no new request.
- The CLI and the registry are one binary from one repository. The CLI a registry was
  deployed with is, by construction, a CLI that works with it.
- A tag is a deliberate "release this to people". Commit time (which both binaries also
  carry) would nag everyone after every deploy, server-only fixes included, and orders
  commits from different branches wrongly.
- It works for a team's own LAN or on-prem registry the same way, with no internet.

Rejected: GitHub releases (the repository is private, so colleagues cannot read them
without GitHub credentials, and it would add a third-party call to the CLI). A static file
on `loadout.lat` (a second thing to keep in step by hand, a new outbound request, and
wrong for a LAN registry that runs an older release on purpose).

A registry with no release tag in its build (today's live registry, `b42ddb9`) advertises
nothing, and the feature stays silent until the first tag is deployed (decision 1).

The **minimum** is a constant in the server, `server.MinimumCLI`, zero (none) to begin
with. It is raised in the same commit that drops a compatibility fallback an older CLI
needs (for example the `refusedField` retry), so the server code that knows what it still
supports also says it.

### buildinfo: one comparable version for every way loadout is built

Four forms of version reach loadout and must compare correctly:

| Built by | String | Release | After it | Modified |
| --- | --- | --- | --- | --- |
| GoReleaser on a tag | `0.4.0` or `v0.4.0` | 0.4.0 | no | no |
| `make build`, deploy.sh | `v0.4.0-3-gabc1234[-dirty]` | 0.4.0 | yes | with `-dirty` |
| `go install` on a tagged commit | `v0.4.0[+dirty]` | 0.4.0 | no | with `+dirty` |
| `go install` after a tag | `v0.4.1-0.20261004192155-b42ddb9b6aba[+dirty]` | 0.4.0 | yes | with `+dirty` |
| `go install`, no tag yet | `v0.0.0-20261004192155-b42ddb9b6aba` | unknown | | |
| tests, `go run`, a bare hash | `dev`, `(devel)`, `b42ddb9` | unknown | | |

```go
// Package buildinfo says what this loadout binary is: the release it is or
// was built after, and how it was installed, so it can be compared with
// the release a registry advertises.
package buildinfo

// Build is this binary's version. The zero value is an unknown build, which
// never counts as behind anything.
type Build struct {
	raw      string         // as `loadout version` prints it
	release  semver.Version // the release it is, or the last one before it
	after    bool           // built from a commit after release
	modified bool           // built from a tree with uncommitted changes
	known    bool
	when     time.Time      // the commit time, when the build says it
}

// Parse reads a version string in any of the forms above.
func Parse(s string) Build

// Current is the running binary: the ldflags version when it parses, else
// the module version and vcs settings Go stamped into the binary, else
// unknown. info is debug.ReadBuildInfo's, passed in for tests.
func Current(ldflags string, info *debug.BuildInfo, ok bool) Build

func (b Build) String() string            // "0.4.0", "0.4.0 (built 4 Oct from b42ddb9)", "dev"
func (b Build) Release() (semver.Version, bool)
// Behind reports whether a release newer than b exists in v: false for an
// unknown or modified build, so developers are never nagged.
func (b Build) Behind(v semver.Version) bool
```

- A pseudo-version `vX.Y.(Z+1)-0.TIME-HASH` means "after X.Y.Z" (Go's own rule), so its
  release is X.Y.Z with `after` set. `vX.0.0-TIME-HASH` without the `-0.` part means no tag
  is reachable: unknown. Prerelease tags (`v1.0.0-rc.1`) are unknown for now, as
  `internal/semver` has no prereleases.
- `Behind(v)` is `known && !modified && release < v`. A build after 0.4.0 is not behind
  0.4.0. It is behind 0.5.0.
- `builtin.Version` delegates to `Parse(s).Release()`, keeping its 0.0.1 for unknown
  builds. The only change is that a pseudo-version after 0.4.0 gives 0.4.0 instead of
  0.0.1. The digest, which sync compares, is unchanged.
- `cmd/loadout` computes `build := buildinfo.Current(version, debug.ReadBuildInfo())` once
  and passes `build.String()` wherever `version` goes today (log, User-Agent, built-in
  skills, `/skills` pages, server health). A colleague's `loadout version` then says
  `loadout 0.0.0 (built 4 Oct from b42ddb9)` or, after the first tag, `0.4.0` instead of
  `dev`.

**Install method**, for the update command. A table, not a switch spread around, in
`buildinfo`:

```go
// Method is how this binary was installed, which decides how to update it.
type Method struct {
	Name   string // "source", "homebrew", "release"
	Update string // the command or the page that updates it
}

// MethodOf picks the first row of the table that matches: a path under a
// Homebrew Caskroom or Cellar, a release build (ldflags set to a tag), else
// a build from source.
func MethodOf(b Build, executable string) Method
```

| Method | Matches | Update |
| --- | --- | --- |
| homebrew | the executable resolves under `/Caskroom/` or `/Cellar/` | `brew upgrade --cask loadout` |
| release | ldflags carried an exact tag | `https://loadout.lat/docs/#1-install-the-cli` |
| source | everything else | `git pull && go install ./cmd/loadout` (in the loadout folder) |

Only "source" applies to anyone today. The other rows cost nothing and keep the text right
on the day downloads become public. The command is always loadout's own text, never
something the registry sent.

### The registry says it: two headers

```go
// internal/api
const (
	// HeaderCLILatest is the newest loadout release the registry
	// recommends, MAJOR.MINOR.PATCH. Absent: it says nothing.
	HeaderCLILatest = "X-Loadout-CLI-Latest"
	// HeaderCLIMinimum is the oldest loadout release the registry still
	// supports. Absent: any.
	HeaderCLIMinimum = "X-Loadout-CLI-Minimum"
)
```

- `server.WithCLI(latest, minimum semver.Version)` sets them. Zero values send nothing.
  `cmd/loadout` passes `build.Release()` (only when known) and `server.MinimumCLI`.
- Set in `server.auth` right after `Authenticate` succeeds (`middleware.go:121`), so every
  authenticated response carries them, errors included (a 403 or 409 is exactly when
  "your CLI is old" helps), and in `health` for a valid token, next to `Version`. An
  anonymous caller gets neither, which keeps the rule that scanners do not learn what runs.
- Two headers instead of a field: every route already passes `auth`, older clients ignore
  unknown headers, no JSON body changes shape, and a bundle download (not JSON) carries
  them too.
- No new route, no new request from the CLI, no migration.

### The client passes them on

```go
// internal/client

// CLIAdvice is what a registry said about loadout versions, as sent. Empty
// strings mean it said nothing.
type CLIAdvice struct{ Latest, Minimum string }

// WithCLIAdvice calls fn with the advice on every response that carries
// any. fn must be safe for concurrent use and fast.
func WithCLIAdvice(fn func(CLIAdvice)) Option
```

`Client.send` reads the two headers after `hc.Do`, each at most 32 bytes (longer is
dropped), and calls `fn`. The client does not parse them: it moves bytes, `cliupdate`
decides. `Deps.newClient` adds the option with a closure that names the remote's URL:
`func(a client.CLIAdvice) { d.Updates.Saw(r.URL, a) }`.

### cliupdate: remember, decide, say

A new package `internal/cliupdate`, built in `cmd/loadout`, reached through
`cli.Deps.Updates`. It owns its small interfaces and opens no network connection: it is
built from the state file and the build only.

```go
// Package cliupdate remembers what registries say about loadout releases
// and decides when to tell the person, or their agent, that this CLI is
// behind.
package cliupdate

// Audience is who will read what loadout prints.
type Audience int

const (
	Person Audience = iota + 1 // a terminal
	Agent                      // Claude Code, Copilot or another agent
	Script                     // no terminal and no agent: cron, CI, a script
)

// Detect decides the audience from the environment and whether stderr is a
// terminal. Agent signals win over the terminal (VS Code's agent runs in
// one and sets nothing of its own).
func Detect(getenv func(string) string, stderrTerminal bool) Audience

// Advice is one registry's word, parsed.
type Advice struct {
	URL     string
	Latest  semver.Version // zero: none
	Minimum semver.Version // zero: none
	Seen    time.Time
}

// Status is what the remembered advice means for this build.
type Status struct {
	Build   buildinfo.Build
	Method  buildinfo.Method
	Latest  semver.Version // the newest any registry advertised in the last 30 days
	From    string         // the host that advertised it
	TooOld  []Advice       // registries whose minimum is above this build
	Checked time.Time
}

func (s Status) Behind() bool
// Line is the one line for an audience; empty when there is nothing to say.
func (s Status) Line(a Audience) string
// Session is the part of the session hook's message.
func (s Status) Session() target.SessionMessage

// Service records advice and decides when to speak.
type Service struct{ /* store, build, method, now, log, seen (mutex) */ }

func New(store *Store, build buildinfo.Build, method buildinfo.Method, now func() time.Time, log *slog.Logger) *Service

// Saw collects one response's advice in memory; safe for concurrent use.
func (s *Service) Saw(url string, a client.CLIAdvice)
// Finish writes what this run saw (only when it changed, or a day after the
// last write) and returns the line for a, at most once a day per version and
// audience; a too-old registry is said every time.
func (s *Service) Finish(ctx context.Context, a Audience) (string, error)
// Status reads the file only, for status, notices and version; it never writes.
func (s *Service) Status() (Status, error)
// SessionMessage is Status().Session() on the agent's clock, for the hook.
func (s *Service) SessionMessage(ctx context.Context) (target.SessionMessage, error)
// Push tells the notifier once per version, for sync.
func (s *Service) Push(ctx context.Context, p Pusher) error

// Pusher sends one message to the person's phone; pushover.Notifier is one.
type Pusher interface {
	Push(ctx context.Context, title, message string) error
}
```

`Saw` takes `client.CLIAdvice` so the callback needs no conversion. `cliupdate` importing
`client` for one struct is acceptable (it already sits on the CLI side). If that grows,
the struct moves to `api`.

**The state file**, `~/.loadout/cliupdate.yaml`, its own file for the same reasons as
`builtin.yaml` and `hooks.yaml`: `syncstate.yaml` and the lock are read strictly by older
loadouts, a new field there would make them call the file damaged, and this has another
writer (every registry command, not only sync) and another lifetime.

```yaml
version: 1
registries:            # at most 16, the least recently seen dropped first
  - url: https://registry.example.com
    latest: 0.5.0
    minimum: 0.3.0     # omitted when the registry sent none
    seen: 2026-10-04T09:00:00Z
told:                  # the last time each audience heard of a version
  person: {version: 0.5.0, at: 2026-10-04T09:00:00Z}
  agent:  {version: 0.5.0, at: 2026-10-04T09:02:00Z}
  push:   {version: 0.5.0, at: 2026-10-04T09:00:00Z}
```

Strict YAML, 0600, written atomically under `internal/flock` (agents run several loadout
commands at once). A damaged or unknown-version file reads as empty and is started afresh
on the next write, like `syncstate`: it costs one repeated line at most. Records not seen
for 30 days are ignored and dropped on the next write, so a registry the person left stops
speaking. A value that does not parse as `semver` is dropped when collected, never stored.

**How often.**

| What | Person (terminal) | Script | Agent, and the session hook | Pushover |
| --- | --- | --- | --- | --- |
| A newer release | once a day per version | once a day per version (the person clock) | once a day per version (one clock shared by agent-run commands and the hook) | once per version, from `sync` only |
| A registry says this CLI is too old | every command | every command | every command | once per version |

The day is counted from `told.<audience>.at`. A new version resets it. `status`, `notices`
and `version` show the line whenever they are run (it is what was asked) and do not touch
the clocks, so they never write.

**When it stays silent.** An unknown or modified build (`buildinfo.Behind` is false, a
too-old warning is also skipped, since a developer's build has no release to compare).
`LOADOUT_UPDATE_NOTICE=off` (in `config`, beside the Pushover variables), which silences
everything but the too-old warning (that one says why commands fail), and the commands
listed under CLI below.

### Texts

Built by `Status`, from fixed words, the two version numbers, the registry's host as
written in the person's own config, and the method's command. Nothing a registry sends
reaches the text except two parsed versions, so a hostile registry cannot put words in
front of an agent.

- Person and script, on stderr after the command's output:

  ```text
  loadout 0.5.0 is out (you have 0.4.0). To update, in your loadout folder: git pull && go install ./cmd/loadout
  ```

- Agent, on stderr:

  ```text
  loadout, for the agent: a newer loadout CLI is available (0.5.0; this is 0.4.0). Tell the user once, in one sentence, and give them the command to update it: git pull && go install ./cmd/loadout, in their loadout folder. Do not update it yourself unless they ask.
  ```

- Too old (person, the agent's version adds "Tell the user now"):

  ```text
  loadout: registry.example.com needs loadout 0.3.0 or newer and this is 0.2.0, so some commands may fail. To update, in your loadout folder: git pull && go install ./cmd/loadout
  ```

- Session hook: `SessionMessage{Person: "loadout 0.5.0 is out (you have 0.4.0): git pull &&
  go install ./cmd/loadout in your loadout folder.", Agent: "A newer loadout CLI is available
  (0.5.0, installed 0.4.0). Mention it to the user once, in one sentence, with that
  command. Do not update it yourself unless they ask."}`.

The agent line names the CLI and says "Tell the user" because Claude Code and Copilot show
stderr to the agent, not always to the person, and the agent otherwise reads it as noise
from the command. The person's line has no instructions, since VS Code's agent mode looks
like a terminal. It is still plain enough for an agent to repeat.

### CLI

`internal/cli` only calls `Updates` and prints.

- **After every command.** `runWith` (`cmd/loadout/main.go`) switches to
  `cmd, err := root.ExecuteContextC(ctx)`, prints the error as now, then calls
  `cli.AfterRun(ctx, deps, cmd, stderr, audience)`, so the line comes last, after an error
  too (that is when "too old" matters). `AfterRun` lives in `internal/cli`, which owns the
  text. `runWith` still only wires and maps exit codes. It is skipped after Ctrl-C, and for
  commands marked with the annotation `quietUpdates`: `notices` (it speaks for itself, and
  `--hook` must print only the tool's JSON), `status` and `version` (they show it in their
  own output), `serve` and the host-side commands run with `--data`, `completion`, `help`.
  It never changes the exit code. An error from `Finish` is logged at debug. For `sync`,
  `AfterRun` also calls `Push` when a notifier is set.
- **The audience** is detected once in `cmd/loadout`: `cliupdate.Detect(getenv,
  stderrTerminal())`, where `stderrTerminal` is `os.Stderr.Stat()` with
  `os.ModeCharDevice` (no new dependency, works on the Windows console), injected through
  `externals` so e2e tests choose.
- **Detect's table** (an agent if any row matches, checked in order):

  | Variable | Set by |
  | --- | --- |
  | `CLAUDECODE` = `1` | Claude Code (verified 2.1.287) |
  | `COPILOT_AGENT_SESSION_ID` non-empty | Copilot CLI (documented) |
  | `AI_AGENT` non-empty | Claude Code and other tools (convention) |

  Then a terminal on stderr is a person, else a script. A new tool is a row.
- **`status`.** Below the notices footer, from `Updates.Status()`, offline:
  `loadout 0.5.0 is out (you have 0.4.0; seen on registry.example.com 4 Oct). To update ...`.
  Nothing when not behind.
- **`notices`.** The plain form adds the same sentence as its last line (it suits a shell
  prompt). `--hook` joins `Updates.SessionMessage` after `notice.Message(ns)` with a new
  `target.SessionMessage.Join`, so the existing JSON shapes carry both. With nothing held
  and nothing behind it still prints nothing. The hook reads one small file more and keeps
  its 1 second deadline. A failure to record the agent clock still prints the message.
- **`version`.** `loadout 0.4.0` (or the build form above) on the first line, as scripts
  and `deploy.sh:120` read it, then, from the cache, `0.5.0 is out (seen on
  registry.example.com 4 Oct). To update ...`, or `up to date as of 4 Oct`, or nothing when
  no registry has said anything. No network call. Any registry command refreshes it.
- **JSON.** No command prints JSON today, so there is no field to add. stdout is never
  touched by this plan. If a `--json` mode comes later, it keeps stdout clean and may carry
  a `cli_update` object.
- **`login`** needs nothing of its own: it calls `GET /v1/me` with the new token
  (`internal/cli/login.go:126`), an authenticated route, so the advice arrives and
  `AfterRun` says it.

### Built-in skills

`loadout-setup` gets a section "When loadout says a newer version is out": tell the person
once, in one sentence, with the command loadout printed. Do not run `git pull` or `go
install` unless they ask. If a registry says the CLI is too old, say that is why a command
failed and suggest updating before trying again. "The line at the start of a session"
(`SKILL.md:87`) names the new line. `references/troubleshooting.md` gets a row for "needs
loadout X or newer". The other three skills get one sentence pointing to `loadout-setup`.
The honesty test (`internal/cli/builtin_skills_test.go`) checks every `loadout` command the
new text names.

### Pushover

`pushover.Notifier` gains `Push(ctx, title, message)`, which `Notify` then uses, so one
HTTP path serves both. `Service.Push` sends "loadout 0.5.0 is out" / "You have 0.4.0.
Update with: git pull && go install ./cmd/loadout" once per version (`told.push`), only
from `sync` (cron), and retries next run when it fails.

### Self-update: out of scope

No `loadout self-update` or `upgrade` in this step. Today there is nothing to download:
the repository is private and has no releases, and everyone builds from a clone, where
the update is `git pull && go install` in a folder loadout does not know. Once releases
are public, a self-update must verify more than `checksums.txt` from the same server (a
signature, for example cosign or minisign, with the key in the binary), replace a running
binary safely on Windows, and refuse Homebrew installs (brew owns that file). That is its
own plan. Printing the right command per install method gets the same result now.

## Data and API changes

- API: response headers `X-Loadout-CLI-Latest` and `X-Loadout-CLI-Minimum` on authenticated
  responses and on health with a valid token. No body, route or status changes.
- Server: `server.WithCLI`, `server.MinimumCLI` (zero).
- Client: `client.CLIAdvice`, `client.WithCLIAdvice`.
- New packages `internal/buildinfo` and `internal/cliupdate`. `builtin.Version` delegates
  to `buildinfo`. `target.SessionMessage.Join`. `pushover.Notifier.Push`.
  `cli.Deps.Updates`, `cli.AfterRun`. `externals.stderrTerminal`, `externals.version` (for
  tests). `config.UpdateNotice` from `LOADOUT_UPDATE_NOTICE`.
- New file `~/.loadout/cliupdate.yaml` (+ `.lock`). The lock, `syncstate.yaml`,
  `builtin.yaml`, `hooks.yaml` and `rebase.yaml` do not change.
- `loadout version` output gains lines after the first. The first line keeps its form.

## Migration

None in the database. An older CLI ignores the headers. An older registry sends none, and
the CLI says nothing. A missing `cliupdate.yaml` is the empty state. After the upgrade,
`loadout version` on a `go install` build prints the build form instead of `dev`.

The live registry advertises nothing until a build with a release tag is deployed. That
deploy changes no schema, so it needs no backup beyond the usual one.

## Order of work

Each step passes `make check` and can ship on its own.

1. **buildinfo.** `Parse`, `Current`, `Behind`, `String`, `MethodOf` with tables of every
   form above. `cmd/loadout` uses `Current`, so `loadout version`, the log, the
   User-Agent and the built-in skills use it. `builtin.Version` delegates. GUIDE (Install
   the CLI: what `loadout version` prints). Shippable alone: no more `dev`.
2. **Release tags** (decision 1). Tag the first release, adjust `release.yml` and
   `.goreleaser.yaml` as decided, and say in `deploy/README.md` that a deploy advertises
   its last tag. No code.
3. **Registry headers.** `api` constants, `server.WithCLI`, `server.MinimumCLI`, set in
   `auth` and `health`. Wiring in `registryOpener`. Server tests. Shippable alone (nobody
   reads them yet). Deploy it with the tag of step 2.
4. **Remember.** `client.WithCLIAdvice`. `cliupdate` with `Store`, `Saw`, `Finish` (no
   output yet), `Status`. `Deps.Updates`, `Deps.newClient` wiring. `AfterRun` that records
   only. Docs: DESIGN packages table and a "CLI updates" section.
5. **Say it.** `Detect`, the texts, the clocks, `AfterRun` printing, `quietUpdates`,
   `LOADOUT_UPDATE_NOTICE`. `status` footer, `notices` line and `--hook` with
   `SessionMessage.Join`. `version` lines. Docs: GUIDE (Stay up to date, Messages in Claude
   Code and Copilot, command reference, Troubleshooting), the `loadout-setup` skill and its
   troubleshooting reference, the other skills' pointer. Rebuild the guide.
6. **Pushover.** `pushover.Notifier.Push`, `Service.Push` from `sync`. GUIDE (Pushover
   paragraph).

## Tests

- **buildinfo** (tables): every row of the version table. `Current` with ldflags set,
  ldflags `dev` plus each kind of `debug.BuildInfo` (tagged, pseudo after a tag, no tag,
  `vcs.modified=true`, `(devel)`), and no build info. `Behind` against older, equal, newer
  releases for a release, an after-build, a modified and an unknown build. `MethodOf` for
  a Caskroom path, a Cellar path, a release build elsewhere, a source build.
  `builtin.Version` keeps its table and gains the pseudo-version row.
- **server**: an authenticated request carries both headers when set and neither when
  zero. A 403 and a 404 still carry them. An anonymous request and health without a token
  carry neither. Health with a token carries them.
- **client**: the callback gets the headers from a JSON call, a bundle download and an
  error response. Values over 32 bytes are dropped. No callback without the option.
- **cliupdate** (tables, fake clock, temp dir): `Detect` for each variable, a terminal, a
  pipe, and an agent variable with a terminal. `Finish` writes only on change or after a
  day. The newest of two registries wins and an old record (31 days) is ignored. An
  invalid version is never stored. The clocks (first time, same day, next day, new
  version, too-old every time, separate person and agent clocks, the hook and agent sharing
  one). `LOADOUT_UPDATE_NOTICE=off` keeps only too-old. An unknown or modified build says
  nothing. A damaged file reads empty and is rewritten. Two concurrent `Finish` calls both
  land. The texts (golden lines per audience and method), with a test that no text
  contains an em-dash. `Push` once per version and again after a failure.
- **cli**: `AfterRun` skips quiet commands and Ctrl-C and never changes the exit code.
  `status` footer. `notices` plain and `--hook` with a held notice, with a CLI line only,
  with both, and with neither (silent). `version` lines.
- **e2e** (`cmd/loadout`, `person`, `newSession`): `externals` gain `version` (the CLI's)
  and `stderrTerminal`. `startRegistry` gains a sibling `startRegistryAt(t, admin,
  "v0.5.0")` that passes that build to `server.New`; `person` gains extra environment.
  - `TestNewerCLIIsAnnounced`: registry at 0.5.0, Bob's CLI 0.4.0 at a terminal. `sync`
    Prints its table on stdout unchanged and the line on stderr. A second `sync` the same
    day prints no line. `status` and `version` show it with the server stopped.
    `notices --hook claude` with `{"source":"startup"}` prints JSON whose `systemMessage`
    and `additionalContext` carry it.
  - `TestAgentIsAskedToTellTheUser`: the same with `CLAUDECODE=1` and no terminal. Stderr
    has the agent line, then with `COPILOT_AGENT_SESSION_ID`.
  - `TestTooOldCLIIsToldEveryTime`: `MinimumCLI` above the CLI (through the option). Every
    command repeats the line, including one that fails. Exit codes are unchanged.
  - `TestDevBuildIsNotNagged`: the CLI's version `dev`. Nothing on stderr.
  - Existing tests run with an unknown build and a registry without a tag, so their stderr
    does not change. A check in the helper fails any existing test whose stderr gains the
    line.
- No page changes, so no UI review.

## Risks

- **Noise.** One line a day per audience, one push per version, and nothing for developers
  (modified or unknown builds). The too-old line repeats on purpose. `LOADOUT_UPDATE_NOTICE=off`
  is the way out.
- **Scripts that read stderr.** Some treat any stderr as failure. The line comes at most
  once a day, never on stdout and never changes the exit code. CI can set the variable.
  The GUIDE says so.
- **Agents misreading it.** An agent could run `git pull` and `go install` unasked, or
  treat the line as the command's failure. The text says "Do not update it yourself unless
  they ask", it is marked as for the agent and comes after the output, and the skill
  repeats the rule. Copilot CLI only shows it to its model, as with the held-back notice.
- **A registry that lies.** A compromised or misconfigured registry could advertise 99.0.0
  or a high minimum: an annoyance only. It names its own host in the line, cannot change
  the words or the command, and the person's update source is their own clone.
- **Version leaks.** Only authenticated callers see the headers, as with health's version.
- **Unreleased commits look current.** A person on a pseudo-version after 0.4.0 sees
  nothing until 0.5.0 is tagged, however old their commit. That is the meaning of a
  release. Tag when the CLI changes in a way people should take.
- **Tags start the release workflow.** Pushing `v*` runs GoReleaser, which needs
  `HOMEBREW_TAP_TOKEN` and publishes to a private repository. See decision 1.
- **Several registries.** The newest wins, named by host. A LAN registry pinned to an older
  release never makes the line go away early.
- **Clock skew.** Days are measured on this machine only. The registry's time is not used.

## Decisions for the user

Settled as recommended. See Decided.

1. **Start release tags, and what the release workflow does with them.** Recommended: tag
   `v0.1.0` now, and from then on tag when the CLI changes in a way people should take.
   Deploy from the tag so the registry advertises it. Keep `release.yml` but set the cask's
   `skip_upload: true` until downloads are public, so a tag does not fail on the Homebrew
   step. The alternative, comparing commit times without tags, works today but would nag
   everyone after every deploy.
2. **Who hears it how often.** Recommended as in the table: once a day per version for a
   person, for scripts and cron, and for agents (one clock shared with the session hook),
   every time for "too old", once per version on Pushover, and `LOADOUT_UPDATE_NOTICE=off`
   to silence all but "too old". The alternative is silence for scripts (no terminal and
   no agent), which keeps cron mails clean but leaves unknown agents uninformed.
3. **Self-update.** Recommended: out of scope (see above), revisited when downloads are
   public and signed.

## Not in this step

- `loadout self-update` or `upgrade`, and signing release artifacts.
- A network check from `loadout version` (`--check`). Any registry command refreshes the
  cache.
- An operator setting to override the advertised version (`LOADOUT_CLI_LATEST` on `serve`).
- Refusing requests from CLIs below the minimum on the server. The minimum only warns.
- Showing on the web which CLI versions people run (the server could read the User-Agent),
  or the latest CLI on the `/skills` page.
- Prerelease tags (`v1.0.0-rc.1`), which `internal/semver` does not model.
- Agent detection for tools that set no variable (Cursor, Codex and others are a row in
  `Detect`'s table when someone uses them).

## Notes from the implementation

### Step 1

- `Build.String()` says "after 0.4.0 (built 4 Oct 2026 from b42ddb9)" for a build after a
  release and "dev (built 4 Oct 2026 from b42ddb9)" for a clone with no tag yet, instead of
  "0.4.0 (built ...)" and "0.0.0 (built ...)": "0.4.0" alone would read as the release
  itself, and "0.0.0" as a release that does not exist. A modified tree adds "modified".
- Machines get `Build.Raw()`, the string as given (the ldflags version, or Go's module
  version): the User-Agent, the server's health answer, the log and `builtin.Version`, so
  no space or bracket lands in a header. People get `String()`: `loadout version` and the
  `/skills` pages. `cli.Deps` keeps `Version` (the raw form) and gains `Build`.
- `Current` falls back to Go's module version even when the ldflags hold a bare hash
  (`make build` without tags), since the module version also carries the commit time.
- `MethodOf` takes the executable's path with links already resolved. Resolving is I/O and
  stays in `cmd/loadout`, so `buildinfo` is pure.
- `externals.version` arrived in this step (one e2e test of `loadout version` uses it).

### Step 2

- No tag was created or pushed: the release step does that, after the user's yes.
- **Deviation: the registry advertises only an exact release.** The plan had a registry
  built at `v0.4.0-3-gabc` advertise 0.4.0. As asked when the decisions were settled, a
  registry advertises its release only when it is built exactly from a clean tag
  (`Build.Exact()`: `v0.4.0`, not `v0.4.0-3-gabc`, `v0.4.0-dirty` or a bare hash). "Deploy
  from tags" then means what it says, and a routine deploy of `main` or of a dirty tree
  never advertises anything. CLIs keep what they last heard for 30 days, so an untagged
  deploy in between does not silence a release they already know about.
- `deploy.sh` and the `Makefile` describe with `--match 'v[0-9]*'`, so another tag never
  becomes a version, and `deploy.sh` says before building which release the registry will
  advertise, or that it advertises none (`deploy/deploy_test.go` pins both).
- `.goreleaser.yaml`: the cask has `skip_upload: true`. A snapshot run with an empty
  `HOMEBREW_TAP_TOKEN` writes the cask into `dist/` and succeeds. GitHub passes an unset
  secret as an empty string, so the release workflow runs without it.
- The main checkout usually holds uncommitted edits, which would make the build
  `-dirty`. The release deploys from a clean worktree of the tag.

**The release step** (run by whoever ships, after the user says yes, `B` is the one-time
header from the `loadout-deploy` skill, step 4):

```sh
cd ~/personal/loadout                      # main, with this branch merged and make check passed
git tag -a v0.1.0 -m "loadout 0.1.0"
git -c http.extraHeader="Authorization: Basic $B" push -q origin main v0.1.0
git worktree add /tmp/loadout-v0.1.0 v0.1.0
LOADOUT_HOST=203.0.113.7 LOADOUT_DOMAIN=registry.example.com sh /tmp/loadout-v0.1.0/deploy/deploy.sh 2>&1 | grep -E 'advertis|Deployed'
#   ==> The registry will tell older loadout CLIs that 0.1.0 is out
#   ==> Deployed v0.1.0 to https://registry.example.com
git worktree remove /tmp/loadout-v0.1.0
```

The deploy migrates no database, so the usual backup is enough. Pushing the tag runs the
release workflow (archives on a private GitHub release, no cask).

### Step 3

- `server.MinimumCLI` is a function returning the zero version rather than a package
  variable, so no test or caller can change it at run time.
- `advertised` in `cmd/loadout` gives `WithCLI` the build's release only for an exact,
  clean release build (see step 2). The server itself takes whatever it is given.
- Headers are set in `auth` before the handler runs, so a 304 for a cached bundle carries
  them too.

### Step 4

- `cliupdate.New` takes an `Options` struct (store, build, method, clock, log) instead of
  positional arguments, so step 5's setting is one more field.
- `Status()` returns a `Status` without an error: an unreadable or damaged file is the
  empty state (logged at debug), as the plan wants for `status`, `notices` and `version`.
- `Finish` only records in this step (`Finish(ctx) error`). Step 5 gives it the audience
  and the line.
- The file layout already has `told` (person, agent, push), so the format does not change
  in steps 5 and 6.
- e2e: `startRegistryAt(t, admin, build, opts...)` serves a registry built as `build`,
  wired through the same `advertised`. `startRegistry` is it with `dev`.

### Step 5

- **Deviation: `MethodOf(Install{Build, Executable, Packaged})`.** "ldflags carried an exact
  tag" also matches `make build` and `deploy.sh` on a tagged commit, which would tell a
  person who built from a clone to download a release. GoReleaser now also stamps
  `-X main.packaged=true`, and only such a build is the "release" method.
  `Build.Stamped` went away with it.
- `Finish(ctx, audience) (string, error)` reads the small state file on every run (the
  clocks live there). It writes only for news, a day-old record or a clock that moves.
- The agent line ends "with the command to update it: git pull && go install
  ./cmd/loadout, in their loadout folder." for a clone and "with the way to update it:
  ..." for the other methods. `loadout version` says "Up to date as of 4 Oct (the newest
  release on HOST is 0.5.0)." and, for a dev or modified build, "The newest release is
  0.5.0 (seen on HOST 4 Oct)." rather than claiming it is up to date.
- `status` prints the footer also when nothing is installed.
- A command that cannot run (the bare `loadout`, which prints help) counts as quiet, like
  `help` and `--help`.
- e2e sessions gained `env`, `version` and `terminal`. `session.exec` runs every command
  through `runWith` with `realExternals()`, and fails any test whose unknown build is told
  about a newer loadout.

### Step 6

- `LOADOUT_UPDATE_NOTICE=off` also keeps the newer-release push out. A registry that needs
  a newer loadout is still pushed, once per release (the decision was "silences all but
  too old").
- The too-old push is counted on the same push clock as the release that is out (a
  too-old build is always behind it), so a person gets one push per release, not one per
  release and one per minimum.
- `cli.Deps.Pusher` is the `*pushover.Notifier` when Pushover is set up, nil otherwise. A
  failed push is a warning on the terminal (as a failed held-back push is) and never fails
  `sync`.
- Not released, as asked: no tag, push, merge or deploy, and `docs/CHANGES.md` is left for
  the release. The release step is under step 2.

### After testing

- The line after a command and the session hook wait at most a second (`lockWait`) for
  another loadout holding `cliupdate.yaml.lock`, then say nothing new (a too-old warning
  still). Under the lock, a run that has nothing to change writes nothing.
- `internal/flock` backs off with jitter, capped at 100 ms: waiters started together (an
  agent's parallel commands) polled in lockstep, so only one could win per 250 ms round
  and a 1 second wait was not enough for six.
- `Push` decides again under the lock and sends while holding it, so two syncs at once
  send one push.
- A tag with build metadata (`v1.2.3+build`) is compared as 1.2.3 but is never exact, so
  the registry advertises nothing, as `deploy.sh` already says.
- `setup --hooks` and login now say the hook also tells when a newer loadout is out.
