# What's new

Status: built and deployed (2026-10-05). Planned 2026-10-04; the page is at https://loadout.lat/docs/whats-new/ and the entries live in `docs/CHANGES.md`.

## Goal

People who use the registry cannot see what an update changed: features arrive and bugs
go away without a word. A "What's new" page next to the user guide lists, newest first,
what each deploy added (Feature) and fixed (Fix), in plain sentences a team member
understands without reading the code.

The history up to now is written once from the git commits. From then on, adding the
entries is a step of the deploy: the `loadout-deploy` skill drafts them from the commits
since the last deploy, the user edits them, and they ship with the deploy.

The one rule holds. The page is static HTML built by pandoc; the registry and the CLI
never touch it. The drafting of entries happens in the developer's own Claude Code, on
their own machine, while running the deploy skill; nothing in loadout calls a model or
holds a key.

## Decided

1. Backfill: the history so far (216 commits on `main` plus the 8 on `stale-proposals`)
   is built once from git, grouped into entries, tagged Feature or Fix, rewritten into
   plain user-facing sentences, with refactors, lint, test-only and internal commits
   dropped. The result is a hand-editable source file committed to the repository.
2. From now on, the deploy skill gets a step that drafts entries from the commits since
   the last deploy, lets the user edit them, commits them, rebuilds the page and ships it
   with the deploy.
3. Last-deploy tracking: the `<!-- through HASH -->` marker in `docs/CHANGES.md`, not a
   git tag (reasons under Design).
4. Backfill grouping: one section per commit day.
5. Registry link: a "What's new" item in the registry sidebar, next to Docs and above the
   account item, part of the main order of work (see "The registry sidebar link").

Assumed, not decided: the page lives at `https://loadout.lat/docs/whats-new/`, a static
page built next to the guide by `docs/guide/build.sh`, and the guide and the page link to
each other from the docs sidebar.

## What exists today

- `docs/guide/build.sh` runs pandoc 3 on `docs/GUIDE.md` with the template
  `docs/guide/page.html` and the Lua filters `table.lua` (wrap tables so they scroll on a
  phone) and `sections.lua` (each level-2 chapter becomes a `<details>`), writes
  `deploy/site/docs/index.html` and copies `docs.js`. The generated files are committed
  (`git ls-files deploy/site`); CI has no pandoc and never builds them.
- `page.html` holds the whole look inline (the site's CSP allows inline style and
  `script-src 'self'` only): the registry's tokens in light, dark and increased contrast,
  a sidebar with the contents above 900 px and a folding "Contents" menu below it. The
  brand link points to `#top`; `docs.js` points it back to the registry page named in
  `?from` when that page is on a loadout site. `docs.js` already copes with a page that
  has no `#chapter-tools`.
- `deploy/deploy.sh` tars `deploy/site` and swaps it into `/var/www/loadout-site` in one
  step, so any new file under `deploy/site/docs/` ships without a change to the script.
  `deploy/Caddyfile` serves it on `loadout.lat` with `redir /docs /docs/ 308`.
- The registry links to the guide from the sidebar: `docsURL` in `internal/web/ui.go`,
  turned into `uiView.DocsURL` by `handler.renderUI` with `?from=` the current page's
  absolute address, and the Docs item in `templates/ui/layout.html` (`.side-docs`, after
  `.side-fill`, before `.side-user`). `TestDocsLink` in `internal/web/ui_test.go` checks
  it. Below 760 px (`static/style.css`) the sidebar becomes a top bar: the brand, then
  Docs as an icon (its word visually hidden by `.side-docs-label`, still the accessible
  name) pushed right by `margin-left: auto`, then the account item with its name capped
  at 140 px; the projects and teams wrap onto a second, scrolling row (`.side-nav`,
  `order: 3`). At 390 px that first row holds about 32 px of padding, a 98 px brand, a
  36 px Docs icon, a 176 px account item (10 + 16 + 10 + 140) and two 4 px gaps: 350 px,
  leaving 40 px. One more 36 px icon and its gap would use all of it, so a long name
  would push the row to wrap. (These widths are estimates from the CSS; the screenshot
  review measures them.)
- There are no git tags. `Makefile` and `deploy.sh` take the version from
  `git describe --tags --always --dirty`, and `builtin.Version` turns that string into
  the version of the built-in skills (anything not semver becomes 0.0.1). The release
  workflow runs on `v*` tags.
- Nothing records when a deploy happened, except the database backups the deploy skill
  names `before-YYYYMMDD-HHMM.db` on the VM, which are taken only when a deploy migrates
  (or when unsure).
- Commit dates on `main`: 52 on 28 September, 134 on 29 September, 2 on 30 September, 3
  on 1 October, 24 on 3 October, 1 on 4 October; 8 more on `stale-proposals`.

## Design

### The source file: `docs/CHANGES.md`

Markdown, not YAML. Reasons:

- The page is built by pandoc, which reads Markdown already; YAML would need a generator
  (a Go program, or a pandoc metadata template) for what is a list of sentences.
- The file is edited by hand at every deploy, in the middle of a terminal session. A
  bulleted list with a word in front is the easiest thing to edit and to review in a diff,
  and it reads well on GitHub as it is.
- Nothing in Go needs it at runtime. The format check is a test that reads a small line
  grammar (below), so the file stays strict without `strictyaml`. If the registry ever
  shows the entries itself, it can embed the same file and parse the same grammar.

The grammar, line by line:

```markdown
# What's new in loadout

What changed in loadout, newest first. Each update lists what it added and what it
fixed. The [user guide](../) explains how to use each one.

## October 2026

### 4 October 2026
<!-- through b7efd71 -->

- Feature: A proposal made from an older release now says it is behind, on its page,
  in the reviews list and in `loadout review list`.
- Feature: Approving a proposal that is behind asks first, and says which release it
  would replace.
- Fix: The diff of a proposal now compares with the release it was made from.

### 3 October 2026
<!-- through 1a2b3c4 -->

- Feature: ...
```

Rules the test enforces:

- One `#` title, then free intro text, then month sections.
- `## Month YYYY` headings, newest first, no month twice.
- `### D Month YYYY` headings (one per deploy day), newest first, each inside its month,
  no date twice. A second deploy on the same day adds to that day's section and moves its
  marker.
- Right under each `###`, the marker `<!-- through HASH -->`: the abbreviated hash
  (7 to 40 hex characters) of the last commit the deploy shipped. The newest marker is
  how the next deploy knows where to start.
- Every item is `- Feature: ` or `- Fix: ` followed by a sentence ending in a full stop
  (an item may wrap onto indented lines). No section is empty. Features come before fixes
  in a section.
- No em-dash anywhere.

The marker is a comment so the page stays about what users get; the filter drops it from
the HTML.

### Knowing "since the last deploy": a marker in the file, not a git tag

The newest `<!-- through HASH -->` names the last shipped commit, and the next draft reads
`git log --no-merges HASH..HEAD`. Recommended over a tag per deploy because:

- One source of truth, committed and reviewed with the entries it belongs to; nothing to
  push separately, nothing lost on another machine.
- A tag would change the version every binary reports: `git describe --tags` in
  `Makefile` and `deploy.sh` would pick `deploy-2026-10-04` as the nearest tag, and once
  real `v*` tags exist the nearest tag could be a deploy tag, which `builtin.Version`
  reads as 0.0.1. Tags would force `--match 'v*'` into both places for no other gain.
- The newest date alone is not precise: two deploys in a day, or a commit dated before
  the previous deploy but merged after it, would be missed or repeated.

A deploy with nothing user-facing adds no section, so its marker does not move; the next
draft sees those commits again and drops them again. That costs a few lines of reading
and keeps the rule "every section has items".

If `HASH` is not an ancestor of `HEAD` (history rewritten), the helper script stops and
says so; the person picks the range by hand.

### The helper script: `scripts/changes-since.sh`

A small POSIX shell script that gives the drafter (the agent in the deploy skill, or a
person) the same input every time. It does no writing and makes no judgement beyond
hints.

- Reads the newest marker from `docs/CHANGES.md`, checks it with
  `git merge-base --is-ancestor`, and prints one block per commit of
  `git log --no-merges --reverse --date=short MARKER..HEAD`: hash, date, subject, body
  first paragraph, and the files it touched.
- Adds a hint per commit from its files: `tests only` (only `*_test.go` and
  `testdata/`), `plan` (only `docs/plans/`), `site only` (only `deploy/site/` and
  `docs/CHANGES.md`, which is the previous changes commit itself). Lint and refactor
  commits cannot be told apart by their files; the subject is left to the drafter.
- `--all` reads from the root commit, for the backfill. `--since HASH` overrides the
  marker.

Shell, not Go: it is git plumbing plus `grep`, like the other scripts in `scripts/`, and
it must not become part of the binary.

### The page: `deploy/site/docs/whats-new/index.html`

Built by the same `build.sh`, with the same template, so the guide and the page look like
one site.

- URL `/docs/whats-new/`. A sibling folder under `/docs/` keeps `docs.js` at the same
  absolute path (`/docs/docs.js`) and the intro's link to the guide relative (`../`).
- Structure: the `h1`, the intro, an `h2` per month and an `h3` per deploy day with its
  list. The contents (sidebar above 900 px, the Contents menu below) list the months,
  `--toc-depth 2`, so the sidebar stays short as the years go by.
- Nothing folds: `sections.lua` is not applied, the newest update is what a visitor came
  for, and older ones are one scroll away.
- Each item shows its tag as a small label before the sentence: "Feature" or "Fix", in
  text, so the tag does not rely on colour. Features use the existing `--own-bg` and
  `--own-text` tokens; fixes get a neutral pair (`--fix-bg`, `--fix-text`) added to
  `:root`, the dark block and both increased-contrast blocks, with AA contrast checked in
  each.
- Each `h3` wraps its text in `<time datetime="2026-10-04">`.

### `docs/guide/changes.lua`

A Lua filter used only for the changes page:

- `RawBlock` of format `html` that is a `<!-- through ... -->` comment: removed.
- `BulletList` items: the first inline of the first block must be `Feature:` or `Fix:`;
  it becomes `<span class="tag tag-feature">Feature</span>` (or `tag-fix`). Any other
  item stops the build with `error("untagged item: ...")`, so a malformed file cannot
  ship from a careless rebuild.
- Level-3 headers: the text is parsed (`D Month YYYY`, a month table) and wrapped in a
  `<time>` element; a heading it cannot parse also stops the build.

### `docs/guide/page.html`

One template for both pages, switched by pandoc metadata:

- The leading comment and `<meta name="description">` come from metadata
  (`$source$`, `$description-meta$`) instead of being fixed to the guide.
- Above "Contents" in the sidebar, a two-item switch: "Guide" (`/docs/`) and "What's
  new" (`/docs/whats-new/`), styled like the contents links, with `aria-current="page"`
  on the page being shown (`$if(changes)$`). The same two links sit in the phone header
  (`.mobile-top`) on the right of the brand, each at least 44 px tall.
- The tag label styles and the two new tokens.

### `docs/guide/docs.js`

`linkBack` already rewrites the brand link. It also carries `?from` onto the Guide and
What's new links (`a[data-keep-from]`), so a visitor who came from the registry can still
get back after switching pages. Without JavaScript the links work and the brand goes to
`#top`, as today.

### `docs/guide/build.sh`

After the guide, a second pandoc run:

```sh
mkdir -p "$root/deploy/site/docs/whats-new"
pandoc "$root/docs/CHANGES.md" \
	--from gfm --to html5 \
	--standalone --template "$here/page.html" \
	--lua-filter "$here/changes.lua" \
	--toc --toc-depth 2 \
	--columns 10000 \
	--metadata pagetitle="What's new in loadout" \
	--metadata changes=true \
	--metadata source="docs/CHANGES.md" \
	--metadata description-meta="What each loadout update added and fixed, newest first." \
	--output - |
	sed -E 's/ id="toc-[^"]*"//' >"$root/deploy/site/docs/whats-new/index.html"
```

The guide's run gains `--metadata source="docs/GUIDE.md"` and its own
`description-meta`, so its output stays the same apart from the new sidebar switch.

### `deploy/Caddyfile`

`redir /docs/whats-new /docs/whats-new/ 308`, next to the existing `/docs` redirect.
Nothing else: the CSP, headers and `file_server` already fit.

### The registry sidebar link

Every page with the sidebar gets a "What's new" item between Docs and the account item,
so a registry user sees it where they already look for help.

`internal/web/ui.go`:

```go
// whatsNewURL lists what each update changed. Like the guide, it links back
// to the page named in ?from.
const whatsNewURL = docsURL + "whats-new/"
```

`uiView` gains `WhatsNewURL string` next to `DocsURL`. `renderUI` encodes `from` once
and builds both:

```go
from := url.Values{"from": {h.origin + r.URL.RequestURI()}}.Encode()
v.DocsURL = docsURL + "?" + from
v.WhatsNewURL = whatsNewURL + "?" + from
```

The static page's `docs.js` already points its brand link back to `?from` (same script,
same template), so the round trip works as it does for Docs. Without JavaScript the link
opens the page and the brand goes to the top of it, as on the guide.

`internal/web/templates/ui/layout.html`, right after the Docs item:

```html
<a class="side-item side-news" href="{{.WhatsNewURL}}">{{template "icon-news"}}<span class="side-news-label">What's new</span></a>
```

and a new `icon-news` definition next to `icon-docs`: a 16 px outline sparkle drawn for
loadout (two four-point stars), same stroke width and `aria-hidden="true"` as the other
icons. No `aria-current`: the page is on another site.

`internal/web/static/style.css`:

- Wide screens: `.side-news` joins `.side-docs` (`color: var(--muted-2)`), so the two help
  links read as one quiet pair at the bottom of the sidebar, Docs above What's new above
  the account. The token already has light, dark and increased-contrast values (5.7:1 in
  light, measured in the September review).
- Phones (the `max-width: 760px` block):
  - `.side-news-label` is visually hidden like `.side-docs-label`; the word stays the
    accessible name. Both become icon buttons in the top row.
  - Margins: `.side-user, .side-docs { margin-left: auto; }` stays; add
    `.side-docs + .side-news, .side-news + .side-user { margin-left: 0; }` (replacing
    `.side-docs + .side-user`), so the three sit together on the right.
  - The account name cap goes from 140 px to 112 px. The row at 390 px becomes 32 px of
    padding, a 98 px brand, two 36 px icons, a 148 px account item (10 + 16 + 10 + 112)
    and three 4 px gaps: 362 px, leaving 28 px. A long name truncates with an ellipsis as
    today, 28 px sooner; "Sign in" for a visitor is far shorter.
  - Below 375 px (`@media (max-width: 374px)`) the cap drops to 84 px, so a 360 px phone
    keeps one row too. If a phone is narrower still, `.side` already has
    `flex-wrap: wrap`, so the account item wraps under the brand instead of overflowing.
  - On touch (`pointer: coarse`) every `.side-item` is already at least 44 px tall; the
    icon items are 36 px wide, as Docs is today, which the September review accepted.
    The review checks whether two of them side by side are still easy to hit, and widens
    both to 44 px (with the account cap at 104 px) if not.
- No new colour tokens: the item reuses the sidebar's.

## Data and API changes

None. No table, no endpoint, no wire type, no CLI flag. The registry binary changes only
in the sidebar template, its CSS and `uiView`.

## Migration

None in the database. The first `docs/CHANGES.md` (the backfill) is the only "migration":
its newest marker must name the last commit that is actually live, or the first deploy
draft starts from the wrong place (step 4).

## CLI and web changes

- CLI: none.
- Static site: the new page, the Guide and What's new switch on both pages.
- Registry pages: the "What's new" sidebar item on every page with the sidebar (team,
  project, account, Start here, settings, new), signed in or not.

## The backfill

A manual pass, helped by the script, rather than a script that writes the file. The
commits need judgement no script has: 134 commits on 29 September alone, many of them
merges, plans, lint and test fixes, several features reworked two or three times in a
day ("Members becomes Settings", the wiki in six branches). What is user-facing, what
merges into one sentence, and what a fix fixed are decisions for a person (with their
agent), and doing them once does not justify code that would never run again.

1. Run `sh scripts/changes-since.sh --all > .scratch/whats-new/commits.txt` (scratch is
   not committed). The hints mark tests-only, plan and site-only commits.
2. Group by day: 28 September, 29 September, 30 September, 1 October, 3 October, and the
   day `stale-proposals` ships. No record of the early deploys exists, so a day is the
   honest unit; 28 September becomes one "First release" section that says what loadout
   did on day one, in at most eight items, instead of 52.
3. Per day, merge commits about one feature into one item, tag it, and write it as a
   user would say it: what they can now do, where (web, CLI command), not which package
   changed. Drop refactors, renames, lint, test-only, plans, borrowed skills, preview
   scripts and anything about the repository itself.
4. Fixes are written by effect ("Sign-in links no longer expire early"), never by
   mechanism. Security fixes say what is now safe, without the attack (the page is
   public and some registries may run older builds).
5. No client, employer or team names (the page is public): use the words the guide uses.
6. Each section's marker is the last commit of that day on `main`. The newest marker
   must be the commit that is live now: check it with `ssh admin@203.0.113.7
   '/usr/local/bin/loadout version'` (read-only), which prints the deployed
   `git describe` hash. Commits after it belong to the next deploy's draft, not the
   backfill.
7. The user reads and edits the whole file; then build, test and commit it.

Expected size: 30 to 60 items across six sections.

## The deploy skill: exact edits to `.claude/skills/loadout-deploy/SKILL.md`

1. Front matter `description`: after "Ship loadout:" add "write what's new for the
   update," so the skill is found when someone asks to update the changes page.

2. Before `make check` in the code block, a new step 0, and the step numbers after it
   stay as they are:

   ```sh
   # 0. What's new: commits since the last deploy (see "What's new" below).
   sh scripts/changes-since.sh
   ```

3. A new section after the code block:

   ```markdown
   ## What's new

   Every deploy that changes something users see adds a section to `docs/CHANGES.md`
   before it ships. Do this on `main`, before `make check`.

   1. Run `sh scripts/changes-since.sh`. It prints the commits since the newest
      `<!-- through HASH -->` marker. If it stops because the marker is not an ancestor,
      ask the user where to start.
   2. Draft the items: one per user-facing change, merged across commits, each
      `- Feature: ` or `- Fix: ` and a plain sentence ending in a full stop. Say what a
      person can now do and where (page or command), not how. Drop refactors, lint,
      tests, plans, skills and scripts for this repository, and commits marked
      "site only". Security fixes say what is safe now, not the attack. No names of
      clients or teams. No em-dashes. Features first, then fixes.
   3. Put them under `### D Month YYYY` for today, inside `## Month YYYY` (add either
      if missing), with `<!-- through $(git rev-parse --short HEAD) -->` right under the
      date. If today's section exists, add to it and replace its marker.
   4. Show the draft to the user and wait. Apply their edits. Nothing ships with a
      draft the user has not seen.
   5. If nothing user-facing changed, tell the user and add no section.
   6. `sh docs/guide/build.sh && go test ./docs/guide/`, then commit
      `docs/CHANGES.md` and `deploy/site/docs/` as "What's new for D Month".
   ```

4. Step 3 (Check) gains one line:

   ```sh
   curl -s -o /dev/null -w '%{http_code}\n' https://loadout.lat/docs/whats-new/
   ```

5. Rules: the existing line "The docs page ships with the deploy (`deploy/site`);
   rebuild it first with `sh docs/guide/build.sh` if `docs/GUIDE.md` changed." becomes
   "The docs pages ship with the deploy (`deploy/site`); rebuild them with
   `sh docs/guide/build.sh` whenever `docs/GUIDE.md` or `docs/CHANGES.md` changed." And a
   new rule: "The What's new draft is the user's to approve; never commit it unseen."

The changes commit lands on `main` before the deploy, so `deploy.sh` builds the binary
from the same tree that holds the entries, and step 4 pushes both together.

## Order of work

Each step leaves `make check` green and could ship on its own.

1. Format and check. `docs/CHANGES.md` with the title, intro and one placeholder section
   for the commits on `stale-proposals` (so the format test has data), and
   `docs/guide/changes_test.go` (below). No page yet.
2. Page. `changes.lua`, the `page.html` switch, tags and tokens, the `docs.js` change,
   the second run in `build.sh`, the Caddy redirect. Rebuild both pages and commit the
   output. UI review (below), recorded in `docs/UI.md`.
3. Helper. `scripts/changes-since.sh` with `--all` and `--since`.
4. Backfill. The manual pass above; the user edits; the newest marker checked against
   the live version; rebuild and commit.
5. Deploy skill. The edits above.
6. Docs. `docs/ARCHITECTURE.md` lists `CHANGES.md` in its table of technical docs and
   the page in the site diagram; `docs/GUIDE.md` gains one sentence in its intro
   pointing to What's new; the guide is rebuilt. Propose to the user (not edit
   unasked) a line in `CLAUDE.md` under "Read first" for `docs/CHANGES.md`.
7. Registry link. `whatsNewURL`, `uiView.WhatsNewURL`, `renderUI`, the layout item and
   `icon-news`, the CSS above, the web tests below. Screenshots with the
   `loadout-preview` skill, then the apple-design review and `accessibility-audit`,
   recorded in `docs/UI.md`. `docs/DESIGN.md` (the sidebar paragraph under the web pages)
   and `docs/GUIDE.md` ("The website") name the new item; rebuild the guide. This step
   comes after step 2, so the page it points to is in the tree and ships in the same
   deploy as the link at the latest.
8. Ship with the next deploy, using the new step for the first time (its draft should
   cover only what came after the backfill's marker). The deploy's checks open a
   registry page and look for the link (below).

## Tests

- `docs/guide/changes_test.go`, package `guide_test` (a folder with only a test file,
  like `deploy/deploy_test.go`), so `go test ./...` and CI check the file without
  pandoc. Table-driven over small inputs for the parser, plus one test on the real file:
  - accepts the example above; refuses an untagged item, `Feature` without the colon, a
    missing full stop, an em-dash, months or days out of order, a day under the wrong
    month, a duplicate date, a missing or malformed marker, an empty section, a fix
    before a feature, a second `#` title;
  - the real `docs/CHANGES.md` passes;
  - drift: the committed `deploy/site/docs/whats-new/index.html` contains every date of
    the file (as `datetime="YYYY-MM-DD"`) and exactly as many tag labels as the file has
    items, so a forgotten rebuild fails the check.
- `build.sh` fails on a malformed file through `changes.lua`; tried once by hand with an
  untagged item.
- `scripts/changes-since.sh`: run against this repository with a known marker, and with
  a marker that is not an ancestor (it must stop). It has no Go test: the deploy skill
  uses it with a person watching, and the format test guards what it feeds.
- Caddy: `caddy adapt` already runs inside `deploy.sh`; `deploy_test.go` keeps passing
  because the site tar picks up the new folder without a change.
- UI review of both docs pages with the `loadout-preview` skill (`scripts/preview`) and
  the apple-design checklist, then `accessibility-audit`: light, dark, increased
  contrast, 390 px, 800 px, 1440 px, keyboard (skip link, switch, contents, links in
  items), without JavaScript, `?from` kept across the switch, tag contrast computed from
  the tokens. Recorded as a dated section in `docs/UI.md`.
- Web tests in `internal/web`, next to `TestDocsLink` in `ui_test.go` (which becomes a
  table over both links):
  - a signed-in team page with a query (`/t/lime?kind=skill`) has
    `class="side-item side-news" href="https://loadout.lat/docs/whats-new/?from=http%3A%2F%2Flocalhost%2Ft%2Flime%3Fkind%3Dskill"`
    and `<span class="side-news-label">What's new</span></a>` (the template escapes the
    apostrophe; the test asserts whatever `html/template` writes, checked once);
  - the order in the page is Docs, then What's new, then the account item (`side-user`),
    by index in the body;
  - a visitor who is not signed in gets the link on `/skills` with its own `from`
    (`ui_skills_test.go`), before the Sign in item;
  - the link never carries `aria-current`;
  - a page rendered without a sidebar (`Bare`) has no link.
- Registry link review with the `loadout-preview` skill: screenshots of a team page and
  `/skills` at 390 px (signed in with a long name, and signed out), 360 px, 800 px and
  1440 px, light and dark, increased contrast, touch and keyboard (tab order: Docs, What's
  new, account), without JavaScript; then the apple-design checklist (hit targets,
  grouping, icon legibility) and `accessibility-audit` (accessible names of the icon-only
  links on phones, focus ring, contrast of `--muted-2` on `--sidebar`). Findings and
  measurements recorded in the same `docs/UI.md` section.
- Deploy check, added to step 3 of the skill with the page check:
  `curl -s https://registry.example.com/skills | grep -c 'side-news'` prints 1.

## Risks

- Leaking internals on a public page: commit subjects name packages, clients and
  security details. The drafting rules forbid them and the user approves every draft;
  the backfill gets a full read before it is committed.
- A wrong first marker (step 4) makes the first deploy draft repeat or miss commits.
  Checking it against `loadout version` on the VM removes the guess.
- History rewrite on `main` orphans the marker; the script stops instead of drafting
  from a wrong range.
- Forgetting to rebuild after editing `CHANGES.md`: the drift test fails in `make check`,
  which the deploy skill runs right after.
- pandoc versions differ between machines and produce noisy diffs in the generated HTML.
  The same risk exists for the guide today; the build stays pinned to pandoc 3.
- The deploy gets slower and needs the user present. That is the point (they approve
  the words), and a deploy with nothing user-facing costs one sentence.
- The phone top bar gets tighter: account names truncate 28 px sooner, and a phone under
  about 340 px wraps the account item to a second line. The screenshot review at 360 and
  390 px decides whether that is acceptable or the icons need to change.
- The registry links to a page served by another host (`loadout.lat`). A self-hosted
  registry links to loadout's own changes, which may describe features its older build
  lacks. Acceptable for now: the Docs link has the same property.
- Growth: about 50 items a busy week, a few KB of HTML. Splitting by year is a later
  step if the page ever passes a few hundred KB.

## Settled questions

1. `stale-proposals` does not wait for this feature, and this feature does not wait for
   it. The backfill stops at the commit that is live on the VM (checked with
   `loadout version`), so its 8 commits land in the first deploy draft. That draft is
   the first real run of the new deploy step, which tests it on real work.
2. `CLAUDE.md` gets a line under "Read first" naming `docs/CHANGES.md` and the deploy
   step. It is added in order of work step 6, in the same commit that creates the file,
   so it never points at a file that does not exist.

## Not in this step

- A "What's new" command in the CLI, or a notice after `loadout sync` that a newer
  CLI has new features.
- A feed (RSS or Atom) of the entries.
- Showing the entries inside the registry, or per-registry entries for self-hosted
  registries.
- Release notes for `v*` releases on GitHub (goreleaser's changelog stays as it is).
- Any generation of entries by the registry or the CLI; drafting stays in the
  developer's own tool.
