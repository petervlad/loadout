package memory_test

import (
	"testing"

	"github.com/petervlad/loadout/internal/memory"
)

func TestHistory(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	e := f.save(t, dana, "lime", "Alpha", "first", "one")
	up := f.save(t, boss, "acme", "Gamma", "project", "")

	rec, err := f.svc.History(ctx, lee, "lime", e.ID)
	if err != nil || rec.Entry.ID != e.ID || len(rec.Versions) != 1 {
		t.Fatalf("History = %+v, %v", rec, err)
	}
	if rec.CanRetract || rec.CanMove {
		t.Errorf("another writer: retract %v, move %v, want neither", rec.CanRetract, rec.CanMove)
	}
	if got, _ := f.store.Get(ctx, e.ID); got.Uses != 0 {
		t.Error("History counted a use")
	}

	rec, _ = f.svc.History(ctx, dana, "lime", e.ID)
	if !rec.CanRetract || rec.CanMove {
		t.Errorf("author: retract %v, move %v, want retract only", rec.CanRetract, rec.CanMove)
	}
	rec, _ = f.svc.History(ctx, boss, "lime", e.ID)
	if !rec.CanRetract || !rec.CanMove {
		t.Errorf("moderator: retract %v, move %v, want both", rec.CanRetract, rec.CanMove)
	}

	// A project entry read through the team: no buttons for a team member.
	rec, err = f.svc.History(ctx, dana, "lime", up.ID)
	if err != nil || rec.CanRetract || rec.CanMove {
		t.Errorf("project entry: %+v, %v", rec, err)
	}

	// The project off: nothing to move to.
	if err := f.switches.Set(ctx, "acme", false, "boss", f.clock.t); err != nil {
		t.Fatal(err)
	}
	rec, _ = f.svc.History(ctx, boss, "lime", e.ID)
	if rec.CanMove {
		t.Error("CanMove with the project off")
	}
	_, err = f.svc.History(ctx, boss, "lime", up.ID)
	wantIs(t, "entry of a layer that is off", err, memory.ErrNoMemory)
}

func TestHistoryRetracted(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	e := f.save(t, dana, "lime", "Alpha", "first", "one")
	if _, err := f.svc.Retract(ctx, dana, e.ID, "wrong"); err != nil {
		t.Fatal(err)
	}
	rec, err := f.svc.History(ctx, boss, "lime", e.ID)
	if err != nil || rec.Entry.Active() || len(rec.Versions) != 2 || rec.CanRetract || rec.CanMove {
		t.Fatalf("History = %+v, %v", rec, err)
	}
	if got := rec.Versions[1]; got.Note != "wrong" || got.By.UserID != "dana" {
		t.Errorf("retract version = %+v", got)
	}
}

func TestHistoryHidden(t *testing.T) {
	f := newFixture(t)
	e := f.save(t, dana, "lime", "Alpha", "first", "one")
	_, err := f.svc.History(t.Context(), outside, "lime", e.ID)
	wantIs(t, "outsider", err, memory.ErrNoMemory)
	_, err = f.svc.History(t.Context(), dana, "lime", 999)
	wantIs(t, "missing id", err, memory.ErrNoMemory)
	_, err = f.svc.History(t.Context(), dana, "acme", e.ID)
	wantIs(t, "entry outside the layers", err, memory.ErrNoMemory)
}
