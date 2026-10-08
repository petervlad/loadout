package web

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestMermaidFences(t *testing.T) {
	var src strings.Builder
	src.WriteString("# Flows\n\n```go\nfmt.Println(1)\n```\n\n```mermaid\ngraph TD\n</code><script>alert(1)</script>\n```\n\n")
	for i := 2; i <= maxFences+1; i++ {
		src.WriteString("```Mermaid\ngraph LR; N" + strconv.Itoa(i) + "-->M\n```\n\n")
	}
	frames := &diagramFrames{route: "/d/doc.md", name: "doc.md"}
	got := docHTML(src.String(), docLinks{}, frames)

	want := []string{
		`<figure class="diagram" data-frame="/d/doc.md?block=1" data-title="Diagram 1 in doc.md">`,
		"<pre class=\"diagram-src\"><code>graph TD\n&lt;/code&gt;&lt;script&gt;alert(1)&lt;/script&gt;\n</code></pre>",
		`data-frame="/d/doc.md?block=20"`,
		`<code class="language-go">fmt.Println(1)`,            // other fences as before
		`<code class="language-Mermaid">graph LR; N21--&gt;M`, // the 21st stays code
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("document() missing %q\n%s", w, got)
		}
	}
	for _, w := range []string{"<script>", "block=21"} {
		if strings.Contains(got, w) {
			t.Errorf("document() has %q", w)
		}
	}
	if frames.total != maxFences+1 {
		t.Errorf("total = %d, want %d", frames.total, maxFences+1)
	}

	// The frame numbers them the same way.
	srcs := fenceSources([]byte(src.String()), "block")
	if len(srcs) != maxFences+1 || !strings.Contains(srcs[0], "</code><script>") || srcs[1] != "graph LR; N2-->M\n" {
		t.Errorf("fenceSources() = %d sources, first %q, second %q", len(srcs), srcs[0], srcs[1])
	}

	// Without frames (the built-in skills' pages), fences stay code.
	if got := docHTML("```mermaid\ngraph TD\n```\n", docLinks{}, nil); !strings.Contains(got, `<code class="language-mermaid">`) {
		t.Errorf("document() without frames = %q, want a code block", got)
	}
}

func TestMarkdownWithManyDiagramsHasANote(t *testing.T) {
	src := strings.Repeat("```mermaid\ngraph TD; A-->B\n```\n\n", maxFences+3)
	b := markdownPreview(fileRef{Path: "a.md", Data: []byte(src), Base: "/b"})
	if want := "This document has 23 diagrams. The first 20 are drawn; the others show as source."; b.Note != want {
		t.Errorf("note = %q, want %q", b.Note, want)
	}
}

func TestLargeMermaidFileIsText(t *testing.T) {
	data := "graph TD\n" + strings.Repeat("A-->B\n", maxMermaid/6+1)
	b := previewOf(fileRef{Path: "big.mmd", Data: []byte(data)})
	if b.Diagram != nil || !strings.HasPrefix(b.Note, "This diagram is too large to draw here") {
		t.Errorf("large .mmd = %+v, want text with a note", b.Note)
	}
	if _, ok := frameOf(fileRef{Path: "big.mmd", Data: []byte(data)}, nil); ok {
		t.Error("frameOf(large .mmd) = true, want no frame")
	}
}

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"gzip": true, "gzip, deflate, br": true, "br;q=1.0, GZIP;q=0.5": true, "": false, "identity": false, "gzip;q=0": false, "br": false,
	} {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Accept-Encoding", header)
		if got := acceptsGzip(r); got != want {
			t.Errorf("acceptsGzip(%q) = %t, want %t", header, got, want)
		}
	}
}
