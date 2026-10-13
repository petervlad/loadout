# Proposals behind the latest release

Status: built and deployed (2026-10-04).

## Goal

A proposal is made from one release of its package (its base) and asks to become the
next one. When someone releases a newer version while the proposal is open, approving it
as it is releases the author's files, which do not have the newer release's changes:
everyone's next sync silently undoes them. The user found this: "a PR open from version
2, an admin published version 3. Warn the PR to rebase. We do not have the functionality
of choosing to merge what into what."

Three changes:

1. The registry records each proposal's base and who published each release, and says,
   everywhere a proposal shows (the proposal page, the reviews list, `review list`,
   `review show`, `review PKG`), what it was made from, what it would become, and
   whether a newer release came out since ("Behind 3.0.0", "3.0.0 by Dana").
2. Approving a proposal that is behind needs an explicit "approve over 3.0.0", which is
   recorded. A release landing while someone approves is caught in the approval's
   transaction.
3. The author brings a proposal up to date with `loadout rebase`: the CLI fetches the
   base, the latest release and the proposal, merges them file by file and line by line
   on the author's machine, writes conflicts with markers for the author to resolve in
   their own editor or AI tool, and the next `loadout propose` replaces the old proposal
   in one step, keeping its prompts. `propose` and `publish` both refuse a folder that is
   behind the latest release, unless `--base` says otherwise.

The one rule holds. The merge is a deterministic three-way line merge in Go
(`internal/textdiff`), the same kind of code as the diffs loadout already shows. It runs
in the author's CLI, never on the registry, and nothing calls a model or needs a key.
Resolving conflicts is the author's job, in their own tool, as with the wiki's stale
push.

## Decided

The user answered the plan's first open questions (2026-10-04):

1. `propose` refuses a folder that is behind the latest release, before uploading.
   `--base V|latest` overrides.
2. Direct `publish` gets the same check, through the same code (`rebase.Locate`) and the
   same `--base` override.
3. The pages say "Behind 3.0.0", not "Out of date".
4. Releases record who published them, so the notice says "3.0.0 by Dana". Releases
   from before this change have no person and show the version alone.

## What exists today

- `registry.Proposal` has no base. `ProposalService.Propose` only checks that the
  version is above the latest release (`checkAboveLatest`). `ApproveProposal` re-checks
  that in its transaction, so a release at or above the proposed version fails approval
  with `ErrVersionConflict` (e2e `TestRejectWithdrawAndStaleApproval`). A release below
  the proposed version (proposal 3.1.0 made from 2.0.0, then 3.0.0 is published) passes,
  and is the bug.
- The web pages guess a base: `handler.baseVersion` in `internal/web/ui_reviews.go` takes
  the newest release below the proposal's version, with one `Releases` call per row of
  the reviews list. For the case above it shows "3.0.0 → 3.1.0" and the diff from 3.0.0,
  which reads as if the author deleted 3.0.0's changes. `review show` diffs against the
  latest release (`currentRelease` in `internal/cli/review.go`), with the same effect.
- Diffs: `textdiff.Unified` (LCS, at most 5,000 lines a file), the web's `handler.diff`,
  `diffLines`, `diffOrder` and `templates/ui/diff.html`, and the CLI's `writeDiff`. They
  compare any two file maps, so all new diff views reuse them.
- Precedents to follow: the wiki's base revision and `ErrStale` (`registry/wiki.go`),
  `wiki.Checkouts` (a 0600 strict-YAML record in `~/.loadout` per absolute file path),
  and "Approve as owner…" (an explicit, recorded step, a `details` popover without
  script, `as_owner` sent only when set so old registries keep working).

## Design

### registry: a proposal's base and where it stands

A small immutable value names a release or none, so "no release yet" is the useful zero
value and never a fake `0.0.0`:

```go
// Base names one release of a package, or none: what a proposal was made
// from, or a package's latest release. The zero Base is none (before the
// first release).
type Base struct {
	version semver.Version
	set     bool
}

func BaseAt(v semver.Version) Base
func ParseBase(s string) (Base, error) // "" is none
func (b Base) Version() (semver.Version, bool)
func (b Base) String() string // "" for none
// Before reports whether b is older than o; none is older than any release.
func (b Base) Before(o Base) bool
```

- `Proposal` gains `Base Base` (stored), `ApprovedOver Base` (the latest release the
  approver accepted dropping, zero unless approved while behind), `Replaces` and
  `ReplacedBy` (proposal ids, see Replace below).
- Where a proposal stands is computed when it is read, never stored, since releases keep
  coming:

```go
// Standing is where a proposal stands against its package's releases now.
type Standing struct {
	Base   Base // what it was made from
	Latest Base // the package's latest release now
}

// Behind reports whether a release came out after the proposal's base.
func (s Standing) Behind() bool { return s.Base.Before(s.Latest) }

// Blocks reports whether the latest release is at or above v, so the
// proposal can no longer be approved as that version at all.
func (s Standing) Blocks(v semver.Version) bool
```

  `ProposalService.Standings(ctx, pr, props []Proposal) ([]Standing, error)` reads the
  latest release of every package of the list's team with one `Store.Packages(team)`
  (the list is one team's, as `Proposals` already narrows it), after the same read check
  as `Proposals`. The proposal page passes a list of one. No new `Store` method.
- `ProposalInput` gains `Base string` ("" means the latest release now, which is what
  every older CLI gets) and `Replaces string`. `Propose` resolves the base from the
  releases `checkAboveLatest` already reads: a given base must be a release of the
  package (`ErrInvalid` "2.5.0 is not a release of lime/planner"). A base older than the
  latest is accepted: the author said so, and the pages warn from the first minute.
- Approve gains a gate in `ProposalService.Approve`, before the owner and note checks:

```go
type ApproveInput struct {
	Note    string
	AsOwner bool
	// Over is the latest release the approver accepts releasing over: the
	// proposal is behind it and approving drops its changes. It must name
	// the latest release exactly; it is ignored when the proposal is not
	// behind.
	Over string
}
```

  Behind and `Over` not equal to the latest release: refused with the new sentinel
  `ErrBehind` (409, code `behind`) and a sentence that names both versions and both ways
  out: "proposal 3f2a9c01b7de is based on 2.0.0 and 3.0.0 was released since, so
  approving releases 3.1.0 without 3.0.0's changes. The author brings it up to date with
  loadout rebase, or approve it over 3.0.0: loadout review approve 3f2a9c01b7de --over
  3.0.0." `Decision` gains `Latest Base` (what the service saw) and `Over Base` (recorded
  when behind). The store compares `Latest` with the package's latest release inside the
  approval transaction, next to its version check, and returns `ErrStale` when another
  release landed between the service's read and the insert. The service turns that into
  the same `ErrBehind` sentence with the new version. So nobody approves over a release
  they never saw.
- Owners approving their own proposal pass the same gate: `--as-owner --over 3.0.0`.

### registry: who published a release

- `Release` gains `PublishedBy Actor` (the same `Actor` proposals use: token id, label,
  user id). The zero `Actor` means nobody is known, which is what every release from
  before this change has.
- `Service.Publish` sets it from the principal with `actorOf` (a machine token counts as
  the user who made it, a token made on the registry host has only its id and label).
  `ApproveProposal` sets it from `d.By`, the approver, in the same transaction that
  inserts the release: the approver is who released it, and the release's `Proposal` id
  still leads to the author. An owner approving their own proposal is both.
- `Store.PutRelease` stores it. Every release query (`Release`, `Releases`, `Packages`,
  `Changes`) reads it back. No new `Store` method.
- The pages name the person with `who(actor, names)`, as proposals do: the user's display
  name, else the token's label. An empty actor shows nothing, so an old release reads
  "3.0.0, released 2 days ago".

### registry: replace a proposal in one step

Rebasing produces new content, so the author proposes again. Doing that as withdraw then
propose has two problems: there is at most one open proposal per version (the new one
usually keeps 3.1.0), and a failure between the two steps loses the open proposal or
leaves two. Prompts that teammates added on the web would also be lost or credited to the
author.

- `ProposalStore` gains one method. Both stores implement it and the contract suite
  covers it:

```go
// ReplaceProposal closes the open proposal old as withdrawn by d.By with
// d.Note, links the two (old's ReplacedBy, p's Replaces) and stores p
// with its bundle and prompts, in one transaction: old is closed first, so
// p may keep old's version. It returns ErrProposalClosed if old is not
// open (approved, rejected or replaced in the meantime) and
// ErrVersionConflict like CreateProposal.
ReplaceProposal(ctx context.Context, old string, p Proposal, bundle []byte, d Decision) error
```

- `Propose` with `in.Replaces`: the old proposal must be readable (else not found), of
  the same package, open, and the caller its author (`Actor.same`, anyone else gets
  `ErrForbidden` "only the author updates a proposal. Propose your own"). The new
  proposal's prompts are the old one's active prompts as they are (author and time
  kept), then the given ones not already there. `checkInput` lets the given list be empty
  only when replacing, and more than `MaxPrompts` in all is `ErrInvalid`. Reports are not
  carried: they compared other content. The old one closes with the note "replaced by
  NEWID".
- Status stays `withdrawn` on the wire, so older CLIs read it. `ReplacedBy` says why.

### textdiff: a three-way line merge

`internal/textdiff` keeps the line algorithms. It gains an exported merge built on its
existing LCS `diff`:

```go
// Labels name the three sides in conflict markers.
type Labels struct{ Base, Ours, Theirs string }

// Merge3 merges the changes from base to ours and from base to theirs.
// Changes to different lines both apply; the same change on both sides
// applies once; different changes to the same lines become a conflict,
// written diff3-style:
//
//	<<<<<<< ours label
//	||||||| base label
//	=======
//	>>>>>>> theirs label
//
// It returns the merged text and the number of conflicts. Files over the
// diff limit return ErrTooLarge and are merged whole by the caller.
func Merge3(base, ours, theirs string, l Labels) (text string, conflicts int, err error)
```

The classic diff3 chunking (stable regions from both edit scripts, unstable ones merged
or marked). It is deterministic, pure and has no I/O, so it is fuzzed like the diff.

### rebase (new package): merge a package folder onto the latest release

`internal/rebase` owns the package-level rules, so `internal/cli` only parses flags and
prints. It owns its interfaces. `*client.Client` satisfies `Registry`, and
`cmd/loadout` builds the service and hands it over as `cli.Deps.NewRebaser`, like
`NewPlanner`.

```go
type Registry interface {
	Releases(ctx context.Context, id pack.ID) ([]api.Release, error)
	Fetch(ctx context.Context, id pack.ID, version string) (*pack.Pack, error)
	Proposals(ctx context.Context, pkg, status string) ([]api.Proposal, error)
	FetchProposal(ctx context.Context, p api.Proposal) (*pack.Pack, error)
}

// Origin is what a package folder was made from, and where that stands.
type Origin struct {
	Base     string // release version, "" before the first release
	Latest   string
	Replaces string // the open proposal this folder updates, if any
	From     Source // how Base was found, for the CLI to say
}

func (o Origin) Behind() bool

// Locate works out a folder's origin. p is the folder as loaded, before
// any --bump changes its version; base is --base ("", a version or latest).
func (s *Service) Locate(ctx context.Context, dir string, p *pack.Pack, base string) (Origin, error)

// Rebase merges the folder's changes since its base onto the latest
// release and writes the result into the folder.
func (s *Service) Rebase(ctx context.Context, dir string, p *pack.Pack, o Origin) (Result, error)
```

`Locate` takes the first of these that applies (`Source` says which):

1. `--base`: a version (must be a release) or `latest`.
2. A rebase record for this folder (below): its `onto` version and the proposal it
   replaces.
3. An open proposal of the package at the folder's manifest version: its recorded base,
   and it replaces that proposal when the caller is its author (the registry checks
   again). This is the user's case: the folder still says 3.1.0, the version proposed.
4. A release at the folder's manifest version: that release (the folder was published or
   synced from it, then edited).
5. Otherwise the newest release below the manifest version, said as a guess.

`Rebase`:

- Fetches the base release, the latest release and, when the folder is missing or empty
  and `--proposal ID` is given, the proposal's bundle as the author's side (an author on
  another machine). Every fetch is digest-checked by the client as today.
- Refuses a kind that differs between the three ("the kind changed in 3.0.0, update the
  folder by hand"), and a folder that holds another package.
- Normalises the manifest's `version` on all three sides before merging, since it always
  differs. The result gets the folder's version when that is still above the latest
  release, else the latest release bumped by patch, and says so.
- Merges per path, a table of cases rather than branches spread around: unchanged on one
  side takes the other. Changed the same way on both takes it once. Changed differently
  is `Merge3`, with labels such as `yours (proposal 3f2a9c01b7de)`, `base 2.0.0` and
  `release 3.0.0`. Added on one side only is kept. Deleted on one side and unchanged on
  the other is deleted. Deleted on one side and changed on the other keeps the changed
  file and reports a conflict. Added on both with different text is a whole-file
  conflict. A file over the diff limit changed on both sides is a whole-file conflict.
- Checks every path with `pack.CheckPath`, then writes through `os.Root` with
  `atomicfile`, one file at a time, and deletes only package files (hidden files and
  folders are never package files, so they are never touched). Nothing is written until
  every file has merged in memory, and a write failure names the files already written.
  Never through a symlinked folder: `pack.Load` already refuses links.
- When nothing conflicts, the result must load with `pack.FromFiles`. When something
  does, it records the conflicted paths.
- Writes the record and returns a `Result` (per file: kept, from the release, yours,
  merged, conflict with a count, deleted) for the CLI to print.

The record is `~/.loadout/rebase.yaml` (`rebase.Records`, the same shape as
`wiki.Checkouts`: strict YAML, 0600, written atomically), keyed by the folder's absolute
path: registry URL, package, base, onto, replaces, the marker labels and the conflicted
paths, and when. It keeps loadout state out of the author's repository. `propose`
deletes the entry once the registry accepted the new proposal.

### CLI

`internal/cli` parses and prints only.

- `loadout rebase [FOLDER] [--base V|latest] [--proposal ID] [--remote R]`. Prints where
  the folder came from and what it merged onto ("planner: proposal 3f2a9c01b7de, made
  from 2.0.0, onto 3.0.0"), one line per changed file, and then either "no conflicts.
  Propose it with: loadout propose planner" or "2 files have conflicts, marked with
  <<<<<<<: SKILL.md, references/api.md. Resolve them in your editor or AI tool, then:
  loadout propose planner". Up to date: "planner is up to date with 3.0.0. Nothing to
  rebase." Exit 0 in both merged cases, since the folder is now the author's to finish.
- `loadout propose` gains `--base V|latest`. It calls `Locate` on the folder before
  `--bump` changes the version. Behind (decided: refuse): it stops before uploading with "planner is based
  on 2.0.0, and 3.0.0 was released since. Its changes are not in your folder. Bring them
  in with: loadout rebase planner. If your folder already has them, propose with --base
  3.0.0." A folder with a rebase record whose conflicted files still hold loadout's
  markers is refused with the file list. Otherwise it sends `base` and, from the record
  or rule 3, `replaces`. With `replaces` and no `--prompt`, the old prompts carry over.
  Output adds "based on 3.0.0" and "replaces 3f2a9c01b7de (closed, its prompts came
  along)".
- `loadout publish` gains the same `--base V|latest` and the same check, through the same
  `Locate` call before `--bump` (decided). Behind, it stops before uploading: "planner is
  based on 2.0.0, and 3.0.0 by Dana was released since. Publishing would drop its
  changes. Bring them in with: loadout rebase planner. If your folder already has them,
  publish with --base 3.0.0." The check is in the CLI only: the registry does not record
  a release's base in this step, and an older CLI publishes as before. A rebase record
  for the folder gives the base, and a successful publish deletes the record. If the
  record names a proposal it replaces, publish says that proposal stays open and is now
  behind or blocked, and how to withdraw it. Under `require_review` publish is refused by
  the registry as today, so the check only adds an earlier, clearer refusal.
- The shared part of both refusals (the sentence and the hint) lives in `rebase` as
  `(*Origin).Refusal(verb, folder string) error`, so propose and publish cannot drift.
- `loadout review list`: the status reads "open, behind 3.0.0" (as "without review" is
  added today, so no column moves). `review list --all` shows "withdrawn, replaced by
  ID".
- `loadout review show ID`: "Made from 2.0.0, proposes 3.1.0. The latest release is
  3.0.0, released after it: approving releases 3.1.0 without 3.0.0's changes." Then the
  diff from the base (what the author changed, the change under review) instead of from
  the latest release, and the commands that show the rest: `loadout diff lime/planner
  2.0.0 3.0.0`. For the author: `loadout rebase --proposal ID`.
- `loadout review approve ID --over V`. Without it on a behind proposal the registry's
  sentence is printed with the two ways out. With it: "approved 3f2a9c01b7de over 3.0.0:
  lime/planner@3.1.0 is released without 3.0.0's changes".
- `loadout review PKG` (`reviewplan`): a behind proposal adds a note to `Plan.Notes`
  ("proposal 3f2a9c01b7de is based on 2.0.0, 3.0.0 was released since, and the report
  compares with what you have installed"). Nothing else changes in the run.

### Web (`internal/web/ui_reviews.go`, `templates/ui/proposal.html`, `reviews.html`, `diff.html`, `static/style.css`)

The page says what merges into what. Every view below works without script.

- Header: "lime/planner 2.0.0 → 3.1.0" from the recorded base (not the guessed one), and
  when behind a `pill-warn` "Behind 3.0.0" after the status. `baseVersion` and its
  per-row `Releases` call go. The base comes with the proposal and the latest from one
  `Standings` call per page.
- A notice above the body when behind (`role="note"`, warn tokens, a heading so it is
  found by screen reader users): "3.0.0 by Dana came out after this proposal. It was
  made from 2.0.0, so approving releases 3.1.0 without 3.0.0's changes to 2 files: SKILL.md and
  references/api.md. 1 of them is also changed here." With several releases since the
  base, it lists each with its publisher and age ("3.0.0 by Dana, 2 days ago, 3.0.1 by
  Sam, today"), at most five and then "and N more". A release without a known person
  shows the version and its age only. Links: "What 3.0.0 changed" and
  "What approving does". For the author it adds the rebase command in a copy row ("Bring
  it up to date on your machine"). For others "The author can bring it up to date." The
  file counts come from comparing the three file maps by content (no line diff), which
  is cheap. Whether lines conflict is the author's CLI's job.
- When `Standing.Blocks` (a release at or above the proposed version): the notice says
  "3.1.0 was released after this proposal, so it cannot be approved as 3.1.0. The author
  rebases and proposes a higher version." and Approve is disabled with that reason.
- Approve when behind: the plain button becomes **Approve over 3.0.0…**, a `details`
  popover like Reject and "Approve as owner…": the sentence above, the two links, a
  hidden `over` with the latest version, Cancel and "Release without 3.0.0's changes". An
  owner's own proposal combines it with the owner popover (one form, both fields). The
  service checks everything again. If a release landed in between, the refusal page says
  which and links back.
- Diff page: one handler, three views chosen by `?view=` from a table (unknown values
  fall back to the first), shown as `nav.tabs` links when the proposal is behind:

| `view` | Tab | From → to |
| --- | --- | --- |
| `changes` (default) | This proposal | base → proposal: what the author changed |
| `since` | Released since | base → latest: what the proposal does not have |
| `effect` | What approving does | latest → proposal: what everyone gets on sync |

  Not behind, the three are the same diff and no tabs show. `handler.diff` becomes a
  function of two file maps plus labels, reused by all three. The side panel's preview
  stays "This proposal".
- Reviews list: the version cell reads "2.0.0 → 3.1.0" from the recorded base. A behind
  row adds a small "Behind 3.0.0" pill under it. The Status column says "Replaced" for a
  replaced proposal (`proposalStatus`, as "Released without review" does).
- Replace: the old page's notice "Replaced by 7c1e… (link) after a rebase onto 3.0.0".
  The new page's byline "Replaces 3f2a… (link). Its reports compare the older content".
  The activity gains "replaced it with 7c1e…" and "approved over 3.0.0: “note”" (tone
  `warn`).
- UI review per `docs/UI.md`: light and dark from the existing `--warn-bg` and
  `--warn-text` tokens (no new color), 390 px (the notice and its file list wrap, the
  tabs scroll as they do on other pages, the popover is full width), 44 px targets,
  keyboard order (notice before the actions it explains), the `apple-design` checklist
  and `accessibility-audit`. Record it in `docs/UI.md`.

## Data and API changes

- `api.ProposalRequest` gains `base` and `replaces` (both `omitempty`).
- `api.Proposal` gains `base`, `latest` (the package's latest release when read, left
  out by older registries), `approved_over`, `replaces`, `replaced_by`, all `omitempty`.
  A client knows a proposal is behind when `latest` is set and differs from `base` (both
  are releases and base is never above latest). An older registry sends neither, so
  nothing shows as behind, which is the truth as far as it knows.
- `api.Release` gains `published_by` (an `api.Actor`, `omitempty`), so `review show`,
  `propose` and `publish` can name who released the newer version. Older registries and
  old releases leave it out.
- `api.DecisionRequest` gains `over` (`omitempty`), sent only when given.
- `api.CodeBehind = "behind"`, `registry.ErrBehind` mapped to 409 in
  `server/errors.go` and to "Something changed" in `handler.refused`.
- Old registries decode the propose metadata strictly. The new client always sends
  `base`. When the registry answers "unknown field" naming `base`, it sends the proposal
  again without it (and without `replaces`, after saying the old proposal stays open and
  should be withdrawn). That second upload happens only against registries that predate
  this plan. `--over` against an old registry gets "this registry is older than
  approving over a newer release", like `ErrOwnerApprovalUnsupported`.
- `GET /v1/proposals` and `GET /v1/proposals/{id}` fill `latest` from `Standings`: one
  more query per request, not per row.

## Migration

`0014_proposal_base.sql`, all columns with defaults that keep old rows valid:

```sql
ALTER TABLE proposals ADD COLUMN base_version  TEXT NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN approved_over TEXT NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN replaces      TEXT NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN replaced_by   TEXT NOT NULL DEFAULT '';

-- Who published a release. Empty means nobody is known: every direct
-- publish before this migration.
ALTER TABLE releases ADD COLUMN published_by_token_id TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN published_by_label    TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN published_by_user_id  TEXT NOT NULL DEFAULT '';

-- A release approved from a proposal was published by whoever approved it,
-- and the proposal still knows who that was.
UPDATE releases SET
    published_by_token_id = (SELECT p.decided_by_token_id FROM proposals p WHERE p.id = releases.proposal_id),
    published_by_label    = (SELECT p.decided_by_label    FROM proposals p WHERE p.id = releases.proposal_id),
    published_by_user_id  = (SELECT p.decided_by_user_id  FROM proposals p WHERE p.id = releases.proposal_id)
WHERE proposal_id != '';

-- Every proposal so far was made when the latest release was the newest
-- one published before it; releases only grow, so that is its base.
UPDATE proposals SET base_version = COALESCE((
    SELECT r.version FROM releases r
    WHERE r.team = proposals.team AND r.name = proposals.name
      AND r.published_at <= proposals.created_at
    ORDER BY r.seq DESC LIMIT 1), '');
```

Times are fixed-width UTC text (`timeFormat`), so the comparison is exact. A release
approved from a proposal is published at its decision time, after the proposal, so it is
never its own base. The open proposals on the live registry that are behind today will
show as behind right after the deploy. That is the point, and the notice says what to do.
`approved_over` stays empty for past approvals: nobody was asked. Direct publishes from
before the migration keep an empty publisher, which the pages show as the version alone.
Releases approved from a proposal get their approver from the proposal row. One migration
for all seven columns, so the live database migrates once (back it up first).

## Notes from step 1

Step 1 is done. Where it differs from the text above:

- The publisher backfill in 0014 only updates a release whose `proposal_id` names a
  proposal that exists (`AND EXISTS (...)`). Without that, a release pointing at a missing
  proposal would set the new `NOT NULL` columns to `NULL` and fail the whole migration.
  Such a release now keeps an empty publisher. The migration test covers it.
- `Base` also has `IsZero` and `Equal` (`Equal` lets `go-cmp` compare proposals without
  `AllowUnexported`).
- `CreateProposal` stores only `Base` of the four new proposal fields. It leaves
  `ApprovedOver`, `Replaces` and `ReplacedBy` empty, as both stores now document. Step 5
  writes `approved_over` in `ApproveProposal`, step 6 writes the links in
  `ReplaceProposal`.
- `Standings` reads once per distinct team in the list (one query for a list from
  `Proposals`, which is always one team's), so it is also correct for a mixed list.
- `ProposalInput.Base` takes "" or a version. `latest` is for the CLI's `--base` to
  resolve (step 8) and the service refuses it as not a version.

## Notes from step 2

Step 2 is done. Where it differs from the text above:

- `api.Release.PublishedBy` is a `*api.Actor` (like `DecidedBy`), so it is left out when
  nobody is known rather than sent as an empty object. `api.Actor` has no user id on the
  wire, as for authors and deciders.
- `api.Proposal` gains only `base` and `latest` here, plus a `Behind()` method so
  clients share one rule. `approved_over`, `replaces` and `replaced_by` come with steps 5
  and 6, and `api.ProposalRequest` gains only `base`.
- `latest` is filled on `GET /v1/proposals` and `GET /v1/proposals/{id}` only. The
  responses of propose, approve, reject and withdraw leave it out.
- The CLI reads the latest release with the new `release.Latest` before `--bump`. The
  resend is in `client.Propose` and triggers only when `base` was sent and the 400 names
  `unknown field "base"`. No e2e test yet: the CLI prints nothing new until step 4.

## Notes from step 3

Step 3 is done. Where it differs from the text above:

- The third diff tab is "After approval", not "What approving does": segment labels should
  be noun phrases. The notice's link keeps "What approving does". The tabs are `.page-tabs`
  segments of equal width and keep one row on phones (labels wrap inside), rather than
  scrolling.
- The notice sits under the byline, inside the header, so it comes before the actions in
  reading and Tab order. With several releases it says "without their changes to N
  files", right after the list of releases.
- No rebase command yet: the author's line says "To bring it up to date, withdraw it, add
  these changes to your folder and propose again." Step 8 swaps in the `loadout rebase`
  copy row. Others read "The author can bring it up to date."
- Blocked (`Standing.Blocks`) is in this step: the notice, a disabled Approve tied to
  "Approve is off: 1.9.0 is already released.", Reject (maintainers) and Withdraw (author)
  kept. `decision` gained `CanReject` for that. The blocked notice says "The author can
  propose again with a higher version" (the author: "Withdraw it and propose a higher
  version"), not "rebases".
- The Behind pill, the notice and the tabs show only for open proposals: an approved one is
  behind its own release. Closed proposals still read their header and diff from the
  recorded base.
- File counts compare content. The manifest counts only when more than its `version:`
  line changed. Release and file names are capped at five, then "and N more".
- `scripts/preview/behind.sh` (after `skills.sh`) seeds a proposal two releases behind (one
  publisher known, one not) and a blocked one. Step 5 can reuse it for the popover.

## Notes from step 4

Step 4 is done. Where it differs from the text above:

- `api.Proposal.Behind()` now holds only for open proposals, the web's rule, and
  `api.Proposal.Blocked()` is new. The CLI and `reviewplan` use only these two.
- `review list` says "open, blocked by 0.2.0" when the proposed version (or a higher one)
  was released, and "open, behind 0.1.1" otherwise.
- `review show` (`internal/cli/reviewstanding.go`): "Made from 2.0.0, proposes 3.1.0."
  then, when behind, "The latest release is 3.0.0 by Dana, released after it: approving
  releases 3.1.0 without 3.0.0's changes.". Several releases since are listed (five, then
  "and N more"). Blocked says it cannot be approved as that version. A release without a
  known publisher shows its version alone. A publisher is named by token label without
  " via loadout login", like review run's author. The diff is "What the author changed
  since 2.0.0" from the recorded base. A proposal made before the first release shows its
  files alone. Against an older registry (no `base`, no `latest`) it diffs from the latest
  release as before. No `loadout rebase --proposal` hint yet (step 8): it says "The author
  can bring it up to date: withdraw it, add those changes to the folder and propose
  again.", as the web does.
- The `reviewplan` note names what the report compares with: what you have installed (and
  its version), the release, or a proposal.
- GUIDE.md is left for step 10 with the rest of the docs.

## Notes from step 5

Step 5 (all but the web popover) is done. Where it differs from the text above:

- `ProposalService.Approve` checks, after the decider check and before the owner and note
  checks: a closed proposal is `ErrProposalClosed`. A blocked one (`Standing.Blocks`) is
  `ErrVersionConflict` with the old sentence. A behind one needs `in.Over` equal to the
  latest release, else `ErrBehind` (`internal/registry/behind.go`). The sentence names
  the base, the latest, and, when `over` was wrong, "You approved it over 0.1.0, but the
  latest release is 0.1.1.". It ends with "The author can bring it up to date (withdraw
  it, add those changes and propose again), or approve it over 0.1.1: loadout review
  approve ID --over 0.1.1" (no `loadout rebase` until step 8).
- `Decision.Latest` is always set on approval (none before the first release), so the
  store's `ErrStale` check runs for every approval, not only behind ones. The service
  turns `ErrStale` into the `ErrBehind` (or `ErrVersionConflict`) the approver would have
  had with the new release in view.
- `api.Proposal` gains `approved_over` in this step (the plan listed it for the API
  generally). `api.ErrBehind` matches a `behind` response. `client.ErrOverUnsupported`
  is the older-registry answer to `--over`.
- `review approve` prints the registry's sentence without the HTTP status. Approving
  over prints "approved ID over 0.1.1: lime/planner@0.2.0 is released on office without
  0.1.1's changes". `review show` adds "Approved over 0.1.1: released without 0.1.1's
  changes".

### For the UI agent (web popover, rest of step 5)

- The handler is ready: `decideProposal` passes the form field `over` to
  `ApproveInput.Over` (with `as_owner` as before, so one form can carry both).
  `handler.refused` maps `ErrBehind` to 409 "Something changed", and `sentence` strips
  the `ErrBehind` prefix.
- Today the plain Approve button on a behind proposal posts without `over` and gets that
  409 page with the registry's sentence, which ends in the CLI command. It never approves
  silently. A stale `over` (a release landed while the page was open) gets the same page
  naming the new release.
- The popover posts `over` = `Standing.Latest.String()` (the version in the notice).
  An owner's own behind proposal needs `as_owner=on`, a note and `over` in one form.
- `registry.Proposal.ApprovedOver` (a `Base`) is set on proposals approved over a
  release, for the activity line "approved over 3.0.0: “note”" (tone `warn`).
- `TestApproveBehindNeedsOver` in `internal/web/ui_behind_test.go` covers the handler.
  The popover's own tests (CSRF, markup) are the UI step's.

### Notes from step 5 (web)

- On a behind proposal the maintainer's Approve is **Approve over 1.0.2…**, a `details`
  popover like Reject: what approving drops (the newer releases and the file count from
  the notice), a link to "What approving does", an optional note, Cancel and **Approve
  over 1.0.2** (`btn-danger`, since it drops others' changes for everyone). It posts
  `over` with the CSRF token. No script. The normal flow no longer meets the 409 page.
  A release landing while the page is open still does, naming the new release.
- An owner's own behind proposal keeps "Approve as owner…": the same form gains the
  hidden `over`, the same warning and the button "Approve without review over 1.0.2".
- `decision.Over` carries the latest release when the proposal is behind and not blocked.
  The Why line says approving "asks first". Blocked proposals are unchanged from step 3.
- Activity: "approved and released it over 1.0.2: “note”", or "approved own proposal as
  owner, without review, over 1.0.2", with the warn dot.

## Notes from step 6

Step 6 is done. Where it differs from the text above:

- Given prompts are deduplicated only against the carried ones (by trimmed text), so a
  plain propose keeps its prompts exactly as before.
- Carried prompts keep their edit time too. `insertPrompt` now stores `edited_at`.
- The client resends without `base` and `replaces` when an older registry refuses
  either by name. The returned proposal then has no `replaces`, which is how the CLI
  (step 8) tells that the old proposal stayed open.
- `review list --all` reads "withdrawn, replaced by ID". `review show` says "Replaces
  proposal ID. Its prompts came along, its reviews compared the older content" and
  "Replaced by proposal ID: loadout review show ID".
- The web reviews list says "Replaced" (`proposalStatus`). The rest is the UI agent's
  (below). No e2e test until the CLI sends `replaces` in step 8.

## Notes from step 7

Step 7 is done. `textdiff.Merge3` is in `internal/textdiff/merge.go`. `ErrTooLarge` is
new and `Unified` wraps it too. As in git's diff3, changes to directly adjacent lines
conflict. One unchanged line between them is enough to merge both. A conflict side whose
last line has no newline gets one, so each marker starts its own line.

## Notes from step 8

Step 8 is done. Where it differs from the text above:

- `rebase.Registry` also has `Proposal(ctx, id)`, for `--proposal ID`. `Locate` takes
  `rebase.Options{Base, Proposal}`. The proposal replaced is worked out apart from the
  base (named, else the record's, else the open one at the folder's version), so
  `--base` does not stop a replace.
- The CLI does not check that the caller wrote the open proposal at the folder's
  version: the registry does, and refuses anyone else with "only the author updates a
  proposal. Propose your own" (such a propose would have hit "already has an open
  proposal" before). So `propose` from a folder at the version of your own open proposal
  now replaces it rather than failing with a version conflict.
- Source names are `SourceFlag`, `SourceRecord`, `SourceProposal`, `SourceRelease`,
  `SourceGuess`, `SourceNone`. The refusal is a `*rebase.BehindError` matching
  `rebase.ErrBehind`. A guessed base says "(the newest release below its version)".
- A file deleted on one side and changed on the other is kept as changed, with no
  markers, and recorded as undecided (`Record.Undecided` in `rebase.yaml`). `propose`
  and `publish` refuse while one is still there: "1 file was deleted on one side and
  changed on the other when loadout rebase merged: deploy.md. Delete it, or keep it with:
  loadout rebase runbooks --resolved deploy.md, then propose again". Deleting the file
  decides it. `loadout rebase [FOLDER] --resolved FILE` (repeatable, a path inside the
  package, refused for a file that is not undecided) keeps it without merging.
  `Unresolved` returns a `rebase.Pending` with the marker files and the undecided ones.
- `Rebase` with no folder (or an empty one) and `--proposal ID` writes the proposal's
  rebased files into the folder named after the package.
- `release.Latest` from step 2 is gone: `propose` reads the base from `Locate`, and
  without a Rebaser (`cli.Deps.NewRebaser` nil) sends none, as older loadouts did.
- The sentences: the registry's behind refusal ends "The author can bring it up to date
  with: loadout rebase --proposal ID, or approve it over V: loadout review approve ID
  --over V". `review show` says "The author brings it up to date with: loadout rebase
  --proposal ID". The web notice tells the author "run `loadout rebase --proposal ID` in
  your package folder, resolve any conflicts, then run `loadout propose`. It replaces
  this proposal" (as inline `<code>`, see the UI list).
- `docs/DESIGN.md` gained the `internal/rebase` row. The rest of the docs wait for step 10.

## Notes from step 9

Step 9 is done. `publish` runs the same `Locate` and `checkReady` (markers, then the
behind refusal) as `propose`, before `--bump`, and clears the record after a successful
upload. Where it differs from the text above:

- The note about a proposal left open shows whenever the folder's origin names one (the
  record, or an open proposal at the folder's version), not only from a record: "your
  proposal ID stays open and is now behind 0.2.0. Withdraw it with: loadout review
  withdraw ID", or "blocked by 0.2.0" when the release is at or above the proposal's
  version (one more read of the proposal, a failed read says "behind").
- `loadout rebase` on a folder that replaces no proposal ends "propose it with: loadout
  propose X (or publish it with: loadout publish X)".
- Existing e2e tests needed no `--base`: a fresh `loadout new` folder at 0.1.0 is based on
  release 0.1.0, which is only behind once someone releases after it.

### For the UI agent (steps 6 and 8)

- Proposal page, replaced proposal (`Proposal.ReplacedBy` set, status withdrawn): a
  notice "Replaced by 7c1e… after a rebase onto 3.0.0" linking to
  `/t/{ns}/reviews/{ReplacedBy}`. The "onto" version is the new proposal's `Base` (read
  it with `Proposals.Proposal`). Its Withdraw/Reject actions are already gone (closed).
- Proposal page, replacement (`Proposal.Replaces` set): in the byline "Replaces 3f2a…
  (link). Its reports compare the older content".
- Activity: on the replaced proposal, "replaced it with 7c1e…" instead of "withdrew it"
  (the decision note is "replaced by ID", `DecidedBy` is the author). Carried prompts
  keep their original authors and times, so the replacement's activity lists them as
  added by their authors at their original times. Consider saying "came along from
  3f2a…" for prompts older than the proposal.
- The author's behind notice now says, inline, "run `loadout rebase --proposal ID` in
  your package folder, resolve any conflicts, then run `loadout propose`. It replaces
  this proposal" (`behindView.ID` is new). The plan's copy row ("Bring it up to date on
  your machine", the command in a copy field) replaces that sentence.
- Reviews list: "Replaced" in the Status column is done (`proposalStatus`).

## Notes from step 10

The docs part is done: GUIDE.md (Concepts: Review, Publish, Propose instead, the new
"Bring a folder up to date" with `--resolved`. Review a proposal with Behind, the
publisher and `--over`. Machine tokens for CI with `--base latest`. The command
reference. Troubleshooting), DESIGN.md (Registry bullet on publishers, Review bullets
on behind, approving over, replacing, rebase and the propose and publish check, Web
pages bullet), README.md (command table and the review paragraph), and the built-in
`loadout-author` and `loadout-review` skills. The site is rebuilt with
`docs/guide/build.sh`. Left for the coordinator: backing up the live database and
deploying (step 1 migrates).

## Notes from testing

- Only the caller's own proposal is replaced, gives a folder its base, or is called
  "your proposal". The registry says whose a proposal is in every response
  (`api.Proposal.Yours`, from `registry.Proposal.By(pr)`). An older registry says
  nothing, and the CLI then treats every proposal as someone else's, which keeps the old
  "already has an open proposal. --bump patch" behaviour. `rebase --proposal` on someone
  else's proposal starts from its files (`Origin.Start`) and proposing them makes a new
  proposal of the caller's own.

## Order of work

Each step can ship on its own.

1. registry: `Base`, `Proposal.Base`, `ApprovedOver`, `Replaces`, `ReplacedBy`.
   `Propose` records the base (`ProposalInput.Base`). `Standing` and `Standings`.
   `Release.PublishedBy`, set by `Publish` and by `ApproveProposal`. Memstore. The
   contract suites read the base back through every proposal query and the publisher
   through every release query. Sqlitestore and migration 0014 with its backfill tests.
2. api, server, client: `base` and `latest` on proposals, `published_by` on releases,
   `base` on propose with the old-registry resend. The CLI sends the latest release as
   base (no refusal yet).
3. Web, read-only: header from the recorded base, the "Behind 3.0.0" pill, the notice
   with publishers and file counts, the three diff views, the reviews list from one
   `Standings` call. Web tests, UI review, `docs/UI.md`.
4. CLI, read-only: `review list`, `review show` (with the publisher), the `reviewplan`
   note.
5. Approval gate: `ApproveInput.Over`, `Decision.Latest` and `Over`, the store's check
   in `ApproveProposal` (both stores, contract), `ErrBehind`, `over` on the wire,
   `review approve --over`, the web popover. From here a behind proposal cannot be
   released by accident.
6. Replace: `ReplaceProposal` (both stores, contract), the service rules and prompt
   carry-over, `replaces` on the wire, the web links and statuses.
7. `textdiff.Merge3` with table and fuzz tests.
8. `internal/rebase`: `Locate`, `Rebase`, `Records`, `Origin.Refusal`. `loadout rebase`.
   `propose` `--base`, the behind refusal, the marker check, `replaces` from the record.
   Wiring in `cmd/loadout` (`cli.Deps.NewRebaser`).
9. `publish`: `--base`, the same `Locate` and `Refusal` before `--bump`, the rebase
   record used and cleared, the note about a replaced proposal left open.
10. Docs and skills: DESIGN.md (Review bullets, Registry bullet on who published a
   release, packages table for `internal/rebase`, migration list, Web pages bullet for
   reviews), GUIDE.md (Concepts: Review, Publish: the behind refusal and `--base`.
   Propose instead: the same. A new "Bring a folder or proposal up to date" for
   `loadout rebase`. Review a proposal: "Behind 3.0.0", "3.0.0 by Dana" and `--over`.
   The command reference for `rebase`, `propose --base`, `publish --base` and
   `review approve --over`. Troubleshooting "planner is based on 2.0.0, and 3.0.0 was
   released since", including the CI case and `--base latest`), rebuild with
   `docs/guide/build.sh`. The built-in `loadout-author` skill learns `loadout rebase`
   and the propose and publish refusals, `loadout-review` learns "Behind" and asks the
   person before `--over` (the honesty test checks every command and flag). Back up the
   live database, then deploy (step 1 migrates).

Each step passes `make check` on its own.

## Tests

- registry (`proposals_test.go`, fake stores): base defaults to the latest release, a
  given base must be a release, a base above none is none. `Standings` with no release,
  equal, behind, and a release at or above the version (`Blocks`). Approve table: not
  behind (Over ignored), behind without Over, behind with an older Over, behind with the
  latest as Over (recorded), owner as owner plus Over, the store's `ErrStale` turned into
  `ErrBehind` with the new version. Replace table: author, other member, maintainer,
  machine token of the author, closed old proposal, other package, prompts carried with
  their authors, given prompts appended and deduplicated, over 20 refused, empty prompts
  only when replacing.
- `registrytest` (memstore and SQLite): base, approved over and links read back through
  `Proposal` and `Proposals`. `PublishedBy` reads back through `Release`, `Releases`,
  `Packages` and `Changes`, is the approver for a release from `ApproveProposal`, and is
  the zero actor for a release stored without one. `ApproveProposal` refuses with `ErrStale` when the latest
  differs from `Decision.Latest`. `ReplaceProposal` is atomic (old closed exactly when the
  new exists), allows the same version, refuses a closed old one, and of two concurrent
  replaces or a replace racing an approval exactly one wins.
- registry (`service_test.go`): `Publish` records the principal as publisher, a machine
  token as the user who made it, a host-made token by id and label.
- sqlitestore migration test, publishers: a 0013 database with a direct release and a
  release approved from a proposal reads back an empty publisher for the first and the
  proposal's decider for the second.
- sqlitestore migration test: a 0013 database with releases 1.0.0, 2.0.0 and proposals
  made before, between and after them (one approved, so its own release is later) reads
  back the expected bases, and a proposal of a package without releases has none.
- textdiff: `Merge3` tables (only ours, only theirs, same change both, adjacent but
  separate lines, overlapping change, deletion against edit, empty files, missing final
  newline, CRLF kept as text) and a fuzz test: merging x with itself on both sides gives
  x, a side equal to base gives the other side, and the output always contains every
  conflict's three parts.
- rebase: `Locate` table over flag, record, open proposal at the manifest version (author
  or not), release at the manifest version, guess. `Rebase` table over the per-path cases,
  manifest version rules, kind change refused, write failures name what was written, a
  hidden file in the folder untouched, records written 0600 and read strictly.
- server: `base`, `replaces` and `over` decoding, 409 `behind`, `latest` on list and get,
  `published_by` on releases and absent for an old release.
- client: the resend without `base` against a strict old registry (an `httptest` server
  that refuses the field), `--over` against it.
- web (`ui_test.go`): behind proposal shows the "Behind 3.0.0" pill, the notice with
  "3.0.0 by" the publisher's name, file counts and the three tabs. A release without a
  publisher shows the version and age only. More than five releases since collapse to
  "and N more", not behind shows none. The author sees the rebase command and others do
  not. Approve over posts `over` with CSRF and releases, a stale `over` gets the refusal
  page naming the new release, a forged CSRF token is 403. A blocked proposal has a
  disabled Approve with the reason. Replaced proposals link both ways. Old rows (base
  from the backfill) render.
- e2e (`cmd/loadout/review_test.go`, `team`, `person`, `propose`): Alice proposes 0.2.0
  from 0.1.0. Bob publishes 0.1.1 directly. `review list` says "behind 0.1.1". Bob's
  plain approve is refused with both hints. Alice runs `loadout rebase planner` (a
  change in another file merges cleanly), then `loadout propose planner` replaces the old
  proposal, keeps a prompt Carol added on the API, and the new one is not behind. Bob
  approves it and Carol's sync gets both changes. A second scenario with a conflicting
  line: rebase reports the conflict, `propose` refuses while the markers are there, and
  goes through once they are resolved. A third: `review approve --over 0.1.1` releases
  and `review show` says it was approved over 0.1.1. `propose` from a folder still at
  0.1.0 after 0.1.1 is refused before uploading, and `--base 0.1.1` lets it through.
  Publish: Bob's folder still at 0.1.0 after Alice publishes 0.1.1, `publish --bump
  patch` is refused before uploading and names Alice as the publisher; `loadout rebase`
  then `publish` goes through and clears the record. `publish --base latest` goes
  through without a rebase. A folder that is not behind publishes exactly as before.
- rebase: `Origin.Refusal` gives the same sentence for propose and publish apart from
  the verb.

## Risks

- A wrong base. Rules 4 and 5 read the folder's manifest version, which lags in a CI
  checkout that publishes with `--bump` and never commits the new version back. Such a
  folder looks behind, and since `publish` now checks too, that CI job starts failing
  after the update. The GUIDE's Publish and Troubleshooting sections say so, and CI
  scripts add `--base latest` where the checkout is known to hold the latest content. The refusal tells how to say otherwise (`--base latest`), and the
  rebase of a folder that already holds the newer changes merges cleanly, because the
  same change on both sides applies once. The registry never trusts the base for safety
  beyond warning: approval still checks versions in its transaction.
- An author who approves over a release loses its changes for everyone. That is now one
  named, recorded step, as approving as owner is.
- `textdiff`'s LCS table is O(n·m) memory: two 5,000-line files take about 200 MB. The
  merge runs on the author's machine, where that is acceptable. The registry only
  compares file contents for the notice and never runs `Merge3`. The existing web diff of
  large files has the same cost today. A linear-space diff is a separate change.
- A rebase rewrites files in the author's folder. Nothing of theirs is lost (clean merges
  hold both changes, conflicts hold all three versions, and the base and latest releases
  and the proposal stay on the registry), but the GUIDE says to commit first, and there is
  no `--abort` in this step.
- Two plans touch `proposal.html` and `decide`. Whichever lands second rebases. This one
  changes the header, the actions block and adds a notice.
- The publisher is shown to everyone who reads the package: a login token's label holds
  the person's email, which proposals already show to the same readers. Machine tokens
  show their label, which their maker chose.
- The resend against an old registry uploads the bundle twice, at most 32 MB, once per
  propose, until the registry is updated.

## Decisions for the user

1. **Approving a proposal that is behind.** Recommended: allowed only with an explicit
   "approve over 3.0.0" that names the latest release, checked again in the approval's
   transaction and recorded on the proposal. Blocking outright makes the author rebase
   even when the newer release is one the proposal deliberately replaces (a hotfix the
   proposal redoes better, a revert). Allowing silently is the bug. The named version
   also catches a release that lands while the reviewer reads the page.
2. **Who merges, and where.** Recommended: the author, with `loadout rebase` doing a
   deterministic three-way merge on their machine, conflicts marked for them to resolve
   in their own editor or AI tool. A server-side "Update proposal" button that merges on
   the registry breaks no rule (no model), but it would release content that neither the
   author nor the prompts ever saw, under the author's name, and it still needs the
   author for every conflict.
3. **What updating does to the old proposal.** Recommended: a new proposal that replaces
   the old one in one transaction (the old one closed and linked, its prompts carried
   with their authors, its reports left with the content they compared). Updating the
   bundle in place would keep one id, but every attached report would then describe
   content the proposal no longer holds, and a proposal's digest would stop meaning one
   thing.

## Not in this step

- Merging on the registry, and a "merges cleanly" badge computed on the server.
- `loadout rebase --abort` and backups of the folder.
- Updating someone else's proposal. Others propose their own.
- Marking releases that were approved over another on the package page.
- Telling an author (mail, push) when their proposal falls behind.
- Detecting renamed files in the merge. A rename is a delete and an add.
- A linear-space diff for large files.

## Notes from steps 6 and 8 (web)

- The replaced proposal's note says "after a rebase onto V" only when the replacement's base differs from its own. Otherwise "Replaced by ID." The note is a plain panel, not warn: nothing is wrong, the review moved.
- "its reports compare the older content" shows only when the replaced proposal had reports.
- Carried prompts read "added a prompt on OLDID" (prompts created before the replacement, when it replaces one). A new field `event.Where` holds the unlinked part.
- `scripts/preview/behind.sh` adds a replaced pair on lime/release-notes (proposed from 0.9.0, 0.9.1 released, rebased and proposed again, with a prompt by Carla carried over).
