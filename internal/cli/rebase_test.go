package cli

import (
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/rebase"
)

// writeRebase names as marked only the files that hold markers: a file
// deleted on one side and changed on the other has none, and is decided by
// deleting it or with --resolved.
func TestWriteRebaseNamesOnlyMarkedFiles(t *testing.T) {
	id, err := pack.ParseID("lime/runbooks")
	if err != nil {
		t.Fatal(err)
	}
	o := rebase.Origin{Package: id, Base: "0.2.0", Latest: "0.2.1", Replaces: "b3207eb76910"}
	conflict := func(path string, undecided bool) rebase.File {
		return rebase.File{Path: path, Change: rebase.Conflict, Conflicts: 1, Undecided: undecided}
	}
	tests := []struct {
		name    string
		res     rebase.Result
		want    []string
		notWant []string
	}{
		{
			name: "markers and an undecided file",
			res: rebase.Result{
				Files:     []rebase.File{conflict("SKILL.md", false), conflict("references/old.md", true)},
				Conflicts: []string{"SKILL.md", "references/old.md"}, Undecided: []string{"references/old.md"},
			},
			want: []string{
				"keep it with: loadout rebase runbooks --resolved references/old.md",
				"1 file has conflicts, marked with <<<<<<<: SKILL.md. Resolve them in your editor or AI tool, then: loadout propose runbooks",
			},
			notWant: []string{"<<<<<<<: SKILL.md, references/old.md"},
		},
		{
			name: "only an undecided file",
			res: rebase.Result{
				Files:     []rebase.File{conflict("references/oncall.md", true)},
				Conflicts: []string{"references/oncall.md"}, Undecided: []string{"references/oncall.md"},
			},
			want:    []string{"keep it with: loadout rebase runbooks --resolved references/oncall.md", "propose it with: loadout propose runbooks"},
			notWant: []string{"<<<<<<<"},
		},
		{
			name: "only markers",
			res: rebase.Result{
				Files:     []rebase.File{conflict("SKILL.md", false)},
				Conflicts: []string{"SKILL.md"},
			},
			want:    []string{"1 file has conflicts, marked with <<<<<<<: SKILL.md."},
			notWant: []string{"--resolved"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			writeRebase(&out, o, tc.res, "runbooks")
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(out.String(), w) {
					t.Errorf("output has %q:\n%s", w, out.String())
				}
			}
		})
	}
}

// One column fits the longest change, and a file the author deleted says
// so rather than "yours".
func TestWriteRebaseColumns(t *testing.T) {
	id, err := pack.ParseID("lime/runbooks")
	if err != nil {
		t.Fatal(err)
	}
	o := rebase.Origin{Package: id, Base: "0.1.0", Latest: "0.1.1"}
	res := rebase.Result{Files: []rebase.File{
		{Path: "a.md", Change: rebase.FromRelease},
		{Path: "b.md", Change: rebase.YouDeleted},
		{Path: "c.md", Change: rebase.Yours},
		{Path: "d.md", Change: rebase.Deleted},
	}}
	var out strings.Builder
	writeRebase(&out, o, res, "runbooks")
	lines := strings.Split(out.String(), "\n")
	col := -1
	for _, l := range lines[1:5] {
		i := strings.Index(l, ".md")
		if col >= 0 && i != col {
			t.Errorf("paths do not line up:\n%s", out.String())
		}
		col = i
	}
	if !strings.Contains(out.String(), "you deleted") || !strings.Contains(out.String(), "deleted in 0.1.1") {
		t.Errorf("output = %q", out.String())
	}
}
