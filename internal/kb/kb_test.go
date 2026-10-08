package kb

import (
	"fmt"
	"maps"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/skill"
)

func testDocs() map[string][]byte {
	return map[string][]byte{
		"README.md":           []byte("# Overview\n\nWhat this is.\n"),
		"runbooks/restart.md": []byte("Intro line.\n\n# Restart a service\n"),
		"data/hosts.csv":      []byte("host,ip\n"),
	}
}

func TestParse(t *testing.T) {
	b, err := Parse("ops-kb", "  How the team runs production.  ", testDocs())
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if b.Name != "ops-kb" || b.Description != "How the team runs production." {
		t.Errorf("Parse() = %q, %q", b.Name, b.Description)
	}
	want := []Doc{
		{Path: "README.md", Title: "Overview"},
		{Path: "data/hosts.csv", Title: "hosts"},
		{Path: "runbooks/restart.md", Title: "Restart a service"},
	}
	if diff := cmp.Diff(want, b.Docs()); diff != "" {
		t.Errorf("Docs() mismatch (-want +got):\n%s", diff)
	}
	b.Docs()[0].Title = "changed"
	if b.Docs()[0].Title != "Overview" {
		t.Error("mutating Docs() changed the knowledge base")
	}
}

func TestTitle(t *testing.T) {
	tests := []struct {
		name, path, data, want string
	}{
		{"first heading", "a.md", "# One\n# Two\n", "One"},
		{"heading after text", "a.md", "text\n\n# Later\n", "Later"},
		{"second level is not a title", "a.md", "## Sub\n", "a"},
		{"no space after hash", "a.md", "#tag\n", "a"},
		{"empty heading", "a.md", "#   \n# Real\n", "Real"},
		{"spaces around", "a.md", "  #   Padded   \n", "Padded"},
		{"crlf", "a.md", "# Windows\r\nmore\r\n", "Windows"},
		{"byte order mark", "a.md", "\ufeff# Marked\n", "Marked"},
		{"markdown extension", "notes.markdown", "# Long form\n", "Long form"},
		{"upper case extension", "NOTES.MD", "# Shouting\n", "Shouting"},
		{"no heading", "guides/setup.md", "Just text.\n", "setup"},
		{"text files have no headings", "a.txt", "# Not a heading here\n", "a"},
		{"heading too far down", "a.md", strings.Repeat("line\n", 50) + "# Late\n", "a"},
		{"empty file", "empty.md", "", "empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Title(tc.path, []byte(tc.data)); got != tc.want {
				t.Errorf("Title(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name  string
		kb    string
		desc  string
		files map[string][]byte
		want  string
	}{
		{"no documents", "ops", "d", nil, "at least one document"},
		{"skill file", "ops", "d", map[string][]byte{skill.File: []byte("x")}, "writes a knowledge base's SKILL.md"},
		{"skill file in another case", "ops", "d", map[string][]byte{"skill.md": []byte("x")}, "writes a knowledge base's SKILL.md"},
		{"unknown extension", "ops", "d", map[string][]byte{"a.md": nil, "tool.py": nil}, "tool.py: not a document type"},
		{"no extension", "ops", "d", map[string][]byte{"Makefile": nil}, "not a document type"},
		{"empty description", "ops", " \n", map[string][]byte{"a.md": nil}, "description is empty"},
		{"bad name", "Ops", "d", map[string][]byte{"a.md": nil}, "lowercase"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.kb, tc.desc, tc.files)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEveryExtensionIsAccepted(t *testing.T) {
	files := map[string][]byte{}
	for _, ext := range Extensions {
		files["doc"+ext] = []byte("x")
		files["upper/DOC"+strings.ToUpper(ext)] = []byte("x")
	}
	b, err := Parse("ops", "d", files)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if got := len(b.Docs()); got != len(files) {
		t.Errorf("Docs() has %d documents, want %d", got, len(files))
	}
}

func TestSkill(t *testing.T) {
	docs := testDocs()
	docs["pipes.md"] = []byte("# A | B | C\n")
	b, err := Parse("ops-kb", "How the team runs production.", docs)
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Skill()
	if err != nil {
		t.Fatalf("Skill() error: %v", err)
	}
	if s.Name != "ops-kb" {
		t.Errorf("Skill().Name = %q", s.Name)
	}
	if want := "How the team runs production. Knowledge base (4 documents): read the ones a task needs."; s.Description != want {
		t.Errorf("Skill().Description = %q, want %q", s.Description, want)
	}
	for _, want := range []string{
		"# ops-kb\n\nHow the team runs production.",
		"| Document | Title |\n|---|---|\n",
		"| [README.md](README.md) | Overview |\n",
		"| [runbooks/restart.md](runbooks/restart.md) | Restart a service |",
		`| [pipes.md](pipes.md) | A \| B \| C |`,
	} {
		if !strings.Contains(s.Body, want) {
			t.Errorf("Skill().Body is missing %q:\n%s", want, s.Body)
		}
	}
	if diff := cmp.Diff(docs, s.Files()); diff != "" {
		t.Errorf("Skill().Files() mismatch (-want +got):\n%s", diff)
	}

	// What loadout installs parses back as the same skill.
	data, err := s.Marshal("<!-- note -->")
	if err != nil {
		t.Fatal(err)
	}
	again, err := skill.Parse(data, s.Files())
	if err != nil {
		t.Fatalf("skill.Parse(Skill()) error: %v\n%s", err, data)
	}
	if again.Name != s.Name || again.Description != s.Description || !strings.HasSuffix(again.Body, s.Body) {
		t.Errorf("skill.Parse(Skill()) = %+v, want %+v", again, s)
	}
}

func TestIndexShapes(t *testing.T) {
	docs := func(n int, dir string) map[string][]byte {
		out := make(map[string][]byte, n)
		for i := range n {
			out[fmt.Sprintf("%s%03d.md", dir, i)] = fmt.Appendf(nil, "# %sPage %d\n", dir, i)
		}
		return out
	}
	merge := func(ms ...map[string][]byte) map[string][]byte {
		out := map[string][]byte{}
		for _, m := range ms {
			maps.Copy(out, m)
		}
		return out
	}
	tests := []struct {
		name string
		docs map[string][]byte
		want []string
		not  []string
	}{
		{
			name: "every document up to the limit",
			docs: merge(docs(maxIndexed-1, "ci/"), map[string][]byte{"README.md": []byte("# Overview\n")}),
			want: []string{"| Document | Title |\n", "| [README.md](README.md) | Overview |\n", "| [ci/148.md](ci/148.md) | ci/Page 148 |\n"},
			not:  []string{"| Folder |"},
		},
		{
			name: "folders above the limit",
			docs: merge(docs(100, "ci/"), docs(60, "ops/runbooks/"), map[string][]byte{"README.md": []byte("# Overview\n"), "pipes.md": []byte("# A | B\n")}),
			want: []string{
				"It has 162 documents, too many to list here",
				"Grep",
				"| Folder | Documents | For example |\n|---|---|---|\n" +
					"| [README.md](README.md) | 1 | Overview |\n" +
					"| [pipes.md](pipes.md) | 1 | A \\| B |\n" +
					"| [ci/](ci/) | 100 | ci/Page 0; ci/Page 1; ci/Page 2 |\n" +
					"| [ops/](ops/) | 60 | ops/runbooks/Page 0; ops/runbooks/Page 1; ops/runbooks/Page 2 |\n",
			},
			not: []string{"| Document | Title |", "ci/003.md"},
		},
		{
			name: "top-level files share a row above the limit",
			docs: merge(docs(maxIndexed+1, ""), docs(2, "ci/")),
			want: []string{"| this folder | 151 | Page 0; Page 1; Page 2 |\n| [ci/](ci/) | 2 | ci/Page 0; ci/Page 1 |\n"},
			not:  []string{"000.md"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := Parse("wiki", "The team wiki.", tc.docs)
			if err != nil {
				t.Fatal(err)
			}
			body := b.index()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("index is missing %q:\n%s", want, body)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(body, not) {
					t.Errorf("index contains %q:\n%s", not, body)
				}
			}
			again, err := Parse("wiki", "The team wiki.", tc.docs)
			if err != nil {
				t.Fatal(err)
			}
			if again.index() != body {
				t.Error("the index differs between two parses of the same documents")
			}
		})
	}
}

func TestSkillDescriptionFitsTheLimit(t *testing.T) {
	tests := []struct {
		name      string
		desc      string
		truncated bool
	}{
		{"short", "Short.", false},
		{"ascii, long", strings.Repeat("word ", 400), true},
		{"multibyte, long", strings.Repeat("\u00fcber ", 400), true},
		{"emoji, long", strings.Repeat("\U0001F680", 1100), true},
		{"multibyte, fits in characters", strings.Repeat("\u00e9", 900), false},
		{"fits exactly", strings.Repeat("x", skill.MaxDescription-len(" Knowledge base (1 documents): read the ones a task needs.")-1) + ".", false},
		{"full stop added, then too long", strings.Repeat("x", skill.MaxDescription-len(" Knowledge base (1 documents): read the ones a task needs.")), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := Parse("ops", tc.desc, map[string][]byte{"a.md": []byte("# A\n")})
			if err != nil {
				t.Fatal(err)
			}
			s, err := b.Skill()
			if err != nil {
				t.Fatalf("Skill() error: %v", err)
			}
			if !utf8.ValidString(s.Description) {
				t.Errorf("Skill().Description is not valid UTF-8: %q", s.Description)
			}
			if n := utf8.RuneCountInString(s.Description); n > skill.MaxDescription {
				t.Errorf("Skill().Description is %d characters, over %d", n, skill.MaxDescription)
			}
			if got := strings.Contains(s.Description, "\u2026"); got != tc.truncated {
				t.Errorf("truncated = %v, want %v: %q", got, tc.truncated, s.Description)
			}
			if !strings.HasSuffix(s.Description, "read the ones a task needs.") {
				t.Errorf("Skill().Description = %q, want the knowledge base suffix", s.Description)
			}
		})
	}
}

// The author's description ends as a sentence before loadout's own.
func TestSkillDescriptionIsTwoSentences(t *testing.T) {
	for _, desc := range []string{"How we deploy", "How we deploy."} {
		b, err := Parse("runbooks", desc, map[string][]byte{"a.md": []byte("# A\n")})
		if err != nil {
			t.Fatal(err)
		}
		s, err := b.Skill()
		if err != nil {
			t.Fatal(err)
		}
		if want := "How we deploy. Knowledge base (1 documents)"; !strings.HasPrefix(s.Description, want) {
			t.Errorf("description of %q = %q, want it to start with %q", desc, s.Description, want)
		}
	}
}
