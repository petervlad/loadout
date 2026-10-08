package web

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/yuin/goldmark/text"
)

// seqWords is n words, w1 to wn, separated by spaces.
func seqWords(from, to int) string {
	var w []string
	for i := from; i <= to; i++ {
		w = append(w, fmt.Sprintf("w%d", i))
	}
	return strings.Join(w, " ")
}

func TestRenderAnswer(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		cut     bool
		words   int
		left    int
		want    []string // in Short
		notWant []string // not in Short
	}{
		{"under the limit", "one two three", false, 3, 0, []string{"<p>one two three</p>"}, nil},
		{"exactly the limit", seqWords(1, 10), false, 10, 0, []string{"w10</p>"}, nil},
		{"limit and slack", seqWords(1, 12), false, 12, 0, []string{"w12</p>"}, nil},
		{"one past the slack", seqWords(1, 13), true, 13, 3, []string{"<p>w1 w2", "w10…</p>"}, []string{"w11"}},
		{"cut mid-paragraph at a word", seqWords(1, 8) + " " + seqWords(9, 30), true, 30, 20, []string{"w10…</p>"}, []string{"w11", "w10 …"}},
		{"cut inside bold", seqWords(1, 8) + " **w9 w10 w11** " + seqWords(12, 20), true, 20, 10, []string{"<strong>w9 w10</strong>…</p>"}, []string{"w11", "w12"}},
		{"cut at the start of bold", seqWords(1, 10) + " **w11 w12** " + seqWords(13, 20), true, 20, 10, []string{"w10…</p>"}, []string{"<strong>", "w11"}},
		{
			"list cut between items", "- w1 w2 w3 w4 w5\n- w6 w7 w8 w9 w10\n- w11 w12 w13 w14 w15", true, 15, 5,
			[]string{"<ul>", "<li>w6 w7 w8 w9 w10</li>", "</ul>"},
			[]string{"w11", "…"},
		},
		{
			"list cut inside an item", "1. w1 w2 w3 w4 w5 w6 w7\n2. w8 w9 w10 w11 w12 w13 w14", true, 14, 4,
			[]string{"<ol>", "<li>w8 w9 w10…</li>", "</ol>"},
			[]string{"w11"},
		},
		{
			"table dropped whole", seqWords(1, 8) + "\n\n| a | b |\n|---|---|\n| c d | e f |\n\n" + seqWords(15, 20), true, 20, 12,
			[]string{"<p>w1 w2"},
			[]string{"<table", "<td", "w15"},
		},
		{
			"code block dropped whole", seqWords(1, 8) + "\n\n```go\nx := y + z\n```\n\n" + seqWords(13, 20), true, 21, 13,
			[]string{"w8</p>"},
			[]string{"<pre", "<code", "w13"},
		},
		{"open fence at the end", seqWords(1, 9) + "\n\n```\nc1 c2 c3 c4 c5", true, 14, 5, []string{"w9</p>"}, []string{"<pre", "c1"}},
		{
			"long first code block kept", "```\n" + seqWords(1, 20) + "\n```\n\n" + seqWords(21, 25), true, 25, 5,
			[]string{"<pre><code>w1", "w20\n</code></pre>"},
			[]string{"w21"},
		},
		{
			"trailing heading dropped", seqWords(1, 8) + "\n\n## h1 h2\n\n" + seqWords(11, 20), true, 20, 12,
			[]string{"w8</p>"},
			[]string{"<h2", "h1", "w11"},
		},
		{"blockquote cut inside", "> " + seqWords(1, 20), true, 20, 10, []string{"<blockquote>", "w10…</p>", "</blockquote>"}, []string{"w11"}},
		{
			"raw html escaped in both", "hi <script>alert(1)</script> [x](javascript:alert(1)) " + seqWords(1, 20), true, 23, 13,
			[]string{"hi &lt;script&gt;alert(1)&lt;/script&gt;", `<a href="">x</a>`},
			[]string{"<script", "javascript:"},
		},
		{
			"raw html crossing the limit dropped whole", seqWords(1, 10) + " <b onclick=x> " + seqWords(12, 20), true, 21, 11,
			[]string{"w10…</p>"},
			[]string{"onclick", "<b"},
		},
		{"chinese counted per character", "这是一个很长的中文句子用于测试", true, 15, 5, []string{"<p>这是一个很长的中文句…</p>"}, []string{"子"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderAnswer(tt.src, 10)
			if got.Cut != tt.cut || got.Words != tt.words || got.Left != tt.left {
				t.Errorf("renderAnswer(%q) = cut %v, %d words, %d left; want %v, %d, %d\nshort: %s", tt.src, got.Cut, got.Words, got.Left, tt.cut, tt.words, tt.left, got.Short)
			}
			if got.Full != markdown(tt.src) {
				t.Errorf("Full = %q, want what markdown renders, %q", got.Full, markdown(tt.src))
			}
			if !tt.cut && got.Short != got.Full {
				t.Errorf("uncut Short = %q, want Full %q", got.Short, got.Full)
			}
			short := string(got.Short)
			for _, w := range tt.want {
				if !strings.Contains(short, w) {
					t.Errorf("Short = %q, want it to contain %q", short, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(short, w) {
					t.Errorf("Short = %q, must not contain %q", short, w)
				}
			}
			if err := balanced(short); err != nil {
				t.Errorf("Short = %q: %v", short, err)
			}
		})
	}
}

func TestWords(t *testing.T) {
	tests := []struct {
		src  string
		want int
	}{
		{"", 0},
		{"**a**", 1},
		{"**bold**ed text", 2},
		{"| a | b |\n|---|---|", 2},
		{"# Title\n\n---\n\n* one\n* two", 3},
		{"café naïve résumé", 3},
		{"中文字", 3},
		{"see 中文 here", 4},
		{"I 🙂 you", 3},
		{"a🙂b", 1},
		{"`x := 1` and <https://loadout.lat>", 5},
		{"```\nfunc main() {}\n```", 3},
		{"line one\nline two", 4},
		{"see ![a chart](https://evil.example/p.png)", 3},
	}
	for _, tt := range tests {
		src := []byte(tt.src)
		doc := answerMD.Parser().Parse(text.NewReader(src))
		if got := words(doc, src); got != tt.want {
			t.Errorf("words(%q) = %d, want %d", tt.src, got, tt.want)
		}
	}
}

func TestRenderAnswerNeverSplitsARune(t *testing.T) {
	src := strings.Repeat("🙂 ", 30)
	got := renderAnswer(src, 10)
	if !got.Cut || !strings.Contains(string(got.Short), strings.TrimSpace(strings.Repeat("🙂 ", 10))+"…") {
		t.Errorf("Short = %q, want ten emoji and an ellipsis", got.Short)
	}
	if !strings.ContainsRune(string(got.Short), '🙂') || strings.ContainsRune(string(got.Short), '�') {
		t.Errorf("Short = %q splits a rune", got.Short)
	}
}

var tagRE = regexp.MustCompile(`<(/?)([a-z][a-z0-9]*)[^>]*>`)

// balanced checks that every tag the renderer opens is closed, in order.
func balanced(h string) error {
	void := map[string]bool{"br": true, "hr": true, "input": true, "img": true}
	var stack []string
	for _, m := range tagRE.FindAllStringSubmatch(h, -1) {
		name := m[2]
		switch {
		case void[name]:
		case m[1] == "":
			stack = append(stack, name)
		case len(stack) == 0 || stack[len(stack)-1] != name:
			return fmt.Errorf("</%s> closes %v", name, stack)
		default:
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		return fmt.Errorf("left open: %v", stack)
	}
	return nil
}
