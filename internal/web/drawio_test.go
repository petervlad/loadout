package web

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func page(name, body string) string {
	attr := ""
	if name != "" {
		attr = ` name="` + name + `"`
	}
	return `<diagram id="x"` + attr + `>` + body + `</diagram>`
}

const plainModel = `<mxGraphModel><root><mxCell id="0"/></root></mxGraphModel>`

func TestDrawioPages(t *testing.T) {
	var many strings.Builder
	many.WriteString("<mxfile>")
	for i := range 150 {
		many.WriteString(page("P"+strconv.Itoa(i+1), plainModel))
	}
	many.WriteString("</mxfile>")
	var first100 []string
	for i := range 100 {
		first100 = append(first100, "P"+strconv.Itoa(i+1))
	}
	tests := []struct {
		name  string
		data  string
		names []string
		total int
		ok    bool
	}{
		{"one page", "<mxfile>" + page("Overview", plainModel) + "</mxfile>", []string{"Overview"}, 1, true},
		{"three named pages", "<mxfile>" + page("A", plainModel) + page("B &amp; C", plainModel) + page("D", plainModel) + "</mxfile>", []string{"A", "B & C", "D"}, 3, true},
		{"pages without names", "<mxfile>" + page("", plainModel) + page("", plainModel) + "</mxfile>", []string{"Page 1", "Page 2"}, 2, true},
		{"compressed page", "<mxfile>" + page("Packed", "7VhNb9sw") + "</mxfile>", []string{"Packed"}, 1, true},
		{"bare model", plainModel, []string{"Page 1"}, 1, true},
		{"150 pages", many.String(), first100, 150, true},
		{"malformed", "<mxfile><diagram name=\"A\">", nil, 0, false},
		{"entity from a doctype", `<!DOCTYPE mxfile [<!ENTITY x "expanded">]><mxfile>` + page("&x;", plainModel) + "</mxfile>", nil, 0, false},
		{"external entity", `<!DOCTYPE mxfile [<!ENTITY x SYSTEM "file:///etc/passwd">]><mxfile>` + page("&x;", plainModel) + "</mxfile>", nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			names, total, ok := drawioPages([]byte(tc.data))
			if diff := cmp.Diff(tc.names, names); diff != "" || total != tc.total || ok != tc.ok {
				t.Errorf("drawioPages() = %d pages, ok %t, want %d, %t; names (-want +got):\n%s", total, ok, tc.total, tc.ok, diff)
			}
		})
	}
}

func TestDrawioPreview(t *testing.T) {
	data := []byte("<mxfile>" + page("Overview", plainModel) + page("Deploy", `<mxGraphModel math="1"><root><UserObject link="https://x"/></root></mxGraphModel>`) + "</mxfile>")
	f := fileRef{Path: "docs/arch.drawio", Data: data, Base: "/b", Page: 2}
	b := previewOf(f)
	if b.What != "draw.io diagram, 2 pages" || b.Diagram == nil {
		t.Fatalf("previewOf(.drawio) = %q, diagram %v", b.What, b.Diagram)
	}
	d := b.Diagram
	if d.Frame != "/b/draw/docs/arch.drawio?page=2" || d.Title != "Diagram: arch.drawio, page Deploy" || !d.Canvas || d.Source != string(data) {
		t.Errorf("diagram = %+v", d)
	}
	want := []pageTab{{"Overview", "/b/files/docs/arch.drawio?page=1", false}, {"Deploy", "/b/files/docs/arch.drawio?page=2", true}}
	if diff := cmp.Diff(want, d.Pages); diff != "" {
		t.Errorf("tabs mismatch (-want +got):\n%s", diff)
	}
	if len(d.Notes) != 2 || !strings.HasPrefix(d.Notes[0], "Links inside the diagram") || !strings.HasPrefix(d.Notes[1], "Formulas") {
		t.Errorf("notes = %q", d.Notes)
	}
	// An upper-case name is still draw.io; a page out of range shows the
	// last one.
	if b := previewOf(fileRef{Path: "A.DRAWIO", Data: data, Page: 9}); b.Diagram == nil || !b.Diagram.Pages[1].Current {
		t.Errorf("previewOf(A.DRAWIO, page 9) = %+v", b)
	}

	for q, want := range map[string]int{"": 0, "page=1": 0, "page=2": 1} {
		v, _ := url.ParseQuery(q)
		src, ok := frameOf(fileRef{Path: "a.drawio", Data: data}, v)
		if !ok || src.Kind != "drawio" || src.Page != want {
			t.Errorf("frameOf(?%s) = %+v, %t; want page %d", q, src, ok, want)
		}
	}
	for _, q := range []string{"page=0", "page=3", "page=x"} {
		v, _ := url.ParseQuery(q)
		if _, ok := frameOf(fileRef{Path: "a.drawio", Data: data}, v); ok {
			t.Errorf("frameOf(?%s) = true, want no frame", q)
		}
	}
}
