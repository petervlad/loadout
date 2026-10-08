package web

import (
	"bytes"
	"html/template"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/bundle"
)

const (
	// maxPreview is how much of a text file a preview shows. Files go up
	// to 2 MB; a page past this is slow to render and to read.
	maxPreview = 512 << 10
	// maxMermaid is the most characters of one Mermaid diagram that are
	// drawn, Mermaid's own default maxTextSize.
	maxMermaid = 50000
	// maxFences is how many diagram fences of one document are drawn,
	// of all kinds together; each is a frame that loads a library.
	maxFences = 20
)

// fileRef is one file of a release and what its preview may link to.
type fileRef struct {
	Path string
	Data []byte
	// Entries are every file of the release, for links.
	Entries []bundle.Entry
	// Base is the release's file routes, "/t/{ns}/p/{team}/{name}/{version}".
	Base string
	// Page is the draw.io page the preview page shows, from 1; 0 is the
	// first.
	Page int
}

// url is the preview page of the file p of the same release.
func (f fileRef) url(p string) string { return f.Base + "/files/" + p }

// frameURL is the diagram frame of the file, with query ("" or "?page=2").
func (f fileRef) frameURL(query string) string { return f.Base + "/draw/" + f.Path + query }

// links leads the file's relative links to the preview pages of the
// release's other files.
func (f fileRef) links() docLinks {
	files := map[string]bool{}
	for _, e := range f.Entries {
		files[e.Path] = true
	}
	return docLinks{dir: path.Dir(f.Path), files: files, to: f.url}
}

// previewBody is what file.html shows; at most one of HTML, Text, Diagram
// and Download is set.
type previewBody struct {
	// What names the preview on the page: "Markdown", "Text".
	What string
	// HTML is rendered Markdown, safe to put in the page.
	HTML template.HTML
	// Text is shown in a pre, escaped by html/template.
	Text string
	// Diagram is a file drawn in a frame, with its source.
	Diagram *diagramView
	// Note says what the preview leaves out.
	Note string
	// Download is set when there is no preview: "No preview for this kind
	// of file."
	Download bool
}

// rendered reports whether the preview is rendered Markdown, so the page
// offers its source. A diagram file has its source folded under the frame.
func (b previewBody) rendered() bool { return b.HTML != "" }

// diagramView is a diagram file on its preview page: a frame the page's
// script puts in, and the source, which is what shows without script.
type diagramView struct {
	// Frame is the URL of the frame, Title its accessible name.
	Frame, Title string
	Source       string
	// Pages are a draw.io file's page tabs, links to this page.
	Pages []pageTab
	// Canvas draws the frame on a light canvas in both themes: draw.io
	// diagrams keep the colors their author picked.
	Canvas bool
	// Notes say what the frame cannot do for this diagram.
	Notes []string
}

// pageTab is one page of a draw.io file.
type pageTab struct {
	Name    string
	URL     string
	Current bool
}

// frameSource is what a diagram frame draws: the library (a key of
// diagramLibs), the diagram's text, and for draw.io the page to open.
type frameSource struct {
	Kind   string
	Source string
	Page   int
}

// A previewKind draws one kind of file on its page. previews is searched
// in order and the first match wins; a new kind of preview is one more
// row.
type previewKind struct {
	match func(f fileRef) bool
	build func(f fileRef) previewBody
	// frame is what the file's diagram frame draws, given the frame's
	// query; nil for a kind without diagrams.
	frame func(f fileRef, q url.Values) (frameSource, bool)
}

var previews = []previewKind{
	{match: ext(".md", ".markdown"), build: markdownPreview, frame: markdownFrame},
	{match: ext(".mmd", ".mermaid"), build: mermaidPreview, frame: mermaidFrame},
	{match: ext(".drawio"), build: drawioPreview, frame: drawioFrame},
	{match: isText, build: textPreview},
}

// kindOf is the row of the table that previews f, if any.
func kindOf(f fileRef) (previewKind, bool) {
	for _, p := range previews {
		if p.match(f) {
			return p, true
		}
	}
	return previewKind{}, false
}

// previewOf builds the preview of f: the first row that matches, or none.
func previewOf(f fileRef) previewBody {
	if p, ok := kindOf(f); ok {
		return p.build(f)
	}
	return previewBody{What: "No preview", Download: true}
}

// frameOf is what the diagram frame of f draws for the query q; false when
// f has no such diagram.
func frameOf(f fileRef, q url.Values) (frameSource, bool) {
	p, ok := kindOf(f)
	if !ok || p.frame == nil {
		return frameSource{}, false
	}
	return p.frame(f, q)
}

// ext matches file names ending in one of exts, ignoring case.
func ext(exts ...string) func(fileRef) bool {
	return func(f fileRef) bool {
		name := strings.ToLower(f.Path)
		for _, e := range exts {
			if strings.HasSuffix(name, e) {
				return true
			}
		}
		return false
	}
}

// isText matches any UTF-8 text. Packages hold nothing else today; the
// fallback is for the day they may.
func isText(f fileRef) bool { return utf8.Valid(f.Data) && bytes.IndexByte(f.Data, 0) < 0 }

func markdownPreview(f fileRef) previewBody {
	// Cutting Markdown by bytes could leave a fence or table open, so a
	// long document is shown as text instead.
	if len(f.Data) > maxPreview {
		return textPreview(f)
	}
	frames := &diagramFrames{route: f.frameURL(""), name: path.Base(f.Path)}
	html, note := document(string(f.Data), f.links(), frames)
	return previewBody{What: "Markdown", HTML: html, Note: note}
}

// markdownFrame is a diagram fence of a Markdown document.
func markdownFrame(f fileRef, q url.Values) (frameSource, bool) { return fenceFrame(f.Data, q) }

func mermaidPreview(f fileRef) previewBody {
	if utf8.RuneCount(f.Data) > maxMermaid {
		b := textPreview(f)
		b.Note = joinNotes("This diagram is too large to draw here: Mermaid draws up to 50,000 characters.", b.Note)
		return b
	}
	return previewBody{What: "Mermaid diagram", Diagram: &diagramView{
		Frame: f.frameURL(""), Title: "Diagram: " + path.Base(f.Path), Source: string(f.Data),
	}}
}

// mermaidFrame is a whole .mmd file.
func mermaidFrame(f fileRef, _ url.Values) (frameSource, bool) {
	if utf8.RuneCount(f.Data) > maxMermaid {
		return frameSource{}, false
	}
	return frameSource{Kind: "mermaid", Source: string(f.Data)}, true
}

func drawioPreview(f fileRef) previewBody {
	names, total, _ := drawioPages(f.Data)
	page := min(max(f.Page, 1), max(total, 1))
	what := "draw.io diagram"
	if total > 1 {
		what += ", " + plural(total, "page")
	}
	d := &diagramView{
		Frame: f.frameURL("?page=" + strconv.Itoa(page)), Title: "Diagram: " + path.Base(f.Path),
		Source: string(f.Data), Canvas: true, Notes: drawioNotes(f.Data),
	}
	if total > 1 && page <= len(names) {
		d.Title += ", page " + names[page-1]
	}
	if len(names) > 1 {
		for i, name := range names {
			d.Pages = append(d.Pages, pageTab{Name: name, URL: f.url(f.Path) + "?page=" + strconv.Itoa(i+1), Current: i+1 == page})
		}
	}
	b := previewBody{What: what, Diagram: d}
	if len(f.Data) > maxPreview {
		// The frame draws all of it; the source below is cut like text.
		d.Source = textPreview(f).Text
		b.Note = "The source below shows the first " + strconv.Itoa(maxPreview>>10) + " KB. Download the file for all of it."
	}
	if total > maxPageTabs {
		b.Note = joinNotes("The tabs name the first "+strconv.Itoa(maxPageTabs)+" of "+strconv.Itoa(total)+" pages; the arrows in the diagram reach the rest.", b.Note)
	}
	return b
}

// drawioNotes say what the frame leaves out of this diagram: it opens no
// links (the sandbox has no popups) and loads no formulas or web fonts
// (the frame has no network).
func drawioNotes(data []byte) []string {
	var notes []string
	if bytes.Contains(data, []byte("link=")) {
		notes = append(notes, "Links inside the diagram do not open here. Download the file and open it in draw.io to follow them.")
	}
	if bytes.Contains(data, []byte(`math="1"`)) {
		notes = append(notes, "Formulas show as their source text here.")
	}
	if bytes.Contains(data, []byte("fontSource=")) {
		notes = append(notes, "Web fonts are not loaded here; text uses a fallback font.")
	}
	return notes
}

// drawioFrame is a whole .drawio file, opened at the page in ?page (from
// 1; none is the first).
func drawioFrame(f fileRef, q url.Values) (frameSource, bool) {
	_, total, _ := drawioPages(f.Data)
	page := 1
	if raw := q.Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > max(total, 1) {
			return frameSource{}, false
		}
		page = n
	}
	return frameSource{Kind: "drawio", Source: string(f.Data), Page: page - 1}, true
}

func textPreview(f fileRef) previewBody {
	b := previewBody{What: "Text", Text: string(f.Data)}
	if len(f.Data) > maxPreview {
		cut := maxPreview
		for cut > 0 && !utf8.RuneStart(f.Data[cut]) {
			cut--
		}
		b.Text = string(f.Data[:cut])
		b.Note = "Showing the first " + strconv.Itoa(maxPreview>>10) + " KB of " + size(len(f.Data)) + ". Download the file to read all of it."
	}
	return b
}

// joinNotes puts two notes in one line, either may be empty.
func joinNotes(a, b string) string { return strings.TrimSpace(a + " " + b) }
