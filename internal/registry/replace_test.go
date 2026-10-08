package registry_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/registry"
)

func TestProposeReplaces(t *testing.T) {
	var (
		alice      = person("u-alice", registry.ScopeRead, limeLayers)
		carol      = person("u-carol", registry.ScopeRead, limeLayers)
		maintainer = person("u-maint", registry.ScopePublish, limeLayers)
		aliceCI    = registry.Principal{TokenID: "ci", Team: "lime", Scope: registry.ScopeRead, Layers: limeLayers, Label: "ci", CreatedBy: "u-alice"}
	)
	many := make([]string, 18)
	for i := range many {
		many[i] = fmt.Sprintf("prompt %d", i)
	}
	tests := []struct {
		name        string
		caller      registry.Principal
		data        func(t *testing.T) []byte
		prompts     []string
		closeFirst  bool
		replaces    string // "" means the proposal made in the test
		wantErr     error
		wantPrompts []string // text by author
	}{
		{
			"author, prompts carried", alice, planner, nil, false, "", nil,
			[]string{"plan A by alice", "plan B by alice", "carol's by carol"},
		},
		{
			"author, given prompts appended once", alice, planner,
			[]string{"plan B", "plan D"},
			false, "", nil,
			[]string{"plan A by alice", "plan B by alice", "carol's by carol", "plan D by alice"},
		},
		{
			"the author's machine token", aliceCI, planner, nil, false, "", nil,
			[]string{"plan A by alice", "plan B by alice", "carol's by carol"},
		},
		{"another member", carol, planner, nil, false, "", registry.ErrForbidden, nil},
		{"a maintainer", maintainer, planner, nil, false, "", registry.ErrForbidden, nil},
		{"a closed proposal", alice, planner, nil, true, "", registry.ErrProposalClosed, nil},
		{"another package", alice, func(t *testing.T) []byte { t.Helper(); return bundleNamed(t, "lime", "reviewer", "0.2.0") }, nil, false, "", registry.ErrInvalid, nil},
		{"no such proposal", alice, planner, nil, false, "abcdefabcdef", registry.ErrNotFound, nil},
		{"over the prompt limit with the carried ones", alice, planner, many, false, "", registry.ErrInvalid, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, props := newOwnerFixture(nil)
			if _, err := svc.Publish(t.Context(), maintainer, bundleFor(t, "lime", "0.1.0")); err != nil {
				t.Fatal(err)
			}
			old, err := props.Propose(t.Context(), alice, bundleFor(t, "lime", "0.2.0"), registry.ProposalInput{Prompts: []string{"plan A", "plan B"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := props.AddPrompt(t.Context(), carol, old.ID, "carol's"); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Publish(t.Context(), maintainer, bundleFor(t, "lime", "0.1.1")); err != nil {
				t.Fatal(err)
			}
			if tc.closeFirst {
				if _, err := props.Withdraw(t.Context(), alice, old.ID, ""); err != nil {
					t.Fatal(err)
				}
			}
			replaces := old.ID
			if tc.replaces != "" {
				replaces = tc.replaces
			}
			got, err := props.Propose(t.Context(), tc.caller, tc.data(t), registry.ProposalInput{Prompts: tc.prompts, Replaces: replaces})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Propose(replaces) error = %v, want %v", err, tc.wantErr)
			}
			before, rerr := props.Proposal(t.Context(), alice, old.ID)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if err != nil {
				if !tc.closeFirst && (before.Status != registry.ProposalOpen || before.ReplacedBy != "") {
					t.Errorf("after a refused replace the old proposal = %+v; want still open", before)
				}
				return
			}
			if before.Status != registry.ProposalWithdrawn || before.ReplacedBy != got.ID || before.DecisionNote != "replaced by "+got.ID {
				t.Errorf("old proposal = %s, replaced by %q, note %q; want withdrawn, replaced by %s", before.Status, before.ReplacedBy, before.DecisionNote, got.ID)
			}
			stored, err := props.Proposal(t.Context(), alice, got.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Replaces != old.ID || stored.Base.String() != "0.1.1" || stored.Version.String() != "0.2.0" {
				t.Errorf("new proposal = replaces %q, base %q, version %s; want %s, 0.1.1, 0.2.0", stored.Replaces, stored.Base, stored.Version, old.ID)
			}
			var texts []string
			for i, p := range stored.Prompts {
				if p.Seq != i+1 {
					t.Errorf("prompt %q has number %d, want %d", p.Text, p.Seq, i+1)
				}
				texts = append(texts, p.Text+" by "+strings.TrimPrefix(p.Author.UserID, "u-"))
			}
			if fmt.Sprint(texts) != fmt.Sprint(tc.wantPrompts) {
				t.Errorf("new proposal prompts = %q, want %q", texts, tc.wantPrompts)
			}
		})
	}
}

func planner(t *testing.T) []byte {
	t.Helper()
	return bundleFor(t, "lime", "0.2.0")
}
