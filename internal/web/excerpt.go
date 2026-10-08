package web

import (
	"bytes"
	"html/template"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// answerHTML is one answer rendered twice from one parse: whole, and cut
// to about limit words. Cut is false when Short would equal Full.
type answerHTML struct {
	Full, Short template.HTML
	Words       int // words of the whole answer
	Left        int // words the cut leaves out
	Cut         bool
}

// renderAnswer parses src once, renders it whole, counts its words, then
// cuts the same tree and renders the start. It cuts on the tree, not the
// text, so Markdown syntax is not counted, a table or code block is never
// cut in half and every tag the start opens is closed. An answer is cut
// only when it runs a fifth past limit, so a reader is never sent to a
// button for a few words.
func renderAnswer(src string, limit int) answerHTML {
	b := []byte(src)
	doc := answerMD.Parser().Parse(text.NewReader(b))
	var full bytes.Buffer
	if err := answerMD.Renderer().Render(&full, b, doc); err != nil {
		h := markdown(src)
		return answerHTML{Full: h, Short: h, Words: words(doc, b)}
	}
	out := answerHTML{Full: template.HTML(full.String()), Words: words(doc, b)} //nolint:gosec // see answerMD
	out.Short = out.Full
	if out.Words <= limit+limit/5 {
		return out
	}
	c := cutter{limit: limit, src: b}
	c.blocks(doc)
	// A heading at the end would introduce nothing.
	for last := doc.LastChild(); last != nil && last.Kind() == ast.KindHeading && doc.ChildCount() > 1; last = doc.LastChild() {
		doc.RemoveChild(doc, last)
	}
	out.Left = out.Words - words(doc, b)
	if out.Left <= 0 {
		return out
	}
	if c.tail != nil && c.tail.Parent() != nil {
		trimEnd(c.tail, b)
		c.tail.AppendChild(c.tail, ast.NewString([]byte("…")))
	}
	var short bytes.Buffer
	if err := answerMD.Renderer().Render(&short, b, doc); err != nil {
		out.Left = 0
		return out
	}
	out.Short = template.HTML(short.String()) //nolint:gosec // see answerMD
	out.Cut = true
	return out
}

// words counts the words in one node's text: fields split on Unicode space,
// plus one word per Han, Hiragana or Katakana rune, as word processors do.
// Words in code blocks and tables count too: a reader reads them.
func words(n ast.Node, src []byte) int {
	c := counter{limit: -1}
	c.node(n, src)
	return c.n
}

// counter counts words across the text nodes of a tree, carrying whether
// it is inside a word from one node to the next, so **bold**ed is one word.
type counter struct {
	n     int
	in    bool
	limit int // stop before word limit+1; -1 never stops
}

// text counts the words in b. It returns the offset where word limit+1
// starts, without counting it, or -1 when b stays within the limit.
func (c *counter) text(b []byte) int {
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		switch {
		case unicode.IsSpace(r):
			c.in = false
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana):
			if c.n == c.limit {
				return i
			}
			c.n++
			c.in = false
		case !c.in:
			if c.n == c.limit {
				return i
			}
			c.n++
			c.in = true
		}
		i += size
	}
	return -1
}

// node counts n and everything under it.
func (c *counter) node(n ast.Node, src []byte) {
	switch n := n.(type) {
	case *ast.Text:
		c.text(n.Segment.Value(src))
		if n.SoftLineBreak() || n.HardLineBreak() {
			c.in = false
		}
		return
	case *ast.String, *ast.AutoLink, *ast.RawHTML:
		for _, v := range pieces(n, src) {
			c.text(v)
		}
		return
	}
	block := n.Type() == ast.TypeBlock
	if block {
		c.in = false
		if n.IsRaw() { // code and HTML blocks keep their text as lines
			lines := n.Lines()
			for i := range lines.Len() {
				line := lines.At(i)
				c.text(line.Value(src))
			}
			if h, ok := n.(*ast.HTMLBlock); ok && h.HasClosure() {
				c.text(h.ClosureLine.Value(src))
			}
		}
	}
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		c.node(ch, src)
	}
	if block {
		c.in = false
	}
}

// cutter keeps the blocks of a tree up to about limit words and removes
// the rest.
type cutter struct {
	counter
	src []byte
	// done is set once the limit is reached: every block after is removed.
	done bool
	// kept is set once any block is kept, so a first block that is too
	// long alone is kept whole rather than leaving the start empty.
	kept bool
	// tail is the block cut inside, which ends with an ellipsis.
	tail ast.Node
}

// blocks walks the blocks under parent in order. Lists and blockquotes
// are entered, so a list ends early but stays a list; paragraphs and
// headings are cut inside; code blocks, tables, breaks and HTML blocks
// are kept or dropped whole.
func (c *cutter) blocks(parent ast.Node) {
	for b := parent.FirstChild(); b != nil; {
		next := b.NextSibling()
		if c.done {
			parent.RemoveChild(parent, b)
			b = next
			continue
		}
		c.in = false
		switch b.Kind() {
		case ast.KindList, ast.KindListItem, ast.KindBlockquote:
			had := b.HasChildren()
			c.blocks(b)
			if had && !b.HasChildren() {
				parent.RemoveChild(parent, b)
			}
		case ast.KindParagraph, ast.KindTextBlock, ast.KindHeading:
			if c.leaves(b) {
				c.done = true
				if b.HasChildren() {
					c.tail = b
				} else {
					parent.RemoveChild(parent, b)
				}
			}
			c.kept = c.kept || b.Parent() != nil
		default:
			w := words(b, c.src)
			switch {
			case c.n+w <= c.limit:
				c.n += w
			case c.kept:
				parent.RemoveChild(parent, b)
				c.done = true
			default:
				c.n += w
				c.done = true
			}
			c.kept = c.kept || b.Parent() != nil
		}
		b = next
	}
}

// leaves counts the inline nodes under n and cuts at the word past the
// limit: the text node it starts in is shortened, and everything after
// it is removed, at every level up to n. It reports whether it cut.
func (c *cutter) leaves(n ast.Node) bool {
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		cut := false
		switch t := ch.(type) {
		case *ast.Text:
			v := t.Segment.Value(c.src)
			if i := c.text(v); i >= 0 {
				t.Segment = t.Segment.WithStop(t.Segment.Start + len(bytes.TrimRightFunc(v[:i], unicode.IsSpace)))
				t.SetSoftLineBreak(false)
				t.SetHardLineBreak(false)
				cut = true
			} else if t.SoftLineBreak() || t.HardLineBreak() {
				c.in = false
			}
		case *ast.String, *ast.AutoLink, *ast.RawHTML:
			// Shown as one piece, so kept or dropped whole.
			probe := c.counter
			for _, v := range pieces(ch, c.src) {
				if probe.text(v) >= 0 {
					removeAfter(n, ch)
					n.RemoveChild(n, ch)
					return true
				}
			}
			c.counter = probe
		default:
			cut = c.leaves(ch)
		}
		if cut {
			removeAfter(n, ch)
			if isEmpty(ch) {
				n.RemoveChild(n, ch)
			}
			return true
		}
	}
	return false
}

// pieces is the text an atomic inline node shows.
func pieces(n ast.Node, src []byte) [][]byte {
	switch t := n.(type) {
	case *ast.String:
		return [][]byte{t.Value}
	case *ast.AutoLink:
		return [][]byte{t.Label(src)}
	case *ast.RawHTML:
		var out [][]byte
		for i := range t.Segments.Len() {
			seg := t.Segments.At(i)
			out = append(out, seg.Value(src))
		}
		return out
	}
	return nil
}

// removeAfter removes the siblings after ch.
func removeAfter(parent, ch ast.Node) {
	for s := ch.NextSibling(); s != nil; {
		next := s.NextSibling()
		parent.RemoveChild(parent, s)
		s = next
	}
}

// isEmpty reports whether an inline node shows nothing: a text node cut
// to nothing, or a container left without children.
func isEmpty(n ast.Node) bool {
	if t, ok := n.(*ast.Text); ok {
		return t.Segment.Len() == 0
	}
	return !n.HasChildren()
}

// trimEnd removes the space at the end of a cut block, so the ellipsis
// follows the last word.
func trimEnd(n ast.Node, src []byte) {
	for {
		last := n.LastChild()
		if last == nil {
			return
		}
		t, ok := last.(*ast.Text)
		if !ok {
			if last.HasChildren() {
				n = last
				continue
			}
			return
		}
		v := t.Segment.Value(src)
		t.Segment = t.Segment.WithStop(t.Segment.Start + len(bytes.TrimRightFunc(v, unicode.IsSpace)))
		t.SetSoftLineBreak(false)
		t.SetHardLineBreak(false)
		if t.Segment.Len() > 0 {
			return
		}
		n.RemoveChild(n, t)
	}
}
