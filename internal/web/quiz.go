package web

import (
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/petervlad/loadout/internal/course"
)

// maxQuizzes is how many quiz fences of one document are drawn as
// quizzes; the others stay code.
const maxQuizzes = 50

// quizzesKey holds, in a parse, the *quizzes of the document. A parse
// without it leaves quiz fences as code, which is how a quiz fence inside
// a quiz's own text stays code.
var quizzesKey = parser.NewContextKey()

// quizzes counts a document's quiz fences as quizBlocks draws them.
type quizzes struct {
	// total is how many quiz fences the document has; drawn how many
	// became quizzes, which numbers their ids.
	total, drawn int
	// prefix starts every id, "quiz_" when empty. Ids hold a "_", which
	// heading ids never do, so a quiz and a heading never share one; a
	// page with two parses gives them different prefixes.
	prefix string
}

// id is the id of the n-th quiz drawn.
func (q *quizzes) id(n int) string {
	if q.prefix == "" {
		return "quiz_" + strconv.Itoa(n)
	}
	return q.prefix + strconv.Itoa(n)
}

// note says how many quizzes the document leaves as source, if any.
func (q *quizzes) note() string {
	if q.total <= maxQuizzes {
		return ""
	}
	return "This document has " + strconv.Itoa(q.total) + " quizzes. The first " + strconv.Itoa(maxQuizzes) + " are shown as questions; the others show as source."
}

var (
	kindQuiz      = ast.NewNodeKind("Quiz")
	kindQuizError = ast.NewNodeKind("QuizError")
)

// quizBlock is a quiz fence that parsed: its pieces, already rendered by
// docMD's rules, and its id on the page.
type quizBlock struct {
	ast.BaseBlock
	id       string
	question string
	answers  []quizAnswer
}

// quizAnswer is one rendered answer: its text (inline HTML), whether it
// is right, and why (block HTML, may be empty).
type quizAnswer struct {
	text, why string
	right     bool
}

func (n *quizBlock) Kind() ast.NodeKind            { return kindQuiz }
func (n *quizBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// quizError is the note above a quiz fence that did not parse; the fence
// stays code below it.
type quizError struct {
	ast.BaseBlock
	msg string
}

func (n *quizError) Kind() ast.NodeKind            { return kindQuizError }
func (n *quizError) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// quizBlocks turns the first maxQuizzes quiz fences of a document into
// quizBlocks. Each piece of a quiz is Markdown of its own, rendered with
// the document's links but without quizzes or diagram frames: a diagram
// belongs in the lesson's text, not in an answer.
type quizBlocks struct{}

func (quizBlocks) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	qs, ok := pc.Get(quizzesKey).(*quizzes)
	if !ok {
		return
	}
	src := reader.Source()
	var fences []*ast.FencedCodeBlock
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if f, ok := n.(*ast.FencedCodeBlock); ok && entering && strings.EqualFold(string(f.Language(src)), "quiz") {
			fences = append(fences, f)
		}
		return ast.WalkContinue, nil
	})
	links, hasLinks := pc.Get(linksKey).(docLinks)
	render := func(md string) string {
		piece := parser.NewContext()
		if hasLinks {
			piece.Set(linksKey, links)
		}
		return string(convert(docMD, md, piece))
	}
	for _, f := range fences {
		qs.total++
		if qs.total > maxQuizzes {
			continue
		}
		q, err := course.ParseQuiz(string(fenceText(f, src)))
		if err != nil {
			f.Parent().InsertBefore(f.Parent(), f, &quizError{msg: "This quiz could not be read: " + strings.TrimSuffix(err.Error(), ".") + "."})
			continue
		}
		qs.drawn++
		n := &quizBlock{id: qs.id(qs.drawn), question: render(q.Question)}
		for _, a := range q.Answers {
			n.answers = append(n.answers, quizAnswer{text: inline(render(a.Text)), why: render(a.Why), right: a.Right})
		}
		f.Parent().ReplaceChild(f.Parent(), f, n)
	}
}

// inline unwraps the one paragraph a line of Markdown renders to, for a
// summary.
func inline(html string) string {
	body, ok := strings.CutPrefix(strings.TrimSpace(html), "<p>")
	if body, ok2 := strings.CutSuffix(body, "</p>"); ok && ok2 && !strings.Contains(body, "<p>") {
		return body
	}
	return html
}

// quizHTML writes a quiz as a group: the question, then each answer as
// its own disclosure, so a reader can open several and compare. The
// verdict is words, written by loadout; color only repeats it. Every
// piece was rendered by docMD; only fixed tags are written around them.
type quizHTML struct{}

func (quizHTML) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindQuiz, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		q := n.(*quizBlock)
		id := q.id
		_, _ = w.WriteString(`<div class="quiz" role="group" aria-labelledby="` + id + `">` + "\n")
		_, _ = w.WriteString(`<p class="quiz-label">Question</p>` + "\n")
		_, _ = w.WriteString(`<div class="quiz-q" id="` + id + `">` + q.question + "</div>\n")
		_, _ = w.WriteString(`<ol class="quiz-answers">` + "\n")
		for i, a := range q.answers {
			verdict, class := "Wrong.", "quiz-wrong"
			if a.right {
				verdict, class = "Right.", "quiz-right"
			}
			_, _ = w.WriteString(`<li><details class="quiz-answer">` + "\n")
			_, _ = w.WriteString(`<summary><span class="quiz-letter" aria-hidden="true">` + string(rune('A'+i)) + `</span><span class="quiz-text">` + a.text + "</span></summary>\n")
			_, _ = w.WriteString(`<div class="quiz-why ` + class + `">` + withVerdict(verdict, a.why) + "</div>\n")
			_, _ = w.WriteString("</details></li>\n")
		}
		_, _ = w.WriteString("</ol>\n</div>\n")
		return ast.WalkSkipChildren, nil
	})
	reg.Register(kindQuizError, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString(`<p class="quiz-error" role="note">`)
			_, _ = w.Write(util.EscapeHTML([]byte(n.(*quizError).msg)))
			_, _ = w.WriteString("</p>\n")
		}
		return ast.WalkSkipChildren, nil
	})
}

// withVerdict puts the verdict at the start of an explanation's first
// paragraph, or alone when there is none.
func withVerdict(verdict, why string) string {
	strong := "<strong>" + verdict + "</strong>"
	if rest, ok := strings.CutPrefix(why, "<p>"); ok {
		return "<p>" + strong + " " + rest
	}
	return "<p>" + strong + "</p>\n" + why
}
