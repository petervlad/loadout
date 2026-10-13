package web

import (
	"os"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/wiki"
)

// TestTeamMemoryWikiPage: the explainer in docs/wiki draws all three of its
// diagrams and says nothing about blocks it could not draw.
func TestTeamMemoryWikiPage(t *testing.T) {
	src, err := os.ReadFile("../../docs/wiki/team-memory.md")
	if err != nil {
		t.Fatal(err)
	}
	format, err := wiki.FormatOf("team-memory.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wiki.NewDoc(format, string(src)); err != nil {
		t.Fatalf("the page is not a valid wiki page: %v", err)
	}
	page, note := wikiDocument(string(src), wiki.Markdown, "/t/lime/wiki/r/1/draw")
	if note != "" {
		t.Errorf("note = %q, want none", note)
	}
	got := string(page)
	if n := strings.Count(got, "<figure"); n != 3 {
		t.Errorf("drew %d diagrams, want 3", n)
	}
	if strings.Contains(got, `language-mermaid`) {
		t.Error("a diagram was left as code")
	}
}
