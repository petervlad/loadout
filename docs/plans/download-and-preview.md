# Download and preview

Status: built and deployed (2026-09-29). See As built at the end.

## Goal

On the package page a person can download a whole package version as one file,
download any single file inside it, and open a preview of any file: Markdown
rendered, Mermaid and draw.io diagrams drawn, everything else as text. There
are no image previews. Who may download or preview is exactly who may open the package
page today. Nothing here runs a model or needs a key: the registry serves files
it already stores and the reader's browser draws the diagrams, so the one rule
holds.

## What packages can hold, and what is previewed

- Packages hold UTF-8 text only (`pack.checkFiles` refuses anything else at
  publish), so there are no PNG, JPEG or PDF files.
- **No images (user's decision).** The registry does not preview images or
  serve them for previews: no PNG, JPEG or SVG preview, no `.drawio.png` or
  `.drawio.svg` support, and Markdown keeps `noImages` exactly as it is (every
  image becomes its alt text). An `.svg` file a skill happens to hold is text,
  so it is previewed as text like any other file and can be downloaded.
- Previewed: Markdown (with `mermaid` fences drawn), `.mmd` and `.mermaid`,
  `.drawio` (plain XML or compressed, publish still refuses compressed files,
  but the viewer and the page-name reader handle both, so a rule change later
  costs nothing here), plain text and code, and a "No preview for this kind of
  file. Download it." fallback.
- Downloads work for every file in a package, whatever its name.
- A knowledge base's `SKILL.md` is generated at install time and is not a file
  of the package. It stays on the package page as today and is neither
  downloaded nor previewed as a file.

## Design

### Where things live

| Behaviour | Place | Why there |
| --- | --- | --- |
| Write a package as a zip. Read one entry of a stored bundle without decoding all of it | `internal/bundle` (`zip.go`, `read.go`) | `bundle` owns the formats a package travels in and the safe reading of stored tar.gz data |
| Routes, access, headers, preview pages | `internal/web` (`ui_files.go`) | pages and their headers belong to `web`. The registry service stays unchanged |
| Which preview a file gets | `internal/web/preview.go`, a table | one place picks by file name. No `switch` on extensions spread around |
| Markdown for documents (mermaid fences, package links) | `internal/web/markdown.go` | next to `answerMD`, sharing its node renderers and transformers |
| draw.io page names | `internal/web/drawio.go` | presentation only. No other package needs it |
| Frame script, vendored libraries | `internal/web/static/preview.js`, `internal/web/static/vendor/` | same-origin static files, like `ui.js` |

### Routes

All are GET, all go through `inTeam` (sign-in, 404 for a namespace the user
cannot open), and all carry the version in the path, so a link always means one
immutable release:

```
GET /t/{ns}/p/{team}/{name}?version=V               the package page, at version V (newest by default)
GET /t/{ns}/p/{team}/{name}/{version}/download      the whole version as a zip
GET /t/{ns}/p/{team}/{name}/{version}/raw/{path...} one file, as an attachment
GET /t/{ns}/p/{team}/{name}/{version}/files/{path...} the preview page of one file
GET /t/{ns}/p/{team}/{name}/{version}/draw/{path...}  the sandboxed diagram frame (?page=N or ?block=K)
```

No API route. The API already serves the bundle (`GET /v1/packages/{team}/{name}/{version}`)
to any token that reads the package, and `loadout install` writes the files.
Nothing in the CLI needs a single file.

### Access: one helper, the rule of the package page

`packagePage` checks today, in order: `pack.NewID`, that the package's team is
at the page's depth or above (`t.pr.Depth`), then `Registry.Releases` and
`Registry.Fetch`, which check `pr.CanRead` again. That sequence moves into one
helper that every route above uses, so no route can skip a step:

```go
// release is one released version of a package the page's user may read.
type release struct {
    rel  registry.Release
    data []byte // the stored bundle, not yet decoded
    base string // "/t/{ns}/p/{team}/{name}/{version}", for links
}

// openRelease resolves the package and version of r for t, or writes the
// 404 the package page would and returns false. version "" is the newest.
func (h *handler) openRelease(w http.ResponseWriter, r *http.Request, t team, version string) (release, bool)
```

- A version that does not parse, is not released (an open proposal's version
  is not a release, and `Fetch` reads only releases), or belongs to a package
  the user cannot read is the same 404 as a missing package.
- The file path from `{path...}` passes `pack.CheckPath` before anything else,
  then must name a file of that release. Otherwise 404. `CheckPath` refuses
  `..`, `.`, hidden segments, empty segments and every character outside
  `[A-Za-z0-9._-]`, so encoded slashes, backslashes and `%2e%2e` never reach a
  lookup. `ServeMux` cleans `..` in the raw path with a redirect before our
  handler runs. The tests cover both.
- The manifest `loadout.yaml` is a file like any other: it is in the zip and
  can be previewed.

### Reading a stored bundle without holding it all

The package page decodes the whole bundle (`bundle.Decode`), up to 64 MB of
files, for every view. That is fine for one page, but a person clicking through
a large knowledge base would decode 30 MB per click. Two streaming readers in
`internal/bundle`, for bundles the registry already stored (they were fully
checked at publish):

```go
// Entry is one file of a stored bundle.
type Entry struct {
    Path string
    Size int
}

// Scan lists the files of a stored bundle and returns the content of the
// ones want accepts. Each file's size is checked against the largest
// limits while reading, as in Decode.
func Scan(data []byte, want func(path string) bool) ([]Entry, map[string][]byte, error)

// Each calls fn with every file in path order, one at a time.
func Each(data []byte, fn func(path string, r io.Reader) error) error
```

A preview needs the file list (for links and the previous and next file) and
one file's bytes: one `Scan`, memory of one file (at most 2 MB). The zip
download streams with `Each`.

### Download of a whole version

- **Format: zip.** It opens with a double click on macOS, Windows and most
  Linux desktops, and the people who want a package as a file are often not
  the terminal users (who have `loadout install`). tar.gz needs a tool on
  Windows. The bundle stays tar.gz on the wire. The zip is only for people.
- **What is in it:** every file of the version, the manifest included, under
  one folder named like the file, so unzipping gives the package folder that
  `loadout publish` and `loadout propose` read (after a version bump).
- **Name:** `{team}-{name}-{version}.zip`, for example
  `payments-planner-1.4.0.zip`, holding `payments-planner-1.4.0/loadout.yaml`,
  `payments-planner-1.4.0/agent.md`. Team, name and version are `ident` and
  semver values, so the name is plain ASCII. The header is still built with
  `mime.FormatMediaType("attachment", ...)`.
- **Which version:** the one in the path. The page's Download button links to
  the version the page shows, the newest unless `?version=` picks another, and
  each row of the releases table has its own Download link.
- **Deterministic:** entries in path order, mode 0644, modification time the
  release's `PublishedAt`, deflate. The same release always gives the same
  bytes.

```go
// Zip writes the files of a stored bundle as a zip, each under root/, in
// path order, dated mod. It streams: one file is in memory at a time.
func Zip(w io.Writer, data []byte, root string, mod time.Time) error
```

- **Headers:** `Content-Type: application/zip`, `Content-Disposition:
  attachment. Filename="..."`, `X-Content-Type-Options: nosniff` (set for
  every page already), `Cache-Control: no-store` (already), and the page CSP.
- **Cost:** a 64 MB knowledge base deflates in about a second. At most 4 zips
  are built at once (a buffered channel in the handler, like the password
  hashing slots). A request waits up to 2 seconds and then gets the "busy, try
  again" 503 page. The handler gives itself 10 minutes to write, with
  `http.ResponseController.SetWriteDeadline`, as `server.allowTransfer` does
  for bundles (four lines copied: `web` may not import `server`, which mounts
  it). A write error midway leaves the browser with a broken download, which it
  reports. Nothing on the server changes.

### Download of one file

- `raw/{path...}` always answers `Content-Disposition: attachment.
  Filename="<base name>"`. `CheckPath` limits names to `[A-Za-z0-9._-]`, so
  the base name is safe as it is.
- `Content-Type` is `text/plain; charset=utf-8` for every file, whatever its
  extension (an `.svg` too). A skill may hold
  `evil.html` or `evil.js`. They are served as text, with nosniff, so a browser
  neither renders nor runs them.
- The response gets its own `Content-Security-Policy: default-src 'none'.
  Style-src 'unsafe-inline'. Sandbox`, so a file opened directly (Firefox
  shows attachments of some types inline when asked) runs no script, loads
  nothing, and gets an opaque origin.
### Preview page

Its own page per file, not an inline fold-out on the package page:

- A knowledge base holds up to 5,000 documents. Folding each out would put
  them all in the package page's HTML, or need script to load them.
- A page has a URL a person can share in a chat, and the back button works.
- Diagrams need a different CSP (a frame, below). Only these pages get it.

The page (`templates/ui/file.html`):

- a back link to the package, `payments/planner` (like `skill.html`'s
  `.back`), then the path as the `h1`, and a line with the version, the size
  and what the preview is ("Markdown", "Mermaid diagram", "draw.io diagram, 3
  pages", "Text").
- Download (the raw route) and, for rendered kinds, Show source (`?source=1`,
  the text as it is. Show preview goes back).
- the preview itself.
- Previous and Next file links at the bottom, in the package's file order, so a
  knowledge base can be read through without going back each time.

The package page changes:

- The header panel gets a **Download** button next to the install command:
  "Download 1.4.0" with the hint ".zip" in its accessible name ("Download
  payments/planner 1.4.0 as a zip").
- Each row of the releases table gets a Download link, and its version links
  to `?version=V`, which shows that version's files and main file. A page on an
  older version says so above the files ("You are looking at 1.3.0. The newest
  is 1.4.0.") with a link to the newest.
- In the files table each name links to its preview page, and each row gets a
  download icon link with a hidden label ("Download architecture.drawio").
- The main-file panel (agent.md, SKILL.md) gets Preview and Download links.
  A knowledge base's generated index gets neither, since it is not a file.

### Which preview a file gets: a table

```go
// A preview draws one file on its page. previews is searched in order and
// the first match wins.
type preview struct {
    what  string                 // "Markdown", "Mermaid diagram", for the page
    match func(path string) bool // by file name, case-insensitive
    build func(f fileRef) (previewBody, error)
}

// fileRef is one file and what its preview may link to.
type fileRef struct {
    Path    string
    Data    []byte
    Entries []bundle.Entry // every file of the release, for links
    Base    string         // release.base
}

// previewBody is what file.html shows; exactly one field is set.
type previewBody struct {
    HTML     template.HTML // Markdown, rendered safely
    Text     string        // shown in <pre>, escaped by html/template
    Diagram  *diagramView  // a Mermaid or draw.io frame, with its source
    Note     string        // "Showing the first 512 KB of 1.8 MB."
    Download bool          // no preview: "No preview for this kind of file."
}
```

| Match, in order | Preview |
| --- | --- |
| `.md`, `.markdown` | Markdown with the document renderer (below) |
| `.mmd`, `.mermaid` | a Mermaid frame, source below it |
| `.drawio` (plain or compressed) | a draw.io frame with page tabs, source below it |
| any other UTF-8 text | `<pre>`, no highlighting |
| anything else | "No preview for this kind of file." and Download |

Highlighting code would add another vendored library for little gain. A
reviewer already sees plain code in diffs.

### Size limits for previews

- Markdown and text: the first 512 KB are shown (files go up to 2 MB). A
  longer file shows a note and Download. Markdown past the limit is shown as
  text, since cutting Markdown by bytes can break a table or fence.
- Mermaid: at most 50,000 characters per diagram (Mermaid's own default
  `maxTextSize`) and 500 edges. Larger shows the source with a note.
- Mermaid fences in one Markdown document: the first 20 are drawn. Later ones
  stay as source with a note, since each is a frame.
- draw.io: the whole file (at most 2 MB, the package limit). Page names are
  read for at most 100 pages. Tabs beyond that are not shown and the viewer's
  own page arrows still reach them.

### Markdown: the document renderer

`answerMD` stays for answers. Documents get `docMD`, built from the same parts:

```go
var docMD = goldmark.New(
    goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList),
    goldmark.WithParserOptions(parser.WithASTTransformers(
        util.Prioritized(noImages{}, 100), // unchanged: every image to its alt text
        util.Prioritized(relinks{}, 200),
        util.Prioritized(mermaidBlocks{}, 300), // numbers the fences, sets their frame URL
    )),
    goldmark.WithRendererOptions(renderer.WithNodeRenderers(
        util.Prioritized(escapedHTML{}, 100),
        util.Prioritized(mermaidFence{}, 100),
    )),
)
```

- **No hard wraps.** Answers keep single line breaks because a terminal shows
  them. Documents are written with wrapped source lines. The built-in skill
  pages move to `docMD` too, since `SKILL.md` and references are documents.
- **Raw HTML** is still escaped text (`escapedHTML`), and dangerous links still
  lose their target (goldmark's default, unchanged).
- **Links** to other files of the package (resolved from the document's
  folder, cleaned, passing `CheckPath`, and in `Entries`) go to that file's
  preview page, through the existing `linksKey` and `relinks`. `linksFrom`
  takes a function from path to URL, so the skill pages keep their `#file-...`
  anchors and the preview pages get `.../files/...`.
- **Images** stay alt text: `noImages` is used unchanged (user's decision).
- **Mermaid fences.** A fenced block whose info string is `mermaid` renders as
  a figure holding the escaped source and the frame URL:

```html
<figure class="diagram" data-frame="/t/.../1.4.0/draw/docs/flow.md?block=2">
  <pre class="diagram-src"><code>graph TD; A--&gt;B</code></pre>
</figure>
```

  Without script that is the code block, with a caption "Diagrams are drawn
  with JavaScript on." With script, `ui.js` puts the frame in and folds the
  source into a Show source disclosure. Other fences render as today.

### Diagrams: a sandboxed frame

Both libraries need inline styles: Mermaid writes a `<style>` element into its
SVG and style attributes, and the draw.io viewer sets `style` on everything.
The pages' CSP (`style-src 'self'`) blocks both. Loosening the page's CSP would
also run a few megabytes of third-party code, which parses untrusted diagram
text, in the origin that holds the session. So diagrams draw in a frame:

- `draw/{path...}` is a small document of its own (`templates/frame.html`,
  parsed apart from the layout pages) with the diagram source embedded as
  escaped text in `<pre hidden id="src">`, our `preview.js` and one vendored
  library. The source always comes from the package file on the server
  (`?block=K` picks the K-th mermaid fence, `?page=N` the draw.io page), never
  from the URL, so the frame cannot be made to draw text an attacker put in a
  link.
- The parent page embeds it as `<iframe sandbox="allow-scripts"
  loading="lazy" title="Diagram: flow.mmd">`. Without `allow-same-origin` the
  frame has an opaque origin: its script cannot read the parent page, its
  cookies or its CSRF token, and requests it makes carry no session cookie (the
  cookie is `SameSite=Lax`). Without `allow-popups` or `allow-top-navigation`
  it cannot open windows or move the page.
- The frame's response has its own headers:

```
Content-Security-Policy: default-src 'none'; script-src {origin}; style-src {origin} 'unsafe-inline';
    img-src {origin} data:; font-src {origin} data:; frame-ancestors 'self'; base-uri 'none';
    form-action 'none'; sandbox allow-scripts
X-Frame-Options: SAMEORIGIN
```

  `{origin}` is the public URL's origin (`h.origin`), written out instead of
  `'self'` because browsers have differed on what `'self'` means inside a
  sandboxed, opaque-origin document. The `sandbox` directive keeps the frame
  sandboxed even when someone opens its URL directly. `connect-src` is
  `'none'`, so neither library can fetch anything.
- The preview page (and only it) adds `frame-src 'self'` to the page CSP.
- Caddy sets `X-Frame-Options "DENY"` on every response today. It becomes
  `?X-Frame-Options "DENY"` (only when loadout sent none), and loadout sends
  `DENY` in `headers` for everything except the frame. Browsers ignore
  `X-Frame-Options` when a CSP `frame-ancestors` is present, but the header
  should not contradict the policy.
- **Height.** The frame posts its drawn height to the parent
  (`parent.postMessage({loadout: "height", h}, origin)`). `ui.js` accepts it
  only when `event.source` is one of the page's diagram frames, clamps it to
  120 to 3,000 px, and sets the frame's height. Larger diagrams scroll inside
  the frame. Until then a frame is 400 px tall.
- **Theme.** The frame is rendered with the reader's `data-theme` from the
  appearance cookie (the frame request carries it: it is a same-site
  navigation). "System" follows `prefers-color-scheme` inside the frame.

#### Mermaid

- `.mmd` and `.mermaid` files, and `mermaid` fences in Markdown.
- `preview.js` calls `mermaid.initialize` once with `startOnLoad: false`,
  `securityLevel: 'strict'` (text is encoded, click handlers off),
  `htmlLabels: false` and `flowchart.htmlLabels: false` (labels are SVG text,
  no `foreignObject` HTML), `maxTextSize: 50000`, `maxEdges: 500`, and the
  theme `default` or `dark`. It adds `htmlLabels`, `flowchart` and
  `dompurifyConfig` to Mermaid's `secure` list (which already holds
  `securityLevel`, `maxTextSize` and `maxEdges`), so an `%%{init: ...}%%`
  directive in a diagram cannot turn them back on. Mermaid's own `sandbox`
  level is not used: it draws into a frame of its own, which our frame
  already is.
- A syntax error shows Mermaid's message as text (`textContent`) above the
  source.

#### draw.io

- Options weighed:
  - **The diagrams.net viewer, vendored** (`viewer-static.min.js`, Apache
    2.0): draws what draw.io draws, with every shape set, multi-page files,
    zoom and layers. Large, but loaded only in diagram frames. Chosen.
  - **A frame on viewer.diagrams.net or embed.diagrams.net:** sends team
    documents to a third party and needs the CSP to allow it. Refused.
  - **Server-side conversion to SVG:** there is no Go library that lays out
    mxGraph with its styles, stencils and HTML labels. The real converter is
    draw.io's Electron app or a headless browser, which would run untrusted
    documents in a browser on the registry host and add a large runtime to the
    deploy. Refused. (Mermaid is the same: its CLI drives headless Chrome.)
- `preview.js` builds the viewer's config object in script (never JSON in an
  HTML attribute written by the server): `xml` from `#src`, `page` from the
  URL, `toolbar: "pages zoom layers"`, `nav: true`, `resize: true`,
  `lightbox: false`, `edit: null`, and the stencil, image and math paths set to
  nothing so it does not try the network (the CSP would block it anyway).
- **Pages.** `drawio.go` reads the `<diagram name="...">` elements with
  `encoding/xml` (a streaming decoder over at most 2 MB, no entity expansion
  beyond the built-in five), up to 100. The preview page shows them as tabs,
  each a link to `?page=N`, rendered by the server, so they work and are
  announced as links without script. A file without names gets "Page 1",
  "Page 2". XML the decoder refuses gets no tabs. The viewer still tries.
- **What does not work, and the page says so under the frame when it applies:**
  links inside a diagram do not open (the sandbox has no popups). Formulas
  (MathJax) and web fonts are not loaded and show as text and a fallback font.
- **Colors.** draw.io diagrams carry the colors their author picked, so they
  draw on a light canvas (`--diagram-canvas`, white, with a hairline border) in
  both themes rather than being recolored. Mermaid follows the theme.
- Compressed pages (deflate plus base64 inside `<diagram>`) are drawn by the
  viewer as they are. The page-name reader only reads the `name` attribute, so
  it never inflates anything.

### Vendored libraries

| Library | Version (at planning) | License | File | Size | gzip |
| --- | --- | --- | --- | --- | --- |
| Mermaid | 12.0.0 | MIT (bundled: d3 ISC, DOMPurify MPL-2.0 or Apache-2.0, KaTeX MIT, cytoscape MIT, others MIT) | `mermaid.min.js` | 5.4 MB | 1.6 MB |
| draw.io viewer | 31.5.3 | Apache-2.0 (bundled: mxGraph Apache-2.0, pako MIT, DOMPurify) | `viewer-static.min.js` | 4.2 MB | 0.9 MB |

- They live in `internal/web/static/vendor/mermaid-12.0.0/` and
  `internal/web/static/vendor/drawio-31.5.3/`, each with its `LICENSE` (and
  `NOTICE` where there is one) and a `THIRD-PARTY.md` naming what the bundle
  includes. The version in the path makes the URL immutable, so they are
  served with `Cache-Control: public, max-age=31536000, immutable`. Everything
  else under `/auth/static/` keeps today's headers.
- **Only in frames.** `layout.html` never loads them. A package page, a
  Markdown document without diagrams and every other page stay as light as
  today.
- **In the binary, compressed.** `web` is linked into the one `loadout`
  binary, so every CLI install would carry these files. They are embedded
  gzipped (`.js.gz`, about 2.5 MB together) and served with
  `Content-Encoding: gzip`. A client that does not accept gzip gets them
  decompressed on the fly. Caddy's `encode` leaves an already encoded response
  alone.
- **Updating.** `scripts/vendor.sh` downloads the pinned versions (Mermaid's
  npm tarball, checked against the registry's `integrity` hash. Draw.io's file
  from the `v{version}` tag of `jgraph/drawio`), writes the gzip files and
  records version, source URL and SHA-256 in `static/vendor/VERSIONS`. A test
  checks that `VERSIONS` matches the embedded files and that `frame.html`
  names paths that exist. Mermaid's security advisories are the thing to read
  before an update. The sandbox limits what a bug can reach, but a newer
  version is still the fix.
- "draw.io" and "diagrams.net" are used only to name the file format, never as
  our branding.

## Data and API changes

None. No store method, no column, no API route. The new code reads releases
through the existing `web.Registry.Releases` and `Fetch`.

## Migration

None. Old releases work unchanged: every stored bundle already holds only
checked paths and UTF-8 text.

## CLI and web changes

- CLI: none. `loadout install` writes packages, and the API bundle route
  serves any token. A later `loadout pull TEAM/NAME[@V] [DIR]` that writes the
  package folder for editing is possible but not asked for.
- Web: `ui_files.go` (new: `openRelease`, `download`, `rawFile`, `filePage`,
  `drawFrame`), `preview.go`, `drawio.go` (new), `markdown.go` (`docMD`,
  `mermaidBlocks`, `mermaidFence`, `linksFrom` with a URL
  function), `ui_team.go` (`packagePage` on `openRelease` and `?version=`),
  `ui_skills.go` (to `docMD`), `ui.go` (routes, and the comment listing them),
  `web.go` (embed pattern for `static/vendor`, per-response CSP, `DENY`),
  templates `package.html`, `file.html` (new), `frame.html` (new),
  `static/style.css` (`.diagram`, `.diagram-frame`, `.page-tabs`, `.file-nav`,
  `--diagram-canvas`), `static/ui.js` (insert frames, height messages),
  `static/preview.js` (new, frame side only).
- Deploy: `deploy/Caddyfile` (`?X-Frame-Options`).

## Order of work

1. `bundle.Scan`, `bundle.Each` and `bundle.Zip`, with tests. Nothing uses them
   yet.
2. `openRelease`. `packagePage` on it with `?version=`. The download and raw
   routes with their headers, the zip slots and the write deadline. The
   Download button, the per-release links and the per-file download links. Web
   tests for access, headers and archive contents. Shippable: downloads work.
3. The preview page for Markdown (`docMD` with package links), text and the
   fallback. File names link to it. Previous and Next. The skill
   pages move to `docMD`. Tests. Shippable: everything but diagrams previews,
   and mermaid fences show as code.
4. The frame: `frame.html`, `drawFrame`, its CSP, `DENY` in `headers`, the
   Caddy change, `preview.js`, the height messages in `ui.js`, `vendor.sh` and
   Mermaid vendored. `.mmd` files and mermaid fences draw. Tests. Shippable.
5. draw.io: `drawio.go`, page tabs, the viewer vendored, `.drawio` files draw.
   Compressed pages too. Tests. Shippable.
6. Seed data in `scripts/preview/seed.sh` (a knowledge base with a multi-page
   `.drawio`, a `.mmd`, a Markdown document with two mermaid fences and a
   relative link, a compressed `.drawio`), screenshots, the UI
   review, docs (below), rebuild the guide, deploy with the new Caddyfile. No
   database migration, so no extra backup is needed for this deploy beyond the
   usual one.

## Tests

- `internal/bundle`: `Scan` returns every entry with its size and only the
  wanted contents, and refuses an oversized entry. `Each` visits in path
  order. `Zip` output opened with `archive/zip` has every file under the root
  folder with the same bytes, 0644, the given time, and two runs give the same
  bytes.
- `internal/web`, access, for each of `download`, `raw`, `files` and `draw`:
  signed out goes to sign-in. A team member opens its own package and the
  project's. A sibling team's package, a team package seen from the project,
  an unknown package, an unknown version, an unparsable version and an open
  proposal's version are all 404. A user who lost the team gets 404.
- Paths: `..`, `%2e%2e`, `a/../b`, `%2F`, a backslash, `.hidden`, an empty
  segment, a path that is a folder, and a missing file are all 404 or the
  mux's redirect to a cleaned path that is then checked. None returns a file.
- Headers: the zip's type, disposition and file name. A raw `.md` is
  `text/plain` with nosniff, attachment and the sandbox CSP. A raw `evil.html`
  in a skill is `text/plain`. A raw `.svg` is `text/plain` too. The preview page's CSP has `frame-src 'self'` and other pages' do not.
  The frame has its CSP, `sandbox allow-scripts` and `SAMEORIGIN`, every other
  page `DENY`. The frame ignores any source in its query (`?src=<script>` does
  not appear in its body).
- Zip slots: with the slots held, a download gets the busy page.
- Markdown, golden tests in `markdown_test.go`: a mermaid fence becomes the
  figure with escaped source and the `?block=` URL (with `</code><script>`
  inside the fence staying escaped). The 21st fence stays a plain code block.
  A relative link to a document goes to its preview page and one to a missing
  file is left as written. Every image (relative, external, `data:`) becomes
  its alt text, exactly as in `answerMD`. Raw HTML stays escaped.
  Single line breaks are not `<br>` in `docMD` and still are in `answerMD`.
- `drawio_test.go`, a table: one page, three named pages, pages without names,
  a compressed page (names still read), 150 pages (100 kept), malformed XML (no
  tabs, no error page), a DOCTYPE with an entity (not expanded).
- Preview table: each row of the table above picks the right preview,
  upper-case extensions, an `.svg` is text (no image), a 600 KB text file gets
  the note.
- `VERSIONS` matches the embedded vendor files.
- In a browser (`scripts/preview/shoot.mjs`): the seeded files in light and
  dark, at 390, 800 and 1280 px, with the keyboard and without JavaScript.
  The script also records `securitypolicyviolation` events in the frame and
  the page, which must be none for the seeded diagrams.
- No e2e in `cmd/loadout`: the CLI does not change.

## Accessibility and layout

- Download and Preview are links (they load something), with names that say
  what: "Download architecture.drawio", "Preview architecture.drawio". Icon
  links have a visually hidden label. On touch screens they are 44 px.
- Page tabs are links in a `nav` with `aria-label="Pages"`, the current one
  `aria-current="page"`, wrapping on phones.
- A diagram frame has a `title` naming the file (and page or block). The
  source is always in the page's HTML, so a screen reader can read the diagram
  as text through Show source.
- Frames are 100% wide. Mermaid scales to the width (`useMaxWidth`). The draw.io
  viewer fits the width and zooms. Wide diagrams scroll inside the frame, never
  the page.
- Colors from tokens. The one new token is `--diagram-canvas`, with the
  canvas's border from `--line`.

## Docs

- `docs/DESIGN.md`: in Web pages, the package page's downloads and
  `?version=`, the preview page, the frame and its CSP next to the pages' CSP,
  and that previews show no images (Markdown images stay alt text). In
  Registry or Web pages, that zips are built from the stored bundle, 4 at once.
- `docs/GUIDE.md`: the Packages bullet (download a version or a file, preview,
  diagrams, older versions). Under Text only, that Mermaid and uncompressed
  draw.io are drawn on the registry's pages. Rebuild with `docs/guide/build.sh`.
- `docs/UI.md`: a review record for the package page and the preview page.
- `docs/plans/skills-and-kb.md` needs no change.

## Risks

- **A bug in Mermaid or the draw.io viewer.** Both parse untrusted text and
  have had XSS fixes. The frame limits a bug to an opaque origin with no
  session, no network and no popups. The worst left is a misleading drawing.
  Keep versions current with `vendor.sh`.
- **A library needs something the frame's CSP forbids** (for example
  `'unsafe-eval'` in parts of mxGraph, or `blob:` images). The browser check in
  step 6 catches it. An allowance is added to the frame's CSP only, where the
  sandbox still holds.
- **Browsers and sandboxed frames.** Script loading by origin inside an opaque
  frame, and `postMessage` from it, are covered by the screenshots in Safari,
  Chrome and Firefox. The fallback when the frame fails is the source, which is
  always on the page.
- **Binary size.** About 2.5 MB more in every `loadout` binary. If that is too
  much for the CLI, the vendor files could be served from a folder on the
  registry host instead (see Choices made).
- **Memory and CPU on the server.** A preview holds one file. A zip one file at
  a time plus its deflate state. At most 4 zips at once. Rendering a 512 KB
  Markdown file is fast.
- **Memory and CPU in the reader's browser.** Each frame parses its library
  (the browser's code cache helps after the first). Frames load lazily and a
  document draws at most 20.
- **Old links.** Every URL carries a version, and releases never change, so a
  shared link keeps showing the same file.

## Not in this step

- Editing diagrams or files in the browser.
- Previews and downloads of an open proposal's files. The preview code takes
  a file list, a file and a base URL, not a release, so the proposal page can
  reuse it later with a `/t/{ns}/reviews/{id}/files/{path}` route and the
  proposal's own access rule.
- Previewing the difference between two versions (the proposal diff exists.
  Drawing two diagrams side by side does not).
- Syntax highlighting, CSV as a table, PlantUML or other diagram formats.
- Image previews of any kind (PNG, JPEG, SVG, `.drawio.png`, `.drawio.svg`),
  by the user's decision. Images in Markdown stay alt text.
- A CLI command to download a package folder.

## Choices made

1. **No images** (the user's decision, 2026-09-29). No image previews, no
   `.drawio.png` or `.drawio.svg`, `noImages` unchanged. Every file can still
   be downloaded.
2. **Diagrams draw in a sandboxed frame** (chosen by the planner, user may
   change): the vendored libraries on the registry's own origin, as above.
   Drawing on the page itself would need `style-src 'unsafe-inline'` in the
   page CSP and run third-party code next to the session. Showing source only
   would miss what was asked.
3. **The vendored JavaScript is embedded gzipped in the binary** (chosen by
   the planner, user may change): about 2.5 MB more for every `loadout`
   binary, CLI users included, so the registry stays one file to deploy. A
   folder on the host copied by `deploy.sh` would keep the CLI smaller but add
   a deploy step and a way for the binary and its scripts to drift apart.
4. **Any release can be previewed and downloaded** through `?version=V` from
   the releases table (chosen by the planner, user may change), not only the
   newest.

## As built (2026-09-29)

Built in the order above, with these differences from the plan:

- **Bundle order.** `Each` and `Zip` visit files in the order of the stored archive, not sorted: the registry keeps uploads as sent, and sorting would mean holding every file. `bundle.Encode` writes path order, so every bundle loadout made is in path order, and any bundle always gives the same zip. `Scan` sorts its list. `Decode` now shares the checked reader (`walk`) with both.
- **The table.** The row type is `previewKind` (`preview` was taken by the diff excerpt). `match` takes the file, not only its name, because the "any other text" row needs the bytes. `build` returns no error. The frame's source is a third column (`frame`), so the draw route asks the same table. `fileRef` has `Page` for draw.io tabs. The frame template is `templates/frame/frame.html`, and `preview.js` loads before the library so the draw.io viewer finds its settings.
- **Show source** is offered only for Markdown. A diagram file's source is always under its frame, folded as **Diagram source** (the folds under diagrams in Markdown are called that too), so a page never has two "Show source" controls.
- **Mermaid size.** A `.mmd` over 50,000 characters is shown as text with a note and gets no frame. A fence that long stays a code block. Edges are left to Mermaid's own `maxEdges: 500`.
- **CSP.** The frame's policy is exactly the planned one. Neither library needed more: the draw.io viewer's `eval` sits behind `mxUtils.eval`, reached only for stencil scripts and function attributes, and fails quietly. Checked in headless Chrome with every frame's console recorded: no violations for Mermaid fences, a `.mmd` and a three-page `.drawio`, in light, dark and at 390 px. Safari and Firefox were not available to check.
- **Headers.** `X-Frame-Options: DENY` is set in `headers` for every response and the frame overrides it with `SAMEORIGIN`. `deploy/Caddyfile` uses `?X-Frame-Options` in the registry block (the static site keeps its fixed DENY). Not deployed.
- **Vendoring.** Mermaid 12.0.0 from its npm tarball, checked against the pinned `integrity` and the one npm publishes. The draw.io viewer 31.5.3 from jsdelivr's copy of the `v31.5.3` tag, checked against a pinned SHA-256 and the copy on raw.githubusercontent.com. `THIRD-PARTY.md` lists Mermaid's dependencies with the license each declares on npm (one, elkjs, is "EPL-2.0 OR GPL-3.0-or-later") and the license comments the draw.io bundle keeps.
- **The manifest** is not a row of the files table (the table lists what the package holds, as before). It is in the zip, and its preview is reachable as the last file through Next and by URL.
- **Seed data** went into `scripts/preview/skills.sh`, where the knowledge base is made: a Markdown document with two Mermaid diagrams and relative links, the `.mmd` and a three-page `.drawio`. A compressed `.drawio` cannot be seeded (publish refuses it). The page-name reader's test covers one. The preview scripts take `LOADOUT_PREVIEW_PORT` so two previews do not share 7431, and `shoot.mjs` opens pages on the public origin (`localhost`, which the frame CSP names) and prints what every frame logged.
- **Screenshots** found and fixed: frames with a scrollbar of their own, a dark strip under draw.io in dark mode, release rows wrapping on phones, a Markdown source view labelled Text (see UI.md).
