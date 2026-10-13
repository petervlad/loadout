# Package delete

Status: built (2026-10-06), steps 1 to 5; step 6 (operate) waits for the user's yes.

Decisions made: open proposals are deleted with the package and the dry run lists each by id; the command is a dry run unless `--yes`, with no extra guard, and `--yes` prints the counts it really removed; the sync change (`update.ErrGone`, "removed from the registry", exit 0, files kept) ships in the same change. Where the build differs from the text below: the e2e test uses Bob (publish token) as the installing machine through the existing `team` helper, and `PackageFootprint` is a method of `registry.Store` implemented for the memory store in `registrytest/mempackages.go`.

## Goal

The operator on the registry host can remove a whole package from the registry with `loadout package delete TEAM/NAME --data DIR`: every release, every proposal with its reviews and prompts, and the package's place as a wiki course. The command lists what would go and deletes nothing unless `--yes` is given. It exists because a registry collects dead packages (redirect stubs and renamed agents) and today the only way to remove them is hand-written SQL on the host.

The one rule holds: this is a database operation on the host and a message in `sync`. Nothing runs a model and no key is involved.

## What a package is in the database

A package has no row of its own. It is the pair `(team, name)` in these places, all in the one SQLite file:

| Table (migration) | Keyed by | Link |
| --- | --- | --- |
| `releases` (0001, 0009, 0010, 0014) | `team`, `name` | `proposal_id` names a proposal of the same package |
| `proposals` (0003, 0004, 0009, 0010, 0014) | `team`, `name` | `replaces`, `replaced_by` name proposals of the same package |
| `proposal_reviews` (0003, 0008) | `proposal_id` | `REFERENCES proposals (id)`, no cascade |
| `proposal_prompts` (0011) | `proposal_id` | `REFERENCES proposals (id)`, no cascade |
| `namespaces.course` (0015) | text `team/name` (`courseText`) | the wiki's course, in any namespace |

`foreign_keys(ON)` is set in `dsn`, so reviews and prompts must go before their proposals. Tokens, memberships, model tiers and wiki revisions are keyed by namespace, never by package, and stay. Replacements and `releases.proposal_id` never point across packages (a replace needs the same package), so nothing dangles afterwards.

## Design

### registry: a footprint value and two Store methods

New file `internal/registry/packages.go`:

```go
// PackageFootprint is what the registry keeps of one package: what
// `loadout package delete` lists before it deletes, and counts after.
type PackageFootprint struct {
	ID        pack.ID
	Releases  []semver.Version // lowest first
	Kind      pack.Kind        // of the newest release; empty without one
	Proposals int              // every status
	Open      []string         // ids of open proposals, oldest first
	Reviews   int              // reports attached to its proposals
	Prompts   int              // prompt rows, removed ones included
	Courses   []string         // namespaces whose wiki course it is, by name
}
```

`registry.Store` gains two methods. They sit on `Store`, not `ProposalStore`, because the subject is the package and its releases; both implementations keep proposals in the same database, as `ApproveProposal` already relies on.

```go
	// PackageFootprint returns what DeletePackage would remove, or
	// ErrNotFound when the package has no release and no proposal.
	PackageFootprint(ctx context.Context, id pack.ID) (PackageFootprint, error)
	// DeletePackage removes every release and proposal of id, the
	// proposals' reviews and prompts, and clears id as any namespace's
	// course, in one transaction, and returns what it removed. It returns
	// ErrNotFound when the package has no release and no proposal.
	DeletePackage(ctx context.Context, id pack.ID) (PackageFootprint, error)
```

A package with proposals but no release (never approved) is a package: it is deleted, not `ErrNotFound`.

### sqlitestore

New file `internal/store/sqlitestore/packages.go`. One helper `footprint(ctx, q, id)` reads with plain `SELECT`s and is shared by both methods (`queryer` in `migrate.go` needs a `QueryContext` sibling, or the helper takes `*sql.Tx` and `*sql.DB` through a two-method interface). `DeletePackage` runs, in one `BeginTx` (immediate, per `dsn`), with the rollback pattern of `DeleteNamespace`:

1. `footprint` inside the transaction; no releases and no proposals is `ErrNotFound`.
2. `DELETE FROM proposal_prompts WHERE proposal_id IN (SELECT id FROM proposals WHERE team = ? AND name = ?)`
3. `DELETE FROM proposal_reviews WHERE proposal_id IN (...)`, the same subquery.
4. `DELETE FROM proposals WHERE team = ? AND name = ?`
5. `DELETE FROM releases WHERE team = ? AND name = ?`
6. `UPDATE namespaces SET course = '' WHERE course = ?` with `courseText(id)`.
7. Commit, and return the footprint read in step 1.

`seq` is `AUTOINCREMENT`, so a later publish under the same name never reuses a deleted seq and `Changes` clients see no step back. The file does not shrink (free pages are reused); `VACUUM` is not part of this.

### registrytest

`MemStore` implements both methods in `internal/registry/registrytest/memstore.go` (releases filtered out of `m.releases`, proposals out of `m.proposals` with their reviews and prompts counted first, `Course` cleared in `m.namespaces`), under `m.mu`.

### registry.Service and account.Service

- `registry.Service.PackageFootprint(ctx, id)` and `DeletePackage(ctx, id)` in `packages.go` wrap the store (`"delete package %s: %w"`) and log `package deleted` with the id and the counts at info. Like `Service.DeleteNamespace` they take no principal: they are not reachable over HTTP.
- `account.Registry` (in `internal/account/service.go`) gains the two methods; `var _ Registry = (*registry.Service)(nil)` keeps it honest.
- `account.Service.PackageFootprint(ctx, actor, id)` and `DeletePackage(ctx, actor, id)` go in `internal/account/admin.go` beside `DeleteUser`. Both refuse anyone but `actor.Operator` with `forbidden("packages are deleted on the registry host")`; the footprint too, since its counts cross namespaces. Admins on the web and maintainers get `ErrForbidden`. Errors keep the sentinel: a missing package is `registry.ErrNotFound`.

### Open proposals: deleted with the package

Open proposals are deleted, not a reason to refuse. Only the author withdraws and only a teammate's publish token rejects, so the operator on the host cannot close them, and a dead stub with a forgotten open proposal would be undeletable. The dry run names each open proposal so the operator sees them before `--yes`.

### CLI

New file `internal/cli/package.go`, `newPackageCmd(d)` added to `NewRoot` in `cli.go`. It only parses, calls `account.Service` through `withRegistry`, and prints.

```
loadout package delete TEAM/NAME [--data DIR] [--yes]
```

- `--data` as in `newUserCmd` (persistent on `package`, default `d.defaultDataDir()`), so `AfterRun` stays quiet for it.
- The argument goes through `pack.ParseID`.
- Without `--yes` (the default): `PackageFootprint`, then print and exit 0:

```
lime/lime-app-dev (agent) would be deleted:
  releases   3: 0.1.0, 0.2.0, 1.0.0
  proposals  2, 1 open: p_7f3a
  reviews    1
  prompts    4
  course of  none
Nothing was deleted. Run it again with --yes to delete it. Back up the database first.
```

- With `--yes`: `DeletePackage`, then one line with the counts it really removed: `deleted lime/lime-app-dev: 3 releases, 2 proposals, 1 review, 4 prompts; cleared it as the course of 0 namespaces`.
- A missing package: `lime/x is not on this registry.` and exit 1.

The formatting of the footprint is a small function in `package.go` with its own table test.

### Machines that have the package installed

Today `sync` asks `update.Registry.Latest`; `registryAdapter.Latest` (`internal/cli/connect.go`) gets a 404 `not_found` from `client.Releases`, `syncOne` returns `OutcomeFailed`, and every later `sync` exits 1 with "check lime/x: registry: 404 ..." until the person uninstalls. That needs a change:

- `internal/update`: `var ErrGone = errors.New("package is no longer on its registry")` and `OutcomeGone Outcome = "removed from the registry"`. `syncOne` maps `errors.Is(err, ErrGone)` to `OutcomeGone`. It is not `Behind`, not a failure, tells the notifier nothing, and `remember` drops its sync-state record (the default branch already does).
- `registryAdapter.Latest` wraps `api.ErrNotFound` from `Releases`, and an empty list, as `update.ErrGone`. A 403 (a team you left) stays a failure, as today.
- `printSync` prints `-` as latest for it and, per gone install, `lime/x is no longer on its registry. loadout keeps your files; remove them with: loadout uninstall lime/x --scope user` (the project form names the folder, like `writeHeld`). Exit code 0.
- Files are never removed by `sync`: removing files nobody asked to remove breaks "never overwrite or delete what the person did not ask for", and an edited file may be the person's work.
- `status` and `notices` are offline and read the lock: the entry still shows as installed, with no stale "update available" since the record was dropped. No change.
- `install lime/x` gets the registry's 404, `list` and `install --all` no longer show it, `uninstall` works from the lock. No change.
- Older CLIs keep the current failure until they update or uninstall; the GUIDE says so.

## Data and API changes

- Two `registry.Store` methods and the `PackageFootprint` type; two `account.Registry` methods.
- No HTTP route, no wire type, no web page: the operation is host-only.
- `update.ErrGone`, `update.OutcomeGone`.

## Migration

None. No column or table changes; old rows need nothing.

## Order of work

Each step ends with `make check` green.

1. **Store.** `PackageFootprint` type, the two `Store` methods, `MemStore`, `sqlitestore/packages.go`, the contract (below) wired in `registrytest/memstore_test.go` and `sqlitestore/sqlitestore_test.go`.
2. **Services.** `registry.Service` methods with the log line; `account.Registry` and `account.Service` methods with the operator check; unit tests.
3. **CLI.** `internal/cli/package.go`, root wiring, footprint printer test, e2e test of the command.
4. **Sync.** `update.ErrGone`, `OutcomeGone`, the adapter, `printSync`; unit tests and the e2e sync part. This step can also go first: it changes nothing until a package disappears.
5. **Docs.** GUIDE, DESIGN, `loadout-admin` built-in skill; `sh docs/guide/build.sh && go test ./docs/guide/`.
6. **Operate** (only with the user's yes): deploy, back up, dry run each stub, show the user the three listings, then `--yes` one by one.

## Tests

- **Contract** `RunPackageDeleteContract(t, func(t) DeleteBackend)` in `internal/registry/registrytest/deletecontract.go`, with `DeleteBackend` = `registry.Store` + `registry.ProposalStore` + `registry.NamespaceStore`. Table cases:
  - missing package: both methods `ErrNotFound`;
  - releases only: all gone, `Releases` is `ErrNotFound`, `Packages(team)` and `Changes` omit it, a sibling `lime/other` and `acme/same-name` are untouched;
  - proposals of every status (open, approved, rejected, withdrawn, replaced) with reviews and prompts (one removed): counts match, `Proposal(id)`, `ReviewText`, `Proposals(filter)` find nothing, another package's reviews and prompts stay;
  - proposals only, no release: deleted, not `ErrNotFound`;
  - course: namespaces whose course is the package are cleared and listed, another course stays;
  - `PackageFootprint` before equals what `DeletePackage` returns;
  - after delete: publishing `1.0.0` again succeeds with a seq above every earlier one, and a proposal at a deleted open proposal's version can be created (the `proposals_one_open` index is free).
- **SQLite only** (`sqlitestore/packages_test.go`): a `TEMP TRIGGER` that raises on `DELETE FROM releases` makes `DeletePackage` fail, and every proposal, review and prompt is still there (one transaction).
- **account** (`internal/account/admin_test.go` or a new `packages_test.go`): table of actors, operator allowed, a signed-in admin and a token actor `ErrForbidden`, missing package `ErrNotFound`.
- **update** (`update_test.go`): a fake registry returning `ErrGone` gives `OutcomeGone`, no failure, record dropped, notifier not called; a 403 stays `OutcomeFailed`.
- **cli**: `printSync` with a gone result prints the uninstall line and returns nil; the footprint printer table.
- **e2e** `cmd/loadout/package_test.go`, real binary, `person`/`newSession`, `startServer` on a throwaway data folder: alice publishes `lime/planner` 0.1.0 and 0.2.0 and proposes 0.3.0; bob installs it.
  1. `package delete lime/planner` prints both versions and the open proposal and exits 0; bob's `list` still shows it.
  2. `package delete lime/planner --yes` prints `2 releases, 1 proposal`; `list` no longer shows it; the proposal is gone from `review list`.
  3. bob's `sync` exits 0, says "removed from the registry" and the uninstall command; his agent file is still on disk; `uninstall lime/planner` works.
  4. A second `--yes` exits 1 with "not on this registry".
  5. Publishing `lime/planner` 0.1.0 again works.
- `builtin_skills_test.go` checks the new command if the `loadout-admin` skill names it.

## Docs to update

- `docs/GUIDE.md`: a row `loadout package delete TEAM/NAME [--yes]` | "List what a package holds and, with `--yes`, delete it with its releases and proposals (on the host)" next to `user delete`; in the sync section, what "removed from the registry" means and that older loadouts show a failure until updated or uninstalled. Rebuild with `docs/guide/build.sh`.
- `docs/DESIGN.md`: a Registry bullet (operator-only delete, what goes, one transaction, open proposals deleted, course cleared, seq not reused, no tombstone); `PackageFootprint`/`DeletePackage` in the `registrytest` sentence; `OutcomeGone` in the sync part; the `Roles` operator sentence.
- `internal/builtin/skills/loadout-admin/SKILL.md`: one line under the host commands, with "ask first and back up".
- `docs/CHANGES.md`: one line under Unreleased.

## Risks

- **Irreversible.** The only undo is restoring the backup, which also undoes everything since. Mitigations: dry run by default, backup step in the skill, the info log with counts.
- **Name reuse at a lower version.** After a delete, the name may be published again at any version. A machine that kept 1.0.0 installed sees 0.1.0 as older and reports "up to date". The GUIDE tells authors to reuse a name only above the deleted version.
- **A 404 that is not a deletion.** Only `not_found` from the releases endpoint is read as gone; the worst case is a wrong sentence, since files are never touched.
- **Older CLIs** fail their sync with a 404 until updated or until the person uninstalls.
- **Concurrency with `serve`.** The delete takes the write lock (immediate transaction, `busy_timeout(5000)`); a large knowledge base's blobs make it a short pause for publishes, not a failure.
- **Wrong `--data`.** Opening a folder without a database creates an empty one and answers "not on this registry"; harmless, and the same as `user delete` today.
- **Courses cleared elsewhere.** The dry run names the namespaces whose course goes.

## Not in this step

- Deleting one version (it breaks "versions are immutable" for installs of that version and needs its own plan).
- Undelete, soft delete or tombstones that block a name.
- An HTTP route or a web button for admins.
- Deleting a namespace together with its packages.
- `status` saying a package is gone (it is offline; `sync` says it).
- Removing installed files on people's machines, and `VACUUM` on the registry.
