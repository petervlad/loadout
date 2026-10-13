package memory_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
	"github.com/petervlad/loadout/internal/registry"
)

// fakeRoles answers rights from a table keyed by user and namespace. A pair
// that is not in the table has no rights, as an unknown team would.
type fakeRoles map[[2]string]memory.Rights

func (f fakeRoles) MemoryRights(_ context.Context, userID, ns string) (memory.Rights, error) {
	return f[[2]string{userID, ns}], nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

var (
	dana    = memory.Caller{UserID: "dana", Label: "dana@example.com"}
	lee     = memory.Caller{UserID: "lee", Label: "lee@example.com"}
	boss    = memory.Caller{UserID: "boss", Label: "boss@example.com"}
	reader  = memory.Caller{UserID: "reader", Label: "reader@example.com"}
	outside = memory.Caller{UserID: "outside", Label: "outside@example.com"}
)

type fixture struct {
	svc      *memory.Service
	store    *memorytest.MemStore
	switches *memorytest.MemSwitchStore
	clock    *clock
	logs     *bytes.Buffer
}

// newFixture has team lime under project acme, both with memory on. dana and
// lee write, boss also moderates and switches, reader reads, outside has
// nothing. Everyone in lime reads acme through the layers.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	layers := []string{"lime", "acme"}
	roles := fakeRoles{
		{"dana", "lime"}:   {Read: true, Write: true, Layers: layers},
		{"lee", "lime"}:    {Read: true, Write: true, Layers: layers},
		{"boss", "lime"}:   {Read: true, Write: true, Moderate: true, Switch: true, Layers: layers},
		{"reader", "lime"}: {Read: true, Layers: layers},
		{"boss", "acme"}:   {Read: true, Write: true, Moderate: true, Switch: true, Layers: []string{"acme"}},
		{"dana", "acme"}:   {Read: true, Layers: []string{"acme"}},
	}
	f := &fixture{
		store:    memorytest.NewMemStore(),
		switches: memorytest.NewMemSwitchStore(),
		clock:    &clock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)},
		logs:     &bytes.Buffer{},
	}
	log := slog.New(slog.NewJSONHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc, err := memory.NewService(f.store, f.switches, roles, f.clock.now, log)
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	for _, ns := range layers {
		if err := f.switches.Set(t.Context(), ns, true, "boss", f.clock.t); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func draft(t *testing.T, ns, name, desc, body string) memory.Draft {
	t.Helper()
	d, err := memory.NewDraft(ns, memory.TypeReference, name, desc, body, nil)
	if err != nil {
		t.Fatalf("NewDraft(%s): %v", name, err)
	}
	return d
}

// save saves a draft with force, so unrelated tests skip the duplicate check.
func (f *fixture) save(t *testing.T, c memory.Caller, ns, name, desc, body string) memory.Entry {
	t.Helper()
	r, err := f.svc.Save(t.Context(), c, ns, draft(t, ns, name, desc, body), true)
	if err != nil || !r.Done() {
		t.Fatalf("Save(%s): %+v, %v", name, r, err)
	}
	return r.Entry
}

func wantIs(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("%s: error %v, want %v", what, err, target)
	}
}

func TestNewServiceNeedsParts(t *testing.T) {
	if _, err := memory.NewService(nil, memorytest.NewMemSwitchStore(), fakeRoles{}, nil, nil); err == nil {
		t.Error("NewService without a store succeeded")
	}
	s, err := memory.NewService(memorytest.NewMemStore(), memorytest.NewMemSwitchStore(), fakeRoles{}, nil, nil)
	if err != nil || s.Now().IsZero() {
		t.Errorf("defaults: %v, now %v", err, s.Now())
	}
}

// TestHiddenLooksTheSame: a team the person is not in, one that does not
// exist, one whose switch is off, and an entry of such a team all answer with
// the one fixed sentence.
func TestHiddenLooksTheSame(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	secret := f.save(t, boss, "acme", "Project fact", "d", "b")
	if err := f.switches.Set(ctx, "acme", false, "boss", f.clock.t); err != nil {
		t.Fatal(err)
	}
	if err := f.switches.Set(ctx, "off", true, "boss", f.clock.t); err != nil {
		t.Fatal(err)
	}
	if err := f.switches.Set(ctx, "off", false, "boss", f.clock.t); err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"search other":   func() error { _, err := f.svc.Search(ctx, outside, "lime", memory.Query{Text: "x"}); return err },
		"search unknown": func() error { _, err := f.svc.Search(ctx, boss, "nosuch", memory.Query{Text: "x"}); return err },
		"search off":     func() error { _, err := f.svc.Search(ctx, dana, "off", memory.Query{Text: "x"}); return err },
		"get":            func() error { _, err := f.svc.Get(ctx, outside, "lime", 1); return err },
		"recent":         func() error { _, err := f.svc.Recent(ctx, outside, "lime", time.Time{}, 0); return err },
		"index":          func() error { _, _, err := f.svc.Index(ctx, outside, "lime"); return err },
		"save": func() error {
			_, err := f.svc.Save(ctx, outside, "lime", draft(t, "lime", "n", "d", "b"), false)
			return err
		},
		"update": func() error {
			_, err := f.svc.Update(ctx, dana, secret.ID, 0, memory.Change{Confirm: true})
			return err
		},
		"retract": func() error { _, err := f.svc.Retract(ctx, dana, secret.ID, "r"); return err },
		"move":    func() error { _, err := f.svc.Move(ctx, boss, secret.ID); return err },
		"missing": func() error { _, err := f.svc.Update(ctx, dana, 999, 0, memory.Change{Confirm: true}); return err },
		"enable":  func() error { return f.svc.SetEnabled(ctx, outside, "lime", true) },
	}
	for name, call := range calls {
		err := call()
		wantIs(t, name, err, memory.ErrNoMemory)
		wantIs(t, name, err, registry.ErrNotFound)
		if err != nil && err.Error() != "No team memory here." {
			t.Errorf("%s: message %q is not the fixed sentence", name, err)
		}
	}
}

func TestSaveSearchGet(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	r, err := f.svc.Save(ctx, dana, "lime", draft(t, "lime", "Tracker host of OPS", "OPS lives on the support host", "use REST"), false)
	if err != nil || !r.Done() || r.Entry.AuthorID != "dana" || r.Entry.Namespace != "lime" {
		t.Fatalf("Save = %+v, %v", r, err)
	}
	got, err := f.svc.Search(ctx, lee, "lime", memory.Query{Text: "ops tracker"})
	if err != nil || len(got) != 1 || got[0].ID != r.Entry.ID {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	if e, _ := f.store.Get(ctx, r.Entry.ID); e.Uses != 0 {
		t.Errorf("search counted a use: %d", e.Uses)
	}
	e, err := f.svc.Get(ctx, lee, "lime", r.Entry.ID)
	if err != nil || e.Uses != 1 || e.Body != "use REST" {
		t.Fatalf("Get = %+v, %v", e, err)
	}
	if stored, _ := f.store.Get(ctx, r.Entry.ID); stored.Uses != 1 {
		t.Errorf("get stored %d uses, want 1", stored.Uses)
	}
	if none, err := f.svc.Search(ctx, lee, "lime", memory.Query{Text: "  "}); err != nil || len(none) != 0 {
		t.Errorf("empty search = %v, %v", none, err)
	}
	if _, err := f.svc.Save(ctx, reader, "lime", draft(t, "lime", "n", "d", "b"), false); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("reader Save error = %v, want forbidden", err)
	}
	if _, err := f.svc.Save(ctx, dana, "lime", draft(t, "acme", "n", "d", "b"), false); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Save of a draft for another team error = %v, want invalid", err)
	}
}

func TestIndexAndRecent(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.save(t, dana, "lime", "Alpha", "d", "b")
	f.clock.t = f.clock.t.Add(48 * time.Hour)
	b := f.save(t, lee, "lime", "Beta", "d", "b")
	idx, total, err := f.svc.Index(ctx, dana, "lime")
	if err != nil || total != 2 || len(idx) != 2 || idx[0].ID != b.ID {
		t.Fatalf("Index = %+v, %d, %v", idx, total, err)
	}
	rec, err := f.svc.Recent(ctx, dana, "lime", f.clock.t.Add(-24*time.Hour), 0)
	if err != nil || len(rec) != 1 || rec[0].ID != b.ID {
		t.Fatalf("Recent = %+v, %v", rec, err)
	}
	_ = a
}

// TestProjectLayer: a team reads its project only while the project's own
// switch is on, and never writes to it.
func TestProjectLayer(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p := f.save(t, boss, "acme", "Release label", "needs the release label", "b")
	own := f.save(t, dana, "lime", "Release notes", "lime release notes", "b")
	q := memory.Query{Text: "release"}

	got, err := f.svc.Search(ctx, dana, "lime", q)
	if err != nil || len(got) != 2 {
		t.Fatalf("with the project on, Search = %+v, %v", got, err)
	}
	if only, _ := f.svc.Search(ctx, dana, "lime", memory.Query{Text: "release", Namespaces: []string{"acme"}}); len(only) != 1 || only[0].ID != p.ID {
		t.Errorf("namespace filter = %+v", only)
	}
	if _, err := f.svc.Get(ctx, dana, "lime", p.ID); err != nil {
		t.Errorf("Get of a project entry: %v", err)
	}
	if _, _, err := f.svc.Index(ctx, dana, "lime"); err != nil {
		t.Error(err)
	}
	if _, err := f.svc.Update(ctx, dana, p.ID, 0, memory.Change{Confirm: true}); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("team member updating a project entry error = %v, want forbidden", err)
	}

	if err := f.switches.Set(ctx, "acme", false, "boss", f.clock.t); err != nil {
		t.Fatal(err)
	}
	got, err = f.svc.Search(ctx, dana, "lime", q)
	if err != nil || len(got) != 1 || got[0].ID != own.ID {
		t.Errorf("with the project off, Search = %+v, %v", got, err)
	}
	if _, err := f.svc.Get(ctx, dana, "lime", p.ID); !errors.Is(err, memory.ErrNoMemory) {
		t.Errorf("Get of a project entry while off error = %v", err)
	}
	if idx, total, _ := f.svc.Index(ctx, dana, "lime"); total != 1 || len(idx) != 1 {
		t.Errorf("Index while off = %+v, %d", idx, total)
	}
}

func TestDuplicates(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	old := f.save(t, dana, "lime", "Kafka retention window", "how long topics keep data", "b")
	tests := []struct {
		name string
		dup  bool
	}{
		{"Kafka retention policy", true},       // 2 of 3 words, 66 percent
		{"kafka RETENTION", true},              // every word
		{"Kafka cluster policy", false},        // 1 of 3, 33 percent
		{"The Kafka and the retention", true},  // stopwords do not count
		{"CI pipeline for the release", false}, // nothing in common
		{"to be", false},                       // no significant word at all
	}
	for _, tt := range tests {
		r, err := f.svc.Save(ctx, lee, "lime", draft(t, "lime", tt.name, "something else", "b"), false)
		if err != nil {
			t.Fatalf("%q: %v", tt.name, err)
		}
		if got := !r.Done(); got != tt.dup {
			t.Errorf("%q: duplicate = %v, want %v (%+v)", tt.name, got, tt.dup, r)
		}
		if tt.dup && (len(r.Duplicates) != 1 || r.Duplicates[0].ID != old.ID || r.Entry.ID != 0) {
			t.Errorf("%q: result %+v", tt.name, r)
		}
	}
	// new: true skips the check.
	r, err := f.svc.Save(ctx, lee, "lime", draft(t, "lime", "Kafka retention policy", "d", "b"), true)
	if err != nil || !r.Done() {
		t.Errorf("forced Save = %+v, %v", r, err)
	}
}

func TestDuplicatesCapAndLayers(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for i := range 8 {
		f.save(t, dana, "lime", fmt.Sprintf("Runner timeout %d", i), "runner timeout", "b")
	}
	f.save(t, boss, "acme", "Runner timeout project", "runner timeout", "b")
	r, err := f.svc.Save(ctx, lee, "lime", draft(t, "lime", "Runner timeout", "d", "b"), false)
	if err != nil || len(r.Duplicates) != memory.MaxDuplicates {
		t.Errorf("duplicates = %d, %v, want %d", len(r.Duplicates), err, memory.MaxDuplicates)
	}
	// A retracted entry is not a duplicate.
	f2 := newFixture(t)
	e := f2.save(t, dana, "lime", "Only one", "d", "b")
	if _, err := f2.svc.Retract(ctx, dana, e.ID, "wrong"); err != nil {
		t.Fatal(err)
	}
	if r, _ := f2.svc.Save(ctx, lee, "lime", draft(t, "lime", "Only one", "d", "b"), false); !r.Done() {
		t.Errorf("retracted entry counted as a duplicate: %+v", r)
	}
}

func TestDailyWriteLimit(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for i := range memory.WritesPerDay {
		f.save(t, dana, "lime", fmt.Sprintf("Fact number %d", i), "d", "b")
	}
	_, err := f.svc.Save(ctx, dana, "lime", draft(t, "lime", "One too many", "d", "b"), true)
	var ref *memory.Refusal
	if !errors.As(err, &ref) || ref.Rule != memory.RuleWritesPerDay {
		t.Fatalf("201st Save error = %v, want the daily refusal", err)
	}
	wantIs(t, "201st Save", err, registry.ErrInvalid)
	if !strings.Contains(err.Error(), "2026-10-10 12:00 UTC") {
		t.Errorf("message does not say when to try again: %v", err)
	}
	if _, err := f.svc.Update(ctx, dana, 1, 0, memory.Change{Confirm: true}); !errors.As(err, &ref) {
		t.Errorf("Update at the limit error = %v", err)
	}
	// Another person is unaffected, and so is taking back a mistake.
	f.save(t, lee, "lime", "Lee can write", "d", "b")
	if _, err := f.svc.Retract(ctx, dana, 1, "mistake"); err != nil {
		t.Errorf("Retract at the limit: %v", err)
	}
	// The window rolls.
	f.clock.t = f.clock.t.Add(memory.WriteWindow + time.Second)
	if _, err := f.svc.Save(ctx, dana, "lime", draft(t, "lime", "Next day", "d", "b"), true); err != nil {
		t.Errorf("Save after the window: %v", err)
	}
}

func TestUpdate(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	e := f.save(t, dana, "lime", "Name", "d", "old body")
	body := "new body"
	f.clock.t = f.clock.t.Add(time.Hour)
	u, err := f.svc.Update(ctx, lee, e.ID, 1, memory.Change{Body: &body, Note: "fixed"})
	if err != nil || u.Version != 2 || u.Body != body || u.AuthorID != "dana" {
		t.Fatalf("Update = %+v, %v", u, err)
	}
	wantIs(t, "stale base", errOf(f.svc.Update(ctx, lee, e.ID, 1, memory.Change{Body: &body})), memory.ErrStale)
	// Confirm only, with the base left at the newest.
	f.clock.t = f.clock.t.Add(time.Hour)
	c, err := f.svc.Update(ctx, lee, e.ID, 0, memory.Change{Confirm: true})
	if err != nil || c.Version != 3 || !c.ConfirmedAt.Equal(f.clock.t) || c.Body != body {
		t.Fatalf("confirm = %+v, %v", c, err)
	}
	h, _ := f.store.History(ctx, e.ID)
	if len(h) != 3 || h[1].Action != memory.ActionUpdate || h[2].Action != memory.ActionConfirm {
		t.Errorf("history = %+v", h)
	}
	var ref *memory.Refusal
	if _, err := f.svc.Update(ctx, lee, e.ID, 0, memory.Change{}); !errors.As(err, &ref) || ref.Rule != memory.RuleNoChange {
		t.Errorf("empty Change error = %v", err)
	}
	if _, err := f.svc.Update(ctx, reader, e.ID, 0, memory.Change{Confirm: true}); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("reader Update error = %v", err)
	}
	other := f.save(t, dana, "lime", "Other", "d", "b")
	links := []int64{other.ID}
	if _, err := f.svc.Update(ctx, dana, e.ID, 0, memory.Change{Links: &links}); err != nil {
		t.Errorf("Update with a link: %v", err)
	}
	bad := []int64{424242}
	if _, err := f.svc.Update(ctx, dana, e.ID, 0, memory.Change{Links: &bad}); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Update with a missing link error = %v", err)
	}
	if _, err := f.svc.Retract(ctx, dana, e.ID, "gone"); err != nil {
		t.Fatal(err)
	}
	wantIs(t, "update of retracted", errOf(f.svc.Update(ctx, dana, e.ID, 0, memory.Change{Confirm: true})), registry.ErrInvalid)
}

func errOf[T any](_ T, err error) error { return err }

func TestSaveLinks(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	proj := f.save(t, boss, "acme", "Project", "d", "b")
	mine := f.save(t, dana, "lime", "Mine", "d", "b")
	gone := f.save(t, dana, "lime", "Gone", "d", "b")
	if _, err := f.svc.Retract(ctx, dana, gone.ID, "r"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		links []int64
		ok    bool
	}{
		{[]int64{mine.ID, proj.ID}, true},
		{[]int64{gone.ID}, false},
		{[]int64{9999}, false},
	} {
		d, err := memory.NewDraft("lime", memory.TypeProject, "Linked", "d", "b", tt.links)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.Save(ctx, dana, "lime", d, true)
		if (err == nil) != tt.ok {
			t.Errorf("links %v: error %v, want ok=%v", tt.links, err, tt.ok)
		}
	}
}

func TestRetract(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	e := f.save(t, dana, "lime", "Wrong fact", "wrong", "b")
	if _, err := f.svc.Retract(ctx, lee, e.ID, "not mine"); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("other member Retract error = %v", err)
	}
	var ref *memory.Refusal
	if _, err := f.svc.Retract(ctx, dana, e.ID, "  "); !errors.As(err, &ref) || ref.Rule != memory.RuleRequired {
		t.Errorf("Retract without a reason error = %v", err)
	}
	if _, err := f.svc.Retract(ctx, boss, e.ID, "moderated"); err != nil {
		t.Fatalf("moderator Retract: %v", err)
	}
	if got, _ := f.svc.Search(ctx, dana, "lime", memory.Query{Text: "wrong"}); len(got) != 0 {
		t.Errorf("retracted entry found: %+v", got)
	}
	if _, total, _ := f.svc.Index(ctx, dana, "lime"); total != 0 {
		t.Errorf("index total = %d", total)
	}
	if got, _ := f.svc.Recent(ctx, dana, "lime", time.Time{}, 0); len(got) != 0 {
		t.Errorf("recent = %+v", got)
	}
	wantIs(t, "Get of retracted", errOf(f.svc.Get(ctx, dana, "lime", e.ID)), memory.ErrNoMemory)
	if h, _ := f.store.History(ctx, e.ID); len(h) != 2 || h[1].Note != "moderated" || h[1].By.UserID != "boss" {
		t.Errorf("history = %+v", h)
	}
	// The author retracts their own entry.
	own := f.save(t, dana, "lime", "Mine", "d", "b")
	if _, err := f.svc.Retract(ctx, dana, own.ID, "oops"); err != nil {
		t.Errorf("author Retract: %v", err)
	}
}

func TestMove(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	e := f.save(t, dana, "lime", "Shared fact", "d", "b")
	if _, err := f.svc.Move(ctx, dana, e.ID); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("author without Moderate: %v", err)
	}
	// Turn the project off: a moderator of the team still cannot move up.
	if err := f.switches.Set(ctx, "acme", false, "boss", f.clock.t); err != nil {
		t.Fatal(err)
	}
	wantIs(t, "project off", errOf(f.svc.Move(ctx, boss, e.ID)), registry.ErrInvalid)
	if err := f.switches.Set(ctx, "acme", true, "boss", f.clock.t); err != nil {
		t.Fatal(err)
	}
	// reader and dana hold no write right on acme, and the mover needs none.
	m, err := f.svc.Move(ctx, boss, e.ID)
	if err != nil || m.Namespace != "acme" || m.ID != e.ID || m.AuthorID != "dana" || m.Version != 2 {
		t.Fatalf("Move = %+v, %v", m, err)
	}
	h, _ := f.store.History(ctx, e.ID)
	if len(h) != 2 || h[1].Action != memory.ActionMove {
		t.Errorf("history = %+v", h)
	}
	// From the project the entry is not in a team: nothing above it.
	wantIs(t, "move from the project", errOf(f.svc.Move(ctx, boss, e.ID)), registry.ErrInvalid)
	// The team still reads it through its project layer.
	if got, err := f.svc.Get(ctx, lee, "lime", e.ID); err != nil || got.Namespace != "acme" {
		t.Errorf("Get after Move = %+v, %v", got, err)
	}
}

func TestSetEnabled(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.svc.SetEnabled(ctx, dana, "lime", false); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("member SetEnabled error = %v", err)
	}
	if err := f.svc.SetEnabled(ctx, boss, "lime", false); err != nil {
		t.Fatal(err)
	}
	wantIs(t, "after off", errOf(f.svc.Search(ctx, dana, "lime", memory.Query{Text: "x"})), memory.ErrNoMemory)
	if err := f.svc.SetEnabled(ctx, boss, "lime", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Search(ctx, dana, "lime", memory.Query{Text: "x"}); err != nil {
		t.Errorf("after on: %v", err)
	}
	if sp, _ := f.switches.Get(ctx, "lime"); !sp.Enabled || sp.ChangedBy != "boss" {
		t.Errorf("switch = %+v", sp)
	}
}

func TestPurge(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	e := f.save(t, dana, "lime", "Leaked", "d", "b")
	p, err := f.svc.Purge(ctx, e.ID)
	if err != nil || p.EntryID != e.ID || p.Versions != 1 || p.Namespace != "lime" {
		t.Fatalf("Purge = %+v, %v", p, err)
	}
	if ps, _ := f.store.Purges(ctx); len(ps) != 1 {
		t.Errorf("purges = %+v", ps)
	}
	// Purging an id that was purged only repeats the scrub, and succeeds.
	if again, err := f.svc.Purge(ctx, e.ID); err != nil || again.EntryID != e.ID {
		t.Errorf("second Purge = %+v, %v, want success", again, err)
	}
	if ps, _ := f.store.Purges(ctx); len(ps) != 1 {
		t.Errorf("the second Purge made a second record: %+v", ps)
	}
	wantIs(t, "Purge(unknown)", errOf(f.svc.Purge(ctx, e.ID+100)), registry.ErrNotFound)
}

// busyStore deletes on Purge and then fails, as a store does whose log cannot
// be truncated, and counts the scrubs it is asked for.
type busyStore struct {
	memory.Store
	busy   bool
	scrubs int
}

func (b *busyStore) Purge(ctx context.Context, id int64, at time.Time) (memory.Purged, error) {
	p, err := b.Store.Purge(ctx, id, at)
	if err == nil && b.busy {
		b.busy = false
		return p, errors.New("the write-ahead log could not be truncated")
	}
	return p, err
}

func (b *busyStore) Scrub(context.Context) error { b.scrubs++; return nil }

// TestPurgeAfterBusyScrub: the second run for an id whose first run deleted it
// but could not scrub succeeds and scrubs again. An id that was never purged
// is still not found.
func TestPurgeAfterBusyScrub(t *testing.T) {
	store := &busyStore{Store: memorytest.NewMemStore(), busy: true}
	svc, err := memory.NewService(store, memorytest.NewMemSwitchStore(), fakeRoles{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	d := draft(t, "lime", "Leaked", "d", "b")
	e, err := store.Insert(ctx, d, dana, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Purge(ctx, e.ID); err == nil {
		t.Fatal("the first Purge hid the failed scrub")
	}
	p, err := svc.Purge(ctx, e.ID)
	if err != nil || p.EntryID != e.ID || p.Namespace != "lime" || store.scrubs != 1 {
		t.Errorf("second Purge = %+v, %v after %d scrubs, want success and one scrub", p, err, store.scrubs)
	}
	wantIs(t, "Purge(never existed)", errOf(svc.Purge(ctx, 999)), registry.ErrNotFound)
	if store.scrubs != 1 {
		t.Errorf("a purge of an unknown id scrubbed (%d scrubs)", store.scrubs)
	}
}

// TestNeverLogsText runs every operation with marker text in every field and
// checks that no marker reaches the log, refusals included.
func TestNeverLogsText(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	const (
		nameM   = "zqxnamemarker"
		descM   = "zqxdescmarker"
		bodyM   = "zqxbodymarker"
		noteM   = "zqxnotemarker"
		reasonM = "zqxreasonmarker"
		queryM  = "zqxquerymarker"
		keyM    = "hunter22zqx" + "keymarker"
	)
	e := f.save(t, dana, "lime", nameM, descM, bodyM)
	if _, err := f.svc.Save(ctx, lee, "lime", draft(t, "lime", nameM+" again", descM, bodyM), false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Search(ctx, dana, "lime", memory.Query{Text: queryM + " " + nameM}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Get(ctx, dana, "lime", e.ID); err != nil {
		t.Fatal(err)
	}
	newBody := bodyM + " two"
	if _, err := f.svc.Update(ctx, dana, e.ID, 0, memory.Change{Body: &newBody, Note: noteM}); err != nil {
		t.Fatal(err)
	}
	secretBody := "password=" + keyM
	_, err := f.svc.Update(ctx, dana, e.ID, 0, memory.Change{Body: &secretBody})
	var ref *memory.Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("secret Update error = %v, want a refusal", err)
	}
	if _, err := f.svc.Retract(ctx, dana, e.ID, "password="+keyM); err == nil {
		t.Error("a reason with a key was accepted")
	}
	if _, err := f.svc.Retract(ctx, dana, e.ID, reasonM); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Move(ctx, boss, e.ID); err == nil {
		t.Error("Move of a retracted entry succeeded")
	}
	log := f.logs.String()
	for _, m := range []string{nameM, descM, bodyM, noteM, reasonM, queryM, keyM} {
		if strings.Contains(log, m) {
			t.Errorf("log holds text %q:\n%s", m, log)
		}
	}
	for _, want := range []string{"memory saved", "memory updated", "memory retracted", `"rule":"` + ref.Rule + `"`, "memory refused"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// TestRemoveNamespace: a deleted namespace leaves no memory behind, so a team
// created later under the same name starts with memory off and no entries.
// The log names the namespace and the counts, never text.
func TestRemoveNamespace(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.save(t, dana, "lime", "Secret-ish name", "d", "marker-body")
	b := f.save(t, dana, "lime", "Second", "d", "b")
	keep := f.save(t, boss, "acme", "Project fact", "d", "b")
	if _, err := f.svc.Update(ctx, dana, b.ID, 0, memory.Change{Confirm: true}); err != nil {
		t.Fatal(err)
	}

	got, err := f.svc.RemoveNamespace(ctx, "lime")
	if err != nil || got.Entries != 2 || got.Versions != 3 {
		t.Fatalf("RemoveNamespace() = %+v, %v, want 2 entries and 3 versions", got, err)
	}
	for _, id := range []int64{a.ID, b.ID} {
		if _, err := f.store.Get(ctx, id); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Get(%d) after removal = %v, want ErrNotFound", id, err)
		}
	}
	if _, err := f.store.Get(ctx, keep.ID); err != nil {
		t.Errorf("the project's entry went with the team: %v", err)
	}
	if sp, _ := f.switches.Get(ctx, "lime"); sp.Enabled {
		t.Error("the switch of the removed namespace is still on")
	}
	if sp, _ := f.switches.Get(ctx, "acme"); !sp.Enabled {
		t.Error("removing lime turned acme off")
	}
	logs := f.logs.String()
	for _, want := range []string{"memory removed with namespace", `"namespace":"lime"`, `"entries":2`, `"versions":3`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
	for _, text := range []string{"marker-body", "Secret-ish"} {
		if strings.Contains(logs, text) {
			t.Errorf("log holds entry text %q", text)
		}
	}
	// Nothing to remove is not an error, so a namespace that never had
	// memory deletes quietly.
	if got, err := f.svc.RemoveNamespace(ctx, "lime"); err != nil || got.Entries != 0 {
		t.Errorf("RemoveNamespace(again) = %+v, %v, want nothing and no error", got, err)
	}
}

func TestLogsMoveAndPurge(t *testing.T) {
	f := newFixture(t)
	e := f.save(t, dana, "lime", "Shared", "d", "b")
	if _, err := f.svc.Move(t.Context(), boss, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Purge(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetEnabled(t.Context(), boss, "lime", true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"memory moved", "memory entry purged", "memory switch set"} {
		if !strings.Contains(f.logs.String(), want) {
			t.Errorf("log lacks %q", want)
		}
	}
}
