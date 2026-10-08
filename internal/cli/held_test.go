package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/update"
)

func heldResult(t *testing.T, outcome update.Outcome, scope target.Scope, held ...syncstate.Blocker) update.Result {
	t.Helper()
	id, err := pack.ParseID("payments/planner")
	if err != nil {
		t.Fatal(err)
	}
	return update.Result{
		Entry:   lock.Entry{ID: id, Version: semver.New(1, 2, 0), Scope: scope, Policy: lock.PolicyAuto},
		Latest:  semver.New(1, 3, 0),
		Outcome: outcome,
		Held:    held,
	}
}

var (
	userScope    = target.Scope{Kind: target.ScopeUser, Root: "/home/a"}
	projectScope = target.Scope{Kind: target.ScopeProject, Root: "/src/billing"}
	editedAgent  = syncstate.Blocker{Path: ".claude/agents/planner.md", Reason: string(install.ReasonModified)}
)

func TestPrintSyncHeld(t *testing.T) {
	tests := []struct {
		name     string
		result   update.Result
		want     []string
		wantNot  []string
		wantCode int
	}{
		{
			name:   "auto, user scope",
			result: heldResult(t, update.OutcomeBlocked, userScope, editedAgent),
			want: []string{
				"held back by your edits",
				"payments/planner 1.3.0 is held back.\nEdited since loadout wrote it:\n  ~/.claude/agents/planner.md\n",
				"Keep your edits and take 1.3.0:   loadout install payments/planner --merge\n" +
					"Replace your edits with 1.3.0:    loadout install payments/planner --force\n" +
					"Keep your version, stop updates:  loadout policy payments/planner pin\n" +
					"See what changed:                 loadout diff payments/planner\n",
			},
			wantNot:  []string{"Run these in"},
			wantCode: exitHeld,
		},
		{
			name:   "auto, project scope",
			result: heldResult(t, update.OutcomeBlocked, projectScope, editedAgent),
			want: []string{
				"  /src/billing/.claude/agents/planner.md\n",
				"loadout install payments/planner --merge --scope project\n",
				"loadout install payments/planner --force --scope project\n",
				"loadout policy payments/planner pin --scope project\n",
				"Run these in /src/billing.\n",
			},
			wantCode: exitHeld,
		},
		{
			name:   "a file loadout did not write",
			result: heldResult(t, update.OutcomeBlocked, userScope, syncstate.Blocker{Path: ".claude/agents/notes.md", Reason: string(install.ReasonUnmanaged)}),
			want: []string{
				"held back by files in the way",
				"Not written by loadout:\n  ~/.claude/agents/notes.md\n",
				"Replace these files with 1.3.0:",
			},
			wantNot:  []string{"--merge"},
			wantCode: exitHeld,
		},
		{
			name: "another package: --force cannot help",
			result: heldResult(t, update.OutcomeBlocked, userScope, editedAgent,
				syncstate.Blocker{Path: ".claude/agents/x.md", Reason: string(install.ReasonOtherPackage), Owner: "payments/x"}),
			want: []string{
				"Belongs to another package of the same layer payments/x:\n  ~/.claude/agents/x.md\n",
				"--force cannot replace all of these files",
			},
			wantNot:  []string{"--force\n"},
			wantCode: exitHeld,
		},
		{
			name:     "notify with edits exits 0",
			result:   heldResult(t, update.OutcomeAvailable, userScope, editedAgent),
			want:     []string{"update available, you edited 1 file", "payments/planner 1.3.0 is out, and your edits would hold it back."},
			wantCode: 0,
		},
		{
			name: "model settings held back",
			result: func() update.Result {
				r := heldResult(t, update.OutcomeBlocked, userScope, editedAgent)
				r.Latest = r.Entry.Version
				return r
			}(),
			want:     []string{"model settings held back by your edits", "your team's new model settings are held back.", "Replace your edits with the new settings:"},
			wantNot:  []string{"loadout diff", "--merge"},
			wantCode: exitHeld,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			err := printSync(&out, []update.Result{tc.result})
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(out.String(), w) {
					t.Errorf("output has %q:\n%s", w, out.String())
				}
			}
			if got := codeOf(err); got != tc.wantCode {
				t.Errorf("exit code = %d (%v), want %d", got, err, tc.wantCode)
			}
		})
	}
}

func TestPrintSyncFailureWinsOverHeld(t *testing.T) {
	failed := heldResult(t, update.OutcomeFailed, userScope)
	failed.Err = errors.New("timeout")
	results := []update.Result{heldResult(t, update.OutcomeBlocked, userScope, editedAgent), heldResult(t, update.OutcomeBlocked, userScope, editedAgent), failed}
	var out strings.Builder
	if err := printSync(&out, results); codeOf(err) != 1 {
		t.Errorf("a failed check with held updates: exit %d (%v), want 1", codeOf(err), err)
	}
	if err := printSync(&out, results[:2]); codeOf(err) != exitHeld || !strings.Contains(err.Error(), "2 updates are held back") {
		t.Errorf("two held: %v, want exit 2 naming both", err)
	}
}

// codeOf is the exit code main gives err.
func codeOf(err error) int {
	if err == nil {
		return 0
	}
	if ec, ok := errors.AsType[exitError](err); ok {
		return ec.ExitCode()
	}
	return 1
}
