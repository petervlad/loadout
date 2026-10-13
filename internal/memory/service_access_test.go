package memory_test

import (
	"slices"
	"testing"

	"github.com/petervlad/loadout/internal/memory"
)

func TestLayers(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	got, err := f.svc.Layers(ctx, dana, "lime")
	if err != nil || !slices.Equal(got, []string{"lime", "acme"}) {
		t.Fatalf("Layers = %v, %v", got, err)
	}
	got[0] = "changed" // a copy: the service keeps its own
	if again, _ := f.svc.Layers(ctx, dana, "lime"); again[0] != "lime" {
		t.Error("Layers returned a shared slice")
	}
	if err := f.switches.Set(ctx, "acme", false, "boss", f.clock.t); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Layers(ctx, dana, "lime"); !slices.Equal(got, []string{"lime"}) {
		t.Errorf("Layers with the project off = %v", got)
	}
	_, err = f.svc.Layers(ctx, outside, "lime")
	wantIs(t, "outsider", err, memory.ErrNoMemory)
	_, err = f.svc.Layers(ctx, dana, "nowhere")
	wantIs(t, "unknown team", err, memory.ErrNoMemory)
}

func TestLinked(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.save(t, dana, "lime", "Alpha", "first", "")
	up := f.save(t, boss, "acme", "Gamma", "project", "")
	gone := f.save(t, dana, "lime", "Delta", "retracted", "")
	if _, err := f.svc.Retract(ctx, dana, gone.ID, "wrong"); err != nil {
		t.Fatal(err)
	}
	// An entry of a namespace the person does not read, put in the store
	// directly.
	other, err := f.store.Insert(ctx, draft(t, "elsewhere", "Beta", "elsewhere", ""), dana, f.clock.t)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Linked(ctx, lee, "lime", []int64{up.ID, a.ID, other.ID, gone.ID, 999})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	if !slices.Equal(ids, []int64{up.ID, a.ID}) {
		t.Errorf("Linked = %v, want [%d %d]", ids, up.ID, a.ID)
	}
	if got, err := f.svc.Linked(ctx, lee, "lime", nil); err != nil || got != nil {
		t.Errorf("Linked(nil) = %v, %v", got, err)
	}
	if e, _ := f.store.Get(ctx, a.ID); e.Uses != 0 {
		t.Error("Linked counted a use")
	}
	_, err = f.svc.Linked(ctx, outside, "lime", []int64{a.ID})
	wantIs(t, "outsider", err, memory.ErrNoMemory)
}
