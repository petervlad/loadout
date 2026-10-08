# Owners approve their own proposals

Status: built (2026-09-29), with the recommended answers to the three
decisions: owners only, an explicit `--as-owner` step, and a note required
when approving your own proposal. Where the build differs:

- There is no `history` command; `review show`, `review list --all` and the
  web pages carry the mark. `review list` says "approved without review" to
  keep the column short.
- `Service.Publish` gets the owner hint through its own option,
  `WithOwnerHints`, since `WithOwners` is the proposal service's.
- The package page marks the version with a short "Without review" pill.
- The site is not rebuilt and nothing is deployed in this step.

## Goal

When a namespace requires review, nobody publishes directly and the author can
never approve their own proposal (`checkReviewer` in
`internal/registry/proposals.go`: "the author cannot approve their own
proposal; ask a teammate"). A small team, or an owner alone on a project, then
has no way to release. Owners may now approve and release any open proposal,
their own included, without a second person. It stays a proposal (prompts,
note, diff, activity), and the record says plainly that review was skipped, so
teammates can see it.

The one rule holds: this is a permission and a record on the registry. No model
runs anywhere and no key is involved.

Why owners get nothing new: an owner can already turn `require_review` off,
publish and turn it on again, with no trace on the version. This change makes
that bypass one visible step instead of three silent ones.

## Who may do it

Exactly the users who govern the package's namespace through a membership: an
owner of that namespace (a project) or an owner of a project above it (every
team below). This is `account.Access.Governs` as `Resolve` computes it, with the
admin flag left out (as `Roles.accessAsMember` already does for login tokens).

| Principal | Own proposal, `--as-owner` |
| --- | --- |
| member, maintainer of the team | refused, as today |
| owner of the project, proposal in the project or a team below | allowed, with a note |
| owner of another project | refused (not found or forbidden, as today) |
| admin who is not an owner there | refused; they make themselves owner first (visible in Members) |
| admin who is also an owner there | allowed, as owner |
| machine token (made by anyone, owners included) | refused: machine tokens never decide |
| token made on the registry host (no user) | refused: no user, so nobody to call an owner |

Admins are left out on purpose. A login token never carries admin rights today
(GUIDE: "Admin actions ... are not available with a login token"); counting the
flag would let one stolen admin token release into every team without review,
which is a new power, not a shortcut. Leaving it out also gives one answer on
the CLI, the API and the web. See decision 1.

## Design

### account: who owns

- `(*Roles).Owns(ctx, userID, namespace string) (bool, error)`: `accessAsMember`
  then `Governs`. An unknown user or namespace is false, not an error.
- `Place` gains `Owns bool`, filled by `Roles.Places` from the memberships it
  already loads, so the web page needs no extra query to show the button.

### registry: one rule, one record

The registry owns a small interface for what it needs, like `Memberships`:

```go
// Owners tells whether a user owns a namespace through their memberships:
// an owner of it or of a project above it. The admin flag does not count.
type Owners interface {
	Owns(ctx context.Context, userID, namespace string) (bool, error)
}

// WithOwners lets owners approve their own proposals; nil (a registry
// without accounts) keeps four eyes for everyone.
func WithOwners(o Owners) ProposalOption

// ApproveInput is what a reviewer sends with an approval.
type ApproveInput struct {
	Note string
	// AsOwner says the caller knows they approve their own proposal and
	// skip review. It is refused unless they own the namespace.
	AsOwner bool
}

func (s *ProposalService) Approve(ctx context.Context, pr Principal, id string, in ApproveInput) (Proposal, Release, error)
```

- `checkReviewer` splits in two: `checkDecider` (machine token, publish token of
  the team) and `isSelf` (same actor or same label). Reject and withdraw keep
  their rules unchanged; an owner still withdraws, not rejects, their own.
- Approve on a self proposal: without `AsOwner` it is refused as today, and the
  message adds "as an owner of acme you may approve it yourself: loadout review
  approve ID --as-owner --note WHY" when `Owns` says so. With `AsOwner` it
  needs `pr.UserID != ""`, `Owns` true, and a non-empty note after trimming
  (`ErrInvalid`: "say why this is released without review"). `Owns` is asked
  only on this path, so ordinary approvals cost no extra query.
- `AsOwner` on someone else's proposal is ignored: a second person reviewed it,
  so nothing is bypassed and nothing extra is recorded.
- `Decision` gains `SelfApproved bool`; `Proposal` gains `SelfApproved bool`.
  `Release` gains `Proposal string` (the id it came from, empty for a direct
  publish) and `SelfApproved bool`. `ApproveProposal` writes all of them in its
  one transaction, so a release can never say less than its proposal.
- The log line is "proposal approved by its author as owner", with user id.
- The `publish` refusal under `require_review` (`Service.Publish`) adds the same
  hint for owners: propose, then approve as owner.

## Data and API changes

- Migration `0010_self_approval.sql`: `proposals.self_approved INTEGER NOT NULL
  DEFAULT 0`, `releases.proposal_id TEXT NOT NULL DEFAULT ''`,
  `releases.self_approved INTEGER NOT NULL DEFAULT 0`. Old rows stay valid:
  every past approval had a second person, and old releases say nothing about
  their proposal.
- `api.DecisionRequest` gains `AsOwner bool json:"as_owner,omitempty"`.
  `api.Proposal` and `api.Release` gain `self_approved` (and the release
  `proposal`), all `omitempty`, so old clients read new answers unchanged.
- The server still decodes `DecisionRequest` strictly. The client sends
  `as_owner` only when set, so a normal approve still works against an old
  registry; with `--as-owner` an old registry answers "unknown field", which
  the client turns into "this registry is older than approving as owner; ask a
  teammate or update the registry".

## CLI and web changes

- `loadout review approve ID --as-owner --note WHY`. Output: "approved your own
  proposal ID as owner, without review: X@V is released on R". `review show`,
  `review list --all` and `history` say "approved by its author as owner,
  without review" next to that version.
- Web, proposal page: `decide` in `ui_reviews.go` gets a third case. The author
  who `Owns` sees Withdraw… and **Approve as owner…**, a `details` popover like
  Reject (works without JavaScript): "This releases V to everyone without a
  second person's review. The activity will say so." plus "No comparison is
  attached." when there is none, a required note, hidden `as_owner=on`, Cancel
  and "Approve without review". The why text: "You proposed this version. As an
  owner of acme you may release it yourself; teammates will see that review was
  skipped." Everyone else sees today's buttons and texts.
- `decideProposal` reads `as_owner` from the form. The service checks
  everything again; the page only hides what would be refused.
- Activity: "approved own proposal as owner, without review: “note”", tone
  `warn` (a `.dot-warn` token in `style.css` if missing). The status pill reads
  "Released without review" on the proposal page and the reviews list; the
  package page marks that version the same way.

## Order of work

1. account: `Roles.Owns`, `Place.Owns`, table tests in `roles_test.go`.
2. registry: `Owners`, `WithOwners`, `ApproveInput`, self-approval rule, new
   fields; memstore; `registrytest` proposal contract asserts the flags on the
   proposal, the release and a later `Releases` read. Without `WithOwners`
   nothing changes.
3. sqlitestore: migration 0010, release and proposal scans; contract suite green.
4. api, server, client: `as_owner`, response fields, old-registry message;
   wire `WithOwners(roles)` in `cmd/loadout`.
5. CLI: `--as-owner`, outputs, `show`, `list`, `history`, publish hint; e2e.
6. Web: `decide`, popover, activity, pill, package page mark; web tests; UI
   review with the Apple design checklist; record it in `docs/UI.md`.
7. Docs: DESIGN.md (Review bullets, roles table "Approve, reject" column for
   owner and "owner of the project above": "yes, their own too as owner, with a
   note, recorded"; admin row unchanged; migration list), GUIDE.md (roles table,
   Review section "Nobody approves their own proposal" gets the owner
   exception, command table), rebuild the site. Back up the live database
   before the deploy, since it migrates.

## Tests

- registry: one table over principals (member, maintainer, author maintainer,
  project owner on a team proposal, project owner on a project proposal, owner
  of another project, admin without membership, admin who owns, machine token
  made by an owner, host token) times (own or not, `AsOwner` or not, note or
  not), with a fake `Owners`. Plus: nil `Owners` keeps today's refusal, an
  `Owns` error is returned wrapped, and a version conflict still wins.
- account: `Roles.Owns` table, including inheritance from the project above
  and the admin flag ignored.
- web (`ui_test.go`): an owner author sees the popover with `as_owner` and a
  required note; a maintainer author still sees the disabled button and why;
  POST without a note is refused with the message; POST with one releases and
  the activity says so; a forged CSRF token is 403.
- e2e (`cmd/loadout/review_test.go`, `accountRegistry`, `loginAs`, `person`,
  `newSession`): review required on lime, the owner proposes, plain approve is
  refused with the hint, `--as-owner` without a note is refused, with a note it
  releases; a maintainer syncs it; `history` and `review show` mark it; the
  owner's machine token is refused with `--as-owner`.

## Risks

- Owners release unreviewed content to every team below their project. That is
  the feature; the note and the marks keep it visible, and it adds no power an
  owner lacks today.
- A release scan that misses the new columns would silently drop the mark; the
  contract suite reads them back through every release query.
- `review-from-the-web.md` is planned at the same time and touches the same
  page (`proposal.html`, `decide`). Whichever lands second rebases; this plan
  only changes the actions block, the why text, the activity and the pill.
- A role taken away between loading the page and posting: the service checks
  `Owns` on the POST, so the approval is refused.

## Decisions for the user

1. Admins: owners only (recommended, reasons above), or also admins signed in
   on the web, as for turning review off.
2. An explicit `--as-owner` / "Approve as owner…" step (recommended: no
   accidental self-release, and old clients get a clear message), or plain
   Approve that just works for owners.
3. Note required when approving your own proposal (recommended). Approving
   someone else's proposal with no comparison attached stays as today.

## Not in this step

- Owners publishing directly when review is required. They propose and approve
  as owner, which keeps the prompts, diff and record; `publish` stays refused.
- A per-namespace switch to forbid self-approval even for owners.
- Notifications to teammates when review is skipped.
- Required comparisons or a minimum number of approvals.
