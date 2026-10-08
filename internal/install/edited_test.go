package install

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestEdited(t *testing.T) {
	tests := []struct {
		name   string
		change func(f fixture) error
		only   []string
		want   []string
	}{
		{name: "intact", change: func(fixture) error { return nil }},
		{name: "edited", change: func(f fixture) error { return edit(f.path("alpha/planner.txt")) }, want: []string{"alpha/planner.txt"}},
		{name: "missing is not edited", change: func(f fixture) error { return os.Remove(f.path("beta/planner.txt")) }},
		{name: "symlink is edited", change: func(f fixture) error {
			return errors.Join(os.Remove(f.path("beta/planner.txt")), os.Symlink(f.path("alpha/planner.txt"), f.path("beta/planner.txt")))
		}, want: []string{"beta/planner.txt"}},
		{name: "only restricts the check", change: func(f fixture) error {
			return errors.Join(edit(f.path("alpha/planner.txt")), edit(f.path("beta/planner.txt")))
		}, only: []string{"beta/planner.txt"}, want: []string{"beta/planner.txt"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			res, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope})
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.change(f); err != nil {
				t.Fatal(err)
			}
			got, err := f.in.Edited(t.Context(), res.Entry, tc.only)
			if err != nil {
				t.Fatalf("Edited() error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Edited() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEditedIgnoresShadowedFiles(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), makeSkill(t, "lime/checklist"), Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	far, err := f.in.Install(t.Context(), makeSkill(t, "base/checklist"), Options{Scope: f.scope, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := edit(f.path("alpha/skills/checklist/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if got, err := f.in.Edited(t.Context(), far.Entry, nil); err != nil || len(got) != 0 {
		t.Errorf("Edited(shadowed install) = %v, %v; want nothing (the closer package owns the file)", got, err)
	}
}

// edit changes an installed file, as a person would.
func edit(path string) error {
	return os.WriteFile(path, []byte("mine"), 0o644) //nolint:gosec // test temp dir
}
