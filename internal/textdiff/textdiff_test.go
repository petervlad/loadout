package textdiff

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestUnified(t *testing.T) {
	tests := []struct {
		name    string
		a, b    string
		context int
		want    string
	}{
		{name: "equal", a: "x\ny\n", b: "x\ny\n", context: 3, want: ""},
		{
			name: "one line changed", context: 1,
			a:    "1\n2\n3\n4\n5\n",
			b:    "1\n2\nthree\n4\n5\n",
			want: "--- a\n+++ b\n@@ -2,3 +2,3 @@\n 2\n-3\n+three\n 4\n",
		},
		{
			name: "insert at end", context: 1,
			a:    "1\n2\n",
			b:    "1\n2\n3\n",
			want: "--- a\n+++ b\n@@ -2,1 +2,2 @@\n 2\n+3\n",
		},
		{
			name: "two far apart changes make two hunks", context: 1,
			a:    "a\nb\nc\nd\ne\nf\ng\nh\n",
			b:    "A\nb\nc\nd\ne\nf\ng\nH\n",
			want: "--- a\n+++ b\n@@ -1,2 +1,2 @@\n-a\n+A\n b\n@@ -7,2 +7,2 @@\n g\n-h\n+H\n",
		},
		{
			name: "from empty", context: 3,
			a:    "",
			b:    "new\n",
			want: "--- a\n+++ b\n@@ -1,0 +1,1 @@\n+new\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Unified("a", "b", tc.a, tc.b, tc.context)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Unified() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTooLarge(t *testing.T) {
	big := strings.Repeat("x\n", maxLines+1)
	if _, err := Unified("a", "b", big, "y", 3); err == nil {
		t.Error("Unified() on a huge input succeeded")
	}
}
