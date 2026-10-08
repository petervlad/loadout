# A course box on the wiki page

Status: built and deployed (2026-10-05); see As built at the end.

## Goal

A team's wiki page (`/t/{ns}/wiki`) gets a **Course** box at its very end, below the folded `##` sections: a list of lessons, one of which can be open at a time, each a Markdown document with optional quiz questions whose answers fold out and say right or wrong and why. The box is part of the page's flow (opening wiki sections pushes it down); it is not a tab and not sticky.

loadout knows nothing about any particular course. A course is a knowledge base (`kind: kb`) that a team or project publishes, such as `lime/lime-course`; an owner picks which one the wiki shows, next to the wiki switch on Settings or with `loadout namespace set lime --course lime/lime-course`. Lessons render with the renderer package documents and the wiki already use (`docMD`), plus one new fenced block, `quiz`.

The one rule holds. The registry reads a stored release and renders its text; nothing calls a model and nothing needs a key. The same knowledge base is installed into each member's AI tool by `loadout sync`, since it lives in a layer the team reads, so an onboarding agent there reads the very same Markdown, quiz blocks included, in the person's own tool.

Why now: Teams get a 7-lesson dev-loop course. A course toolkit composes each team's course from shared L0 lessons plus that team's own slots at publish time and publishes the result as a KB. loadout only has to show a KB as a course; the composing stays in the toolkit. (The mockup linked in the request was not opened for this plan; the lesson side is designed from the request's description.)

## Decisions (confirmed by the user on 2026-10-05)

- **Lesson order comes from file names.** No manifest field and no index file. Lessons are the Markdown documents at the top level of the KB, in path order, which is the order `kb.Base.Docs()` already uses. Documents in folders are reference material that lessons link to. Authors number files: `01-setup.md`, `02-propose.md`.
- **A team without a course of its own shows the closest course above it** (the project's), labelled "From acme". Evidence below.
- **The quiz block is a Markdown task list inside a `quiz` fence**, parsed line by line, and drawn in every document `docMD` renders (lessons, package file previews, skill pages and wiki pages), as draw.io fences are.
- **The course follows the package's latest release**, like a sync with the `auto` policy; the setting stores the package id, never a version.

## Design

### Where each piece lives

| Piece | Lives in | Why there |
| --- | --- | --- |
| Which files are lessons, their order and titles; the quiz format | new `internal/course` | Pure values and parsing, no HTTP, no goldmark, so the CLI can lint with it later and tests are tables |
| A document's title | `internal/kb` (`title` exported as `Title`) | One rule for "the first `#` heading, else the file name", shared by the KB index and the lesson list |
| The setting on a namespace | `registry.Namespace.Course`, `NamespaceStore.SetCourse` | A namespace setting like `RequireReview` and `Wiki` |
| What a course setting may name | `registry.Service.SetCourse` | The registry knows chains, releases and kinds |
| Which course a page shows (closest wins) | `registry.CourseOf` | One pure function over a chain, like `tier.Stack.Resolve` for models |
| Who may set it | `account.Service.SetCourse` (`account/wiki.go`), `Access.CanSetWiki` | The same people who turn the wiki on and off |
| The box | `internal/web/ui_course.go`, `templates/ui/wiki.html` (a `course` template), `static/style.css` | A part of the wiki page, built by its own handler code |
| Quiz rendering | `internal/web/quiz.go`: an AST transformer and a node renderer added to `docMD` | Next to `diagramBlocks`, the same shape |

### `internal/course`: lessons and quizzes as values

```go
package course

// MaxLessons is how many lessons a course box lists; a KB with more
// top-level documents shows the first MaxLessons and says so.
const MaxLessons = 50

// IsLesson reports whether a KB path is a lesson: a Markdown document
// (.md, .markdown) at the top level. Documents in folders are reference
// material the lessons link to.
func IsLesson(path string) bool

// Lesson is one lesson: its file and its title (kb.Title).
type Lesson struct {
	Path  string
	Title string
}

// Lessons returns the lessons among files (path to content, as
// bundle.Scan returns them for IsLesson), in path order, at most
// MaxLessons, and how many there were in all.
func Lessons(files map[string][]byte) (lessons []Lesson, total int)

// Quiz is one question of a quiz fence. Built only by ParseQuiz.
type Quiz struct {
	Question string   // Markdown
	Answers  []Answer // 2 to MaxAnswers, at least one right
}

// Answer is one choice: its text (one line of Markdown), whether it is
// right, and why (Markdown, may be empty).
type Answer struct {
	Text  string
	Right bool
	Why   string
}

const (
	MaxAnswers = 8
	MaxQuiz    = 16 << 10 // bytes of one quiz fence
)

// ParseQuiz reads the text of a quiz fence. Its errors name the line,
// for the note the page shows above a quiz it could not read.
func ParseQuiz(src string) (Quiz, error)
```

`Quiz` and `Answer` are plain values handed to a renderer and never changed after parsing; `ParseQuiz` is the only constructor that checks them.

### Lesson order and titles

- A lesson is a top-level Markdown file of the KB; its title is `kb.Title` (the first `# ` heading in its first 40 lines, else the file name without extension). Path order is byte order, so `10-x.md` sorts before `2-y.md`; the guide tells authors to pad (`01-`, `02-`). The KB index the AI tool reads lists documents in the same order, so the agent and the box agree.
- Why not a field in `loadout.yaml`: the manifest is shared by every kind and decoded with `strictyaml`, so a `lessons:` field would be refused by every older CLI that loads the folder (`pack.Load`), and it would duplicate what file names already say. Why not an index file (`COURSE.md` listing links): it adds a second source of truth that can name missing files and needs its own errors, for no gain a course toolkit needs (it writes the files and can name them). If a team later needs lessons in folders, an index file is the step to take then; nothing here blocks it.
- `course.Lessons` takes only the files `IsLesson` accepts, so the web reads the stored bundle once with `bundle.Scan(data, course.IsLesson)` and keeps only the lessons in memory, never the reference documents.

### The quiz block

Written in a lesson (or any Markdown document) as:

````markdown
```quiz
Your proposal is behind the latest release. What do you run first?

- [ ] loadout publish --base latest
  Not quite. That skips the merge, and a teammate may lose the release's changes.
- [x] loadout rebase
  Right. It merges your folder onto the latest release on your machine; then you propose again.
- [ ] Withdraw it and start over
  You can, but rebase keeps your work and the prompts.
```
````

The rules, in `ParseQuiz`:

- Every line before the first answer line is the question, as Markdown. It must not be empty.
- An answer line starts at the first column with `- [ ] ` (wrong) or `- [x] ` (right; `*` for `-` and `X` for `x` are accepted). The rest of the line is the answer, one line of Markdown.
- Lines after an answer line, up to the next one, are its explanation: each indented by at least two spaces, or blank. The common indentation is removed. A line that is neither indented nor an answer is an error ("line 7: indent the explanation under its answer, or start a new answer with - [ ] or - [x]").
- 2 to 8 answers, at least one right; a fence of at most 16 KiB. Several right answers are allowed: each answer says for itself whether it is right.
- The verdict ("Right." or "Wrong.") is written by loadout, not the author, so an explanation that starts "Not quite." still reads well after it. The guide says to explain, not to repeat the verdict.

A task list inside a fence was chosen because it reads plainly as text: the onboarding agent in the AI tool sees an ordinary question with ticked and unticked boxes, and an author previews it on the package's file page before choosing it as a course. A YAML block would be stricter to parse but worse to write and to read raw.

Rendering (`internal/web/quiz.go`):

- `quizBlocks`, an AST transformer in `docMD` (priority 250, after `relinks` and before `diagramBlocks`), finds fences whose language is `quiz` (any case), runs `course.ParseQuiz` on `fenceText`, and replaces each with a `quizBlock` node holding the already rendered HTML of its question, answers and explanations. Each piece is rendered with `convert(docMD, piece, pc)` and a fresh context that carries the document's `linksKey` (so a link in an explanation to a reference document works) and a `quizDepthKey`, so a quiz fence inside a quiz piece stays code. No frames inside quizzes: diagrams belong in the lesson text.
- At most `maxQuizzes` (50) per document; further ones stay code, and the note says so, as `diagramFrames.note` does for diagrams.
- A fence `ParseQuiz` refuses stays a code block, preceded by `<p class="quiz-error" role="note">This quiz could not be read: line 7: ...</p>`. Readers see the question as text; the author sees why on the package's preview page.
- The node renderer writes, with every piece escaped by `docMD`'s rules (raw HTML as text, dangerous links dropped, images as alt text):

```html
<div class="quiz" role="group" aria-labelledby="quiz_2">
  <p class="quiz-label">Question</p>
  <div class="quiz-q" id="quiz_2">...question...</div>
  <ol class="quiz-answers">
    <li><details class="quiz-answer">
      <summary><span class="quiz-letter" aria-hidden="true">A</span>...answer...</summary>
      <div class="quiz-why quiz-wrong"><p><strong>Wrong.</strong> ...why...</p></div>
    </details></li>
  </ol>
</div>
```

- Ids use `_` (`quiz_1`, `quiz_2` in document order), which heading ids never contain (`slug` writes only letters, digits and hyphens), so they never collide with a wiki heading. Each answer is its own `details`, so a reader can open several and compare; no `name` attribute, no script.
- The verdict is words first, color second: "Right." in `--ok-text` on `--ok-bg`, "Wrong." in `--bad-text` on `--bad-bg`, both checked at 4.5:1 or more in light and dark. The summary row is at least 44 px tall with a chevron like `.wiki-section`.
- `Open all` in `ui.js` acts only on `.wiki-section`, so it never reveals answers.

### The course setting and layers

`registry.Namespace` gains `Course pack.ID`; the zero `pack.ID` (`IsZero`) means "none of its own".

```go
// internal/registry/namespace.go

// NamespaceStore gains:
	// SetCourse sets the knowledge base the namespace's wiki shows as its
	// course; the zero ID clears it. ErrNotFound for a missing namespace.
	SetCourse(ctx context.Context, name string, course pack.ID) error

// SetCourse checks and stores the course of a namespace: the zero ID
// clears it; otherwise the package must live in the namespace or a
// namespace above it, so everyone who reads the wiki reads the package,
// and its latest release must be a knowledge base. Callers check who may.
func (s *Service) SetCourse(ctx context.Context, name string, course pack.ID) (Namespace, error)

// Course is the course a namespace's wiki shows and where it is set.
type Course struct {
	Package pack.ID
	// From is the namespace whose setting it is: the page's own, or one
	// above it.
	From string
}

// CourseOf returns the course of the first namespace in chain (closest
// first) that sets one, and false when none does.
func CourseOf(chain []Namespace) (Course, bool)
```

Why the closest course above is shown (the evidence asked for):

- Packages already flow down layers: a team member installs the project's knowledge bases with `install --all`, and a closer layer's package replaces a farther one's whole folder. A course is a package, so the box behaving like a layer is what people already expect.
- Model tiers inherit too, closest first (`tier.Stack`).
- The wiki page does not inherit (`docs/plans/wiki-and-settings.md`, decision 2: "a link, not inheritance"). That decision was about text a namespace's own members edit. The course box is not edited on the page; it shows a package the team already reads.
- A course toolkit publishes a whole course per team, composed from L0 lessons and the team's slots, so a team's own course replaces the project's as a whole, which is what "closest wins" does. A new team whose course is not published yet still gets the project's course on day one.
- Every reader of a team's wiki reads the project above (`Places` includes ancestors), so showing the project's course gives nobody anything they could not already open.

What inheritance costs: a team cannot hide the project's course without choosing one of its own. That is left out of v1 (see Not in this step); an owner who wants none can turn the team's wiki off or pick the team's own course.

The setting is independent of the wiki switch: an owner may choose a course while the wiki is off (the box shows once it is on), and a project's course reaches the teams below whether or not the project's own wiki is on, since the setting belongs to the project's layer, not to its page.

### Who may, and who sees it

- **Set:** `account.Service.SetCourse(ctx, actor, namespace string, course pack.ID) (registry.Namespace, error)` in `account/wiki.go`, refused unless `Access.CanSetWiki()` (owners of it or above, admins): "choosing the course of lime needs an owner (or an admin)". It logs `course set` with namespace, package and user id. `account.Registry` gains `SetCourse`.
- **See:** whoever reads the wiki page sees the box. The registry's check at set time (the package is in the namespace's chain) means every reader of the wiki reads the package; the box still fetches through `h.ui.Registry` with the page's principal (`t.pr`), so the normal read check runs on every view, and a reader through a team below sees the project's course through the same principal that shows them the project.
- **Diagram frames** in lessons use the release's existing route, `/t/{ns}/p/{team}/{name}/{version}/draw/{path}?block=K`, whose `openRelease` requires the package's team at the page's layer or above, which a course always is. The wiki page already sends `filePageCSP` (`frame-src 'self'`).

### When the course cannot be shown

The box never fails the wiki page; it is left out or replaced by one line, decided in `courseBox`:

| Case | Readers | Those who may set it (`CanSetWiki`) |
| --- | --- | --- |
| No course in the chain | no box | no box (Settings offers one) |
| The package has no release, or the reader's principal gets `ErrNotFound` or `ErrForbidden` | no box | a panel: "The course lime/lime-course cannot be found. Choose another in Settings." with a link |
| The latest release is not a KB any more (publish does not stop a kind change) | no box | "lime/lime-course is a skill now, not a knowledge base. Choose another in Settings." |
| The KB has no top-level Markdown document | no box | "lime/lime-course has no lessons: Markdown files at its top level are its lessons." |
| Any other error (store, a broken bundle) | "The course could not be loaded just now." | the same |

The other error is logged at warn with namespace and package, never the content. Packages cannot be deleted today (only an empty namespace can), so "cannot be found" mostly covers a future delete and a sibling's package set by hand on the host; it costs one branch.

## Data and API changes

### Migration `internal/store/sqlitestore/migrations/0015_course.sql`

```sql
-- The knowledge base a namespace's wiki shows as its course, as
-- "team/name"; empty means none of its own, so every namespace that
-- exists keeps showing what it shows today: nothing.
ALTER TABLE namespaces ADD COLUMN course TEXT NOT NULL DEFAULT '';
```

- The number is the next free one when this is built; other plans in flight may take 0015 first, in which case this becomes the next.
- `sqlitestore/namespaces.go`: `namespaceColumns`, `CreateNamespace` and `scanNamespace` carry `course` (parsed with `pack.ParseID`; a stored value that does not parse reads as none and is logged, since a hand-edited row must not break every page). `SetCourse` like `SetWiki`. `DeleteNamespace` needs nothing: the column goes with the row. A course that names a deleted namespace's package is not cleared; the page falls into "cannot be found".
- `registrytest/memstore.go`: the field and `SetCourse`, under its mutex.

### Store contract

`registrytest.RunNamespaceStoreContract` gains "course setting": a new namespace has none; set, read back through `Namespace` and `Namespaces`; set another; clear with the zero ID; a missing namespace is `ErrNotFound`; `CreateNamespace` with a course keeps it. Run against SQLite and `MemStore` as today.

### HTTP API

- `api.Namespace` gains `Course string json:"course,omitempty"`; `api.NamespaceSettings` gains `Course *string json:"course,omitempty"`, where `""` clears.
- `PATCH /v1/namespaces/{ns}` applies `course` **first**, then `require_review`, then `wiki`. The course is the only setting that can be refused for its value (`ErrInvalid`), and all three need the same role, so a refusal for any reason comes before any change.
- `server.Manage` gains `SetCourse`; the route comment block in `manage.go` mentions the field.
- `client.SetCourse(ctx, ns, id string)` in `client/manage.go`. An older registry decodes with `DisallowUnknownFields` and answers 400 naming `course`; the client turns that into "URL does not know courses yet; it needs a newer loadout on the registry", as it does for `base` on proposals.
- No new GET route: the CLI does not show courses, and the AI tool gets the KB through `sync`.

## CLI

`loadout namespace set NS --course TEAM/NAME|none`, parsed in `newNamespaceSetCmd` beside `--wiki`; the `manager` interface and `hostManager` gain `SetCourse`. `internal/cli` only parses and prints:

- `lime's wiki now shows the course lime/lime-course: open it at URL/t/lime/wiki#course`
- when the wiki is off, a second line: `It shows once the wiki is on: loadout namespace set lime --wiki on`
- `none`: `lime has no course of its own now` and, when one above applies, `its wiki shows acme/onboarding, from acme`.
- Refusals are the service's sentences: "lime/planner is an agent; a course is a knowledge base (kind: kb)", "web/notes is not in lime or a project above it, so not everyone in lime can read it", "lime/lime-course has no release yet; publish it first".

The `loadout-admin` built-in skill names the flag next to `--wiki`; the honesty test checks it exists.

## Web

### The box (`internal/web/ui_course.go`)

`wikiPage` (the newest revision, rendered, not `?source=1`) calls `h.courseBox(ctx, t, r.URL.Query())` and puts the result on `wikiView.Course *courseView`. Old revisions, the source view, the edit page and the preview have no box. The box also shows below the empty-wiki panel, since a team may have a course before it writes its page.

```go
// courseView is the Course box at the end of the wiki page.
type courseView struct {
	Package     pack.ID
	Version     string
	Description string
	From        string // set when the course comes from a namespace above
	PackageURL  string // its package page
	Lessons     []lessonLink
	More        int    // lessons past course.MaxLessons, not listed
	Open        *lessonView
	Missing     string // the one line of "When the course cannot be shown"
	Settings    string // link for those who may change it
}

type lessonLink struct {
	N           int
	Title, URL  string
	Current     bool
}

type lessonView struct {
	N, Of      int
	Title      string
	HTML       template.HTML
	Note       string // from the renderer: diagrams or quizzes left as source, a cut
	Prev, Next *lessonLink
}
```

- **The chain** comes from `t.places` (the page's namespace and its ancestors, which `wikiParent` already walks); `registry.CourseOf` picks the course. No extra store call.
- **Reading:** `openRelease`'s logic for the newest release, through `h.ui.Registry.Releases` and `Fetch` with `t.pr`, then `bundle.Scan(data, course.IsLesson)` and `course.Lessons`. Only lesson files are held in memory, at most the KB's limits.
- **Opening a lesson:** `?lesson=PATH` on the wiki page, where PATH is a lesson's file name (`02-propose.md`), so a link keeps naming the same lesson when a new release adds one before it. It must pass `pack.CheckPath` and be a lesson of the newest release; anything else shows the box closed with "That lesson is not in the course any more." Links end in `#lesson-h`, so the browser scrolls to the open lesson.
- **Rendering a lesson:** `document()` with `docLinks` that send a link to another lesson to `?lesson=PATH#lesson-h` and a link to any other file of the KB to the release's preview page (`{base}/files/{path}`), and `diagramFrames` on the release's `draw/{path}` route. At most `maxPreview` (512 KB) of a lesson is rendered, with the preview's note when cut. A parse context key `lessonKey` turns on a small transformer that drops the lesson's first level-1 heading when it equals the title (the box writes the title as `h3 id="lesson-h"`) and shifts the other headings down two levels (h1 to h3, at most h6), so the page's outline reads Wiki, Course, lesson.
- **Ids** `course`, `course-h`, `lesson-h` and `lessons-h` join `reservedIDs` (a heading could slug to them); the existing test that writes a heading for every page id covers them.

### Layout (`templates/ui/wiki.html`, a `course` template)

At the end of the page, after the wiki panel and the "has a wiki too" line, a `<section class="panel course" id="course" aria-labelledby="course-h">`:

1. A panel head: `h2` **Course**, then a meta line "lime-course 1.4.0 · 7 lessons" linking the package page, and "From acme" when inherited.
2. The KB's description, escaped, one paragraph.
3. **No lesson open** (the default): the lessons as an ordered list of links, 44 px rows with the number and the title, like a grouped list. "Start with lesson 1" is not needed; the first row is the start.
4. **A lesson open:** the list folds into `<details class="course-all"><summary>All 7 lessons</summary>...</details>` (closed), then an `<article class="course-lesson" aria-labelledby="lesson-h">` with "Lesson 3 of 7", the title (`h3`), the lesson, and a footer with **Previous** and **Next** (links, `rel="prev"`/`rel="next"`) and "Close lesson" (a link to the page without `?lesson`, ending in `#course`). The current row in the list has `aria-current="page"`.

The box uses only existing tokens (`--panel`, `--line`, `--text-2`, `--link`, `--ok-*`, `--bad-*`) and the `.wiki-md` text styles for the lesson; nothing sticky, no script. At 390 px the list rows wrap their titles and Previous and Next share one row.

Why links and not one `details` per lesson: rendering every lesson on every view of the wiki would parse and send up to 50 documents and their diagram frames when a reader wants one; a link renders one, gives each lesson an address to send to a new teammate ("do lesson 3 today"), and keeps Back working. The cost is a page load per lesson, which also closes any wiki sections the reader opened; the box is at the end of the page, so the jump lands on the lesson either way.

### Settings (`templates/ui/settings.html`, `ui_settings.go`)

A **Course** panel right after the Wiki panel, for `CanSetWiki` only, built like it: a `.setting` form posting to `POST /t/{ns}/settings/course` (`saveCourse`, `inTeam`, CSRF) with a labelled `select`:

- the first option: "No course" on a project, or "acme's course (acme/onboarding)" on a team when one applies above (both send the empty value);
- then every knowledge base the namespace reads (its own and the layers above, from `h.ui.Registry.Packages(ctx, t.pr)` filtered to `pack.KindKB`), as "lime/lime-course", grouped by namespace with `optgroup`;
- text: "The Course box at the end of the wiki shows a knowledge base as lessons: its Markdown files at the top level, in name order." When the wiki is off: "It shows once the wiki is on."
- notices `course-set` ("The wiki shows lime/lime-course as its course.") and `course-none` ("lime has no course of its own.").

A refusal from the service shows the page again with the field error (400), as the other settings forms do. `web.Teams` gains `SetCourse`.

## Order of work

Each step ends with `make check` green and could ship alone.

1. **Quiz format and renderer.** `internal/course` (`ParseQuiz`, `Quiz`, `Answer`) with tables; `internal/web/quiz.go` (`quizBlocks`, `quizBlock`, `quizHTML`, `quizDepthKey`) added to `docMD`; CSS for `.quiz`; `markdown_test.go` cases. Ships alone: quiz fences render in package previews, skill pages and wiki pages.
2. **Lessons.** `kb.Title` exported (the KB index unchanged), `course.IsLesson`, `course.Lessons`, `MaxLessons`; tests.
3. **The setting.** `registry.Namespace.Course`, `NamespaceStore.SetCourse`, `Service.SetCourse`, `CourseOf`; migration 0015; `sqlitestore`, `MemStore`, the contract; `account.Service.SetCourse`; `api`, `server` PATCH order, `client.SetCourse`; `namespace set --course`, `manager`, `hostManager`; the `loadout-admin` skill line. Ships alone: the setting exists and nothing shows it yet.
4. **The box.** `ui_course.go`, `wikiView.Course`, the `course` template, `lessonKey` heading shift, `reservedIDs`, CSS; web tests.
5. **Settings panel.** `saveCourse`, the Course panel, `web.Teams.SetCourse`; web tests.
6. **Docs and review.** GUIDE, DESIGN, UI.md record, screenshots (below). Back up the live registry before the deploy that runs migration 0015.

Steps 1 and 2 touch different files from step 3 and can be built at the same time.

## Tests

**Unit tables**

- `internal/course/quiz_test.go`: one right answer; several right; `*` and `X`; an explanation over several lines and with a blank line, its indentation removed; an answer without an explanation; Markdown in the question kept verbatim; refused with the line named: no question, one answer, nine answers, no right answer, an unindented line after answers, a fence over 16 KiB, CRLF input read as LF.
- `internal/course/lessons_test.go`: `IsLesson` (`01-a.md` yes, `A.MARKDOWN` yes, `refs/b.md` no, `notes.txt` no, `README.md` yes); order `01-`, `02-`, `10-`; titles from the heading and from the file name; 60 lessons give 50 and a total of 60.
- `internal/kb`: the existing title table runs against `Title`.
- `internal/registry`: `SetCourse` accepts the namespace's own KB and an ancestor's, refuses a sibling's (`ErrInvalid`), an agent or skill, a package without releases, a missing namespace (`ErrNotFound`); the zero ID clears. `CourseOf`: own wins over the project's, the project's when the team has none, none at all, a three-layer chain.
- `internal/account`: `SetCourse` for owner, owner of the project above and admin; refused for maintainer, member and a reader through a team below.
- `internal/web/markdown_test.go`: a quiz renders a group with its question and one `details` per answer saying Right or Wrong; an invalid quiz stays code with the note; a `quiz` fence inside an explanation stays code; 51 quizzes draw 50; ids are `quiz_N` and never equal a heading id; `xss` in the question, an answer, an explanation, a link target in an explanation (`javascript:`) and an image alt, never unescaped.

**Contract suite:** "course setting" in `RunNamespaceStoreContract`, against SQLite and `MemStore`. A migration test opens a database at version 14 with namespaces and reads `Course.IsZero()` for each.

**Server:** `PATCH` with `course` alone, with all three settings, with an invalid course and `wiki` (nothing changes, 400), by a maintainer (403, nothing changes).

**Web** (`internal/web/ui_course_test.go`, on `newUISite`):

- The box shows the lessons of lime's own course in file order, with the version and the package link; with no course in the chain there is no box; a team without its own shows acme's, labelled "From acme".
- `?lesson=02-b.md` renders that lesson with Lesson 2 of 3, Previous and Next, the list folded, `aria-current` on its row; the first has no Previous and the last no Next; `?lesson=refs/x.md`, `?lesson=../x`, `?lesson=gone.md` show the closed box with the sentence, status 200.
- Links in a lesson: to another lesson becomes `?lesson=...#lesson-h`, to a reference document the release's preview page, to another site unchanged by the usual rules. A mermaid fence becomes a frame on the release's `draw/` route, and that route answers for a reader through the team.
- The headings of a lesson are shifted and its first `#` heading dropped when it equals the title.
- Missing cases from the table: the owner sees the line and the Settings link, a member sees no box; a KB whose newest release is a skill; a KB without top-level Markdown.
- Old revisions, `?source=1` and the edit preview have no box; the empty wiki still has it.
- Settings: olga (owner of acme) sees the Course panel on lime with lime's and acme's KBs and the inherited first option; alice (maintainer) does not, and her `POST /t/lime/settings/course` is 403; an agent's id posted by hand is 400 with the field error; the notices.
- `xss` in the KB description, a lesson title and a lesson's text never unescaped (`wantOut`).

**End to end** (`cmd/loadout/course_test.go`, `ownedRegistry`, `person`, `webBrowser`): olga publishes `acme/onboarding` and alice `lime/lime-course` (two lessons, one quiz, one reference document in a folder); alice's `namespace set lime --course lime/lime-course` is refused, olga's works and prints the wiki address and the "once the wiki is on" line; `--course lime/planner` is refused as an agent; `--course web/styles` is refused as not in lime's chain; with the wiki on, alice's browser `GET /t/lime/wiki` holds both lesson titles and `?lesson=` the quiz; `--course none` and the page shows acme's course with "From acme"; `loadout sync` installs `lime-course` for alice as a skill whose files hold the quiz fence as written.

**UI review** (CLAUDE.md order): the rules in `docs/UI.md`; then the `apple-design` skill as a checklist (disclosure controls, lists and tables, layout, writing, color, accessibility); then the rampstack `frontend-component-build`, `design-standards` and `accessibility-audit` skills. Screenshots with the `loadout-preview` skill: a typical course (7 lessons, a mermaid diagram, three quizzes) on a team wiki with a long page above it; no lesson open, lesson 3 open with one answer opened, the inherited course, the owner's missing-course line, the Settings panel; light and dark, 390 and 1280 px, keyboard only (Tab through the list, Enter on an answer), with a screen reader's reading of a quiz, and without JavaScript. The record goes in `docs/UI.md` as "Course box on the wiki (October 2026)".

## Docs

- `docs/GUIDE.md`: "Knowledge bases" gains **A KB as a course**: top-level Markdown files are lessons in name order (pad the numbers), folders hold reference material, the quiz block with an example and its rules, preview it on the package page before choosing it. "The website" (Settings and Wiki) gains the Course panel and the box, with the inheritance rule. "The same from the CLI" and the Command reference gain `namespace set --course`. "Rules and limits": 50 lessons listed, 8 answers, 16 KiB per quiz. Rebuild with `docs/guide/build.sh`.
- `docs/DESIGN.md`: the Wiki section gains a **Course** bullet (setting, `CourseOf`, who may, the box reads the newest release through the page's principal, links and frames); "Web pages" mentions the box on `/t/{ns}/wiki` and the quiz fence among the `docMD` rules; the storage line gains 0015; the packages table gains `internal/course`.
- The `loadout-author` skill may mention that a KB can be a course and how quizzes are written; the honesty test checks any command it names.

## Risks

- **Stored XSS.** Lessons, descriptions and quiz pieces are untrusted text shown to every reader of the wiki and the teams below. Every piece goes through `docMD` (raw HTML as text, no dangerous links, no images); the quiz renderer writes only fixed tags around HTML the renderer produced; tests send the `xss` constant through every field.
- **Work per view.** Each wiki view with a course decompresses the newest release (up to 32 MB compressed) to find the lessons, as each file preview page does today, and renders at most one lesson. If the wiki becomes a busy landing page, a small cache of lesson lists keyed by release digest (immutable, so never stale) is the fix; not built until measured.
- **A course change reaches everyone at once.** The box follows the latest release, so a broken lesson is live as soon as it is published. Review before release (`require_review`) on the course's namespace is the guard, and it already exists.
- **Quiz fences in existing documents** start to render as quizzes after step 1. A document that used a `quiz` fence for something else shows a quiz or the could-not-read note above its code; the text stays readable.
- **Inheritance surprises.** A project owner's course appears on every team's wiki below it that has none. The label "From acme" says where it comes from, and the team's Settings shows it as the first option.
- **Partial PATCH.** Ordering course first keeps a value refusal from leaving the other settings half changed; a store failure between two settings can still leave the first applied, as today with review and wiki.
- **Rollback.** Migration 0015 makes an older binary refuse the database, as every migration does: back up before the deploy, roll back by restoring.

## Not in this step

- **Progress per person (v2).** A table `course_progress (user_id, namespace, package, lesson_path, done_at)` with a primary key on the first four, added by its own migration (no rows means nothing done, so old data stays valid; deleting a user cascades). Lessons are keyed by path, so progress survives a new release. A "Mark as done" `POST` form per lesson (CSRF, no script), ticks in the list, and an `account.Service` method that only lets a person write their own rows. Open questions for then: whether owners see a team's progress, and whether quiz answers are recorded at all (recommendation: no, only "done").
- An explicit "no course" for a team below a project that has one.
- Lessons in folders, or an index file that orders them.
- A lint for quiz fences at `loadout publish` (the `course` package makes it a small step).
- A course on the project's page for readers whose team has its own (they open the project's wiki).
- Showing the course in the CLI, or a separate course page outside the wiki.
- Scoring, timers, or anything that grades a person.

## Decided by the user (2026-10-05)

1. **A team without its own course shows the project's.** Closest wins, labelled "From acme". A team cannot hide an inherited course without choosing its own; that opt-out stays out of v1.
2. **Lesson order comes from file names.** Lessons are the top-level Markdown files of the KB (`01-`, `02-`); folders hold reference material. No manifest field, no index file.
3. **Quiz fences render everywhere `docMD` renders**: package previews, skill pages, wiki pages and the course box.

## As built (2026-10-05)

Where the build differs from the plan above:

- **No `quizDepthKey`.** A parse without the `quizzes` count in its context leaves quiz fences as code, and a quiz's pieces are rendered without it, so a quiz inside a quiz stays code without a key of its own.
- **`document` returns its note.** It renders through a `docParse` value (links, frames, quizzes, lesson title) and returns `(page, note)`, the frames' note joined with the quizzes'. Pages that render several documents (a built-in skill's page) share one count so quiz ids stay unique; a lesson's quizzes use `lesson_quiz_N`, so they never meet the wiki's `quiz_N` on the same page.
- **`course_above` and `course_from`.** The CLI's "its wiki shows acme/onboarding, from acme" after `--course none` needed to know the course above, and there is no GET of a namespace's settings. The answer to a PATCH that clears the course carries them (`api.Namespace.CourseAbove`, `CourseFrom`), from a new `account.Service.CourseAbove`.
- **`pack.Kind.WithArticle`.** "is an agent", "is a skill" come from the kinds table instead of a helper in each package.
- **End to end** installs the course with `loadout install lime/lime-course` rather than `sync`, since `sync` updates what is installed and a fresh person has nothing installed yet.
- **Migration number** 0015 was free on `main` when built; other plans in flight may need the next one.
- **Preview seed** `scripts/preview/course.sh` adds the course, an inherited one, a missing one and an owner's session (`olga-session`).
