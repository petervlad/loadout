package wiki_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/wiki"
)

func TestFormatOf(t *testing.T) {
	tests := []struct {
		name string
		want wiki.Format
		err  error
	}{
		{"wiki.md", wiki.Markdown, nil},
		{"WIKI.MD", wiki.Markdown, nil},
		{"notes/team.markdown", wiki.Markdown, nil},
		{"wiki.mdx", wiki.MDX, nil},
		{"wiki.MDX", wiki.MDX, nil},
		{"wiki.txt", "", wiki.ErrFormat},
		{"wiki", "", wiki.ErrFormat},
		{"md", "", wiki.ErrFormat},
	}
	for _, tt := range tests {
		got, err := wiki.FormatOf(tt.name)
		if got != tt.want || !errors.Is(err, tt.err) {
			t.Errorf("FormatOf(%q) = %q, %v; want %q, %v", tt.name, got, err, tt.want, tt.err)
		}
	}
}

func TestFormatFile(t *testing.T) {
	for f, want := range map[wiki.Format]string{wiki.Markdown: "wiki.md", wiki.MDX: "wiki.mdx", "": "wiki.md"} {
		if got := f.File(); got != want {
			t.Errorf("Format(%q).File() = %q, want %q", f, got, want)
		}
	}
}

func TestNewDoc(t *testing.T) {
	tests := []struct {
		name   string
		format wiki.Format
		text   string
		want   string
		err    error
	}{
		{"plain", wiki.Markdown, "# Lime\n\nHello.\n", "# Lime\n\nHello.\n", nil},
		{"crlf", wiki.Markdown, "a\r\nb\r\n", "a\nb\n", nil},
		{"lone cr", wiki.MDX, "a\rb\r\r\nc", "a\nb\n\nc", nil},
		{"empty", wiki.Markdown, "", "", nil},
		{"unicode", wiki.Markdown, "Grüße, 日本, 🥝, \u200d", "Grüße, 日本, 🥝, \u200d", nil},
		{"exactly the limit", wiki.Markdown, strings.Repeat("a", wiki.MaxSize), strings.Repeat("a", wiki.MaxSize), nil},
		{"the limit after crlf", wiki.Markdown, strings.Repeat("a\r\n", wiki.MaxSize/2), strings.Repeat("a\n", wiki.MaxSize/2), nil},
		{"one byte more", wiki.Markdown, strings.Repeat("a", wiki.MaxSize+1), "", wiki.ErrTooLarge},
		{"invalid utf-8", wiki.Markdown, "a\xffb", "", wiki.ErrNotText},
		{"nul", wiki.Markdown, "a\x00b", "", wiki.ErrNotText},
		{"unknown format", "txt", "a", "", wiki.ErrFormat},
		{"no format", "", "a", "", wiki.ErrFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := wiki.NewDoc(tt.format, tt.text)
			if !errors.Is(err, tt.err) {
				t.Fatalf("NewDoc() error = %v, want %v", err, tt.err)
			}
			if err != nil {
				return
			}
			if d.Text() != tt.want || d.Size() != len(tt.want) || d.Format() != tt.format {
				t.Errorf("NewDoc() = %q (%d bytes, %s), want %q (%s)", d.Text(), d.Size(), d.Format(), tt.want, tt.format)
			}
		})
	}
}

func TestZeroDoc(t *testing.T) {
	var d wiki.Doc
	if d.Format() != wiki.Markdown || d.Text() != "" || d.Size() != 0 {
		t.Errorf("zero Doc = %s %q %d, want empty Markdown", d.Format(), d.Text(), d.Size())
	}
	empty, err := wiki.NewDoc(wiki.Markdown, "")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Equal(empty) {
		t.Error("zero Doc is not Equal to an empty Markdown page")
	}
	mdx, err := wiki.NewDoc(wiki.MDX, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Equal(mdx) {
		t.Error("an empty MDX page is Equal to an empty Markdown page")
	}
}
