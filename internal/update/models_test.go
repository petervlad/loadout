package update

import (
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/tier"
)

func copilotDeep(t *testing.T, model string) tier.Table {
	t.Helper()
	tb, err := tier.New(map[tier.Key]tier.Choice{{Tool: "copilot", Hint: agent.ModelDeep}: {Model: model}})
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func TestSyncAppliesChangedModelsPerPolicy(t *testing.T) {
	models := copilotDeep(t, "Claude Sonnet 5")
	tests := []struct {
		name          string
		policy        lock.Policy
		recorded      string // the entry's Models
		rerenders     bool
		unchanged     bool
		want          Outcome
		wantInstall   bool
		wantNotified  bool
		wantFetchedAt string
	}{
		{name: "auto writes it again", policy: lock.PolicyAuto, rerenders: true, want: OutcomeRerendered, wantInstall: true, wantFetchedAt: "lime/a@1.0.0"},
		{name: "auto, nothing to change, only records", policy: lock.PolicyAuto, unchanged: true, want: OutcomeUpToDate, wantInstall: true, wantFetchedAt: "lime/a@1.0.0"},
		{name: "notify reports", policy: lock.PolicyNotify, rerenders: true, want: OutcomeModelsChanged, wantNotified: true, wantFetchedAt: "lime/a@1.0.0"},
		{name: "notify, nothing to change, only records", policy: lock.PolicyNotify, unchanged: true, want: OutcomeUpToDate, wantInstall: true, wantFetchedAt: "lime/a@1.0.0"},
		{name: "pin does nothing", policy: lock.PolicyPin, rerenders: true, want: OutcomeUpToDate},
		{name: "same digest does nothing", policy: lock.PolicyAuto, recorded: models.Digest(), rerenders: true, want: OutcomeUpToDate},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := entry(t, "a", "1.0.0", tc.policy, "office")
			e.Models = tc.recorded
			inst := &fakeInstaller{entries: []lock.Entry{e}, rerenders: tc.rerenders, unchanged: tc.unchanged}
			var fetched []string
			reg := fakeRegistry{latest: map[string]semver.Version{"lime/a": semver.New(1, 0, 0)}, models: models, fetched: &fetched}
			notes := &recordingNotifier{}
			results, err := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, notes, &memMemory{}, slog.New(slog.DiscardHandler)).Sync(t.Context())
			if err != nil || len(results) != 1 {
				t.Fatalf("Sync() = %+v, %v", results, err)
			}
			if results[0].Outcome != tc.want {
				t.Errorf("outcome = %q, want %q", results[0].Outcome, tc.want)
			}
			if got := len(inst.opts) == 1; got != tc.wantInstall {
				t.Fatalf("installed %v, want install: %v", inst.installed, tc.wantInstall)
			}
			if tc.wantInstall && inst.opts[0].Models.Digest() != models.Digest() {
				t.Errorf("installed with models %q, want the team's %q", inst.opts[0].Models.Digest(), models.Digest())
			}
			if got := len(notes.got) == 1 && notes.got[0].ModelsChanged; got != tc.wantNotified {
				t.Errorf("notifications = %+v, want a models notification: %v", notes.got, tc.wantNotified)
			}
			if want := slices.DeleteFunc([]string{tc.wantFetchedAt}, func(s string) bool { return s == "" }); !slices.Equal(fetched, want) {
				t.Errorf("fetched %v, want %v", fetched, want)
			}
		})
	}
}

func TestSyncUpdatesWithTheTeamsModels(t *testing.T) {
	models := copilotDeep(t, "Claude Sonnet 5")
	inst := &fakeInstaller{entries: []lock.Entry{entry(t, "a", "1.0.0", lock.PolicyAuto, "office")}}
	reg := fakeRegistry{latest: map[string]semver.Version{"lime/a": semver.New(1, 1, 0)}, models: models}
	results, err := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, &recordingNotifier{}, &memMemory{}, slog.New(slog.DiscardHandler)).Sync(t.Context())
	if err != nil || len(results) != 1 || results[0].Outcome != OutcomeUpdated {
		t.Fatalf("Sync() = %+v, %v; want updated", results, err)
	}
	if inst.opts[0].Models.Digest() != models.Digest() {
		t.Error("the update was installed without the team's models")
	}
}

func TestSyncFailsWhenTheModelsCannotBeRead(t *testing.T) {
	// Updating without the table would write the built-in models over the
	// team's, so the installs of that source fail instead.
	inst := &fakeInstaller{entries: []lock.Entry{entry(t, "a", "1.0.0", lock.PolicyAuto, "office"), entry(t, "b", "1.0.0", lock.PolicyAuto, "office")}}
	reg := fakeRegistry{latest: map[string]semver.Version{"lime/a": semver.New(2, 0, 0), "lime/b": semver.New(2, 0, 0)}, tierErr: errors.New("timeout")}
	results, err := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, &recordingNotifier{}, &memMemory{}, slog.New(slog.DiscardHandler)).Sync(t.Context())
	if err != nil || len(results) != 2 {
		t.Fatalf("Sync() = %+v, %v", results, err)
	}
	for _, r := range results {
		if r.Outcome != OutcomeFailed || r.Err == nil {
			t.Errorf("%s: outcome %q, want failed", r.Entry.ID, r.Outcome)
		}
	}
	if len(inst.installed) != 0 {
		t.Errorf("installed %v without the model table", inst.installed)
	}
}

func TestSyncLeavesAFullyShadowedInstallAlone(t *testing.T) {
	e := entry(t, "a", "1.0.0", lock.PolicyAuto, "office")
	e.Shadowed = []lock.Shadow{{Target: "claude", Path: e.Files[0].Path}}
	e.Files = nil
	inst := &fakeInstaller{entries: []lock.Entry{e}, rerenders: true}
	reg := fakeRegistry{latest: map[string]semver.Version{"lime/a": semver.New(1, 0, 0)}, models: copilotDeep(t, "x")}
	results, err := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, &recordingNotifier{}, &memMemory{}, slog.New(slog.DiscardHandler)).Sync(t.Context())
	if err != nil || len(results) != 1 || results[0].Outcome != OutcomeUpToDate || len(inst.installed) != 0 {
		t.Errorf("Sync() = %+v, %v, installed %v; want up to date and nothing installed", results, err, inst.installed)
	}
}

// TestModelChangeThatRendersNothingHoldsNothingBack: new team model
// settings change no file of a skill or a knowledge base, so an edit in one
// holds nothing back, whatever the policy. Installing only records the new
// table's digest, and an edit refuses that install; sync must not call it a
// held update (exit 2 every run, a push, a session line) when there is
// nothing to take.
func TestModelChangeThatRendersNothingHoldsNothingBack(t *testing.T) {
	for _, policy := range []lock.Policy{lock.PolicyAuto, lock.PolicyNotify} {
		t.Run(string(policy), func(t *testing.T) {
			s := newSyncer(t, entry(t, "a", "1.0.0", policy, "office"))
			s.reg.latest["lime/a"] = semver.New(1, 0, 0)
			s.reg.models = copilotDeep(t, "Claude Sonnet 5")
			s.inst.rerenders = false
			s.inst.installErr = &install.ConflictError{Conflicts: []install.Conflict{{Path: "/home/a/.claude/skills/a/SKILL.md", Reason: install.ReasonModified}}}
			r := s.sync()[0]
			if r.Outcome != OutcomeUpToDate || len(r.Held) != 0 {
				t.Errorf("result = %q, held %v; want up to date with nothing held", r.Outcome, r.Held)
			}
			if len(s.notes.got) != 0 {
				t.Errorf("notifications = %+v, want none", s.notes.got)
			}
			if b, ok := s.record(); ok {
				t.Errorf("record = %+v, want none", b)
			}
		})
	}
}
