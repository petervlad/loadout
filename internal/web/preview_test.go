package web

import (
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/bundle"
)

func TestPreviewTable(t *testing.T) {
	long := strings.Repeat("# a\n\ntext\n", 600<<10/10)
	tests := []struct {
		path, data string
		want       string
		html, note bool
	}{
		{"README.md", "# Hi", "Markdown", true, false},
		{"docs/GUIDE.MD", "# Hi", "Markdown", true, false},
		{"notes.markdown", "# Hi", "Markdown", true, false},
		{"notes.txt", "hi", "Text", false, false},
		{"icon.svg", `<svg xmlns="http://www.w3.org/2000/svg"/>`, "Text", false, false},
		{"arch.drawio.svg", `<svg/>`, "Text", false, false},
		{"evil.html", "<script>alert(1)</script>", "Text", false, false},
		{"long.md", long, "Text", false, true},
		{"long.txt", long, "Text", false, true},
		{"blob.bin", "\x00\x01\x02", "No preview", false, false},
		{"bad.txt", "\xff\xfe", "No preview", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := previewOf(fileRef{Path: tc.path, Data: []byte(tc.data), Base: "/b"})
			if got.What != tc.want || (got.HTML != "") != tc.html || (got.Note != "") != tc.note {
				t.Errorf("previewOf(%s) = %q html=%t note=%q, want %q html=%t note=%t", tc.path, got.What, got.HTML != "", got.Note, tc.want, tc.html, tc.note)
			}
			if got.Download != (tc.want == "No preview") {
				t.Errorf("previewOf(%s).Download = %t", tc.path, got.Download)
			}
			if tc.note && len(got.Text) > maxPreview {
				t.Errorf("previewOf(%s) shows %d bytes, want at most %d", tc.path, len(got.Text), maxPreview)
			}
		})
	}
}

func TestReadingOrder(t *testing.T) {
	entries := []bundle.Entry{{Path: "SKILL.md"}, {Path: "a.md"}, {Path: "loadout.yaml"}, {Path: "z/b.md"}}
	got := strings.Join(readingOrder(entries, "SKILL.md"), " ")
	if want := "SKILL.md a.md z/b.md loadout.yaml"; got != want {
		t.Errorf("readingOrder() = %s, want %s", got, want)
	}
	got = strings.Join(readingOrder(entries, ""), " ")
	if want := "SKILL.md a.md z/b.md loadout.yaml"; got != want {
		t.Errorf("readingOrder() without a main file = %s, want %s", got, want)
	}
}

func TestDocLinks(t *testing.T) {
	l := docLinks{dir: "ops", files: map[string]bool{"README.md": true, "ops/deploy.md": true, "ops/db/fail.md": true}, to: func(p string) string { return "/f/" + p }}
	tests := map[string]string{
		"deploy.md":           "/f/ops/deploy.md",
		"./deploy.md":         "/f/ops/deploy.md",
		"db/fail.md#steps":    "/f/ops/db/fail.md",
		"../README.md":        "/f/README.md",
		"../ops/../README.md": "/f/README.md",
		"../../README.md":     "",
		"README.md":           "",
		"/README.md":          "",
		"#top":                "",
		"https://x/deploy.md": "",
		"javascript:x":        "",
		"db/":                 "",
		"deploy.md?x=1":       "",
		"%2e%2e/README.md":    "",
	}
	for dest, want := range tests {
		got, ok := l.resolve(dest)
		if got != want || ok != (want != "") {
			t.Errorf("resolve(%q) = %q, %t; want %q", dest, got, ok, want)
		}
	}
}
