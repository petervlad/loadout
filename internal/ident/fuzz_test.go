package ident

import (
	"strings"
	"testing"
	"unicode"
)

func FuzzCheck(f *testing.F) {
	seeds := []string{
		"lime",
		"planner-2",
		"a",
		strings.Repeat("a", maxLen),
		strings.Repeat("a", maxLen+1),
		"",
		"-a",
		"a-",
		"a--b",
		"Lime",
		"a/b",
		"..",
		"a b",
		"a\n",
		"a\t",
		"été",
		"a b",
		"K",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if err := Check("team", s); err != nil {
			return
		}
		if s == "" || len(s) > maxLen {
			t.Errorf("Check(%q) = nil, want error for length %d", s, len(s))
		}
		if strings.Contains(s, "/") || strings.Contains(s, "..") {
			t.Errorf("Check(%q) = nil, want error for a path separator or dot-dot", s)
		}
		for _, r := range s {
			if unicode.IsUpper(r) || unicode.IsSpace(r) {
				t.Errorf("Check(%q) = nil, want error for rune %q", s, r)
			}
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				t.Errorf("Check(%q) = nil, want error for rune %q outside [a-z0-9-]", s, r)
			}
		}
	})
}
