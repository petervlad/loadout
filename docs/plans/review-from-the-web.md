# Review from the web

Status: built and deployed (2026-09-29).

## Decisions

- Context comes from the registry (latest release of every package in the team's
  layers), not from the reviewer's lock: the same for every reviewer, listed exactly in
  the report, and it works for someone who installed nothing.
- `loadout review PKG` uploads by default when the "to" side is an open proposal;
  `--no-upload` keeps it local. The upload line names the proposal and the team that sees
  it, since full access can put the reviewer's own files into answers.
- Copilot in safe mode stays as it is and gets the notice (see Caveat); no read-only tool
  list is added for it. Full access is the normal way to review.
- The warning before a full-access run names the author by the email in their token
  label (`alice@example.com via loadout login` shown as `alice@example.com`); the API
  gets no display names.
- Only the person who added a prompt edits or removes it. Maintainers get no override;
  the activity list shows who added what.

## Goal

A reviewer opens a proposal on the website, adds the prompts they care about, copies one
short command (`loadout review koala/koala-frontdesk`) and gets both versions
answered in their own AI tool with the same access they normally have, next to the team's
other skills and knowledge bases. The result lands on the proposal page. Three changes:

1. Compare runs each side with the team's packages beside it, and without the restricted
   permission mode unless `--safe` is given.
2. Prompts live on the registry as a list people edit on the proposal page; compare always
   uses the current list.
3. `loadout review PKG` picks the two versions itself; `review compare ID` stays.

The one rule holds: the registry stores text and never runs anything. Every run still
starts on the reviewer's machine, in their own Claude Code or Copilot CLI, on their
subscription. Prompts added on the web are untrusted text that only a reviewer's own
command sends to their own tool.

Found on the live registry (proposal fd9af97ec0b9): the throwaway folder held only the
package under review, and `--permission-mode dontAsk` refused reading `koala-kb` and
`~/work`, so both sides answered thinly.

## Design

### review: modes, context and a richer report

- `review.Mode` (`ModeFull`, `ModeSafe`), a field of `Options` and of `Request`. The
  zero value is invalid (`Compare` refuses it), so no caller gets full access by accident;
  the CLI sets it from `--safe`.
- Runners map the mode to flags; nothing else knows them:

| Runner | Full (default) | Safe (`--safe`, today's behaviour) |
| --- | --- | --- |
| `ClaudeCode` | `--permission-mode bypassPermissions` | `--permission-mode dontAsk` |
| `CopilotCLI` | `--allow-all` (tools, paths, URLs) | no allow flags, and `COPILOT_ALLOW_ALL` removed from the env |

  The working directory stays the throwaway folder in both modes. `withoutLoadoutEnv`
  keeps removing `LOADOUT_*`. In safe mode the Copilot runner also drops
  `COPILOT_ALLOW_ALL`, since that variable would silently undo safe.
- A runner may say what a mode cannot do, through an optional interface the Comparer
  checks, so the knowledge stays next to the flags and no `switch tool` appears elsewhere:

```go
// Caveat is implemented by runners whose tool cannot fully review in a mode.
type Caveat interface {
	Caveat(m Mode) string // "" when there is nothing to say
}
```

  `CopilotCLI.Caveat(ModeSafe)` returns: "Copilot CLI refuses every tool call in safe
  mode, so the agent answers from the prompt alone: it cannot read skills, knowledge
  bases or files. Run without --safe, or with --tool claude, for a real review."
  (Copilot's help says `--allow-all-tools` is required for prompt mode.) `Compare`
  copies it into `Report.Notes`.
- Hide: the reviewer may have the package under review installed for their user
  (`~/.claude/skills/NAME`), and Claude Code runs a personal skill over a project skill
  of the same name, so both sides could see the reviewer's copy. `Request.Hide` lists
  skill names to turn off for the run; the Claude runner passes them as
  `--settings '{"skillOverrides":{"NAME":"off"}}'`, a per-run setting that changes no
  file and keeps the reviewer's login. Each side is rendered as `current-NAME` or
  `candidate-NAME`, so only the reviewer's own copy is hidden. `Compare` sets Hide to the
  real name of a skill or KB under review.
- Context: `Options.Context iter.Seq2[*pack.Pack, error]`. `Compare` walks it once and
  renders each package into every side's folder with `target.Render` (project scope),
  then drops it, so memory holds one package at a time. Rendering both sides from the
  same sequence is what keeps the comparison fair.
- `side.prepare` becomes a small `workspace` type (one per side) that owns the folder's
  `os.Root`, writes units, and refuses a second unit at the same path. The side's own
  version is written first under `current-NAME` / `candidate-NAME` as today, so a
  context package can never replace it.
- Limits: `MaxContextPackages = 100` and `MaxContextBytes = 256 << 20` (uncompressed,
  counted while rendering, per side). Over either, `Compare` fails before any run with a
  message that names `--no-context`.
- `Report` gains, all additive with `omitempty` so reports already stored still decode:

```go
type Report struct {
	// ...existing fields...
	Mode    Mode             `json:"mode,omitempty"`    // "full" or "safe"; empty in old reports
	Context []ContextPackage `json:"context,omitempty"` // what sat beside each side
	Notes   []string         `json:"notes,omitempty"`   // runner caveats, e.g. Copilot in safe mode
}

type Version struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
	// Source says where the side came from: "release", "installed" or
	// "proposal"; empty in old reports, which read as "release".
	Source     string `json:"source,omitempty"`
	ProposalID string `json:"proposal_id,omitempty"`
}

type ContextPackage struct {
	ID, Version, Kind string
}
```

  `Report.Markdown` prints the mode, the side labels ("Installed 1.2.0", "Proposal
  fd9af97ec0b9 (1.4.0)") and the context list. JSON field names `current` and
  `candidate` stay, because the web page already reads them.

### reviewplan (new package): which two versions, which context

`internal/cli` must not hold the selection rules, and `review` should stay free of the
registry, so a new package resolves a request into a plan. It owns its interfaces;
`*client.Client` and `*release.Service` satisfy them.

```go
type Registry interface {
	Releases(ctx context.Context, id pack.ID) ([]api.Release, error)
	Fetch(ctx context.Context, id pack.ID, version string) (*pack.Pack, error)
	Proposals(ctx context.Context, pkg, status string) ([]api.Proposal, error)
	Proposal(ctx context.Context, id string) (api.Proposal, error)
	FetchProposal(ctx context.Context, p api.Proposal) (*pack.Pack, error)
	Packages(ctx context.Context) ([]api.Release, error) // in the package's own view
}

type Installs interface {
	Installed(ctx context.Context, id pack.ID) ([]lock.Entry, error)
}

type Request struct {
	Package  pack.ID
	From, To string // "" picks the default; a version, or a proposal id
}

type Plan struct {
	From, To Side          // From.Pack is nil before a first release
	Proposal *api.Proposal // set when To is a proposal: its prompts, its author, the upload target
	Context  iter.Seq2[*pack.Pack, error]
	Notes    []string      // e.g. "you have no install; comparing with the latest release 1.3.0"
}
```

Rules, in `Planner.Resolve`:

- From, by default: the reviewer's installed version (`Installs`; the highest version if
  several scopes differ, said in `Notes`); a folder install or a version the registry no
  longer has falls back to the latest release; nothing installed also means the latest
  release.
- To, by default: the newest open proposal of the package; with none, the newest release.
  From equal to To is an error that says to pass `--from`.
- `--from` / `--to` take a version (a release first, then an open proposal of that
  version) or a proposal id (12 lowercase hex characters; a semver always has dots, so
  the two never clash).
- Context: every package of the package's namespace view (its team and the layers
  above, the same list `install --all` sees), latest release of each, all kinds, without
  the package under review. When two layers ship the same name the closer layer wins, as
  in the installer. Fetched lazily inside the sequence; each fetch is digest-checked by
  the client as today.

Why the registry and not the reviewer's lock for context: two reviewers get the same
context, the report can list it exactly, it works for a reviewer who installed nothing,
and it matches what a teammate gets from `install --all`. The reviewer's own user-scope
installs stay visible to the tool anyway (see Risks).

### registry: prompts as a list

- `registry.Prompt {Seq int; Text string; Author Actor; CreatedAt, EditedAt time.Time}`.
  `Proposal.Prompts` becomes `[]Prompt` (active ones, oldest first) and gains
  `RemovedPrompts []Prompt` (with `RemovedAt`, `RemovedBy`) for the activity list only.
  `Proposal.PromptTexts() []string` serves every caller that only needs text.
- `ProposalStore` gains three methods; both stores implement them and
  `registrytest/proposalcontract.go` covers them:

```go
// AddPrompt appends a prompt to an open proposal and returns it with Seq set.
// ErrProposalClosed if closed; ErrInvalid at limit active prompts or maxRows rows.
AddPrompt(ctx context.Context, id string, p Prompt, limit, maxRows int) (Prompt, error)
// EditPrompt replaces the text of an active prompt of an open proposal.
EditPrompt(ctx context.Context, id string, seq int, text string, at time.Time) (Prompt, error)
// RemovePrompt marks an active prompt removed; its Seq is never reused.
RemovePrompt(ctx context.Context, id string, seq int, by Actor, at time.Time) error
```

- `ProposalService.AddPrompt / EditPrompt / RemovePrompt` hold the rules. Add: any
  principal that reads the proposal's namespace (the same as `AddReview`, so members of
  a team below may add prompts to a project proposal they can already propose to).
  Edit and remove: only the prompt's adder (`Actor.same`), so the author edits theirs and
  a reviewer removes theirs. Open proposals only. Limits: `MaxPrompts` (20) active,
  `MaxPromptSize` (4 KiB, valid UTF-8, not blank) per prompt as `checkInput` does now,
  and `maxPromptRows = 60` rows per proposal so add and remove cannot grow a table
  without end. Soft removal keeps numbers stable: a stale form can never remove a newer
  prompt that took the same number.
- A proposal still needs 1 to 20 prompts from its author on `propose`; removing the last
  active prompt is refused ("a proposal keeps at least one prompt").

## Data and API changes

- Migration `0010_proposal_prompts.sql`: table `proposal_prompts (proposal_id, seq,
  body, author_token_id, author_label, author_user_id, created_at, edited_at DEFAULT '',
  removed_at DEFAULT '', removed_by_token_id DEFAULT '', removed_by_user_id DEFAULT '',
  PRIMARY KEY (proposal_id, seq))`, filled from every existing row with
  `json_each(proposals.prompts)`, author and time taken from the proposal. Old rows stay
  valid: each old prompt becomes the author's. The `prompts` column stays and is still
  written on `CreateProposal` with the author's prompts, so an older binary after a
  rollback still works; nothing reads it any more.
- `api.Proposal` keeps `prompts []string` (the active texts, so old CLIs compare with the
  current list) and adds `prompt_list []api.Prompt` (`seq`, `text`, `author`,
  `created_at`, `edited_at`). New clients fall back to `prompts` from an old registry.
- Routes in `internal/server/proposals.go`, same auth and `withID`:
  - `POST /v1/proposals/{id}/prompts` `{"text"}` in, `PromptResponse` out, 201.
  - `PUT /v1/proposals/{id}/prompts/{seq}` `{"text"}` in, `PromptResponse` out.
  - `DELETE /v1/proposals/{id}/prompts/{seq}`, 204.
  Bodies capped at 64 KiB, strict JSON. Client methods `AddPrompt`, `EditPrompt`,
  `RemovePrompt` for tests and later CLI use.
- `review.Report` JSON gains `mode`, `context`, `source`, `proposal_id` (above).

## CLI and web changes

CLI (`internal/cli/review.go`, flags and printing only; `reviewplan.Planner` and
`review.Comparer` reach it through `cli.Deps`):

- `loadout review PKG [--from V|ID] [--to V|ID] [--safe] [--no-context] [--no-upload]`
  plus today's `--tool --runs --timeout --model --prompt --out`. The parent `review`
  command gets `Args: cobra.ExactArgs(1)` and a `RunE`; subcommands are found first and a
  package id always has a slash, so `review list` still means list.
- Before any run it prints one line to stderr, for example:
  `warning: running proposal fd9af97ec0b9 by alice@example.com with full access to this
  machine (files, shell, web); add --safe to refuse anything that needs approval.`
  For two releases it names the package instead of an author.
- With `--safe --tool copilot` it prints the runner's caveat as a notice before the run
  (`notice: Copilot CLI refuses every tool call in safe mode, ...`). The run still goes
  ahead, since a prompt-only answer can be what the reviewer wants; the notice is also
  the first line of the report.
- Upload: by default when To is an open proposal; `--no-upload` keeps the report local.
  Any other To (a release, a closed proposal) writes the file only and says why.
- `review compare ID` stays as an alias for `review PKG --to ID --from <latest release>`
  with its old defaults (upload only with `--upload`), so scripts keep their meaning; it
  gains `--safe` and runs full by default like everything else.
- Output lists both sides with their source, the mode, and the context packages.

Web (`internal/web/ui_reviews.go`, `templates/ui/proposal.html`, `static/style.css`):

- A Prompts panel on the proposal page lists the active prompts with who added each.
  Open proposals get an "Add a prompt" `<details>` with a textarea (maxlength 4096) and
  a count ("7 of 20"); each prompt the viewer added gets Edit (a `<details>` form) and
  Remove (a popover with Cancel, as Reject does). Plain POST forms with the `csrf` field,
  redirect with a `done` notice, errors through `h.refused`: no JavaScript needed.
- Routes: `POST /t/{ns}/reviews/{id}/prompts`, `POST /t/{ns}/reviews/{id}/prompts/{seq}`,
  `POST /t/{ns}/reviews/{id}/prompts/{seq}/remove`. The literal `prompts` is more specific
  than the existing `{action}` pattern, so the mux has no conflict.
- A prompt newer than the shown report gets a small "not run yet" tag, found by
  comparing texts with the report's prompts.
- The Try it box shows `loadout review TEAM/NAME` when this is the package's newest open
  proposal, else `loadout review TEAM/NAME --to ID`, with one sentence: it compares the
  version you have installed with this proposal and runs with your tool's full access.
- Column heads read from `Version.Source`: "Installed · 1.2.0", "Latest release ·
  1.3.0", "Proposal · 1.4.0"; old reports keep "Current". The report bar adds "Full
  access" or "Safe mode" and a "With 6 team packages" disclosure listing the context.
  A report with `notes` shows them in a notice above the answers, so a teammate reading
  a Copilot safe-mode report knows the answers had no tools.
- Activity gains "added a prompt", "edited a prompt", "removed a prompt"; `names()`
  collects the prompt authors too.
- Prompts are escaped text with line breaks kept, like notes; never Markdown.
- Check light and dark from the tokens, 390 px (the aside stacks, the textarea is full
  width), 44 px touch targets, keyboard order, no JavaScript. Record the review in
  `docs/UI.md`.

## Order of work

1. review: `Mode`, runner flags, `COPILOT_ALLOW_ALL` in safe mode, `Report.Mode`,
   `Version.Source`, Markdown. `compare` keeps working (defaults to full, `--safe`).
2. review: `workspace`, context rendering from `iter.Seq2`, limits, `Report.Context`.
3. reviewplan: `Planner.Resolve` with the from/to rules and the context sequence.
4. CLI: `review PKG`, the warning, upload by default, `compare` as alias; wiring in
   `cmd/loadout`.
5. registry: `Prompt`, store methods, memstore, contract suite, sqlitestore and migration
   0010, service rules.
6. server and client: prompt routes, `api.Prompt`, `prompt_list`.
7. web: Prompts panel and routes, Try it command, side labels, mode and context in the
   report bar, activity; UI review and `docs/UI.md`.
8. Docs: DESIGN.md (Review, Rules that protect users, Web pages), GUIDE.md (Review a
   proposal, command table), rebuild with `docs/guide/build.sh`. Back up the live
   database, then deploy (step 5 migrates).

Each step passes `make check` on its own.

## Tests

- `review`: `CopilotCLI.Caveat` is set only for safe mode, `ClaudeCode` has none, and
  `Compare` copies it into `Report.Notes` and the Markdown.
- `review`: the Claude runner turns `Request.Hide` into one `--settings` JSON argument
  (names JSON-encoded, never spliced as text).
- `review`: tables for runner arguments per mode (fake binary via `runTool`), env
  filtering (`LOADOUT_*` always, `COPILOT_ALLOW_ALL` in safe), zero `Mode` refused;
  context rendered identically into both sides, the side's unit never replaced, limits
  hit before any run; old report JSON (no `mode`, no `source`) still decodes.
- `reviewplan`: table over installed / not installed / folder install / several scopes,
  open proposal / several / none, `--from` and `--to` as version or id, equal sides,
  layer shadowing in the context; fakes for `Registry` and `Installs`.
- `registrytest`: add, edit, remove, limits (20 active, 60 rows), closed proposal,
  numbers never reused, removal of the last prompt refused, concurrent adds under the
  limit; runs against the memstore and SQLite. Migration test: a 0009 database with
  JSON prompts reads back the same prompts, authored by the proposal's author.
- `registry`: who may add, edit, remove (adder, author, other member, team-below member,
  machine token, closed proposal).
- `server`: route tables for status codes, body limits, unknown fields.
- `cmd/loadout` e2e with `person`/`newSession` and `fakeAI`: `review lime/planner` picks
  Carol's installed 0.1.0 and Alice's proposal, uploads, and the run saw the team's KB in
  its folder; `--safe` and full reach the fake as `Request.Mode`; `--no-upload`;
  `--from/--to` with ids and versions; `review compare ID` unchanged; a prompt added over
  the API is what the next compare runs; `--safe --tool copilot` prints the notice.
- `internal/web`: add, edit, remove by role (form posts with CSRF, missing token refused),
  other people's prompts show no buttons, limits shown as a refusal page, Try it command
  text, "Installed" label from a report with `source`, old report still "Current".

## Risks

- Full access runs a teammate's text with the reviewer's rights: it can read and change
  any file, run commands and reach the network. This is the user's decision; the warning
  line, the report and the page all say which mode ran, and `--safe` stays one flag away.
  Update DESIGN.md "Rules that protect users" with: review runs start in a throwaway
  folder, full access by default, safe on request, `LOADOUT_*` never reaches the tool.
- Uploaded answers may quote the reviewer's private files, now that the tool can read
  them. The upload line names the proposal and the team that will see it.
- Claude Code runs a personal skill (`~/.claude/skills`) over a project skill of the same
  name. For the package under review this is handled (see Hide above). For context
  packages the reviewer also installed for their user, both sides see the reviewer's
  copy, which may be an older version; that is fair (both sides get the same one) and
  the report says which context packages the reviewer has installed for their user.
- Copilot may have the same shadowing and has no per-run setting known to hide one
  skill; the report notes a user-scope install of the reviewed package for Copilot runs.
- `bypassPermissions` can be disabled by managed settings or refused for root; the run
  then fails with Claude's message and the CLI suggests `--safe`.
- Context size: a team with many large KBs downloads a lot per review. The limits stop it
  early; `--no-context` skips it.
- Migration 0010 relies on SQLite JSON functions (`json_each`), built into
  modernc.org/sqlite; the migration test proves it on the pinned version.

## Open questions

None. One assumption is checked by hand at the start of step 2: that
`--settings '{"skillOverrides":{"NAME":"off"}}'` hides the reviewer's personal copy of a
skill and leaves `candidate-NAME` visible, in the installed Claude Code.

## Not in this step

- A read-only tool list for Copilot in safe mode.
- Running a comparison from the browser; that would need a model on the server.
- CLI commands to add or edit prompts (`loadout review prompt ...`); the API is ready.
- Choosing a context set by hand (`--with PKG`), or the reviewer's lock as the source.
- Attaching a report to a release, or a comparison of two releases, on the registry.
- Hiding the reviewer's own copies of context packages (they are fair to both sides).
