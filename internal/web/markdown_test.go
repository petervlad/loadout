package web

import (
	"net/url"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/wiki"
)

func TestMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    []string
		notWant []string
	}{
		{"bold", "**Answer:** yes", []string{"<strong>Answer:</strong> yes"}, nil},
		{"list", "1. one\n2. two", []string{"<ol>", "<li>one</li>"}, nil},
		{"table", "| a | b |\n|---|---|\n| 1 | 2 |", []string{"<table>", "<td>1</td>"}, nil},
		{"code", "```\n<b>x</b>\n```", []string{"<pre><code>&lt;b&gt;x&lt;/b&gt;"}, []string{"<b>"}},
		{"raw html escaped", "hi <script>alert(1)</script>\n\n<div onclick=x>y</div>", []string{"hi &lt;script&gt;alert(1)&lt;/script&gt;", "&lt;div onclick=x&gt;y&lt;/div&gt;"}, []string{"<script", "<div"}},
		{"line breaks kept", "**Request:** access\n**Why:** builds", []string{"access<br>\n<strong>Why:</strong>"}, nil},
		{"placeholder kept", "Requester: <your name>", []string{"Requester: &lt;your name&gt;"}, nil},
		{"javascript link", "[x](javascript:alert(1))", []string{`<a href="">x</a>`}, []string{"javascript:"}},
		{"plain link kept", "[docs](https://loadout.lat/docs)", []string{`<a href="https://loadout.lat/docs">docs</a>`}, nil},
		{"image becomes alt", "see ![a chart](https://evil.example/p.png)", []string{"see a chart"}, []string{"<img", "evil.example"}},
		{"image in link", "[![logo](/x.png)](https://a.example)", []string{`<a href="https://a.example">logo</a>`}, []string{"<img"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(markdown(tt.src))
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, want it to contain %q", tt.src, got, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, must not contain %q", tt.src, got, w)
				}
			}
		})
	}
}

// TestDocument: package documents keep every rule of answers but hard
// wraps, and their links to the package's files lead to the registry.
func TestDocument(t *testing.T) {
	links := docLinks{dir: ".", files: map[string]bool{"a.md": true, "b/c.md": true}, to: func(p string) string { return "/f/" + p }}
	tests := []struct {
		name    string
		src     string
		want    []string
		notWant []string
	}{
		{"no hard wraps", "one\ntwo", []string{"one\ntwo"}, []string{"<br>"}},
		{"raw html escaped", "hi <script>alert(1)</script>\n\n<div onclick=x>y</div>", []string{"hi &lt;script&gt;", "&lt;div onclick=x&gt;"}, []string{"<script", "<div"}},
		{"relative image is alt text", "![arch](b/arch.svg)", []string{"<p>arch</p>"}, []string{"<img", "arch.svg"}},
		{"remote image is alt text", "![x](https://evil.example/x.png) ![d](data:image/svg+xml,abc)", []string{"x d"}, []string{"<img", "evil.example", "data:"}},
		{"package link", "[a](a.md) [c](b/c.md#s)", []string{`<a href="/f/a.md">a</a>`, `<a href="/f/b/c.md">c</a>`}, nil},
		{"missing file kept", "[m](missing.md)", []string{`<a href="missing.md">m</a>`}, nil},
		{"leaving the package kept", "[o](../a.md)", []string{`<a href="../a.md">o</a>`}, nil},
		{"javascript link", "[x](javascript:alert(1))", []string{`<a href="">x</a>`}, []string{"javascript:"}},
		{"code fence escaped", "```mermaid\n</code><script>x</script>\n```", []string{"&lt;/code&gt;&lt;script&gt;"}, []string{"<script>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := docHTML(tt.src, links, nil)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, want it to contain %q", tt.src, got, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, must not contain %q", tt.src, got, w)
				}
			}
		})
	}
}

const evil = "<script>alert(1)</script>"

// docHTML is the page document renders, without its note.
func docHTML(src string, links docLinks, frames *diagramFrames) string {
	page, _ := document(src, links, frames)
	return string(page)
}

// fence is a fenced block of lang holding body.
func fence(lang, body string) string { return "```" + lang + "\n" + body + "\n```\n\n" }

// TestDiagramFenceKinds: draw.io fences are drawn beside mermaid ones,
// each kind numbered on its own, at most maxFences frames in all.
func TestDiagramFenceKinds(t *testing.T) {
	compressed := `<mxfile><diagram id="a">7VhNj5swEP01HLcCzEdyTLLp9lBpq+bQ3l0iY9WAGTkh</diagram></mxfile>`
	tests := []struct {
		name    string
		src     string
		want    []string
		notWant []string
	}{
		{"draw.io fence", fence("drawio", plainModel), []string{
			`<figure class="diagram diagram-canvas" data-frame="/d/doc.md?drawio=1" data-title="Diagram 1 in doc.md">`,
			`<pre class="diagram-src"><code>&lt;mxGraphModel&gt;`,
		}, []string{"language-drawio"}},
		{"draw.io spelled with a dot", fence("Draw.io", "<mxfile>"+plainModel+"</mxfile>"), []string{`data-frame="/d/doc.md?drawio=1"`}, nil},
		{"compressed stays code", fence("drawio", compressed), []string{`<code class="language-drawio">&lt;mxfile&gt;`}, []string{"<figure"}},
		{"not draw.io XML stays code", fence("drawio", "graph TD"), []string{`<code class="language-drawio">graph TD`}, []string{"<figure"}},
		{"over-size stays code", fence("drawio", "<mxfile>"+strings.Repeat("<mxGraphModel/>", maxPreview/15)+"</mxfile>"), []string{`<code class="language-drawio">`}, []string{"<figure"}},
		{"mermaid numbering unchanged", fence("drawio", plainModel) + fence("mermaid", "graph TD"), []string{
			`data-frame="/d/doc.md?drawio=1" data-title="Diagram 1 in doc.md"`,
			`<figure class="diagram" data-frame="/d/doc.md?block=1" data-title="Diagram 2 in doc.md">`,
		}, nil},
		{"xss in a mermaid fence", fence("mermaid", "graph TD\n</code>"+evil), []string{"&lt;/code&gt;&lt;script&gt;"}, []string{"<script", "</code>" + evil}},
		{"xss in a draw.io fence", fence("drawio", `<mxGraphModel><root><mxCell value="`+evil+`"/></root></mxGraphModel></code>`+evil), []string{"&lt;script&gt;alert(1)"}, []string{"<script"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := docHTML(tt.src, docLinks{}, &diagramFrames{route: "/d/doc.md", name: "doc.md"})
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, want it to contain %q", tt.src, got, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, must not contain %q", tt.src, got, w)
				}
			}
		})
	}
}

// 21 diagram fences of both kinds draw 20.
func TestDiagramFencesShareTheLimit(t *testing.T) {
	var src strings.Builder
	for i := range maxFences + 1 {
		if i%2 == 0 {
			src.WriteString(fence("mermaid", "graph TD"))
		} else {
			src.WriteString(fence("drawio", plainModel))
		}
	}
	frames := &diagramFrames{route: "/d", name: "doc.md"}
	got := docHTML(src.String(), docLinks{}, frames)
	if n := strings.Count(got, "<figure"); n != maxFences {
		t.Errorf("%d figures, want %d", n, maxFences)
	}
	if frames.total != maxFences+1 || frames.note() == "" {
		t.Errorf("total = %d, note %q; want %d and a note", frames.total, frames.note(), maxFences+1)
	}
	if !strings.Contains(got, `?drawio=10"`) || strings.Contains(got, "?block=11") {
		t.Errorf("want draw.io fences up to 10 and mermaid up to 10:\n%s", got)
	}
}

// The frame route finds the K-th fence of the kind its query names, and
// only one it would draw.
func TestFenceFrame(t *testing.T) {
	data := []byte(fence("drawio", plainModel) + fence("mermaid", "graph TD") + fence("drawio", "<diagram>abc</diagram>"))
	tests := []struct {
		query string
		want  frameSource
		ok    bool
	}{
		{"block=1", frameSource{Kind: "mermaid", Source: "graph TD\n"}, true},
		{"drawio=1", frameSource{Kind: "drawio", Source: plainModel + "\n"}, true},
		{"drawio=2", frameSource{}, false}, // compressed
		{"drawio=3", frameSource{}, false},
		{"block=2", frameSource{}, false},
		{"block=0", frameSource{}, false},
		{"block=21", frameSource{}, false},
		{"drawio=x", frameSource{}, false},
		{"", frameSource{}, false},
	}
	for _, tt := range tests {
		q, err := url.ParseQuery(tt.query)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := fenceFrame(data, q)
		if ok != tt.ok || got != tt.want {
			t.Errorf("fenceFrame(%q) = %+v, %t; want %+v, %t", tt.query, got, ok, tt.want, tt.ok)
		}
	}
}

func TestWikiDocument(t *testing.T) {
	mdx := "import { Tabs } from './tabs'\nexport const meta = {}\n\n# Setup\n\n<Tabs>\n  <Tab label=\"Mac\">brew</Tab>\n</Tabs>\n\nRun it <Badge /> now {meta}.\n"
	tests := []struct {
		name, src string
		format    wiki.Format
		draw      string
		want      []string
		notWant   []string
		note      string
	}{
		{"mdx imports and components are code", mdx, "mdx", "/w", []string{
			"<pre><code>import { Tabs } from './tabs'\nexport const meta = {}</code></pre>",
			"<pre><code>&lt;Tabs&gt;\n  &lt;Tab label=&quot;Mac&quot;&gt;brew&lt;/Tab&gt;\n&lt;/Tabs&gt;\n</code></pre>",
			"<p>Run it &lt;Badge /&gt; now {meta}.</p>",
			`<h1 id="setup">Setup</h1>`,
		}, []string{"<p>import", "<p>&lt;Tabs"}, ""},
		{"the same as markdown", mdx, "md", "/w", []string{"<p>import { Tabs }", "<p>&lt;Tabs&gt;", `<h1 id="setup">Setup</h1>`}, []string{"<pre>"}, ""},
		{"front matter is code", "---\ntitle: Setup\n---\n## Hi\n", "md", "/w", []string{"<pre><code class=\"language-yaml\">title: Setup</code></pre>", `<summary><h2 id="hi">Hi</h2>`}, []string{"<hr", "<h2>title"}, ""},
		{"front matter in mdx", "---\ntitle: " + evil + "\n---\nimport x from 'y'\n", "mdx", "/w", []string{"<code class=\"language-yaml\">title: &lt;script&gt;", "<pre><code>import x"}, []string{"<script"}, ""},
		{"unclosed front matter is markdown", "---\ntitle: x\n", "md", "/w", []string{"<hr"}, nil, ""},
		{"diagrams drawn", fence("mermaid", "graph TD") + fence("drawio", plainModel), "md", "/w/r/3/draw", []string{
			`data-frame="/w/r/3/draw?block=1" data-title="Diagram 1 in the wiki"`, `data-frame="/w/r/3/draw?drawio=1"`,
		}, nil, ""},
		{"preview draws nothing", fence("mermaid", "graph TD"), "md", "", []string{`<code class="language-mermaid">`}, []string{"<figure"}, "Diagrams are drawn once you save."},
		{
			"too many diagrams", strings.Repeat(fence("mermaid", "graph TD"), maxFences+1), "md", "/w", nil, nil,
			"This document has 21 diagrams. The first 20 are drawn; the others show as source.",
		},
		{"xss in a jsx block", "<Callout>\n" + evil + "\n</Callout>\n", "mdx", "", []string{"&lt;script&gt;alert(1)&lt;/script&gt;"}, []string{"<script", "<Callout"}, ""},
		{"xss in an import line", "import x from '" + evil + "'\n", "mdx", "", []string{"<pre><code>import x from '&lt;script&gt;"}, []string{"<script"}, ""},
		{"xss in a fence info string", "```" + evil + "\nx\n```\n\n```x\"onmouseover=\"alert(1)\ny\n```\n", "mdx", "", []string{"language-&lt;script&gt;", "language-x&quot;onmouseover"}, []string{"<script", `x"onmouseover`}, ""},
		{"xss in a mermaid fence", fence("mermaid", "graph TD\n</code></pre>"+evil), "md", "/w", []string{"&lt;/code&gt;&lt;/pre&gt;&lt;script&gt;"}, []string{"<script"}, ""},
		{"xss in a draw.io fence", fence("drawio", "<mxfile>"+plainModel+evil+"</mxfile>"), "mdx", "/w", []string{`data-frame="/w?drawio=1"`, "&lt;script&gt;"}, []string{"<script"}, ""},
		{"javascript link", "[x](javascript:alert(1)) [y](JAVASCRIPT:alert(1))", "mdx", "", []string{`<a href="">x</a>`}, []string{"javascript:", "JAVASCRIPT:"}, ""},
		{"image alt", "![" + evil + "](https://evil.example/x.png)", "mdx", "", []string{"&lt;script&gt;"}, []string{"<img", "<script", "evil.example"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, note := wikiDocument(tt.src, tt.format, tt.draw)
			got := string(page)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, want it to contain %q", tt.src, got, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, must not contain %q", tt.src, got, w)
				}
			}
			if note != tt.note {
				t.Errorf("note = %q, want %q", note, tt.note)
			}
		})
	}
}

// The wiki's frames number the fences of the body, after its front matter.
func TestWikiFence(t *testing.T) {
	page := "---\nnotes: |\n  ```mermaid\n  graph LR\n  ```\n---\n" + fence("mermaid", "graph TD")
	got, ok := wikiFence(page, url.Values{"block": {"1"}})
	if !ok || got.Source != "graph TD\n" {
		t.Errorf("wikiFence(block=1) = %+v, %t; want graph TD", got, ok)
	}
	if _, ok := wikiFence(page, url.Values{"block": {"2"}}); ok {
		t.Error("wikiFence(block=2) found a fence in the front matter")
	}
}

// Every ## section of a wiki page folds; what comes before the first one
// stays open, and headings get ids a link can name.
func TestWikiSections(t *testing.T) {
	const closed, open = `<details class="wiki-section">`, `<details class="wiki-section" open>`
	tests := []struct {
		name, src string
		format    wiki.Format
		draw      string
		want      []string
		notWant   []string
	}{
		{
			"no sections", "# Title\n\nIntro.\n\n### Small\n\nText.\n", "md", "/w",
			[]string{"<h1 id=\"title\">Title</h1>\n<p>Intro.</p>\n<h3 id=\"small\">Small</h3>\n<p>Text.</p>\n"},
			[]string{"<details"},
		},
		{"sections fold, intro stays", "# Title\n\nIntro.\n\n## One\n\nA.\n\n### Deeper\n\nB.\n\n## Two\n\nC.\n", "md", "/w", []string{
			"<h1 id=\"title\">Title</h1>\n<p>Intro.</p>\n" +
				closed + "\n<summary><h2 id=\"one\">One</h2>\n</summary>\n<div class=\"wiki-section-body\">\n<p>A.</p>\n<h3 id=\"deeper\">Deeper</h3>\n<p>B.</p>\n</div>\n</details>\n" +
				closed + "\n<summary><h2 id=\"two\">Two</h2>\n</summary>\n<div class=\"wiki-section-body\">\n<p>C.</p>\n</div>\n</details>\n",
		}, []string{open}},
		{"a level-1 heading ends a section", "## One\n\nA.\n\n# Part two\n\nB.\n\n## Three\n", "md", "/w", []string{
			"<p>A.</p>\n</div>\n</details>\n<h1 id=\"part-two\">Part two</h1>\n<p>B.</p>\n" + closed,
		}, nil},
		{"setext headings", "One\n---\n\nA.\n", "md", "/w", []string{closed + "\n<summary><h2 id=\"one\">One</h2>"}, nil},
		{"nested headings stay", "> ## Quoted\n\n- ## Listed\n", "md", "/w", []string{`<h2 id="quoted">Quoted</h2>`, `<h2 id="listed">Listed</h2>`}, []string{"<details"}},
		{"preview starts open", "## One\n\nA.\n", "md", "", []string{open + "\n<summary><h2 id=\"one\">One</h2>"}, []string{closed}},
		{"mdx folds too", "import x from 'y'\n\n## One\n\n<Tabs>\n</Tabs>\n", "mdx", "/w", []string{
			"<pre><code>import x from 'y'</code></pre>\n" + closed, "<div class=\"wiki-section-body\">\n<pre><code>&lt;Tabs&gt;",
		}, nil},
		{"diagram inside a section", "## Flow\n\n" + fence("mermaid", "graph TD"), "md", "/w", []string{
			"<div class=\"wiki-section-body\">\n<figure class=\"diagram\" data-frame=\"/w?block=1\"",
		}, nil},
		{"ids are plain text", "## **Deploy** to `prod` [now](https://x.example)\n", "md", "/w", []string{`<h2 id="deploy-to-prod-now">`}, nil},
		{"ids keep letters of any script", "## Über die Größe\n", "md", "/w", []string{`<h2 id="über-die-größe">`}, nil},
		{"repeated ids are numbered", "## Notes\n\n## Notes\n\n### Notes\n", "md", "/w", []string{`id="notes"`, `id="notes-1"`, `id="notes-2"`}, nil},
		{"ids of the page are left alone", "## Main\n\n## Live\n\n## Wiki push\n", "md", "/w", []string{`id="main-1"`, `id="live-1"`, `id="wiki-push-1"`}, []string{`id="main"`, `id="live"`, `id="wiki-push"`}},
		{
			"ids of the course box are left alone", "## Course\n\n## Course h\n\n## Lesson h\n\n## Lessons h\n", "md", "/w",
			[]string{`id="course-1"`, `id="course-h-1"`, `id="lesson-h-1"`, `id="lessons-h-1"`},
			[]string{`id="course"`, `id="course-h"`, `id="lesson-h"`, `id="lessons-h"`},
		},
		{"a heading without letters", "## ???\n", "md", "/w", []string{`<h2 id="section">???</h2>`}, nil},
		{"xss in a heading", "## " + evil + " \"onclick=\"x\n", "md", "/w", []string{
			`<summary><h2 id="scriptalert1script-onclickx">&lt;script&gt;alert(1)&lt;/script&gt; &quot;onclick=&quot;x</h2>`,
		}, []string{"<script", `"onclick`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, _ := wikiDocument(tt.src, tt.format, tt.draw)
			got := string(page)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("render(%q) =\n%s\nwant it to contain\n%s", tt.src, got, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("render(%q) =\n%s\nmust not contain %q", tt.src, got, w)
				}
			}
		})
	}
}

// Package documents keep their headings as they were: no ids, no folds.
func TestDocumentDoesNotFold(t *testing.T) {
	got := docHTML("## One\n\nA.\n", docLinks{}, nil)
	if got != "<h2>One</h2>\n<p>A.</p>\n" {
		t.Errorf("document = %q", got)
	}
}

// quizSrc is a quiz fence with a right and a wrong answer.
func quizSrc(question string) string {
	return fence("quiz", question+"\n\n- [ ] wrong one\n  Not this.\n- [x] right one\n  Yes, see [a](a.md).")
}

// TestQuiz: a quiz fence becomes a question whose answers fold out with
// a verdict loadout writes; anything it cannot read stays code with a
// note, and every piece keeps docMD's rules.
func TestQuiz(t *testing.T) {
	links := docLinks{dir: ".", files: map[string]bool{"a.md": true}, to: func(p string) string { return "/f/" + p }}
	nested := "````quiz\nQ\n- [x] a\n  ```quiz\n  Inner\n  - [x] b\n  - [ ] c\n  ```\n- [ ] d\n````\n"
	tests := []struct {
		name    string
		src     string
		want    []string
		notWant []string
	}{
		{"drawn", quizSrc("Which **one**?"), []string{
			`<div class="quiz" role="group" aria-labelledby="quiz_1">`,
			`<p class="quiz-label">Question</p>`,
			`<div class="quiz-q" id="quiz_1"><p>Which <strong>one</strong>?</p>`,
			`<li><details class="quiz-answer">` + "\n" + `<summary><span class="quiz-letter" aria-hidden="true">A</span><span class="quiz-text">wrong one</span></summary>`,
			`<div class="quiz-why quiz-wrong"><p><strong>Wrong.</strong> Not this.</p>`,
			`<span class="quiz-letter" aria-hidden="true">B</span><span class="quiz-text">right one</span>`,
			`<div class="quiz-why quiz-right"><p><strong>Right.</strong> Yes, see <a href="/f/a.md">a</a>.</p>`,
		}, []string{"language-quiz", "- [x]"}},
		{"any case", fence("QUIZ", "Q\n- [x] a\n- [ ] b"), []string{`id="quiz_1"`, `<p><strong>Right.</strong></p>`}, nil},
		{"numbered in order", quizSrc("One") + quizSrc("Two"), []string{`id="quiz_1"><p>One`, `id="quiz_2"><p>Two`}, nil},
		{"unreadable stays code", fence("quiz", "Q\n- [x] a\n- [ ] b\nloose"), []string{
			`<p class="quiz-error" role="note">This quiz could not be read: line 4: indent the explanation under its answer, or start a new answer with - [ ] or - [x].</p>`,
			`<pre><code class="language-quiz">Q`,
		}, []string{`class="quiz"`}},
		{"a quiz inside a quiz stays code", nested, []string{`id="quiz_1"`, `<code class="language-quiz">Inner`}, []string{`id="quiz_2"`}},
		{"xss in every piece", fence("quiz", "Q "+evil+" ![alt "+evil+"](https://evil.example/i.png)\n- [x] A "+evil+"\n  Why "+evil+" [x](javascript:alert(1))\n- [ ] b"), []string{
			"Q &lt;script&gt;alert(1)&lt;/script&gt;", "A &lt;script&gt;", "Why &lt;script&gt;", `<a href="">x</a>`,
		}, []string{"<script", "javascript:", "<img", "evil.example"}},
		{"xss in an unreadable quiz", fence("quiz", evil+"\n- [x] only"), []string{"&lt;script&gt;"}, []string{"<script"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := docHTML(tt.src, links, nil)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("render(%q) =\n%s\nwant it to contain %q", tt.src, got, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("render(%q) =\n%s\nmust not contain %q", tt.src, got, w)
				}
			}
		})
	}
}

// TestQuizLimit: past maxQuizzes, quiz fences stay code and the note
// says so.
func TestQuizLimit(t *testing.T) {
	var src strings.Builder
	for range maxQuizzes + 1 {
		src.WriteString(quizSrc("Q"))
	}
	got, note := document(src.String(), docLinks{}, nil)
	if n := strings.Count(string(got), `<div class="quiz" `); n != maxQuizzes {
		t.Errorf("drawn %d quizzes, want %d", n, maxQuizzes)
	}
	if !strings.Contains(string(got), `<code class="language-quiz">`) {
		t.Error("the quiz past the limit is not code")
	}
	if want := "This document has 51 quizzes. The first 50 are shown as questions; the others show as source."; note != want {
		t.Errorf("note = %q, want %q", note, want)
	}
}

// TestQuizInWiki: a wiki page draws quizzes in its sections, and no
// heading takes a quiz's id.
func TestQuizInWiki(t *testing.T) {
	page, note := wikiDocument("# quiz_1\n\n## Quiz 1\n\n"+quizSrc("Q"), wiki.Markdown, "/t/lime/wiki/r/1/draw")
	got := string(page)
	if note != "" {
		t.Errorf("note = %q", note)
	}
	if strings.Count(got, `id="quiz_1"`) != 1 || !strings.Contains(got, `id="quiz1"`) || !strings.Contains(got, `id="quiz-1"`) {
		t.Errorf("ids:\n%s", got)
	}
	if !strings.Contains(got, `<div class="wiki-section-body">`+"\n"+`<div class="quiz"`) {
		t.Errorf("the quiz is not in its section:\n%s", got)
	}
}

// TestLessonHeadings: a lesson's headings sit under the box's: its first
// # heading goes when the box already shows it as the title, and the
// others move down two levels, at most to h6.
func TestLessonHeadings(t *testing.T) {
	src := "# Setup\n\nText.\n\n# Second top\n\n## Part\n\n##### Deep\n"
	tests := []struct {
		title   string
		want    []string
		notWant []string
	}{
		{"Setup", []string{"<h3>Second top</h3>", "<h4>Part</h4>", "<h6>Deep</h6>"}, []string{"Setup</h", "<h1>", "<h2>"}},
		{"Another title", []string{"<h3>Setup</h3>", "<h3>Second top</h3>", "<h4>Part</h4>"}, []string{"<h1>"}},
	}
	for _, tt := range tests {
		page, _ := docParse{quizzes: &quizzes{}, lesson: tt.title}.render(src)
		got := string(page)
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("title %q: %s\nwant %q", tt.title, got, w)
			}
		}
		for _, w := range tt.notWant {
			if strings.Contains(got, w) {
				t.Errorf("title %q: %s\nmust not contain %q", tt.title, got, w)
			}
		}
	}
}
