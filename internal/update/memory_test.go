package update

import (
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/syncstate"
)

// syncer runs syncs of one install set against one registry, keeping the
// state between runs as the real store would.
type syncer struct {
	t     *testing.T
	inst  *fakeInstaller
	reg   *fakeRegistry
	notes *recordingNotifier
	mem   *memMemory
	clock time.Time
}

func newSyncer(t *testing.T, entries ...lock.Entry) *syncer {
	t.Helper()
	return &syncer{
		t:     t,
		inst:  &fakeInstaller{entries: entries},
		reg:   &fakeRegistry{latest: map[string]semver.Version{}},
		notes: &recordingNotifier{},
		mem:   &memMemory{},
		clock: time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC),
	}
}

func (s *syncer) sync() []Result {
	s.t.Helper()
	svc := New(s.inst, func(lock.Entry) (Source, error) { return Source{Registry: *s.reg, Remote: "office"}, nil }, s.notes, s.mem, slog.New(slog.DiscardHandler))
	svc.now = func() time.Time { return s.clock }
	s.clock = s.clock.Add(24 * time.Hour)
	results, err := svc.Sync(s.t.Context())
	if err != nil {
		s.t.Fatalf("Sync() error: %v", err)
	}
	return results
}

// record returns the record of lime/a, the install these tests sync.
func (s *syncer) record() (syncstate.Behind, bool) {
	for _, b := range s.mem.st.All() {
		if b.ID.Name() == "a" {
			return b, true
		}
	}
	return syncstate.Behind{}, false
}

func TestNotifyWithEditsIsAvailableAndHeld(t *testing.T) {
	s := newSyncer(t, entry(t, "a", "1.0.0", lock.PolicyNotify, "office"))
	s.reg.latest["lime/a"] = semver.New(1, 1, 0)
	s.inst.edited = map[string][]string{"lime/a": {".claude/agents/a.md"}}
	r := s.sync()[0]
	if r.Outcome != OutcomeAvailable || !slices.Equal(r.HeldPaths(), []string{".claude/agents/a.md"}) {
		t.Errorf("result = %q held %v; want update available, held by the edited file", r.Outcome, r.HeldPaths())
	}
	if len(s.notes.got) != 1 || !slices.Equal(s.notes.got[0].Held, []string{".claude/agents/a.md"}) || s.notes.got[0].Remote != "office" {
		t.Errorf("notifications = %+v, want one naming the edited file", s.notes.got)
	}
	if b, ok := s.record(); !ok || !b.IsHeld() || b.Latest.String() != "1.1.0" || !b.Told {
		t.Errorf("record = %+v, %v; want held 1.1.0, told", b, ok)
	}
}

func TestAutoConflictIsHeldWithReasons(t *testing.T) {
	e := entry(t, "a", "1.0.0", lock.PolicyAuto, "office")
	s := newSyncer(t, e)
	s.reg.latest["lime/a"] = semver.New(2, 0, 0)
	other := mustID(t, "lime/b")
	s.inst.installErr = &install.ConflictError{Conflicts: []install.Conflict{
		{Path: "/home/a/.claude/agents/a.md", Reason: install.ReasonModified},
		{Path: "/home/a/.claude/agents/x.md", Reason: install.ReasonOtherPackage, Owner: other},
	}}
	r := s.sync()[0]
	want := []syncstate.Blocker{
		{Path: ".claude/agents/a.md", Reason: string(install.ReasonModified)},
		{Path: ".claude/agents/x.md", Reason: string(install.ReasonOtherPackage), Owner: "lime/b"},
	}
	if r.Outcome != OutcomeBlocked || !slices.Equal(r.Held, want) {
		t.Errorf("result = %q held %+v; want held back with %+v", r.Outcome, r.Held, want)
	}
	if len(s.notes.got) != 1 || s.notes.got[0].Latest.String() != "2.0.0" || len(s.notes.got[0].Held) != 2 {
		t.Errorf("notifications = %+v, want one for the held 2.0.0", s.notes.got)
	}
}

func TestModelOnlyHold(t *testing.T) {
	models := copilotDeep(t, "Claude Sonnet 5")
	s := newSyncer(t, entry(t, "a", "1.0.0", lock.PolicyAuto, "office"))
	s.reg.latest["lime/a"] = semver.New(1, 0, 0)
	s.reg.models = models
	s.inst.rerenders = true // the agent's file names the model
	s.inst.installErr = &install.ConflictError{Conflicts: []install.Conflict{{Path: "/home/a/.claude/agents/a.md", Reason: install.ReasonModified}}}
	r := s.sync()[0]
	if r.Outcome != OutcomeBlocked || !r.ModelsOnly() || r.Models != models.Digest() {
		t.Errorf("result = %+v; want a model-only hold with the table digest", r)
	}
	if len(s.notes.got) != 1 || !s.notes.got[0].ModelsChanged {
		t.Errorf("notifications = %+v, want one model change", s.notes.got)
	}
	if b, ok := s.record(); !ok || !b.ModelsOnly() || b.Models != models.Digest() {
		t.Errorf("record = %+v, %v", b, ok)
	}
}

func TestNotifiesOncePerVersion(t *testing.T) {
	s := newSyncer(t, entry(t, "a", "1.0.0", lock.PolicyNotify, "office"))
	s.reg.latest["lime/a"] = semver.New(1, 1, 0)

	s.sync()
	first, _ := s.record()
	s.sync()
	if len(s.notes.got) != 1 {
		t.Fatalf("two syncs of the same release notified %d times, want once", len(s.notes.got))
	}
	if b, _ := s.record(); !b.Since.Equal(first.Since) || !b.Checked.After(first.Checked) {
		t.Errorf("record since %v checked %v; want since kept at %v and checked moved on", b.Since, b.Checked, first.Since)
	}

	s.inst.edited = map[string][]string{"lime/a": {".claude/agents/a.md"}}
	s.sync()
	if len(s.notes.got) != 2 || len(s.notes.got[1].Held) != 1 {
		t.Fatalf("an available update that became held: notifications %+v, want a second one", s.notes.got)
	}
	s.sync()
	if len(s.notes.got) != 2 {
		t.Fatalf("held twice: %d notifications, want 2", len(s.notes.got))
	}

	s.reg.latest["lime/a"] = semver.New(1, 2, 0)
	s.sync()
	if len(s.notes.got) != 3 || s.notes.got[2].Latest.String() != "1.2.0" {
		t.Fatalf("a newer release: notifications %+v, want a third for 1.2.0", s.notes.got)
	}
}

func TestFailedNotificationIsRetried(t *testing.T) {
	s := newSyncer(t, entry(t, "a", "1.0.0", lock.PolicyNotify, "office"))
	s.reg.latest["lime/a"] = semver.New(1, 1, 0)
	s.notes.err = errors.New("pushover down")
	s.sync()
	if b, _ := s.record(); b.Told {
		t.Error("a failed notification was recorded as told")
	}
	s.notes.err = nil
	s.sync()
	s.sync()
	if len(s.notes.got) != 2 {
		t.Errorf("notifications = %d, want 2 (the failed one, then the retry, then none)", len(s.notes.got))
	}
}

func TestRecordsFollowTheOutcome(t *testing.T) {
	e := entry(t, "a", "1.0.0", lock.PolicyAuto, "office")
	s := newSyncer(t, e)
	s.reg.latest["lime/a"] = semver.New(1, 1, 0)
	s.inst.installErr = &install.ConflictError{Conflicts: []install.Conflict{{Path: "/home/a/.claude/agents/a.md", Reason: install.ReasonModified}}}
	s.sync()
	if _, ok := s.record(); !ok {
		t.Fatal("a held update was not recorded")
	}

	// The registry is down: the record stays.
	s.reg.tierErr = errors.New("timeout")
	if r := s.sync()[0]; r.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", r.Outcome)
	}
	if _, ok := s.record(); !ok {
		t.Error("a failed check dropped the record")
	}

	// The edit is gone and the update goes in: the record is dropped.
	s.reg.tierErr, s.inst.installErr = nil, nil
	if r := s.sync()[0]; r.Outcome != OutcomeUpdated {
		t.Fatalf("outcome = %q, want updated", r.Outcome)
	}
	if b, ok := s.record(); ok {
		t.Errorf("record %+v kept after the update went in", b)
	}

	// Up to date: nothing recorded.
	s.inst.entries[0].Version = semver.New(1, 1, 0)
	s.sync()
	if _, ok := s.record(); ok {
		t.Error("an install in step has a record")
	}
}

func TestStateFailuresNeverFailTheSync(t *testing.T) {
	s := newSyncer(t, entry(t, "a", "1.0.0", lock.PolicyNotify, "office"))
	s.reg.latest["lime/a"] = semver.New(1, 1, 0)
	s.mem.loadErr = errors.New("damaged")
	s.mem.updateErr = errors.New("disk full")
	if r := s.sync(); len(r) != 1 || r[0].Outcome != OutcomeAvailable {
		t.Errorf("Sync() = %+v, want update available despite the state", r)
	}
	if len(s.notes.got) != 1 {
		t.Errorf("notifications = %d, want 1 (no record means news)", len(s.notes.got))
	}
}

func mustID(t *testing.T, s string) pack.ID {
	t.Helper()
	id, err := pack.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestNoChannelKeepsNewsPending: without a notifier nothing is marked
// told, so a channel set up later still hears of the pending update.
func TestNoChannelKeepsNewsPending(t *testing.T) {
	s := newSyncer(t, entry(t, "a", "1.0.0", lock.PolicyNotify, "office"))
	s.reg.latest["lime/a"] = semver.New(1, 1, 0)
	svc := New(s.inst, func(lock.Entry) (Source, error) { return Source{Registry: *s.reg}, nil }, nil, s.mem, slog.New(slog.DiscardHandler))
	if _, err := svc.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b, ok := s.record(); !ok || b.Told {
		t.Fatalf("record = %+v, %v; want recorded, not told", b, ok)
	}
	s.sync() // now with a channel
	if len(s.notes.got) != 1 {
		t.Errorf("notifications once a channel exists = %d, want 1", len(s.notes.got))
	}
}
