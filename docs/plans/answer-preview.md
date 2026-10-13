# Answer preview on the review page

Status: built and deployed (2026-09-29).

## Goal

On a proposal page the current and candidate answers sit side by side. Real
answers run 150 to 550 words, so the two columns get long and hard to compare.
Each answer is shown cut to its first 150 words, with one control that shows
both answers whole, and each column head gives the answer's word count next to
the time and tokens. Nothing here runs a model or needs a key: the registry only
reshapes text a reviewer already uploaded, so the one rule holds.

## Design

### Where the cut happens: on the goldmark AST

Cutting the Markdown source would count `|---|`, `**` and fence lines as words
and could stop inside a table row or leave a fence open, which turns the rest
into code. Cutting the rendered HTML means parsing HTML again. The cut therefore
works on the AST that `answerMD` already builds, after its transformers (so
images are already alt text) and before rendering. The escaping, link and hard
wrap rules of `markdown.go` stay exactly as they are, for both views.

New file `internal/web/excerpt.go`, next to `markdown.go`:

```go
// answerHTML is one answer rendered twice from one parse: whole, and cut
// to about limit words. Cut is false when Short would equal Full.
type answerHTML struct {
    Full, Short template.HTML
    Words       int // words of the whole answer
    Left        int // words the cut leaves out
    Cut         bool
}

// renderAnswer parses src once, renders it whole, counts its words, then
// cuts the same tree and renders the start.
func renderAnswer(src string, limit int) answerHTML

// words counts the words in one node's text: fields split on Unicode space,
// plus one word per Han, Hiragana or Katakana rune, as word processors do.
func words(n ast.Node, src []byte) int
```

`markdown(src)` stays as the escape-only fallback: when parsing or rendering
fails, `Full` and `Short` are both `markdown(src)` and `Cut` is false.

How the cut walks, top-level blocks in order, with a running count:

- Paragraphs, headings and blockquote lines count their text (`ast.Text`,
  code spans, autolinks, link text). Raw HTML counts as the text it shows.
- A paragraph that crosses the limit is cut inside, at the end of the text
  node that crosses it, trimmed back to the last word boundary within the
  limit. The inline siblings after it are removed and an ellipsis Text node is
  appended. Inline nodes always close, because the renderer closes what it opens.
- Lists and blockquotes are containers: the walk goes into their items and
  drops the items after the cut, so a list ends early but stays a valid list.
  An item that crosses the limit is cut like a paragraph.
- Fenced and indented code blocks, tables and thematic breaks are atomic. They
  are kept whole if they end within the limit, else dropped whole with
  everything after them. A code block never loses lines and a fence is never
  left open. Their words count towards the total (a reader reads them, and an
  agent's word limit usually includes them).
- If the first block is atomic and alone passes the limit, it is kept whole so
  the preview is never empty.
- A heading left as the last kept block is dropped, since it would introduce
  nothing.

The limit is a constant, next to `promptPicker` and `maxDiffPreview` in
`ui_reviews.go`:

```go
// answerPreview is how many words of each answer the proposal page shows
// until the reader asks for the whole answers.
const answerPreview = 150
```

An answer is cut only when it has more than `answerPreview + answerSlack`
words (slack 30), so a 160-word answer is shown whole instead of hiding ten
words behind a button. No team setting and no query parameter for the number:
a setting would need a column, a form and a role for a value nobody has asked to
change, and the constant can move later without breaking anything.

### View types

- `answerView` (in `ui_reviews.go`) gains `Words int` and `Cut bool`, and
  `HTML` becomes the HTML the page shows: `Short` when the page is not in full
  view and the answer is cut, else `Full`. `Text` stays.
- `answerOf(o, full bool)` calls `renderAnswer` once and picks. The word count
  joins `Stats`: `11.8 s · 3.7k in / 900 out · 412 words`, through the existing
  `plural` in `ui_format.go`. Failed runs are unchanged: no count,
  no cut, `Stats` stays `failed`.
- `reportView` gains `Full bool` (the page asked for whole answers) and
  `AnyCut bool` (at least one answer is cut, or would be in the short view),
  set in `reportOf`, which gains a `full bool` argument read from `?full=1` in
  `handler.report`.

### The control, without JavaScript

One link for both columns, not a `<details>` per column. Expanding one column
alone would put the two answers out of step, which is the thing the page is for.
A `<details>` would also send every answer twice in the HTML and need `:has()`
to hide the short copy.

- Below `.sides`, a bar shown when `AnyCut` is true:
  - short view: muted text "Showing the first 150 words of each answer." and
    `<a class="btn" href="?report=N&prompt=P&run=R&full=1#answers">Show whole
    answers</a>`.
  - full view: `<a class="btn" href="?report=N&prompt=P&run=R#answers">Show the
    first 150 words</a>`.
- Each cut column ends with a muted line "262 more words" (`.answer-more`),
  so the reader sees which side was cut and by how much.
- The prompt picker form carries `<input type="hidden" name="full" value="1">`
  in full view, so flipping prompts or runs keeps the chosen view.
- `.sides` gets `id="answers"` and `tabindex="-1"`, so the link lands on the
  answers, not the top of the page, and the next Tab continues from there.
- JavaScript: nothing is required. A later nicety could swap the columns in
  place with `fetch`, but a same-page GET with an anchor is already quick.

## Data and API changes

None. The report JSON, the store and the API are untouched. The cut is computed
per request from the answer text the page already loads (at most 256 KiB per
report), one extra render per answer.

## Migration

None.

## CLI and web changes

- Web: `excerpt.go` (new), `ui_reviews.go` (`answerView`, `answerOf`,
  `reportView`, `reportOf`, `report`), `templates/ui/proposal.html` (the
  `.sides` block, the picker's hidden input, the bar), `static/style.css`
  (`.answer-bar`, `.answer-more`, tokens only: `--muted`, `--line-soft`,
  `--panel-alt`). `ui.js` unchanged.
- CLI: none. `review show` prints whole answers as today.

## Order of work

1. `excerpt.go` with `renderAnswer` and `words`, and its table tests. Not yet
   used, so the page is unchanged and green.
2. `answerOf` uses it: word count in `Stats`, still full answers. Web tests
   updated for the count.
3. The cut, `?full=1`, the bar, the "more words" line, the hidden input and the
   anchor. Web tests for both views.
4. Styles, a long answer in `scripts/preview/seed.sh` (one of 400 words with a
   list, a table and a code block, one of 120), screenshots, UI review.
5. Docs: DESIGN.md (web line for `/t/{ns}/reviews/{id}` and the untrusted
   answers line), GUIDE.md (the Reviews bullet), UI.md (a review record).
   Rebuild the guide with `docs/guide/build.sh`.

## Tests

- `excerpt_test.go`, table-driven on `renderAnswer(src, 10)` with a small
  limit so cases stay readable: under the limit (no cut, `Short == Full`).
  Exactly the limit. Limit plus slack and one more. A long paragraph cut
  mid-way at a word boundary with the ellipsis. A list cut between items and
  inside an item (output still has matching `<ul>`/`</ul>`). A table and a
  fenced code block that cross the limit (dropped whole, no `<table>` or
  `<pre>` in `Short`). An open fence at the end of the source. A first block
  that is a long code block (kept whole). A trailing heading (dropped). Unicode
  (accented Latin counted by spaces, Chinese counted per character, emoji not
  split). Markdown syntax not counted (`**a**` is one word, a table's `---` row
  none). Raw HTML and `javascript:` links still escaped and dropped in `Short`.
  Every `Short` output is also checked for balanced tags with a small stack
  over the tags the renderer emits (no new dependency, `x/net/html` is not in
  `go.mod`).
- `ui_test.go`: `addReport` gains a long answer. The page shows "412 words" in
  the head, "Show whole answers", "more words", and not the answer's last
  sentence. With `?full=1` it shows the last sentence and "Show the first 150
  words", and the picker form has `name="full"`. A short answer shows no bar.
  A failed run shows "failed" and no count. `xss` stays out in both views.
- Screenshots with `scripts/preview/shoot.mjs`: short and full view, light and
  dark (7441, 7442), at 390, 800 and 1280 px, and the keyboard path from the
  link to the answers.

## Accessibility and layout

- The control is a link, because it loads a view. Its text is its name ("Show
  whole answers", "Show the first 150 words"), so a screen reader hears "link,
  Show whole answers". The muted sentence beside it is plain text before it in
  reading order. It is at least 44 px tall on touch screens, like the other
  buttons, and keeps the focus ring.
- "262 more words" is text, not a control, so it is not a second tab stop.
- Colors come from the existing tokens, so light, dark and increased contrast
  follow. `--muted` on `--panel` has to be checked for 4.5:1 in both themes.
  Use `--text-2` if it falls short, as the kind filter did.
- On phones `.sides` stacks. The bar sits under both columns and wraps its text
  above the button.

## Overlaps with other plans

`docs/plans/review-from-the-web.md` and `docs/plans/owner-merge.md` are being
written at the same time. This change touches only, in `proposal.html`, the
compare-bar form (one hidden input), the `.sides` block and the new bar after
it, and in `ui_reviews.go`, `answerView`, `answerOf`, `reportView`, `reportOf`
and the query read in `report`. The header actions, `Decision`, the aside and
the empty "No comparison yet" state are left alone. If either plan adds query
parameters to the proposal page, the two links here must carry them too. The
simplest is a `reportView.Query(full bool) string` helper that builds the link
from every view parameter in one place.

## Risks

- A long first code block or table makes the preview long. Accepted: cutting
  it would show broken code, which is worse than a long column.
- Word counts differ from what a model or the agent's author counts. The number
  is a guide, and it is the same rule for both columns, so they compare fairly.
- Switching views reloads the page, one GET that reads the report again. If
  it feels slow, the in-place swap above is the fix.

## Not in this step

- A per-team or per-user preview length.
- Cutting the Markdown-only report (`<pre class="markdown">`). It is shown as
  sent.
- Word counts in `loadout review show` or in the Markdown report.
- Highlighting differences between the two answers.
