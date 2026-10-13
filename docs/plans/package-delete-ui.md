# Package delete in the web UI

Status: phase 1 and phase 2 built (2026-10-06). Phase 2 deviations at the end of this status line. Phase 1 deviations: the copy is named `before-delete-TEAM-NAME-<UTC time>.db` (label first, so the backup script prunes by prefix). The page heading is an `h2` because the team header already holds the `h1`. A failed copy is `account.ErrBackupFailed`, shown as a 500 page that says nothing was deleted. A backup folder that is set but not usable logs a warning and leaves web deletes off instead of stopping the server. The package-page control is a link to the confirmation page. The screenshots and Apple HIG review by the `loadout-ui` agent are still open. Phase 2 deviations: (1) the web needs `LOADOUT_BACKUP_DIR` for the whole request flow (`web.UI.Requests` is set only with it, like `Deletes`), not just the direct delete, because an approval without a copy would be refused anyway. The API routes are always served and an approval without a folder is `ErrForbidden` naming the folder. (2) The new web interface is `web.DeleteRequests` (field `UI.Requests`), since `Deletes` was taken. (3) `DeletionService` also has `Footprint` (so a member sees what a deletion removes without the right to delete), `Standing` (stale check, footprint now) and `RecordDelete`. The direct delete records through `account.Options.Deletions`, which the registry always sets, and the operator's CLI delete records too with `--reason` (default "Deleted on the registry host."). (4) `ErrBackupFailed` moved to `registry` (`account.ErrBackupFailed` is the same value) and `Snapshots` is `registry.Snapshots` (`account.Snapshots` an alias), because both services use it. (5) A stale approval is `registry.ErrBehind` (409 `behind`, the sentence says to withdraw and ask again), not a new sentinel. A second open request is `ErrExists`, now mapped to 409 `exists` (it had no status before). (6) `ErrProposalClosed` on a web decision redirects to the request page with a notice instead of an error page. (7) `proposals.go` four-eyes helpers take a `subject` rather than the author `Actor` and team separately. (8) The `propose` command's skills now write `loadout propose ./planner` because a word after a command with subcommands is read as a subcommand by `TestBuiltinSkillsNameRealCommands`. `loadout propose planner` still works. (9) The reviews list's first column head is "Package", the Reviews tab counts open requests, and the request page uses the team header (not `Bare`). (10) The admin's direct delete shows "Deletion requests" in its footprint and the fingerprint includes them. Not done: the screenshots as repo files, and the Apple HIG review by the `loadout-ui` agent. Owners, admins and the folder were decided by the owner: members, maintainers and tokens never delete, and the button stays for admins only after phase 2. Builds on `docs/plans/package-delete.md` (built: `registry.Store.PackageFootprint` and `DeletePackage`, `account.Service.PackageFootprint` and `DeletePackage` in `internal/account/admin.go`, operator-only, `loadout package delete` in `internal/cli/package.go`). Two phases that ship separately: phase 1 is a direct delete for admins and owners, phase 2 is a deletion proposal that goes through review.

## Goal

A person who governs a package's namespace deletes it from the package page, without ssh: a confirmation page lists what goes (the same footprint as the dry run), they type the package id, and loadout saves a copy of the database before it deletes. In phase 2, anyone who may propose to the namespace may instead propose the deletion with a reason. A maintainer or owner who is not the author approves it, and the record of who asked, who approved and what went is kept.

The one rule holds: both phases are database operations behind role checks and a page. Nothing runs a model and no key is involved.

## Decisions this plan takes

- **Who deletes directly (phase 1):** a person signed in on the web whose access to the package's own namespace (`id.Team()`, not the page's namespace) `Governs`: an owner of the project holding it or of a project above, or an admin. Never maintainers or members: a delete removes other people's proposals, reviews and history, and maintainers today cannot delete anything that is not theirs. Never a login token or account token: over HTTP the rule is the same as for deleting namespaces (a person on the web, or the operator on the host). The operator keeps the CLI.
- **Where:** a "Delete package" section at the end of the package page, only for those who may. It links to a confirmation page. Members and maintainers never see it. Not on Settings: Settings is about the namespace, and the package page is where the person looks at what they would delete.
- **Backup:** required and automatic. Without `LOADOUT_BACKUP_DIR` the routes are not registered and the section does not show. The CLI path is unchanged.
- **Phase 2 versus the button:** recommended, phase 2 keeps the direct button for admins only. Owners use the deletion proposal and may approve their own as owner with a note ("Deleted without review"), which is one more step than today and leaves a record.

## Phase 1: direct delete

### account: a rule, a backup, a log line

`internal/account/roles.go`, next to the other `Can` methods:

```go
// CanDeletePackage reports whether the user may delete a package of this
// namespace, with every release and proposal: those who govern it.
func (a Access) CanDeletePackage() bool { return a.Governs }
```

`internal/account/admin.go`: `PackageFootprint` and `DeletePackage` keep their signatures and share one check instead of `if !actor.Operator`:

```go
// mayDeletePackage lets the operator, and a person signed in on the web who
// governs the package's namespace when this registry keeps backups.
func (s *Service) mayDeletePackage(ctx context.Context, actor Actor, id pack.ID) error
```

- `actor.Operator`: allowed, no snapshot (as today, the operator backs up by hand).
- `actor.viaToken`: `forbidden("packages are deleted on the web by an owner or an admin, or on the registry host")`.
- `s.snapshots == nil`: `forbidden("deleting packages on the web needs a backup folder (LOADOUT_BACKUP_DIR) on the registry")`.
- otherwise `s.access(ctx, actor, id.Team())` and `!a.CanDeletePackage()`: `forbidden("only an owner of " + team + " or an admin may delete " + id)`.

The check runs before any lookup, so a refused person learns nothing about whether the package exists. `DeletePackage` for a web user then, under `s.deleteMu` (a `sync.Mutex` on `Service`, so two clicks or two admins run one after the other):

1. `s.reg.PackageFootprint`: `ErrNotFound` returns at once, before any snapshot.
2. `name, err := s.snapshots.Take(ctx, "before-delete-"+team+"-"+name)`. A failure returns `fmt.Errorf("back up before deleting %s: %w", id, err)` and nothing is deleted.
3. `s.reg.DeletePackage`.
4. `s.log.InfoContext(ctx, "package deleted on the web", "package", id, "user_id", actor.UserID, "backup", name, "releases", n, "proposals", n, ...)`. This and `registry.Service`'s existing "package deleted" line are the audit record of phase 1 (journald on the VM).

New consumer interface and option in `internal/account/service.go`:

```go
// Snapshots keeps a copy of the database before a change nothing undoes.
type Snapshots interface {
	// Take writes a consistent copy named after label and returns its
	// file name.
	Take(ctx context.Context, label string) (string, error)
}
```

`Options.Snapshots Snapshots`: nil turns web deletes off.

### snapshot: a copy with SQLite's VACUUM INTO

- `internal/store/sqlitestore/snapshot.go`: `func (s *Store) SnapshotTo(ctx context.Context, path string) error` runs `VACUUM INTO ?`. It is a read transaction in WAL mode, so publishes keep going, and it refuses an existing file.
- New package `internal/snapshot`, type `Dir`:

```go
// Dir writes database copies into one folder, named by time and label:
// before-delete-20261006T101500Z-lime-lime-app-dev.db.
type Dir struct { /* dir, db, now; immutable after New */ }

func New(dir string, db interface{ SnapshotTo(context.Context, string) error }, now func() time.Time) (*Dir, error)
func (d *Dir) Take(ctx context.Context, label string) (string, error)
```

`New` refuses a relative or missing folder. `Take` accepts only labels that pass `ident`-style characters (letters, digits, `-`), builds the name itself, removes a partial file on failure, and leaves the file 0600 (the unit's `UMask=0077`).
- Why not the backup script: `loadout serve` cannot run `sqlite3` or write a tar under its systemd hardening, and `VACUUM INTO` gives the same consistent copy without a dependency.
- Where: `LOADOUT_BACKUP_DIR` (`/var/backups/loadout`), beside the nightly archives, so it never grows the data folder or the nightly tar. `deploy/loadout.service` adds `/var/backups/loadout` to `ReadWritePaths`. `deploy/loadout-backup.sh` prunes `before-delete-*.db` with the same `KEEP_DAYS`. Restoring is the GUIDE's restore steps with this file in place of the archive's database.
- `cmd/loadout/login.go` builds `snapshot.New(stats.BackupDir, store, time.Now)` only when `BackupDir` is set, passes it as `account.Options.Snapshots`, and sets `web.UI.Deletes` to `accounts` only then.
- Test double: `registrytest`-style `accounttest.Snapshots` that records labels and can fail.

### web: two routes, one page

`internal/web/ui.go`: add to the route comment and `routeUI` (only when `h.ui.Deletes != nil`):

```
//	GET  /t/{ns}/p/{team}/{name}/delete        what deleting it removes, and the form (owners, admins)
//	POST /t/{ns}/p/{team}/{name}/delete        delete it, then back to /t/{ns} with the counts
```

These have one segment fewer than `/{version}/download`, so the mux sees no conflict.

New consumer interface in `ui.go`, a new `UI` field, and `var _ Deletes = (*account.Service)(nil)`:

```go
// Deletes is what the delete page needs from the account service; nil
// leaves out its routes and the package page's Delete section.
type Deletes interface {
	PackageFootprint(ctx context.Context, actor account.Actor, id pack.ID) (registry.PackageFootprint, error)
	DeletePackage(ctx context.Context, actor account.Actor, id pack.ID) (registry.PackageFootprint, error)
}
```

New file `internal/web/ui_delete.go`:

- `deleteTarget(w, r, t) (pack.ID, bool)`: `pack.NewID` from the path and the same depth check as `openRelease` (the package's team at the page's layer or above), else `h.notFound`. It does not read releases, so a package that only has proposals can be deleted by its URL.
- `deletePage` (GET): `Deletes.PackageFootprint(t.actor(), id)`. Errors go through `h.refused` (403 "Not allowed" with the service's sentence, 404 for not found). Renders `delete.html` with `deleteView`.
- `deletePackage` (POST, CSRF checked by `inTeam` with the usual "Form expired" page):
  - `confirm` (trimmed) must equal `id.String()`, else 400 with the page again, `Error: "Type lime/lime-app-dev exactly to delete it."`, `Field: "delete-confirm"`.
  - `seen` must equal `fingerprint(fp)` of a fresh `PackageFootprint` (a short SHA-256 of versions, open ids and counts), else 409 with the page showing the new footprint and `Error: "Something changed since you opened this page. Check the list again."`.
  - `Deletes.DeletePackage`. `ErrNotFound` here (a second submit, or someone else was faster) redirects to `/t/{ns}?done=deleted-gone&package=ID`. Other errors go through `h.refused`.
  - Success: `back(w, r, "/t/"+ns, "deleted", url.Values{"package", "releases", "proposals", "reviews", "prompts"})`.
- `deleteView`: `ID`, `Back` (package page), `Releases []string`, `Open []string` (each linked to `/t/{ns}/reviews/{id}`), `Proposals`, `Reviews`, `Prompts`, `Courses`, `Readers` (who may have it installed, below), `Seen`, `Confirm` (the typed text, kept after an error).
- `packagesPage` sets `Notice: packagesNotice(r.URL.Query())`: "lime/lime-app-dev was deleted: 3 releases, 2 proposals, 1 review, 4 prompts. A copy of the database from before is in the backup folder." and "lime/x was already deleted." Counts use the existing `plural` helper. The query is display only, like the email in `settingsNotice`.
- `packagePage` sets `packageView.CanDelete` from the viewer's access to `id.Team()`, found in `t.places` (as `inheritedMembers` does), and `DeletePath`. Never shown for a token or a member.

### Who has it installed

The registry records no installs or syncs, so it cannot say who. The page says who could: "loadout does not record who installed it. Anyone who reads lime may have it." For a project's package it names the project and its teams (from `t.places` and the namespace tree the page already has).

### Template, copy, style

`internal/web/templates/ui/delete.html`, inside the team header (tab "packages"):

- `h1` "Delete lime/lime-app-dev?"
- A panel with `dl.facts` (existing style): Releases "3: 0.1.0, 0.2.0, 1.0.0". Proposals "2, 1 open: 7f3a9c2e1b04" (links). Reviews "1". Prompts "4". Course of "none" or the namespaces.
- What happens, as plain sentences: "This cannot be undone. Every release, proposal, review and prompt of lime/lime-app-dev is removed, and the name is free to publish again. People who installed it keep their files. Their next loadout sync says it was removed from the registry and how to uninstall it. Older versions of loadout report a failed check until they are updated. loadout saves a copy of the database first. Restoring it also undoes everything changed after."
- The installed sentence above.
- Form `method="post"`: hidden `csrf` and `seen`. `label for="delete-confirm"` "Type lime/lime-app-dev to confirm". `input id="delete-confirm" name="confirm" class="field" required autocomplete="off" autocapitalize="off" spellcheck="false"` with `{{invalid $.Field "delete-confirm"}}`, `aria-describedby` the hint and, on error, `form-error`, and `autofocus` only after an error. `div.row` with Cancel (`a.btn`, back to the package page) and `button.btn.btn-lg.btn-danger` "Delete package".
- The package page's section: `section.panel` with `h2` "Delete package", one sentence "Removes every release and proposal. You confirm on the next page.", and `a.btn.btn-danger` "Delete package…".

No new colour tokens: `.btn-danger` uses `--bad-text`, defined for light, dark and the forced dark theme, and the error uses `.alert` with `role="alert"` and `id="form-error"` from `layout.html`. One small rule if needed: `.danger-zone { border-color: var(--bad-text); }` on the section, checked for contrast in both themes. At 390 px, `dl.facts` already stacks. The button row wraps with Cancel first, and the destructive button is never the default action (no `btn-primary`). No JavaScript: the page is a GET, the form a plain POST, the error a server render.

### Tests, phase 1

- **account** (`internal/account/packages_test.go`, extend the table): operator allowed. Web admin allowed. Owner of `acme` allowed for `lime/x` and `acme/x`. Maintainer, member, an owner of another project, and every token actor (`TokenActor`, `WholeAccountActor`) get `ErrForbidden` with the sentence. No snapshots configured is `ErrForbidden` for a web user and fine for the operator. A failing snapshot deletes nothing. A missing package is `ErrNotFound` and takes no snapshot. The log line names the user and the backup.
- **snapshot** (`internal/snapshot/snapshot_test.go`): name format, bad labels refused, an existing file refused, partial file removed on error. `sqlitestore/snapshot_test.go`: the copy opens and has the same rows, taken while a write transaction waits.
- **web** (`internal/web/ui_delete_test.go`, `newUISiteWith` setting `Deletes` and an admin and an owner of `acme` added to the site): table over admin, owner, maintainer (alice), member (bob), anonymous: GET status 200, 200, 403, 403, redirect to sign in. The package page shows "Delete package" only for the first two. Plus: bad CSRF is 403 "Form expired" and nothing deleted. Wrong typed name is 400 with `aria-invalid` and the package still there. A changed footprint is 409. A sibling team's package (`koala/...` from `/t/lime`) is 404. Success redirects to `/t/lime?done=deleted...` and the notice reads the counts. A second POST lands on "already deleted". `Deletes` nil means 404 on the route and no section.
- **e2e** (`cmd/loadout/package_test.go`): serve with `LOADOUT_BACKUP_DIR` set to a temp folder, sign an admin in through the web test helpers, delete `lime/planner` through the form, check the snapshot file exists and opens, bob's `sync` says "removed from the registry".
- **deploy** (`deploy/deploy_test.go`): the unit lists the backup folder in `ReadWritePaths`.
- **UI review** (the `loadout-ui` agent, `loadout-preview` skill): `seed.sh` gains an admin session (`ada-session`, admin) and uses `olga-session` (owner of acme from `course.sh`). Proxy ports for them. `shoot.mjs` jobs: the package page and the delete page as admin, light and dark, at 1280 and 390 px, with `"nojs": true` once. The wrong-name error state. The packages page with the notice. Review against `docs/UI.md`, the apple-design checklist and `accessibility-audit` (label, focus on the invalid field, error tied by `aria-describedby`, the destructive button's name, 44 px targets on touch).

## Phase 2: deletion proposals

### Why a table of its own

A deletion is not a version: proposals have `version`, `digest` and a `bundle` that are `NOT NULL`, a unique open index per version, reports, prompts and a diff, none of which mean anything here. And `DeletePackage` removes every proposal of the package, so a deletion kept in `proposals` would delete its own record. A `deletions` table keeps the request and its outcome after the package is gone.

### Migration `0016_deletions.sql`

```sql
-- Requests to delete a package, reviewed like proposals. A row outlives
-- the package: it is the record of who asked, who decided and what went.
CREATE TABLE deletions (
    id                  TEXT    PRIMARY KEY,           -- del-xxxxxxxxx
    team                TEXT    NOT NULL,
    name                TEXT    NOT NULL,
    reason              TEXT    NOT NULL,
    newest              TEXT    NOT NULL DEFAULT '',   -- newest release when asked
    author_token_id     TEXT    NOT NULL,
    author_label        TEXT    NOT NULL,
    author_user_id      TEXT    NOT NULL DEFAULT '',
    status              TEXT    NOT NULL CHECK (status IN ('open', 'approved', 'rejected', 'withdrawn', 'gone')),
    created_at          TEXT    NOT NULL,
    decided_at          TEXT    NOT NULL DEFAULT '',
    decided_by_token_id TEXT    NOT NULL DEFAULT '',
    decided_by_label    TEXT    NOT NULL DEFAULT '',
    decided_by_user_id  TEXT    NOT NULL DEFAULT '',
    decision_note       TEXT    NOT NULL DEFAULT '',
    self_approved       INTEGER NOT NULL DEFAULT 0,
    removed             TEXT    NOT NULL DEFAULT ''    -- JSON counts at approval
);
CREATE UNIQUE INDEX deletions_one_open ON deletions (team, name) WHERE status = 'open';
CREATE INDEX deletions_team_created ON deletions (team, created_at);
```

A new table, so old rows need nothing. `gone` means the package was deleted another way (directly, or by the operator) while the request was open.

### registry: types, store, service

New file `internal/registry/deletion.go`: `Deletion` (ID, Package, Reason, Newest, Author `Actor`, Status, CreatedAt, DecidedAt, DecidedBy, DecisionNote, SelfApproved, Removed `PackageFootprint`), `DeletionFilter` (team, status), and:

```go
type DeletionStore interface {
	// CreateDeletion stores an open request; ErrExists if the package has one.
	CreateDeletion(ctx context.Context, d Deletion) error
	Deletion(ctx context.Context, id string) (Deletion, error)
	Deletions(ctx context.Context, f DeletionFilter) ([]Deletion, error)
	// DecideDeletion closes an open request as rejected or withdrawn.
	DecideDeletion(ctx context.Context, id string, d Decision) (Deletion, error)
	// ApproveDeletion deletes the package and closes the request as
	// approved with what went, in one transaction. ErrStale if a release
	// newer than d.Newest exists; ErrProposalClosed if it is not open.
	ApproveDeletion(ctx context.Context, id string, d Decision) (Deletion, error)
}
```

- `sqlitestore/deletions.go`: `ApproveDeletion` and `DeletePackage` share a `deletePackageTx(ctx, tx, id, at)` taken out of `packages.go`. That helper also sets open deletions of the package to `gone`. `Store.DeletePackage` gains `at time.Time` for it (the registry service passes `s.now()`), and `PackageFootprint` gains `Deletions []string`, the open requests that close.
- `registrytest/memstore.go`: the same under `m.mu`. New contract `RunDeletionContract` in `registrytest/deletioncontract.go`: one open per package, ErrExists for a second, reject and withdraw, approve deletes everything the package contract checks and the row survives with its counts, `ErrStale` after a newer release, a direct `DeletePackage` closes open requests as `gone`, the package can be published and asked to be deleted again afterwards.
- `DeletionService` in `internal/registry/deletions.go`, built like `ProposalService` (stores, `WithOwners`, a `Snapshots` consumer interface of its own, clock, log): `Propose(pr, id, reason)`, `Deletions`, `Deletion`, `Approve(pr, id, ApproveInput)`, `Reject`, `Withdraw`. It reuses `checkDecider`, `selfError` and `checkOwnApproval`, changed to take the author `Actor` and the team instead of a `Proposal`, so the four-eyes rules are one code for both.
  - Propose: a person (no machine token) who may propose to the package's namespace (`pr.CanPropose`-style check as for proposals, so readers through a team below may ask for a project's package). The package must exist. Reason required, at most `MaxNoteSize`.
  - Approve: `checkDecider` (a publish token of the team: maintainer, owner or admin, never a machine), not the author, except an owner of the namespace through memberships with `AsOwner` and a note, recorded as `SelfApproved` and shown as "Deleted without review". Snapshot first (as phase 1, refused without one), then `ApproveDeletion`. Logs "deletion approved" with ids and counts.
  - Reject needs a teammate as for proposals. Only the author withdraws.

### Who has it installed

Same as phase 1: not knowable, the page says who could have it.

### API and CLI

- `internal/api/deletions.go`: `DeletionRequest{Package, Reason}`, `Deletion` wire type. `internal/server/deletions.go`: `POST /v1/deletions`, `GET /v1/deletions?team=`, `GET /v1/deletions/{id}`, `POST /v1/deletions/{id}/approve|reject|withdraw`, ids checked against `^del-[a-z0-9]{1,60}$`. The proposals' `idPattern` refuses a hyphen, so an older CLI asked to approve `del-...` gets a clean 400.
- `internal/cli/propose.go`: subcommand `propose delete TEAM/NAME --reason TEXT [--remote R]`. A folder named `delete` is then written `./delete`. The help says so.
- `internal/cli/review.go`: `review list` adds open deletions (kind column "delete"). `review show`, `approve [--as-owner --note]`, `reject`, `withdraw` route ids starting `del-` to the deletion endpoints. `review compare` and `report` refuse them with a sentence.

### Web

- Routes in `ui.go`: `GET /t/{ns}/deletions/{id}` and `POST /t/{ns}/deletions/{id}/{action}` (not under `reviews/`, where `reviews/delete/{id}` would conflict with `reviews/{id}/diff`), and `GET|POST /t/{ns}/p/{team}/{name}/delete-request`.
- Package page: members and maintainers see "Ask to delete…" (to `delete-request`, a form with the footprint and a required Reason textarea). With an open request, "Deletion asked by bob, 2 days ago" linking to it instead.
- `reviews.html`: deletion rows mixed in by date with a `pill-bad` "Delete" pill, the reason as description, the author and age. The Reviews tab count adds open deletions.
- New `deletion.html`: the reason, the footprint now (and "1.2.0 was published after this was asked" when stale, with Approve disabled), the installed sentence, Approve / "Approve as owner…" (popover with required note, as `proposal.html`), Reject…, Withdraw…, and after a decision the outcome: "Deleted 3 releases, 2 proposals ... on 6 Oct by alice" or "Deleted without review by olga: NOTE".
- Phase 2 change to phase 1: the direct page keeps working for admins only (`CanDeletePackage` becomes `a.Role == RoleAdmin`), asks for a reason too, and writes an approved, self-approved `deletions` row in the same transaction, so every delete on the web leaves a record. The operator's CLI delete writes one too, with author "operator".

### Tests, phase 2

Contract suite above for both stores. `DeletionService` table tests per role and token kind (member proposes, maintainer approves, author refused, owner self-approves only with `AsOwner` and a note, machine token refused, stale refused, no snapshot refused). Server handler tests. CLI tests for routing by `del-`. Web tests for each role on the package page, the request form (missing reason is 400 with the field marked), the reviews list row, approve, reject, withdraw, CSRF. E2e: bob asks, alice approves on the web, the row stays with counts, bob's `sync` says removed. Screenshots of the request form, the list with a deletion row, and the deletion page open, approved and "without review", light and dark, 390 px.

## Data and API changes

- Phase 1: `account.Access.CanDeletePackage`, `account.Snapshots`, `account.Options.Snapshots`, `sqlitestore.Store.SnapshotTo`, `internal/snapshot`, `web.Deletes`, `web.UI.Deletes`. No wire type, no API route, no migration.
- Phase 2: the `deletions` table, `registry.Deletion`, `DeletionStore`, `DeletionService`, `Store.DeletePackage` gains `at`, `PackageFootprint.Deletions`, API routes and types.

## Order of work

Each step ends with `make check` green.

Phase 1:

1. `sqlitestore.SnapshotTo` and `internal/snapshot` with tests.
2. `CanDeletePackage`, `mayDeletePackage`, `Options.Snapshots`, the mutex and the log line in `account`. Table tests.
3. `cmd/loadout` wiring. `deploy/loadout.service` and `loadout-backup.sh`. Deploy test.
4. `web.Deletes`, `ui_delete.go`, `delete.html`, the package page section, the packages notice. Web tests.
5. e2e test. Preview seed sessions. Screenshots and the UI review, findings recorded in `docs/UI.md`.
6. Docs (below), `docs/guide/build.sh`, `go test ./docs/guide/`.

Phase 2:

7. Migration, `Deletion` types, `DeletionStore` in both stores, contract. `deletePackageTx` shared and `gone`.
8. `DeletionService` with the shared four-eyes helpers. Tests.
9. API, server routes, CLI `propose delete` and `review` routing. Tests.
10. Web: request form, list rows, deletion page, the phase 1 page narrowed to admins with a reason and a row. Tests, e2e, screenshots.
11. Docs.

## Docs to update

- `docs/GUIDE.md`: phase 1, a "Delete a package" section (who, the confirmation, the backup copy and how to restore from it). Phase 2, asking for a deletion and reviewing one. `propose delete` in the command table.
- `docs/DESIGN.md`: the Roles table gets a "Delete packages" column (owner and admin, phase 2: admin directly, others through review), the Registry bullet and the Web pages bullet, the operator sentence, `LOADOUT_BACKUP_DIR` now also receives pre-delete copies. Phase 2 the `deletions` table and the Review rules.
- `docs/UI.md`: the delete page and the review record.
- `internal/builtin/skills/loadout-admin/SKILL.md`: the web way first, the host command as the fallback.
- `docs/CHANGES.md`: one line under Unreleased.

## Risks

- **Irreversible.** Mitigated by the typed id, the fingerprint check, the automatic copy, the log line, and in phase 2 the second person.
- **Disk.** Every delete writes a full copy of the database. A full disk makes the copy fail and the delete refused, which is the safe side. Copies are pruned after `KEEP_DAYS`. A burst of deletes multiplies the size. The server page does not count these copies yet.
- **Time.** `VACUUM INTO` on a large database takes seconds inside the 60 s write timeout. Publishes continue (WAL), and the mutex keeps one delete at a time.
- **Owners are trusted more than before.** An owner can now remove a team's package without a second person until phase 2. The decision below lets the user choose admins only.
- **Stale page.** The fingerprint is compared just before the delete, not inside its transaction. A publish in that gap of milliseconds is deleted too. Phase 2's `ErrStale` closes it for proposals.
- **Older CLIs** still fail `sync` with a 404 after a delete (unchanged from the CLI delete), and cannot approve `del-` ids.
- **Live data.** The unit change and migration 0016 go out with a backup first. Nothing is deleted on a live registry without its operator's yes.

## Not in this step

- Undelete, soft delete, tombstones that block a name.
- Deleting one version.
- Bulk delete, deleting a namespace with its packages.
- Recording installs, so the page could say who has it.
- Counting the pre-delete copies on the server page.
- Comments, reports or prompts on deletion requests.
