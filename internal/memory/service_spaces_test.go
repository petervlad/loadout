package memory_test

import (
	"slices"
	"testing"
)

func TestSpacesListsReadableNamespacesThatAreOn(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	// dana reads lime and acme, both on. outside reads nothing.
	got, err := f.svc.Spaces(ctx, dana)
	if err != nil {
		t.Fatalf("Spaces(dana) error: %v", err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Namespace)
	}
	if !slices.Equal(names, []string{"acme", "lime"}) {
		t.Errorf("Spaces(dana) = %v, want [acme lime]", names)
	}
	if got, err := f.svc.Spaces(ctx, outside); err != nil || len(got) != 0 {
		t.Errorf("Spaces(outside) = %v, %v, want none", got, err)
	}

	if err := f.svc.SetEnabled(ctx, boss, "lime", false); err != nil {
		t.Fatal(err)
	}
	got, err = f.svc.Spaces(ctx, dana)
	if err != nil || len(got) != 1 || got[0].Namespace != "acme" {
		t.Errorf("Spaces(dana) with lime off = %v, %v, want only acme", got, err)
	}
}

func TestEnabledFollowsTheSwitch(t *testing.T) {
	f := newFixture(t)
	on, err := f.svc.Enabled(t.Context(), "lime")
	if err != nil || !on {
		t.Fatalf("Enabled(lime) = %v, %v, want true", on, err)
	}
	if err := f.svc.SetEnabled(t.Context(), boss, "lime", false); err != nil {
		t.Fatal(err)
	}
	if on, err := f.svc.Enabled(t.Context(), "lime"); err != nil || on {
		t.Errorf("Enabled(lime) after off = %v, %v, want false", on, err)
	}
	if on, err := f.svc.Enabled(t.Context(), "ghost"); err != nil || on {
		t.Errorf("Enabled(ghost) = %v, %v, want false, nil", on, err)
	}
}
