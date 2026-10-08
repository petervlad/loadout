package web

import (
	"bytes"
	"html/template"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/petervlad/loadout/internal/pack"
)

// answerMD renders an answer's Markdown. Answers are untrusted, so raw
// HTML is shown as escaped text (answers often hold placeholders like
// <your ID>, which dropping would lose), javascript: and other dangerous
// links lose their href, and images become their alt text: even with the
// page's img-src 'self', an image would make the reader's browser fetch a
// URL the answer chose. Hard wraps keep single line breaks, as the
// terminal the answer was written for shows them.
var answerMD = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList),
	goldmark.WithParserOptions(parser.WithASTTransformers(
		util.Prioritized(noImages{}, 100),
	)),
	goldmark.WithRendererOptions(html.WithHardWraps(), renderer.WithNodeRenderers(
		util.Prioritized(escapedHTML{}, 100),
	)),
)

// docMD renders a document of a package: a skill's files, a knowledge
// base's documents. They are as untrusted as answers and get the same
// rules (raw HTML as text, no dangerous links, images as their alt text),
// but they are written with wrapped source lines, so a single line break
// is a space, their relative links lead to the package's other files, and
// a quiz fence becomes a question with answers that fold out (quiz.go).
var docMD = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList),
	goldmark.WithParserOptions(parser.WithASTTransformers(
		util.Prioritized(noImages{}, 100),
		util.Prioritized(relinks{}, 200),
		util.Prioritized(quizBlocks{}, 250),
		util.Prioritized(diagramBlocks{}, 300),
		util.Prioritized(mdxCode{}, 400),
		util.Prioritized(wikiSections{}, 500),
		util.Prioritized(lessonHeadings{}, 600),
	)),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(
		util.Prioritized(escapedHTML{}, 100),
		util.Prioritized(diagramFigure{}, 100),
		util.Prioritized(sectionHTML{}, 100),
		util.Prioritized(quizHTML{}, 100),
	)),
)

// escapedHTML writes raw HTML, inline or as a block, as the text it is.
type escapedHTML struct{}

func (escapedHTML) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindRawHTML, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			segs := n.(*ast.RawHTML).Segments
			for i := range segs.Len() {
				seg := segs.At(i)
				_, _ = w.Write(util.EscapeHTML(seg.Value(src)))
			}
		}
		return ast.WalkSkipChildren, nil
	})
	reg.Register(ast.KindHTMLBlock, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		b := n.(*ast.HTMLBlock)
		_, _ = w.WriteString("<p>")
		lines := b.Lines()
		for i := range lines.Len() {
			line := lines.At(i)
			_, _ = w.Write(util.EscapeHTML(line.Value(src)))
		}
		if b.HasClosure() {
			_, _ = w.Write(util.EscapeHTML(b.ClosureLine.Value(src)))
		}
		_, _ = w.WriteString("</p>\n")
		return ast.WalkSkipChildren, nil
	})
}

// noImages replaces every image with the text of its alt.
type noImages struct{}

func (noImages) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	var images []*ast.Image
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if img, ok := n.(*ast.Image); ok && entering {
			images = append(images, img)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	for _, img := range images {
		parent := img.Parent()
		for c := img.FirstChild(); c != nil; {
			next := c.NextSibling()
			parent.InsertBefore(parent, img, c)
			c = next
		}
		parent.RemoveChild(parent, img)
	}
}

// linksKey holds, in a parse, the docLinks of the document.
var linksKey = parser.NewContextKey()

// docLinks turns a document's relative links to other files of its
// package into links on the registry: a section of a skill's page, or a
// file's preview page.
type docLinks struct {
	// dir is the document's folder, "." at the top.
	dir string
	// files are the paths a link may lead to.
	files map[string]bool
	// to is the URL of a file.
	to func(path string) string
}

// resolve returns the URL of the package file dest names, resolved from
// the document's folder; false for anything else (another site, an
// anchor, a path that leaves the package or names no file). A fragment is
// dropped: the pages give headings no ids.
func (l docLinks) resolve(dest string) (string, bool) {
	target, _, _ := strings.Cut(dest, "#")
	if target == "" || strings.HasPrefix(target, "/") || strings.Contains(target, ":") {
		return "", false
	}
	p := path.Join(l.dir, target)
	if pack.CheckPath(p) != nil || !l.files[p] {
		return "", false
	}
	return l.to(p), true
}

// relinks points a document's links to its package's files (docLinks) at
// the registry; any other link is left to the renderer's usual checks.
type relinks struct{}

func (relinks) Transform(doc *ast.Document, _ text.Reader, pc parser.Context) {
	l, ok := pc.Get(linksKey).(docLinks)
	if !ok {
		return
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if a, ok := n.(*ast.Link); ok && entering {
			if dest, ok := l.resolve(string(a.Destination)); ok {
				a.Destination = []byte(dest)
			}
		}
		return ast.WalkContinue, nil
	})
}

// framesKey holds, in a parse, the *diagramFrames of the document.
var framesKey = parser.NewContextKey()

// A fenceDiagram is a fenced block language drawn in a frame. A new kind
// of diagram fence is one more row of fenceDiagrams.
type fenceDiagram struct {
	// langs are the fence languages, in any case.
	langs []string
	// query is the frame's parameter, which numbers the fences of this
	// kind from 1: ?block=K is the K-th mermaid fence.
	query string
	// kind is the frame's library, a key of diagramLibs.
	kind string
	// draw is false for a fence the frame cannot draw; it stays code.
	draw func(src []byte) bool
	// canvas draws the frame on a light canvas in both themes: draw.io
	// diagrams keep the colors their author picked.
	canvas bool
}

var fenceDiagrams = []fenceDiagram{
	{langs: []string{"mermaid"}, query: "block", kind: "mermaid", draw: fitsMermaid},
	{langs: []string{"drawio", "draw.io"}, query: "drawio", kind: "drawio", draw: plainDrawio, canvas: true},
}

// fitsMermaid is a diagram Mermaid draws: at most maxMermaid characters.
func fitsMermaid(src []byte) bool { return utf8.RuneCount(src) <= maxMermaid }

// plainDrawio is an uncompressed draw.io diagram of at most maxPreview
// bytes. A compressed one would be pasted base64, which nobody can review
// in the page's text, so it stays code.
func plainDrawio(src []byte) bool {
	xml := bytes.Contains(src, []byte("<mxfile")) || bytes.Contains(src, []byte("<mxGraphModel"))
	return len(src) <= maxPreview && xml && !pack.DrawioCompressed(src)
}

// fenceDiagramOf is the row of fenceDiagrams for a fence's language.
func fenceDiagramOf(lang []byte) (fenceDiagram, bool) {
	for _, d := range fenceDiagrams {
		for _, l := range d.langs {
			if strings.EqualFold(string(lang), l) {
				return d, true
			}
		}
	}
	return fenceDiagram{}, false
}

// diagramFrames gives a document's diagram fences their frames.
type diagramFrames struct {
	// route is the document's frame route; a fence's frame adds its
	// kind's query. Empty draws nothing and only counts the fences, for a
	// text that is not stored yet.
	route string
	// name is the document's name, for the frames' titles.
	name string
	// total is how many diagram fences the document has; the parse sets it.
	total int
}

// url is the frame of the k-th fence of kind d.
func (fr *diagramFrames) url(d fenceDiagram, k int) string {
	return fr.route + "?" + d.query + "=" + strconv.Itoa(k)
}

// note says how many diagrams the document leaves as source, if any.
func (fr *diagramFrames) note() string {
	if fr.total <= maxFences {
		return ""
	}
	return "This document has " + strconv.Itoa(fr.total) + " diagrams. The first " + strconv.Itoa(maxFences) + " are drawn; the others show as source."
}

// kindDiagram is the node a drawn diagram fence becomes.
var kindDiagram = ast.NewNodeKind("Diagram")

// diagramBlock is a diagram fence that gets a frame: its source and the
// frame's URL and title.
type diagramBlock struct {
	ast.BaseBlock
	source       []byte
	frame, title string
	canvas       bool
}

func (n *diagramBlock) Kind() ast.NodeKind            { return kindDiagram }
func (n *diagramBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// diagramFence is a fenced block of a kind in fenceDiagrams, and which
// fence of its kind it is, from 1.
type diagramFence struct {
	node    *ast.FencedCodeBlock
	diagram fenceDiagram
	k       int
}

// diagramBlocks turns the first maxFences diagram fences of a document
// into diagramBlocks, except those their kind cannot draw. Each kind
// numbers its own fences, so ?block=K stays the K-th mermaid fence
// whatever other diagrams the document has, and the frame route numbers
// them the same way (fenceSources).
type diagramBlocks struct{}

func (diagramBlocks) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	fr, ok := pc.Get(framesKey).(*diagramFrames)
	if !ok {
		return
	}
	src := reader.Source()
	for i, f := range diagramFences(doc, src) {
		fr.total++
		body := fenceText(f.node, src)
		if fr.route == "" || i >= maxFences || !f.diagram.draw(body) {
			continue
		}
		n := &diagramBlock{
			source: body, frame: fr.url(f.diagram, f.k), canvas: f.diagram.canvas,
			title: "Diagram " + strconv.Itoa(i+1) + " in " + fr.name,
		}
		f.node.Parent().ReplaceChild(f.node.Parent(), f.node, n)
	}
}

// diagramFences are the diagram fences of doc, in document order.
func diagramFences(doc ast.Node, src []byte) []diagramFence {
	var out []diagramFence
	seen := map[string]int{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		f, ok := n.(*ast.FencedCodeBlock)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		if d, ok := fenceDiagramOf(f.Language(src)); ok {
			seen[d.query]++
			out = append(out, diagramFence{node: f, diagram: d, k: seen[d.query]})
		}
		return ast.WalkContinue, nil
	})
	return out
}

// fenceText is the content of a fenced block.
func fenceText(f *ast.FencedCodeBlock, src []byte) []byte {
	var buf bytes.Buffer
	lines := f.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		buf.Write(seg.Value(src))
	}
	return buf.Bytes()
}

// fenceSources are the fences of a Markdown document of the kind whose
// frame parameter is query, numbered as diagramBlocks numbers them.
func fenceSources(data []byte, query string) []string {
	doc := docMD.Parser().Parse(text.NewReader(data))
	var out []string
	for _, f := range diagramFences(doc, data) {
		if f.diagram.query == query {
			out = append(out, string(fenceText(f.node, data)))
		}
	}
	return out
}

// fenceFrame is what the frame of a Markdown document draws for the query
// q: the K-th fence of the kind whose parameter q holds. The source comes
// from data, never from the query.
func fenceFrame(data []byte, q url.Values) (frameSource, bool) {
	for _, d := range fenceDiagrams {
		if !q.Has(d.query) {
			continue
		}
		k, err := strconv.Atoi(q.Get(d.query))
		if err != nil || k < 1 || k > maxFences {
			return frameSource{}, false
		}
		fences := fenceSources(data, d.query)
		if k > len(fences) || !d.draw([]byte(fences[k-1])) {
			return frameSource{}, false
		}
		return frameSource{Kind: d.kind, Source: fences[k-1]}, true
	}
	return frameSource{}, false
}

// diagramFigure writes a diagramBlock as a figure with its escaped source
// and its frame's URL. ui.js puts the frame in and folds the source into a
// Diagram source disclosure; without script the source is what shows.
type diagramFigure struct{}

func (diagramFigure) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindDiagram, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		d := n.(*diagramBlock)
		_, _ = w.WriteString(`<figure class="diagram`)
		if d.canvas {
			_, _ = w.WriteString(" diagram-canvas")
		}
		_, _ = w.WriteString(`" data-frame="`)
		_, _ = w.Write(util.EscapeHTML([]byte(d.frame)))
		_, _ = w.WriteString(`" data-title="`)
		_, _ = w.Write(util.EscapeHTML([]byte(d.title)))
		_, _ = w.WriteString(`">` + "\n" + `<pre class="diagram-src"><code>`)
		_, _ = w.Write(util.EscapeHTML(d.source))
		_, _ = w.WriteString("</code></pre>\n" + `<figcaption class="diagram-note js-hide">Diagrams are drawn with JavaScript on.</figcaption>` + "\n</figure>\n")
		return ast.WalkSkipChildren, nil
	})
}

// markdown renders an untrusted answer to HTML that is safe to put in a
// page. When it cannot, the text is shown escaped, as it was sent.
func markdown(src string) template.HTML { return convert(answerMD, src, parser.NewContext()) }

// document renders a package document, its links to the package's files
// leading where links says. With frames, its diagram fences get diagram
// frames; without, they stay code. note says what the page leaves as
// source, or "".
func document(src string, links docLinks, frames *diagramFrames) (page template.HTML, note string) {
	return docParse{links: links, frames: frames, quizzes: &quizzes{}}.render(src)
}

// docParse is what one rendering of documents with docMD carries. Pages
// that show several documents share one quizzes, so the ids of their
// quizzes stay unique on the page.
type docParse struct {
	links   docLinks
	frames  *diagramFrames
	quizzes *quizzes
	// lesson, when set, is the title of a lesson in the Course box
	// (lessonHeadings).
	lesson string
}

// render renders src and says what it leaves as source, or "".
func (d docParse) render(src string) (page template.HTML, note string) {
	pc := parser.NewContext()
	pc.Set(linksKey, d.links)
	pc.Set(quizzesKey, d.quizzes)
	if d.frames != nil {
		pc.Set(framesKey, d.frames)
	}
	if d.lesson != "" {
		pc.Set(lessonKey, d.lesson)
	}
	page = convert(docMD, src, pc)
	if d.frames != nil {
		note = d.frames.note()
	}
	return page, joinNotes(note, d.quizzes.note())
}

func convert(md goldmark.Markdown, src string, pc parser.Context) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf, parser.WithContext(pc)); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(src) + "</p>") //nolint:gosec // escaped just before
	}
	return template.HTML(buf.String()) //nolint:gosec // raw HTML is escaped and dangerous links dropped, see answerMD and docMD
}
