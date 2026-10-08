# Settings tab and a wiki per project and team

Status: built (2026-10-03); see As built at the end for where the code differs.

## Goal

The Members tab of a project or team becomes **Settings**: it keeps everything Members holds today (people, invites, machine tokens, review before release, models) and gains one switch, **Wiki**. When an owner turns the wiki on, the namespace gets a **Wiki** tab: one Markdown page (`wiki.md`, or `.mdx` accepted), edited in the browser (Write, Preview, Save, all without JavaScript) or from the CLI (`loadout wiki pull` and `push`), with a history of who saved what and when, and a refusal of any save based on an old revision.

The one rule holds: the wiki is text people write and read. The registry stores it, renders it with the same safe Markdown renderer the package previews use and draws its diagrams in the existing sandboxed frames. Nothing calls a model and nothing needs a key. MDX components are never run; they show as code.

## Decisions (settled by the user, 2026-10-03)

1. Members is renamed Settings on project and team views, and holds what Members holds plus the Wiki switch.
2. A Wiki tab per namespace, shown only when that namespace's switch is on (off by default). Each namespace has its own switch.
3. One page per namespace, a single Markdown document.
4. Editing in the browser and from the CLI, with a version history (who, when) and protection against lost updates.
5. Owners and members of the namespace edit; readers only read. Owners switch the wiki on and off. Whoever can see the namespace's pages reads it.
6. Markdown is stored; `.mdx` is accepted but JSX and `import`/`export` lines show as code. Mermaid and draw.io blocks render as diagrams in the sandboxed frames. No images. A size limit per page.

## Design

### Recommendations this plan makes (open for the user, see the end)

- **URLs.** The page moves to `GET /t/{ns}/settings`. `GET /t/{ns}/members` answers `301` to `/t/{ns}/settings` with its query kept (`?invite=1`, `?done=...`), so bookmarks and old links keep working; pages are `no-store`, so a rollback is not stuck behind a cached redirect. The POST form actions (`/members`, `/members/role`, `/members/remove`, `/invite`, `/tokens`, `/tokens/revoke`) keep their URLs: they are not addresses people see, and moving them buys nothing. After a POST they redirect to `/settings`. Mails never linked to `/members` (the invite link is `/auth/login?...&next=/t/{project}`), so no mail changes.
- **A team and its project's wiki.** A link, not inheritance. A team's wiki is its own page. Team members can already open the project's pages read-only (`account.Place.Via`); when the project's wiki is on they see a Wiki tab there too (read-only), and the team's Wiki page says "acme, the project above, has a wiki too." with a link. Nothing is merged or copied.
- **Who counts as a member for editing.** People with a role stored on the namespace (member, maintainer, owner), owners of a project above, and admins. A member of a team below, who reads the project through the team, only reads the project's wiki.

### New package `internal/wiki`: the page as a value

Shared by the registry (server rules) and the CLI (checkouts), so one place says what a valid page is. It imports nothing of loadout's except `atomicfile` and `strictyaml` (for the checkouts file).

```go
package wiki

// Format is how a page is written. It is stored, so it is a string.
type Format string

const (
	Markdown Format = "md"
	MDX      Format = "mdx"
)

// FormatOf reads the format from a file name: .md and .markdown are
// Markdown, .mdx is MDX; anything else is an error naming the three.
func FormatOf(name string) (Format, error)

// File is the default file name of a page in this format: wiki.md, wiki.mdx.
func (f Format) File() string

const (
	MaxSize      = 256 << 10 // bytes of one page, after line endings are normalised
	MaxNote      = 200       // runes of a save's note
	MaxRevisions = 200       // revisions kept per namespace; older ones are pruned
)

// Doc is one page's text and format. Built only by NewDoc, never changed
// after. The zero Doc is the empty Markdown page.
type Doc struct {
	format Format
	text   string
}

// NewDoc checks and normalises a page: a known format, UTF-8 without NUL,
// CRLF and lone CR turned into LF (browsers send textarea text with CRLF),
// at most MaxSize bytes.
func NewDoc(f Format, text string) (Doc, error)

func (d Doc) Format() Format // Markdown for the zero Doc
func (d Doc) Text() string
func (d Doc) Size() int

// Info is a revision without its text, for history lists.
type Info struct {
	Namespace    string
	Seq          int // 1, 2, 3 ... per namespace; 0 means nothing written yet
	Format       Format
	Size         int
	AuthorUserID string
	Author       string // the author's email when they saved
	Note         string
	CreatedAt    time.Time
}

// Revision is one saved version of a page.
type Revision struct {
	Info
	Doc Doc
}
```

- Errors from `NewDoc` and `FormatOf` are plain errors (`ErrTooLarge`, `ErrNotText`, `ErrFormat` sentinels in `wiki`); the registry wraps them with `registry.ErrInvalid`. `wiki` cannot import `registry` (the registry imports `wiki`).
- `Checkouts` (CLI side, below) also lives here, with the `Syncer` that pulls and pushes through a consumer-owned `Remote` interface.

### Registry: storage and rules

**The switch** is a namespace setting next to `require_review`:

- `registry.Namespace` gains `Wiki bool`.
- `registry.NamespaceStore` gains `SetWiki(ctx, name string, on bool) error` (`ErrNotFound` for a missing namespace), like `SetRequireReview`.
- `registry.Service.SetWiki(ctx, name, on) (Namespace, error)` logs and returns the namespace, like `SetRequireReview`. Callers check who may.

**The page** has its own store interface, as proposals do (`ProposalStore`), so `Store` and `NamespaceStore` stay as they are for every other consumer:

```go
// internal/registry/wiki.go

// ErrStale means a change was based on a version that is no longer the
// newest: someone saved in between.
var ErrStale = errors.New("changed since it was read")

// WikiStore keeps the revisions of each namespace's wiki page.
// Implementations must be safe for concurrent use.
type WikiStore interface {
	// WikiRevision returns revision seq of namespace's page, the newest for
	// seq 0. ErrNotFound when there is no such revision (none yet, pruned,
	// or a missing namespace).
	WikiRevision(ctx context.Context, namespace string, seq int) (wiki.Revision, error)
	// WikiRevisions lists the kept revisions, newest first, without text.
	WikiRevisions(ctx context.Context, namespace string) ([]wiki.Info, error)
	// PutWikiRevision stores rev as revision base+1 if base is the newest
	// (0: none yet), then keeps only the newest keep revisions, in one
	// transaction. ErrStale when another revision came first, ErrNotFound
	// for a missing namespace.
	PutWikiRevision(ctx context.Context, rev wiki.Revision, base, keep int) (wiki.Info, error)
}

// WikiEdit is one save: the revision it started from, the new page, who
// saves and why.
type WikiEdit struct {
	Base         int
	Doc          wiki.Doc
	Note         string
	AuthorUserID string
	Author       string
}

// WikiService holds the rules of the wiki: it exists only while the
// namespace's switch is on, a page has a size limit, a save must start
// from the newest revision. Callers check who may read and edit.
type WikiService struct {
	wikis      WikiStore
	namespaces NamespaceStore
	now        func() time.Time
	log        *slog.Logger
}

func NewWikiService(wikis WikiStore, namespaces NamespaceStore, now func() time.Time, log *slog.Logger) *WikiService

// Revision returns revision seq (0: newest). An empty page is the zero
// Revision with Seq 0. A namespace whose wiki is off is ErrNotFound with
// a sentence saying an owner turns it on in Settings.
func (s *WikiService) Revision(ctx context.Context, namespace string, seq int) (wiki.Revision, error)
func (s *WikiService) Revisions(ctx context.Context, namespace string) ([]wiki.Info, error)

// Save stores e as the next revision and reports whether it wrote one: a
// save whose text and format equal the base revision writes nothing. A
// stale base is ErrStale, wrapped with which revision is the newest, by
// whom and when, so the CLI and the page can say it.
func (s *WikiService) Save(ctx context.Context, namespace string, e WikiEdit) (wiki.Info, bool, error)
```

- Rules in `Save`: switch on (else `ErrNotFound`, as for reading), note at most `wiki.MaxNote` runes and one line (else `ErrInvalid`), `Base >= 0`, the `Doc` is already valid (only `wiki.NewDoc` builds one). The service passes `wiki.MaxRevisions` as `keep`.
- Lost updates are prevented by the store, not by a read then a write: the insert of `(namespace, base+1)` under the primary key fails for the second of two concurrent saves, and the store turns that into `ErrStale`. The check "is base the newest" and the insert run in one transaction.
- The stale message: `the wiki of lime changed since revision 12: revision 14 by dana@acme.example (2026-10-03 10:14 UTC) is the newest; pull it, merge your changes and save again`.
- Logging: `wiki saved` with namespace, revision, user id and size; never the text or the note.

### Account: who may

`account.Access` learns where a member role comes from, since `Roles.accessOf` raises a member of a team below to member of the project, and that person must read the project's wiki, not edit it:

```go
type Access struct {
	Role    Role
	Governs bool
	// Below is true when Role comes only from a membership in a team
	// below: the user reads the namespace and may propose to it.
	Below bool
}

// CanEditWiki reports whether the user may save the namespace's wiki: a
// role of their own there, ownership above, or admin.
func (a Access) CanEditWiki() bool { return a.Role.AtLeast(RoleMember) && !a.Below }

// CanSetWiki reports whether the user may turn the wiki on or off.
func (a Access) CanSetWiki() bool { return a.Governs }
```

`Roles.accessOf` sets `Below` where it raises the role today. Tests that compare `Access` values for that case get the new field.

`account.Service` checks and delegates, exactly like the model tiers (`account/tiers.go`), in a new `internal/account/wiki.go`:

```go
// Wikis is what the account service needs from the wiki service.
type Wikis interface {
	Revision(ctx context.Context, namespace string, seq int) (wiki.Revision, error)
	Revisions(ctx context.Context, namespace string) ([]wiki.Info, error)
	Save(ctx context.Context, namespace string, e registry.WikiEdit) (wiki.Info, bool, error)
}

func (s *Service) Wiki(ctx context.Context, actor Actor, namespace string, seq int) (wiki.Revision, error)      // CanRead
func (s *Service) WikiHistory(ctx context.Context, actor Actor, namespace string) ([]wiki.Info, error)         // CanRead
func (s *Service) SaveWiki(ctx context.Context, actor Actor, namespace string, base int, doc wiki.Doc, note string) (wiki.Info, bool, error) // CanEditWiki
func (s *Service) SetWiki(ctx context.Context, actor Actor, namespace string, on bool) (registry.Namespace, error) // CanSetWiki
```

- `account.Options` gains `Wiki Wikis`. Nil means this registry has no wiki: the four methods answer `ErrNotFound` ("this registry has no wiki"). Both `account.NewService` calls in `cmd/loadout` (`main.go` for the host commands, `login.go` for the server) pass the one `registry.WikiService`.
- `SaveWiki` fills `Author` from the user's email (`Store.UserByID`), so history keeps the address even after the user is deleted, as proposals keep `author_label`.
- `account.Registry` gains `SetWiki`. The refusal texts: "editing the wiki of lime needs a role in lime; members of a team below only read it", "turning the wiki of lime on or off needs an owner (or an admin)".
- Machine tokens act for nobody, so the API routes (on `asUser`) refuse them. An older per-namespace login token with read scope is refused by `tokenCovers` as for every management route; account tokens from `loadout login` (the normal case since one-login) work.

## Data and API changes

### Migration `internal/store/sqlitestore/migrations/0013_wiki.sql`

```sql
-- A wiki page per namespace, off by default so nothing changes for the
-- namespaces that exist. Every save is a revision, numbered per namespace.
ALTER TABLE namespaces ADD COLUMN wiki INTEGER NOT NULL DEFAULT 0;

CREATE TABLE wiki_revisions (
    namespace      TEXT    NOT NULL REFERENCES namespaces (name),
    seq            INTEGER NOT NULL CHECK (seq > 0),
    format         TEXT    NOT NULL DEFAULT 'md' CHECK (format IN ('md', 'mdx')),
    body           TEXT    NOT NULL,
    size           INTEGER NOT NULL,
    author_user_id TEXT    NOT NULL,
    author_label   TEXT    NOT NULL,
    note           TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    NOT NULL,
    PRIMARY KEY (namespace, seq)
);
```

- `sqlitestore/namespaces.go`: `namespaceColumns` and `scanNamespace` read `wiki`; `SetWiki` like `SetRequireReview`; `DeleteNamespace` also runs `DELETE FROM wiki_revisions WHERE namespace = ?` in its transaction (a wiki does not make a namespace "not empty"; see Risks).
- New `sqlitestore/wiki.go`: the three `WikiStore` methods. `WikiRevisions` selects every column but `body`. `PutWikiRevision` reads `MAX(seq)` and inserts in one transaction, maps a unique violation (`isUniqueViolation`) to `registry.ErrStale`, and prunes `seq <= new - keep`.
- `registrytest/memstore.go`: `wiki` field on `Namespace` records, `SetWiki`, and the three methods on `MemStore`, under its mutex.

### Store contract suites

- `registrytest.RunNamespaceStoreContract` gains "wiki switch": new namespaces are off, on and off again round-trip, a missing namespace is `ErrNotFound`, `DeleteNamespace` removes the revisions.
- New `registrytest/wikicontract.go` with `RunWikiStoreContract(t, newStore func(t) (registry.WikiStore, registry.NamespaceStore))`, run by `sqlitestore_test.go` and `registrytest/memstore_test.go`:
  - an empty page: revision 0 and the list are `ErrNotFound` and empty;
  - put on base 0 gives 1, put on base 0 again is `ErrStale`, put on base 1 gives 2;
  - a revision by number and the newest by 0, with text, format, author, note and time intact (a 256 KiB text, a text with every kind of Unicode);
  - the list is newest first, has sizes, and has no text;
  - keep 3: after five saves only 3, 4 and 5 remain, and asking for 1 is `ErrNotFound`;
  - 20 goroutines saving on the same base: exactly one wins, the rest are `ErrStale`;
  - a missing namespace is `ErrNotFound` on every method.

### HTTP API (for the CLI)

In `internal/api/wiki.go`:

```go
// WikiPage is the body of GET /v1/namespaces/{ns}/wiki (?revision=N) and of
// a PUT that saved. Revision 0 means nothing is written yet.
type WikiPage struct {
	Namespace string    `json:"namespace"`
	Revision  int       `json:"revision"`
	Format    string    `json:"format"`
	Text      string    `json:"text"`
	Author    string    `json:"author,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	// Saved is false when the text equalled the base and nothing was written.
	Saved bool `json:"saved,omitempty"`
}

// WikiSave is the body of PUT /v1/namespaces/{ns}/wiki.
type WikiSave struct {
	Base   int    `json:"base"`
	Format string `json:"format"`
	Text   string `json:"text"`
	Note   string `json:"note,omitempty"`
}

// WikiRevisions is the body of GET /v1/namespaces/{ns}/wiki/revisions.
type WikiRevisions struct {
	Namespace string         `json:"namespace"`
	Revisions []WikiRevision `json:"revisions"`
}

type WikiRevision struct {
	Revision  int       `json:"revision"`
	Format    string    `json:"format"`
	Size      int       `json:"size"`
	Author    string    `json:"author"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CodeStale is the code of a 409 for a save based on an old revision.
const CodeStale = "stale"
```

- `api.Namespace` gains `Wiki bool json:"wiki"` and `api.NamespaceSettings` gains `Wiki *bool json:"wiki,omitempty"`; `PATCH /v1/namespaces/{ns}` applies whichever is set (both may be), calling `Manage.SetRequireReview` and the new `Manage.SetWiki`.
- New `internal/server/wiki.go`: a consumer-owned `server.Wikis` interface (`Wiki`, `WikiHistory`, `SaveWiki`, implemented by `account.Service`) and `server.WithWiki(w Wikis)`, which registers `GET` and `PUT /v1/namespaces/{ns}/wiki` and `GET /v1/namespaces/{ns}/wiki/revisions` on `asUser`. `cmd/loadout/login.go` adds `server.WithWiki(accounts)` next to `WithManagement`.
- The PUT body gets its own cap, `maxWikiBody = 6*wiki.MaxSize + 16<<10` (Go's JSON encoder writes `<`, `>` and `&` as six-byte escapes), through a `decodeBodyLimit` beside `decodeBody`, which keeps its 4 KiB for everything else. The service checks the real size after decoding.
- `server.classify` maps `registry.ErrStale` to `409` and `api.CodeStale`. The manage route comment block in `manage.go` lists the new routes.
- `internal/client/wiki.go`: `Wiki(ctx, ns, rev)`, `WikiRevisions(ctx, ns)`, `SaveWiki(ctx, ns, api.WikiSave)` (encoder with `SetEscapeHTML(false)`), and `SetWiki(ctx, ns, on)` in `client/manage.go`. An older registry answers 404 to the wiki routes; the CLI says "this registry has no wiki yet; it needs loadout 1.x or later".

## CLI

### `loadout namespace set NS --wiki on|off`

`newNamespaceSetCmd` gets `--wiki`, parsed like `--require-review`; the `manager` interface and `hostManager` gain `SetWiki`. Output: "lime now has a wiki: open it at https://registry.example.com/t/lime/wiki, or loadout wiki pull --team lime" and "lime's wiki is off; its history is kept".

### `loadout wiki`

`internal/cli/wiki.go` only parses flags and prints. The work is in `internal/wiki`:

```go
// Remote is what the wiki commands need from a registry.
type Remote interface {
	Page(ctx context.Context, namespace string, rev int) (Revision, error)
	History(ctx context.Context, namespace string) ([]Info, error)
	Save(ctx context.Context, namespace string, base int, doc Doc, note string) (Info, bool, error)
}

// Checkouts records, per local file, which page and revision it was pulled
// or pushed at and the digest of what loadout wrote, in
// ~/.loadout/wiki.yaml (strict YAML, 0600, written atomically).
type Checkouts struct{ path string }

// Syncer pulls a page into a file and pushes a file as a new revision.
type Syncer struct{ checkouts *Checkouts }

func (s *Syncer) Pull(ctx context.Context, r Remote, ref Ref, file string, rev int, force bool) (Revision, error)
func (s *Syncer) Push(ctx context.Context, r Remote, ref Ref, file string, base *int, note string) (Info, bool, error)

// Ref names a page: the remote's URL and the namespace.
type Ref struct{ Remote, Namespace string }
```

A small adapter in `internal/cli/wiki.go` implements `wiki.Remote` over `*client.Client`, like `registryAdapter`. `cli.Deps` gets `Wiki *wiki.Syncer`, built in `cmd/loadout/main.go` with the state folder.

| Command | What it does |
| --- | --- |
| `loadout wiki show --team NS [--revision N]` | Prints the page to standard output. |
| `loadout wiki pull [FILE] --team NS [--revision N] [--force]` | Writes the page to FILE (default `wiki.md`, or `wiki.mdx` for an MDX page) and records the revision. |
| `loadout wiki push FILE --team NS [--note TEXT] [--base N]` | Saves FILE as the next revision; the format comes from its name. |
| `loadout wiki log --team NS` | REV, WHEN, BY, SIZE, NOTE, newest first. |

- `--team` picks the registry with `connectFor(remote, ns, "")` as other commands do; `--remote` overrides. `--team` is required (one page per namespace, no guessing).
- **Never overwrite an edit.** `pull` refuses to replace an existing FILE that has no checkout record, or whose bytes differ from the recorded digest, unless `--force`: "wiki.md has changes that are not on the registry. Push them first, pull into another file, or pass --force to replace them." It refuses a symlink (`os.Lstat`) and writes with `atomicfile.Write`.
- **Lost updates.** `push` takes its base from the checkout record of FILE for that page, else `--base`, else 0 (which the registry accepts only while the page is empty). On a 409 (`api.CodeStale`) it prints the registry's sentence and what to do: "Run loadout wiki pull theirs.md --team lime, merge it into wiki.md, then loadout wiki push wiki.md --team lime --base 14." FILE is left as it is. On success it records the new revision and digest.
- `push` checks the file locally with `wiki.FormatOf` and `wiki.NewDoc` before sending, so a 300 KB file or a `.txt` is refused without a request. The text it sends is the normalised one (LF), and the digest it records is of the file as it is on disk, so a CRLF file on Windows does not look edited after a push.
- `push` of an unchanged file prints "Nothing changed: revision 12 is the same text."

## Web

### Routes (`internal/web/ui.go`, comment block updated)

| Route | Handler | File |
| --- | --- | --- |
| `GET /t/{ns}/settings` | `settingsPage` (was `membersPage`) | `ui_members.go` renamed `ui_settings.go` |
| `GET /t/{ns}/members` | `toSettings`: 301 to `/t/{ns}/settings`, query kept | `ui_settings.go` |
| `POST /t/{ns}/settings/review` | `saveReview` (was `saveSettings`) | `ui_settings.go` |
| `POST /t/{ns}/settings` | the same handler, kept so a page open from before the release still saves | `ui_settings.go` |
| `POST /t/{ns}/settings/wiki` | `saveWikiSwitch` | `ui_settings.go` |
| `GET /t/{ns}/wiki` | `wikiPage`: newest revision, rendered | `ui_wiki.go` |
| `GET /t/{ns}/wiki/edit` | `wikiEdit` (`?from=N` starts from an old revision) | `ui_wiki.go` |
| `POST /t/{ns}/wiki/edit` | `wikiEdit` with the posted text: `view=write` or `view=preview` | `ui_wiki.go` |
| `POST /t/{ns}/wiki` | `saveWiki` | `ui_wiki.go` |
| `GET /t/{ns}/wiki/history` | `wikiHistory` | `ui_wiki.go` |
| `GET /t/{ns}/wiki/r/{seq}` | `wikiRevision`: an old revision (`?source=1`) | `ui_wiki.go` |
| `GET /t/{ns}/wiki/r/{seq}/diff` | `wikiDiff`: changes from the revision before | `ui_wiki.go` |
| `GET /t/{ns}/wiki/r/{seq}/raw` | `wikiRaw`: the text as an attachment | `ui_wiki.go` |
| `GET /t/{ns}/wiki/r/{seq}/draw` | `wikiFrame`: a diagram frame (`?block=K`, `?drawio=K`) | `ui_wiki.go` |

- Every wiki route carries the namespace's switch: off is the usual 404 ("Nothing here"), whether or not a page exists.
- Every route under `r/{seq}` names one immutable revision, like the release routes name a version; `seq` must be a canonical positive integer and a kept revision, else 404.
- The handlers that end in `back(...)` and `refused(..., "/t/"+ns+"/members")` in `ui_members.go`, `ui_tiers.go`, `ui_invite.go` and `ui_new.go` point at `/t/{ns}/settings`.

### Consumer interfaces

```go
// Wiki is what the wiki pages need from the account service.
type Wiki interface {
	Wiki(ctx context.Context, actor account.Actor, namespace string, seq int) (wiki.Revision, error)
	WikiHistory(ctx context.Context, actor account.Actor, namespace string) ([]wiki.Info, error)
	SaveWiki(ctx context.Context, actor account.Actor, namespace string, base int, doc wiki.Doc, note string) (wiki.Info, bool, error)
}
```

`web.UI` gains `Wiki Wiki`; nil leaves out the wiki routes, the tab and the switch (as `Skills` does). `web.Teams` gains `SetWiki`. `var _ Wiki = (*account.Service)(nil)` next to the others.

### Header and tabs (`templates/ui/layout.html`, `renderTeam`)

- Tabs: Packages, Reviews, Wiki (when on), Settings. The tab id `members` becomes `settings` (`ui_tiers.go` renders under it too).
- `teamHeader` gains `Wiki bool` (from `t.place.Namespace.Wiki`, which `Places` already loads). `Tabs` keeps its meaning, the full set for a member. A reader through a team below (`Via`) gets a tab bar of Packages and Wiki when the project's wiki is on, and no tab bar otherwise, as today.

### Settings page (`templates/ui/members.html` renamed `settings.html`)

- Title "Settings · lime". The people part gets a panel heading **Members** (h2) above the add or invite form and the table, since the page is no longer named after them; Through the project, Review before release, Models for agents and Tokens for machines stay as they are.
- A new **Wiki** panel, for those who may change it (`CanSetWiki`), built like Review before release: a `.setting` form posting to `/settings/wiki` with a checkbox "Show a wiki" and Save. Text: on, "lime has a Wiki tab: one page its members write and everyone in lime reads." with a link "Open the wiki"; off, "Off: lime has no Wiki tab. Turning it off hides the page and keeps its history." Notices: `wiki-on` "The wiki is on. Open the Wiki tab to write the first version.", `wiki-off` "The wiki is off. Its history is kept if you turn it on again."
- The tiers page's back link reads Settings; the Reviews page's "Change in Members" reads "Change in Settings" and links to `/t/{ns}/settings#review-h`.

### Wiki page (`templates/ui/wiki.html`)

- A reading page (1240 px, not `Wide`), with `filePageCSP` (adds `frame-src 'self'`) for the diagram frames.
- A bar above the page: "Revision 12 · Dana Smith · 2 days ago" (name from `Teams.DisplayNames`, else the stored email; the time a `<time>` with the exact date as its title), then **Edit** (primary, only for `CanEditWiki`, which the page learns from `t.place.Access.CanEditWiki()` and `t.member()`), **History** and a download icon link ("Download wiki.md").
- The page itself in a `.md` panel (72ch measure for prose, code and tables full width).
- Empty: a panel "No wiki page yet". Editors get "Write what everyone in lime should know: how to set up, who to ask, where things live." and **Write the first version**; readers get "Nobody has written it yet." Both get the command `loadout wiki push wiki.md --team lime` with Copy.
- On a team page whose project's wiki is on and readable: a line under the page, "acme, the project above, has a wiki too." linking `/t/acme/wiki`.
- An old revision (`wikiRevision`) shows a `role="note"` panel, "You are looking at revision 12. The newest is 15.", with a link to the newest and, for editors, **Edit from this revision** (`/wiki/edit?from=12`), which is how a page is restored.

### Edit page (`templates/ui/wiki_edit.html`)

One form, no script needed:

- A segmented pair of submit buttons, **Write** and **Preview** (`name="view"`, `formaction="/t/{ns}/wiki/edit"`, the current one `aria-pressed="true"`), above the text.
- Write: a labelled `textarea` (`id="wiki-text"`, monospaced, 30 rows, `spellcheck`), its hint tied with `aria-describedby`: "Markdown. Mermaid and draw.io blocks become diagrams once you save. Images are not shown. Up to 256 KB." For an MDX page: "This page is MDX: components and import lines show as code; the Markdown around them renders."
- Preview: the rendered text in a `.md` panel; the textarea stays in the form with `hidden` (a hidden field is still sent), so Write brings the text back unchanged. Diagram fences show as their source with "Diagrams are drawn once you save." The frame rule stays: a frame's source always comes from a stored revision, never from a form or a URL.
- "What changed (optional)", a one-line field with `maxlength="200"`.
- Hidden `base` (the revision the edit started from) and `format` (the base revision's format; a new page is Markdown), the CSRF field, **Save** (primary, posts to `/t/{ns}/wiki`) and Cancel (a link to the wiki).
- Saved: 303 to `/t/{ns}/wiki?done=saved`, "Saved revision 13." Nothing changed: "Nothing changed, so no revision was saved."
- **Stale save:** status 409 and the edit page again, never the error page, so the text is not lost. The banner (`role="alert"`, `#form-error`, the textarea marked `aria-invalid` and focused) says "Dana saved revision 14 while you were editing. Your text is still below. Look at what changed, merge it into your text, then save again." A panel "What changed since revision 12" shows `textdiff.Unified(base, newest)` with the `diff-file` template; when the base was pruned or the diff is too large (`textdiff` stops at 5,000 lines) it links to revision 14 instead. The form's `base` is now 14, so the next Save is a deliberate one.
- Too large or not text: status 400, the same page with the text kept and the field error, as the other forms do.

### History (`templates/ui/wiki_history.html`)

A back link "Wiki", then a `.list` table: Revision (link to `r/{seq}`), Who, When, Note (escaped, one line), Size, and Changes (link to `r/{seq}/diff`, not on revision 1). At most `wiki.MaxRevisions` rows, so no paging; a line under the table says "loadout keeps the newest 200 revisions." when that many exist.

### Form size

`web.go` caps every body at `maxForm` (16 KiB) before the mux runs. A `formLimit(r)` function returns `maxWikiForm = 3*wiki.MaxSize + 16<<10` (percent-encoding grows UTF-8 up to three times) for `POST /t/{ns}/wiki` and `POST /t/{ns}/wiki/edit`, and `maxForm` for every other request.

## Rendering: Markdown, MDX and diagrams

All in `internal/web/markdown.go`, on the existing `docMD` (raw HTML as escaped text, dangerous links dropped, images as their alt text). Nothing new is trusted.

### Diagram fences become a table

`mermaidBlocks`, `mermaidFrames` and `mermaidSources` become `diagramBlocks`, `diagramFrames` and `fenceSources`, driven by a table instead of a second copy of the code:

```go
// A fenceDiagram is a fenced block language drawn in a frame.
type fenceDiagram struct {
	langs []string                  // fence languages, any case
	query string                    // the frame's parameter: block, drawio
	kind  string                    // the frame library, a key of diagramLibs
	draw  func(src []byte) bool     // false leaves the fence as code
}

var fenceDiagrams = []fenceDiagram{
	{langs: []string{"mermaid"}, query: "block", kind: "mermaid", draw: fitsMermaid},
	{langs: []string{"drawio", "draw.io"}, query: "drawio", kind: "drawio", draw: plainDrawio},
}
```

- Each kind numbers its own fences, so `?block=K` keeps meaning the K-th mermaid fence and existing frame URLs of package documents do not shift; `?drawio=K` is the K-th draw.io fence. At most `maxFences` (20) frames per document across both kinds.
- `plainDrawio` accepts uncompressed XML only (`<mxfile` or `<mxGraphModel`), at most `maxPreview` bytes. A compressed one stays code: `pack` exports its existing check as `pack.DrawioCompressed(data []byte) bool`, which `checkDrawio` then uses too.
- This also draws draw.io fences in package documents (preview pages and skill pages), since they share `docMD`. `fenceFrame` in `preview.go` reads either parameter.
- `drawFrame`'s tail (template, CSP, `X-Frame-Options: SAMEORIGIN`) moves into `h.serveFrame(w, r, title string, src frameSource)`, used by both `drawFrame` and `wikiFrame`. The frame policy and the sandbox are unchanged.

### MDX shows as code, Markdown renders

A parse context key `mdxKey`, set only for MDX pages, turns on an AST transformer `mdxCode`:

- A top-level paragraph whose first line starts with `import ` or `export ` becomes an `ast.CodeBlock` with the paragraph's line segments.
- An HTML block (in MDX, a JSX element on its own lines such as `<Tabs>` or `<Callout type="warn">`) becomes an `ast.CodeBlock` with its lines and closure line, instead of the escaped paragraph `escapedHTML` writes for Markdown pages.
- Inline JSX inside a sentence (`<Badge />`) stays escaped inline text, as raw inline HTML is today, so the sentence still reads. `{expressions}` are plain text.
- The code blocks are rendered by goldmark's own code renderer, which escapes everything.

A YAML front matter block at the top of either format (split with `frontmatter.Split`) is shown as a code block above the page, so `---` lines do not render as rules and headings. The frame route renders the same body, so diagram numbers match.

### One entry point

`wikiDocument(rev wiki.Revision, base string) (template.HTML, note string)` in `internal/web/wiki_render.go` sets the contexts (frames for the revision at `base + "/r/{seq}/draw"`, MDX when the format says so) and calls `convert(docMD, ...)`. The preview calls it with no frames. `wikiFrame` finds the K-th fence of the stored revision with `fenceSources`, as `fenceFrame` does for files.

## Order of work

Steps 1, 2 and 4 touch different files and can be fanned out at once. Each step ends with `make check` green and could ship alone.

1. **Core (no UI).** `internal/wiki` (Format, Doc, Info, Revision, limits) with tests; `registry.Namespace.Wiki`, `NamespaceStore.SetWiki`, `registry.Service.SetWiki`; `registry.ErrStale`, `WikiStore`, `WikiEdit`, `WikiService`; migration 0013; `sqlitestore` (`namespaces.go`, new `wiki.go`); `registrytest` MemStore and `RunWikiStoreContract`; `account.Access.Below`, `CanEditWiki`, `CanSetWiki`; `account/wiki.go` with `Options.Wiki`; wiring in `cmd/loadout/main.go` and `login.go`; `DESIGN.md` storage line.
2. **Members becomes Settings.** Route and redirect, `ui_members.go` to `ui_settings.go`, `members.html` to `settings.html`, tab id and label, the Members heading on the page, every `/members` link and back path, `POST /settings/review` with the old route kept; web tests; GUIDE and the `loadout-admin` skill say Settings.
3. **The switch** (needs 1 and 2). Wiki panel on Settings and its notices; `web.Teams.SetWiki`; `api.Namespace.Wiki`, `NamespaceSettings.Wiki`, `PATCH` handling, `Manage.SetWiki`; `client.SetWiki`; `namespace set --wiki`, `manager` and `hostManager`.
4. **Renderer.** Diagram fence table with draw.io fences, `pack.DrawioCompressed`, `serveFrame`, `mdxCode` and front matter, `wikiDocument`; unit tests including the `xss` cases. Ships on its own as draw.io fences in package documents.
5. **Wiki pages** (needs 1, 3 and 4). `web.Wiki`, header Wiki tab (also for `Via` readers), view, empty state, edit with Write and Preview, save, stale handling with the diff, history, old revision, raw, frame, `formLimit`; web tests.
6. **API** (needs 1, can run beside 5). `api/wiki.go`, `server/wiki.go` with `WithWiki` and `decodeBodyLimit`, `classify` for `ErrStale`, `client/wiki.go`; server tests.
7. **CLI** (needs 6). `wiki.Checkouts`, `wiki.Syncer`, `cli/wiki.go` and the adapter, `cli.Deps.Wiki`; unit tests and e2e.
8. **Docs and review.** `docs/GUIDE.md` (The website: Settings and Wiki; The same from the CLI; Roles; Rules and limits: 256 KB, 200 revisions; Command reference), rebuilt with `docs/guide/build.sh`; `docs/DESIGN.md` (Web pages, Roles table, Registry storage line for 0013, the diagram fences); the `loadout-admin` skill names `loadout namespace set NS --wiki on` and the `loadout-author` skill `loadout wiki push` (the honesty test checks both); the UI review with the apple-design checklist and the rampstack `accessibility-audit` and `form-strategy` skills, recorded in `docs/UI.md`. Back up the live registry before the deploy that runs migration 0013.

## Tests

**Unit tables**

- `internal/wiki`: `FormatOf` (`.md`, `.MD`, `.markdown`, `.mdx`, `.txt` refused, no extension refused); `NewDoc` (CRLF and lone CR to LF, exactly `MaxSize` accepted and one byte more refused after normalising, invalid UTF-8 and NUL refused, zero Doc is empty Markdown); `Checkouts` round trip, unknown fields refused, 0600; `Syncer` with a fake `Remote`: pull into a new file, pull over an unedited file, refusal over an edited file and over a file without a record, `--force`, symlink refused, push with the recorded base, with `--base`, with none on an empty page, 409 leaves the file and the record alone, unchanged push.
- `internal/registry` (`wiki_test.go`): switch off reads and saves `ErrNotFound`; note too long or with a line break `ErrInvalid`; unchanged text writes nothing; the stale error names the newest revision and author; author and time recorded; `MaxRevisions` passed as keep.
- `internal/account` (`roles_test.go`, `wiki_test.go`): `CanEditWiki` and `CanSetWiki` for member, maintainer, owner, owner of the project above, admin, member of a team below (`Below`), no role; `SaveWiki` refused for a reader through a team and allowed for a project's direct member; `SetWiki` refused for a maintainer; nil `Options.Wiki` answers `ErrNotFound`; a login token of another namespace refused by `tokenCovers`.
- `internal/web/markdown_test.go`: draw.io fence becomes a figure with `?drawio=1`; compressed and over-size draw.io fences stay code; mermaid numbering unchanged with a draw.io fence before it; 21 diagram fences draw 20; MDX: `import` and `export` paragraphs and a `<Tabs>` block become `<pre><code>` with their text escaped, a sentence with `<Badge />` keeps its text, Markdown around them renders, the same input as Markdown keeps today's escaped paragraph; front matter becomes a code block; `xss` in a JSX block, an import line, a fence info string, a mermaid fence, a draw.io fence, a link target (`javascript:`) and an image alt.

**Contract suites**: `RunNamespaceStoreContract` (wiki switch, delete removes revisions) and the new `RunWikiStoreContract`, both against SQLite and `MemStore`. A migration test in `sqlitestore_test.go` opens a database at version 12 with namespaces and checks they read `Wiki == false`.

**Web** (`internal/web/ui_settings_test.go`, `ui_wiki_test.go`, on `newUISite`)

- Settings: the tab says Settings; `GET /t/lime/members?invite=1` answers 301 to `/t/lime/settings?invite=1`; every POST lands on `/settings`; the old `POST /t/lime/settings` still saves review; the Wiki panel shows for olga (owner of acme) and not for alice (maintainer), whose `POST /settings/wiki` is 403.
- Wiki: no tab and a 404 while off; tab after on; empty state differs for editor and reader; a Write, Preview, Write round trip returns the same text byte for byte (with CRLF sent, LF stored); save gives revision 1 and the notice; a second browser saving on the same base gets 409 with its text in the textarea, the diff panel and `base` moved on; a 300 KB text is 400 with the text kept; `maxForm` still refuses a 20 KiB body on `/t/lime/members`; bob (member of lime) edits; a member of lime reads acme's wiki through the team, gets the Wiki tab but no Edit, and `POST /t/acme/wiki` is refused; history lists who and when; an old revision shows the note and "Edit from this revision"; raw is `text/plain; charset=utf-8`, an attachment named `wiki.md` or `wiki.mdx`, with `rawCSP`; the frame route has the frame CSP and `X-Frame-Options: SAMEORIGIN`, draws only stored fences, and answers 404 for `block=0`, `block=21`, a missing revision and a wiki that is off; the wiki page has `frame-src 'self'`, other pages do not.
- `xss` in the page text, the note, the author's display name, the preview, the stale diff and a namespace's history: never unescaped in any response (`wantOut`).

**Server** (`internal/server/wiki_test.go`): GET of an empty page is revision 0; PUT on base 0 then 0 again is 409 with `stale`; a machine token is 403; a body over `maxWikiBody` is 413; a body of 300 KB text is 400; PATCH with `wiki` alone, `require_review` alone and both.

**End to end** (`cmd/loadout/wiki_test.go`, `ownedRegistry`, `person`, `newSession`): alice's `wiki push` before the switch is refused with "an owner turns it on"; alice's `namespace set lime --wiki on` is refused, olga's works; alice pulls the empty page (creates `wiki.md`), writes, pushes revision 1; olga pulls, edits, pushes revision 2; alice edits her stale copy and her push fails with the merge hint and leaves the file; her pull without `--force` refuses to replace her edit; `pull theirs.md`, merge, `push --base 2` gives revision 3; `wiki log` lists three revisions with both emails; `wiki show --revision 1`; a pushed `.mdx` file stores MDX and a pull without FILE writes `wiki.mdx`; a 300 KB file is refused before any request; `namespace set lime --wiki off`, then `wiki show` is refused and turning it on again shows revision 3.

**UI review**: Settings with every panel (owner, maintainer, member), Wiki empty (editor and reader), a page with headings, a table, code, two Mermaid diagrams and a draw.io diagram, an MDX page with imports and components, Write, Preview, the stale page, History and an old revision; light and dark, 390, 1000 and 1440 px, keyboard only, and without JavaScript. Checked with the apple-design skill as a checklist (segmented controls, text views, editing, alerts, lists and tables) and rampstack `accessibility-audit` and `form-strategy`; the record goes in `docs/UI.md`.

## Risks

- **Stored XSS.** A wiki page is untrusted text shown to everyone in the namespace and the teams below. It goes only through `docMD` (no raw HTML, no dangerous links, no images); MDX is code, never run; diagrams run only in the sandboxed frames with their source from a stored revision. The tests send the `xss` constant through every field.
- **Bigger forms.** Raising the 16 KiB cap is limited to the two wiki POST routes and to 784 KiB; the service checks 256 KiB on the decoded text. A signed-in member could still post 784 KiB repeatedly; saves need a role in the namespace and every save is in the history with their name.
- **Storage.** At most 200 revisions of at most 256 KiB, about 50 MB per namespace in the worst case, pruned in the save's transaction; the live VM has a 40 GB disk and the server page shows the database size. Backups cover the table with the rest of the database.
- **Editing is wider than publishing.** Members edit, without review. A bad edit is undone with Edit from this revision; the history names who saved what. Review of wiki edits is out of scope.
- **Lost updates.** Prevented by the compare-and-insert in the store and the primary key; the web keeps the person's text on a conflict, the CLI never overwrites a file it did not write.
- **Rollback.** Migration 0013 makes an older binary refuse the database (`ErrSchemaTooNew`), as every migration does: back up before the deploy, roll back by restoring the backup.
- **Deleting a namespace** also deletes its wiki. Deletion needs an admin and an empty namespace; the confirmation text on the CLI and the web says the wiki goes too.
- **A deleted user** stays named by email in the history, as proposal authors do.
- **draw.io fences in packages** start to draw after step 4. Package documents that have such fences today show a diagram instead of XML; the source stays one click away under Diagram source.
- **`Access.Below`** is a new field on a widely used value; a missed caller would only give less (a reader who cannot edit), never more, since only `CanEditWiki` reads it.

## Not in this step

- More than one page per namespace, page links, a table of contents or headings with anchors.
- Images and attachments (the no-images rule stays).
- Running MDX components or any script from a page.
- A project wiki shown inside a team's wiki (inheritance); the link is all.
- Review or approval of wiki edits, and mail or Pushover notifications about them.
- Search across wikis.
- Installing a wiki into the AI tools as a knowledge base skill (a natural next step: a KB package built from the page).
- `loadout wiki diff`, `push --force`, and a script editor with toolbar or live preview.
- Counts of wiki revisions on the admins' server page.

## Decisions for the user

Accepted on 2026-10-03: the user said "implement" without changing them, so all three recommendations below hold.


1. **Settings URL.** Recommended: the page moves to `/t/{ns}/settings` with a 301 from `/t/{ns}/members` (query kept), and the POST actions keep their URLs. The alternative, keeping `/members` as the address of a tab named Settings, avoids the redirect but leaves the name and the URL apart for good.
2. **A team and its project's wiki.** Recommended: a link, not inheritance. Members of a team read the project's wiki read-only, through a Wiki tab on the project's page and a line on the team's wiki, and do not edit it unless they have a role in the project.
3. **Limits and draw.io in packages.** Recommended: 256 KB per page, the newest 200 revisions kept, and draw.io fences drawn in package documents too, since the renderer is shared.

## As built

Steps 1 to 8 are built. Where the code differs from the plan above, the code wins; these are the differences.

**Registry and account**

- The stale error is a type, `registry.StaleError` (it unwraps to `ErrStale`), carrying the base and the newest revision's `wiki.Info`. The server sends that revision in the 409 body (`api.ErrorBody.Newest`, read into `client.APIError.Newest`), so the CLI and the page name who saved in between without parsing the message.
- `WikiService.Save` reads the newest revision first: a base above it is `ErrInvalid` ("has no revision N; the newest is M"), a base below it is stale before any write, and the store's check and primary key still catch two saves that race past that read.
- `wiki.Doc.Equal` decides "nothing changed"; `wiki.Format.Valid` checks a format.
- The SQLite store checks the newest and inserts in one transaction that takes the write lock at BEGIN; the unique violation is mapped to `ErrStale` as a second guard, not the only one.
- Saves from the registry host are recorded with the author "the registry host".
- `CanEditWiki` and `CanSetWiki` live in `account/wiki.go`, next to the service methods, not in `roles.go`.
- Older per-namespace login tokens: those with read scope are refused by every wiki route, reading included, since the routes go through `tokenCovers`; publish-scope ones work in their namespace and below. Account tokens from `loadout login` are the normal case.
- Deleting a namespace deletes its wiki, but the delete confirmations on the CLI and the web do not say so yet.

**API and client**

- An older registry's 404 (no error body) reads "URL has no wiki yet; it needs a newer loadout on the registry", without a version number.
- The PATCH applies `require_review` first, then `wiki`; both need the same role, so a refusal comes before any change.

**CLI**

- `wiki.Remote` has only `Page` and `Save`; `wiki log` calls the client's history directly, since the `Syncer` does not need it.
- `Syncer.Pull` and `Push` take a `wiki.File{Dir, Name}` instead of a path string: messages say the name as typed, the checkout record is keyed by the absolute path. `Pull` returns the file it wrote, since an empty name becomes `wiki.md` or `wiki.mdx`.
- Push takes its base from `--base` first, then the file's checkout record for that page, then 0. The plan had the record first; an explicit flag winning is what the merge hint relies on.
- Pull also replaces a file that has no record when it already holds the page's text, and `--force` still refuses a symbolic link.
- Pulling an empty page writes an empty file and says to push it; `wiki show` of an empty page says so on standard error.
- The stale hint keeps `--remote` when it was given, and ends "wiki.md is left as it is".
- `wiki log` drops control characters from authors and notes, so a note cannot move the cursor.
- `namespace set --wiki on` on the registry host has no public URL to print and says "open its Wiki tab" instead.

**Web**

- `wikiDocument(text, format, draw)` takes the text and the frame route instead of a revision, so the preview can render unsaved text with no route; `wikiFence` gives the frame route its fence.
- The wiki page and an old revision have **Show source** (`?source=1`), as package files do, and the meta line says MDX for an MDX page.
- The edit form starts with a hidden Save button, so Enter in the note field saves instead of switching to Write (the first submit button of a form is its default).
- A Preview of text that is too large or not text shows Write again with the error (400). A note that is too long or has a line break marks the note field, not the text.
- Editing from an old revision (`?from=N`) shows a note that saving makes it the newest and the revisions after it stay in the history.
- The empty page shows the `loadout wiki push` command to editors only; readers get "Nobody has written it yet."
- "Nothing changed" is the notice `?done=same`; "Saved revision N." reads `rev` from the query, checked as a number.
- History shows **Changes** only when the revision before is still kept; the diff page says when a revision changed nothing in the text, or when the change is too large to show.
- The stale panel's heading is "What was saved while you were editing" when the edit started from the empty page.
- Diagram fences carry a `canvas` flag in the `fenceDiagrams` table, so draw.io frames keep a light canvas in both themes.
