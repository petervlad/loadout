package update

import (
	"errors"
	"fmt"
	"testing"

	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/semver"
)

// A package the registry deleted is neither behind nor a failure: it is
// reported once per sync, its files stay, and nobody is notified.
func TestSyncGoneIsNotAFailure(t *testing.T) {
	for _, tt := range []struct {
		name        string
		err         error
		wantOutcome Outcome
		wantRecord  bool // the record of the earlier "behind" state survives
	}{
		{"deleted on the registry", fmt.Errorf("check lime/a: %w", ErrGone), OutcomeGone, false},
		{"forbidden stays a failure", errors.New("check lime/a: registry: 403 forbidden"), OutcomeFailed, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newSyncer(t, entry(t, "a", "1.0.0", lock.PolicyNotify, "office"))
			s.reg.latest["lime/a"] = semver.New(1, 1, 0)
			s.sync() // behind: a record exists and the notifier heard it
			if _, ok := s.record(); !ok || len(s.notes.got) != 1 {
				t.Fatalf("setup: record %v, notifications %d; want a record and one notification", ok, len(s.notes.got))
			}

			s.reg.latestErr = map[string]error{"lime/a": tt.err}
			r := s.sync()[0]
			if r.Outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", r.Outcome, tt.wantOutcome)
			}
			if r.Behind() {
				t.Error("a package that is gone counts as behind")
			}
			if len(s.notes.got) != 1 {
				t.Errorf("notifications = %d, want the one from before", len(s.notes.got))
			}
			if _, ok := s.record(); ok != tt.wantRecord {
				t.Errorf("record kept = %v, want %v", ok, tt.wantRecord)
			}
			if tt.wantOutcome == OutcomeGone && r.Err != nil {
				t.Errorf("Err = %v, want none for a package that is gone", r.Err)
			}
			if len(s.inst.installed) != 0 {
				t.Errorf("installed = %v, want no files touched", s.inst.installed)
			}
		})
	}
}
