package web

import (
	"bytes"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/petervlad/loadout/internal/frontmatter"
	"github.com/petervlad/loadout/internal/wiki"
)

// wikiDocument renders a wiki page: its text and its format. draw is the frame route of the
// stored revision the text comes from ("/t/{ns}/wiki/r/{seq}/draw");
// empty, for the edit preview, leaves diagram fences as code, since a
// frame only ever draws a stored revision. note says what the page does
// not draw, or "". Every ## section folds (wikiSections): closed on the
// page, so it opens as a list of its sections, and open in the preview,
// where the author wants to read what they just wrote.
func wikiDocument(text string, format wiki.Format, draw string) (page template.HTML, note string) {
	front, body := wikiParts(text)
	pc := parser.NewContext()
	if format == wiki.MDX {
		pc.Set(mdxKey, true)
	}
	pc.Set(sectionsKey, sectionsOpen(draw == ""))
	frames := &diagramFrames{route: draw, name: "the wiki"}
	pc.Set(framesKey, frames)
	qs := &quizzes{}
	pc.Set(quizzesKey, qs)
	page = convert(docMD, body, pc)
	if front != "" {
		page = template.HTML(`<pre><code class="language-yaml">`+template.HTMLEscapeString(front)+"</code></pre>\n") + page //nolint:gosec // escaped just before
	}
	if draw == "" && frames.total > 0 {
		return page, joinNotes("Diagrams are drawn once you save.", qs.note())
	}
	return page, joinNotes(frames.note(), qs.note())
}

// wikiFence is what the frame of a stored wiki page draws for the query q
// (?block=K, ?drawio=K), numbered as wikiDocument numbers the fences.
func wikiFence(text string, q url.Values) (frameSource, bool) {
	_, body := wikiParts(text)
	return fenceFrame([]byte(body), q)
}

// wikiParts splits a page into its YAML front matter, if it has one, and
// the Markdown after it. The front matter is shown as code, so its ---
// lines do not render as rules and headings.
func wikiParts(text string) (front, body string) {
	meta, rest, err := frontmatter.Split([]byte(text))
	if err != nil {
		return "", text
	}
	return string(meta), string(rest)
}

// mdxKey, set in a parse, marks the text as MDX for mdxCode.
var mdxKey = parser.NewContextKey()

// mdxCode shows the parts of an MDX page that MDX would run as code:
// import and export paragraphs at the top level, and JSX on lines of its
// own, which Markdown reads as HTML blocks. Nothing of them runs; goldmark
// escapes a code block's text. Inline JSX stays escaped text, as raw HTML
// is in Markdown, so a sentence still reads.
type mdxCode struct{}

func (mdxCode) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	if pc.Get(mdxKey) == nil {
		return
	}
	src := reader.Source()
	var code []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.HTMLBlock:
			code = append(code, n)
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph:
			if n.Parent() == doc && isESM(n, src) {
				code = append(code, n)
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	for _, n := range code {
		cb := ast.NewCodeBlock()
		lines := text.NewSegments()
		lines.AppendAll(n.Lines().Sliced(0, n.Lines().Len()))
		if h, ok := n.(*ast.HTMLBlock); ok && h.HasClosure() {
			lines.Append(h.ClosureLine)
		}
		cb.SetLines(lines)
		n.Parent().ReplaceChild(n.Parent(), n, cb)
	}
}

// isESM reports whether a paragraph's first line is an MDX import or
// export.
func isESM(p *ast.Paragraph, src []byte) bool {
	if p.Lines().Len() == 0 {
		return false
	}
	first := p.Lines().At(0)
	line := first.Value(src)
	return bytes.HasPrefix(line, []byte("import ")) || bytes.HasPrefix(line, []byte("export "))
}

// sectionsKey, set in a parse, folds the document's sections; its value
// is a sectionsOpen.
var sectionsKey = parser.NewContextKey()

// sectionsOpen says whether the folded sections start open.
type sectionsOpen bool

// Nodes of a folded section: a details element holding a summary (the
// section's heading) and a body (everything after it).
var (
	kindSection     = ast.NewNodeKind("WikiSection")
	kindSummary     = ast.NewNodeKind("WikiSummary")
	kindSectionBody = ast.NewNodeKind("WikiSectionBody")
)

type sectionNode struct {
	ast.BaseBlock
	open bool
}

func (n *sectionNode) Kind() ast.NodeKind            { return kindSection }
func (n *sectionNode) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

type summaryNode struct{ ast.BaseBlock }

func (n *summaryNode) Kind() ast.NodeKind            { return kindSummary }
func (n *summaryNode) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

type sectionBodyNode struct{ ast.BaseBlock }

func (n *sectionBodyNode) Kind() ast.NodeKind            { return kindSectionBody }
func (n *sectionBodyNode) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// wikiSections gives a wiki page's headings ids and folds it: each
// level-2 heading at the top level, with everything after it up to the
// next level-1 or level-2 heading, becomes a section that opens and
// closes. What comes before the first one (the title and the intro)
// stays as it is; deeper headings stay inside their section. It runs
// last, so mdxCode still sees the page's top level as it was written.
type wikiSections struct{}

func (wikiSections) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	open, ok := pc.Get(sectionsKey).(sectionsOpen)
	if !ok {
		return
	}
	headingIDs(doc, reader.Source())
	for n := doc.FirstChild(); n != nil; {
		if h, ok := n.(*ast.Heading); !ok || h.Level != 2 {
			n = n.NextSibling()
			continue
		}
		sec, sum, body := &sectionNode{open: bool(open)}, &summaryNode{}, &sectionBodyNode{}
		doc.InsertBefore(doc, n, sec)
		next := n.NextSibling()
		sum.AppendChild(sum, n)
		for next != nil && !sectionBreak(next) {
			after := next.NextSibling()
			body.AppendChild(body, next)
			next = after
		}
		sec.AppendChild(sec, sum)
		sec.AppendChild(sec, body)
		n = next
	}
}

// sectionBreak reports whether n ends a section: a heading of level 1
// or 2.
func sectionBreak(n ast.Node) bool {
	h, ok := n.(*ast.Heading)
	return ok && h.Level <= 2
}

// reservedIDs are the ids of the wiki's pages around the text; a heading
// that would take one gets a number instead. Sidebar ids have a "_",
// which headingIDs never writes.
var reservedIDs = []string{
	"main", "live", "form-error", "nav-projects", "nav-teams",
	"wiki-h", "wiki-empty-h", "wiki-push",
	"edit-h", "stale-h", "wiki-text", "wiki-hint", "wiki-note",
	"course", "course-h", "lesson-h", "lessons-h",
}

// headingIDs gives every heading an id made of its text, as GitHub does,
// so a link to #how-to-deploy finds its heading: lower case letters and
// digits, with a hyphen for each space or hyphen. A repeated one gets
// -1, -2 and so on.
func headingIDs(doc *ast.Document, src []byte) {
	used := map[string]bool{}
	for _, id := range reservedIDs {
		used[id] = true
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		id := slug(plainText(h, src))
		if id == "" {
			id = "section"
		}
		for i, base := 1, id; used[id]; i++ {
			id = base + "-" + strconv.Itoa(i)
		}
		used[id] = true
		h.SetAttributeString("id", []byte(id))
		return ast.WalkSkipChildren, nil
	})
}

// plainText is the text of n's inline content, without its Markdown.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b.Write(c.Segment.Value(src))
		case *ast.String:
			b.Write(c.Value)
		case *ast.RawHTML:
			for i := range c.Segments.Len() {
				seg := c.Segments.At(i)
				b.Write(seg.Value(src))
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// slug keeps the letters and digits of s in lower case and turns spaces
// and hyphens into hyphens.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		case r == ' ' || r == '-' || r == '\t':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// sectionHTML writes a folded section as a native disclosure: a details
// element whose summary holds the heading, so the heading keeps its
// level and its id, and the summary is the control.
type sectionHTML struct{}

func (sectionHTML) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindSection, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch {
		case !entering:
			_, _ = w.WriteString("</details>\n")
		case n.(*sectionNode).open:
			_, _ = w.WriteString(`<details class="wiki-section" open>` + "\n")
		default:
			_, _ = w.WriteString(`<details class="wiki-section">` + "\n")
		}
		return ast.WalkContinue, nil
	})
	reg.Register(kindSummary, func(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString("<summary>")
		} else {
			_, _ = w.WriteString("</summary>\n")
		}
		return ast.WalkContinue, nil
	})
	reg.Register(kindSectionBody, func(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString(`<div class="wiki-section-body">` + "\n")
		} else {
			_, _ = w.WriteString("</div>\n")
		}
		return ast.WalkContinue, nil
	})
}
