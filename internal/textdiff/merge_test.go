package textdiff

import (
	"errors"
	"strings"
	"testing"
)

var labels = Labels{Base: "base 2.0.0", Ours: "yours", Theirs: "release 3.0.0"}

func conflict(ours, base, theirs string) string {
	return "<<<<<<< yours\n" + ours + "||||||| base 2.0.0\n" + base + "=======\n" + theirs + ">>>>>>> release 3.0.0\n"
}

func TestMerge3(t *testing.T) {
	tests := []struct {
		name               string
		base, ours, theirs string
		want               string
		wantConflicts      int
	}{
		{"nothing changed", "a\nb\n", "a\nb\n", "a\nb\n", "a\nb\n", 0},
		{"only ours", "a\nb\nc\n", "a\nB\nc\n", "a\nb\nc\n", "a\nB\nc\n", 0},
		{"only theirs", "a\nb\nc\n", "a\nb\nc\n", "a\nb\nC\n", "a\nb\nC\n", 0},
		{"the same change on both", "a\nb\nc\n", "a\nB\nc\n", "a\nB\nc\n", "a\nB\nc\n", 0},
		{"separate lines", "a\nb\nc\nd\ne\n", "A\nb\nc\nd\ne\n", "a\nb\nc\nd\nE\n", "A\nb\nc\nd\nE\n", 0},
		{"one line apart", "a\nb\nc\n", "A\nb\nc\n", "a\nb\nC\n", "A\nb\nC\n", 0},
		{"adjacent lines conflict", "a\nb\nc\n", "A\nb\nc\n", "a\nB\nc\n", conflict("A\nb\n", "a\nb\n", "a\nB\n") + "c\n", 1},
		{"overlapping change", "a\nb\nc\n", "a\nX\nc\n", "a\nY\nc\n", "a\n" + conflict("X\n", "b\n", "Y\n") + "c\n", 1},
		{"insertions at the same place", "a\nc\n", "a\nb1\nc\n", "a\nb2\nc\n", "a\n" + conflict("b1\n", "", "b2\n") + "c\n", 1},
		{"insertions apart", "a\nb\nc\n", "a\nnew\nb\nc\n", "a\nb\nc\nend\n", "a\nnew\nb\nc\nend\n", 0},
		{"deletion against edit", "a\nb\nc\n", "a\nc\n", "a\nB\nc\n", "a\n" + conflict("", "b\n", "B\n") + "c\n", 1},
		{"deletion on one side", "a\nb\nc\n", "a\nc\n", "a\nb\nc\n", "a\nc\n", 0},
		{
			"two conflicts", "a\nb\nc\nd\ne\n", "X\nb\nc\nd\nY\n", "P\nb\nc\nd\nQ\n",
			conflict("X\n", "a\n", "P\n") + "b\nc\nd\n" + conflict("Y\n", "e\n", "Q\n"), 2,
		},
		{"empty base, both add", "", "a\n", "b\n", conflict("a\n", "", "b\n"), 1},
		{"empty base, one adds", "", "a\n", "", "a\n", 0},
		{"all empty", "", "", "", "", 0},
		{"missing final newline kept", "a\nb", "A\nb", "a\nb", "A\nb", 0},
		{"missing final newline in a conflict", "a\nb", "a\nX", "a\nY", "a\n" + conflict("X\n", "b\n", "Y\n"), 1},
		{"CRLF kept as text", "a\r\nb\r\nc\r\n", "a\r\nb\r\nC\r\n", "A\r\nb\r\nc\r\n", "A\r\nb\r\nC\r\n", 0},
		{"a line ending change is a change", "a\nb\nc\n", "a\r\nb\nc\n", "a\nb\nc\n", "a\r\nb\nc\n", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, n, err := Merge3(tc.base, tc.ours, tc.theirs, labels)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || n != tc.wantConflicts {
				t.Errorf("Merge3() = %q, %d conflicts\nwant %q, %d", got, n, tc.want, tc.wantConflicts)
			}
		})
	}
}

func TestMerge3TooLarge(t *testing.T) {
	big := strings.Repeat("x\n", maxLines+1)
	for _, sides := range [][3]string{{big, "a\n", "a\n"}, {"a\n", big, "a\n"}, {"a\n", "a\n", big}} {
		if _, _, err := Merge3(sides[0], sides[1], sides[2], labels); !errors.Is(err, ErrTooLarge) {
			t.Errorf("Merge3(over %d lines) error = %v, want ErrTooLarge", maxLines, err)
		}
	}
}
