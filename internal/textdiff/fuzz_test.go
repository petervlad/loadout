package textdiff

import (
	"math"
	"strings"
	"testing"
)

// maxFuzzInput caps each side so the O(n*m) diff stays fast under fuzzing.
const maxFuzzInput = 4 << 10

func FuzzUnified(f *testing.F) {
	seeds := []struct {
		a, b    string
		context int
	}{
		{"a\nb\nc\n", "a\nB\nc\n", 3},
		{"", "new\n", 3},
		{"old\n", "", 3},
		{"same\n", "same\n", 3},
		{"x\n", "x", 3},
		{"a\nb\nc\nd\ne\nf\ng\nh\n", "a\nX\nc\nd\ne\nf\nY\nh\n", 1},
		{"a\nb\n", "b\na\n", 0},
		{"\n\n\n", "\n", 2},
		{"a\r\nb\r\n", "a\nb\n", 3},
		{"a\nb\nc\n", "a\nc\n", -1},
		{"a\nb\nc\n", "a\nc\n", 1000},
		{"a\nb\nc\n", "a\nc\n", math.MaxInt},
	}
	for _, s := range seeds {
		f.Add(s.a, s.b, s.context)
	}

	f.Fuzz(func(t *testing.T, a, b string, context int) {
		if len(a) > maxFuzzInput || len(b) > maxFuzzInput {
			t.Skip("input over the fuzz size cap")
		}
		got, err := Unified("a.md", "b.md", a, b, context)
		if err != nil {
			t.Fatalf("Unified(%q, %q, %d) error: %v", a, b, context, err)
		}
		if a == b {
			if got != "" {
				t.Errorf("Unified(%q, %q, %d) = %q, want \"\" for equal inputs", a, b, context, got)
			}
			return
		}
		if !strings.HasPrefix(got, "--- ") {
			t.Errorf("Unified(%q, %q, %d) = %q, want a diff starting with \"--- \"", a, b, context, got)
		}
	})
}

func FuzzMerge3(f *testing.F) {
	for _, s := range [][3]string{
		{"a\nb\nc\n", "a\nB\nc\n", "a\nb\nC\n"},
		{"a\nb\nc\n", "a\nX\nc\n", "a\nY\nc\n"},
		{"", "a\n", "b\n"},
		{"a\nb", "a\nX", "a\nY"},
		{"a\r\nb\r\n", "a\nb\n", "a\r\nB\r\n"},
		{"x\nx\nx\n", "x\n", "x\nx\nx\nx\n"},
	} {
		f.Add(s[0], s[1], s[2])
	}
	l := Labels{Base: "BASE-7c1e", Ours: "OURS-7c1e", Theirs: "THEIRS-7c1e"}
	f.Fuzz(func(t *testing.T, base, x, y string) {
		if len(base) > maxFuzzInput || len(x) > maxFuzzInput || len(y) > maxFuzzInput {
			t.Skip("input over the fuzz size cap")
		}
		if got, n, err := Merge3(base, x, x, l); err != nil || n != 0 || got != x {
			t.Errorf("Merge3(%q, x, x) = %q, %d, %v; want x = %q", base, got, n, err, x)
		}
		if got, n, err := Merge3(base, base, y, l); err != nil || n != 0 || got != y {
			t.Errorf("Merge3(base, base, y) = %q, %d, %v; want y = %q", got, n, err, y)
		}
		if got, n, err := Merge3(base, x, base, l); err != nil || n != 0 || got != x {
			t.Errorf("Merge3(base, x, base) = %q, %d, %v; want x = %q", got, n, err, x)
		}
		got, n, err := Merge3(base, x, y, l)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{"<<<<<<< " + l.Ours + "\n", "||||||| " + l.Base + "\n", "=======\n", ">>>>>>> " + l.Theirs + "\n"} {
			if c := strings.Count(got, marker); c < n {
				t.Errorf("Merge3(%q, %q, %q) has %d conflicts but %d of %q", base, x, y, n, c, marker)
			}
		}
	})
}
