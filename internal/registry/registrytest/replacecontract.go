package registrytest

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/registry"
)

// errNotOne marks a listing that did not hold exactly one proposal.
var errNotOne = errors.New("not exactly one proposal")

// runReplaceContract checks ReplaceProposal: one step that closes the old
// proposal and stores the new one, or neither.
func runReplaceContract(t *testing.T, newStore func(t *testing.T) ProposalBackend, decision func(registry.ProposalStatus) registry.Decision) {
	t.Helper()
	carol := registry.Actor{TokenID: "carol", Label: "carol", UserID: "u-carol"}
	withdraw := func(note string) registry.Decision {
		d := decision(registry.ProposalWithdrawn)
		d.Note = note
		return d
	}
	// replacement is p2, a new proposal of lime/planner at version, made from
	// 0.1.1 and carrying a prompt Carol added (edited) and Alice's.
	replacement := func(t *testing.T, version string) registry.Proposal {
		t.Helper()
		p := Proposal(t, "p2", "lime", "planner", version)
		p.Base = BaseAt(t, "0.1.1")
		p.CreatedAt = proposedAt.Add(2 * time.Hour)
		p.Prompts = []registry.Prompt{
			{Seq: 3, Text: "carol's", Author: carol, CreatedAt: proposedAt.Add(time.Minute), EditedAt: proposedAt.Add(2 * time.Minute)},
			{Seq: 1, Text: "plan ticket 1", Author: p.Author, CreatedAt: proposedAt},
		}
		return p
	}

	t.Run("replace closes the old proposal and stores the new one, linked", func(t *testing.T) {
		s := newStore(t)
		old := Proposal(t, "p1", "lime", "planner", "0.2.0")
		old.Base = BaseAt(t, "0.1.0")
		if err := s.CreateProposal(t.Context(), old, []byte("old")); err != nil {
			t.Fatal(err)
		}
		in := replacement(t, "0.2.0") // the same version: the old one closes first
		if err := s.ReplaceProposal(t.Context(), "p1", in, []byte("rebased"), withdraw("replaced by p2")); err != nil {
			t.Fatalf("ReplaceProposal() error: %v", err)
		}
		gotOld, err := s.Proposal(t.Context(), "p1")
		if err != nil {
			t.Fatal(err)
		}
		if gotOld.Status != registry.ProposalWithdrawn || gotOld.ReplacedBy != "p2" || gotOld.DecisionNote != "replaced by p2" || gotOld.Replaces != "" {
			t.Errorf("old proposal after replace = %+v; want withdrawn, replaced by p2", gotOld)
		}
		want := in
		want.Replaces = "p1"
		want.Prompts = []registry.Prompt{in.Prompts[0], in.Prompts[1]}
		want.Prompts[0].Seq, want.Prompts[1].Seq = 1, 2
		for what, get := range map[string]func() (registry.Proposal, error){
			"Proposal()": func() (registry.Proposal, error) { return s.Proposal(t.Context(), "p2") },
			"Proposals()": func() (registry.Proposal, error) {
				list, err := s.Proposals(t.Context(), registry.ProposalFilter{Team: "lime", Status: registry.ProposalOpen, Limit: 10})
				if err != nil || len(list) != 1 {
					return registry.Proposal{}, fmt.Errorf("open proposals = %+v, want only p2: %w", list, errors.Join(err, errNotOne))
				}
				return list[0], nil
			},
		} {
			got, err := get()
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
			if diff := cmp.Diff(want, got, cmp.AllowUnexported(want.Package, want.Version)); diff != "" {
				t.Errorf("%s of the new proposal mismatch (-want +got):\n%s", what, diff)
			}
		}
		if data, err := s.ProposalBundle(t.Context(), "p2"); err != nil || !bytes.Equal(data, []byte("rebased")) {
			t.Errorf("ProposalBundle(p2) = %q, %v; want the rebased bundle", data, err)
		}
	})

	t.Run("replace refuses an old proposal that is not open", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateProposal(t.Context(), Proposal(t, "p1", "lime", "planner", "0.2.0"), []byte("old")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DecideProposal(t.Context(), "p1", decision(registry.ProposalRejected)); err != nil {
			t.Fatal(err)
		}
		if err := s.ReplaceProposal(t.Context(), "p1", replacement(t, "0.2.0"), []byte("x"), withdraw("")); !errors.Is(err, registry.ErrProposalClosed) {
			t.Errorf("ReplaceProposal(rejected) error = %v, want ErrProposalClosed", err)
		}
		if err := s.ReplaceProposal(t.Context(), "nope", replacement(t, "0.2.0"), []byte("x"), withdraw("")); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("ReplaceProposal(unknown) error = %v, want ErrNotFound", err)
		}
		if _, err := s.Proposal(t.Context(), "p2"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("a refused replace stored the new proposal: %v", err)
		}
	})

	t.Run("replace is one step: a refused new proposal leaves the old one open", func(t *testing.T) {
		s := newStore(t)
		for _, p := range []registry.Proposal{Proposal(t, "p1", "lime", "planner", "0.2.0"), Proposal(t, "p3", "lime", "planner", "0.3.0")} {
			if err := s.CreateProposal(t.Context(), p, []byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		// 0.3.0 already has an open proposal, p3.
		if err := s.ReplaceProposal(t.Context(), "p1", replacement(t, "0.3.0"), []byte("x"), withdraw("")); !errors.Is(err, registry.ErrVersionConflict) {
			t.Fatalf("ReplaceProposal(onto an open version) error = %v, want ErrVersionConflict", err)
		}
		if p, err := s.Proposal(t.Context(), "p1"); err != nil || p.Status != registry.ProposalOpen || p.ReplacedBy != "" {
			t.Errorf("old proposal after a failed replace = %+v, %v; want still open", p, err)
		}
		if _, err := s.Proposal(t.Context(), "p2"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("a failed replace stored the new proposal: %v", err)
		}
	})

	t.Run("of replaces and approvals racing, exactly one wins", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateProposal(t.Context(), Proposal(t, "p1", "lime", "planner", "0.2.0"), []byte("x")); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := range 8 {
			wg.Go(func() {
				if i%2 == 0 {
					_, _, err := s.ApproveProposal(t.Context(), "p1", decision(registry.ProposalApproved))
					errs <- err
					return
				}
				p := replacement(t, "0.2.0")
				p.ID = fmt.Sprintf("r%d", i)
				errs <- s.ReplaceProposal(t.Context(), "p1", p, []byte("x"), withdraw(""))
			})
		}
		wg.Wait()
		close(errs)
		ok := 0
		for err := range errs {
			switch {
			case err == nil:
				ok++
			case !errors.Is(err, registry.ErrProposalClosed):
				t.Errorf("racing replace or approve error: %v", err)
			}
		}
		if ok != 1 {
			t.Fatalf("%d of the racing replaces and approvals succeeded, want 1", ok)
		}
		open, err := s.Proposals(t.Context(), registry.ProposalFilter{Team: "lime", Status: registry.ProposalOpen, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		old, err := s.Proposal(t.Context(), "p1")
		if err != nil {
			t.Fatal(err)
		}
		switch old.Status {
		case registry.ProposalApproved:
			if len(open) != 0 {
				t.Errorf("approved, yet open proposals = %+v", open)
			}
		case registry.ProposalWithdrawn:
			if len(open) != 1 || open[0].ID != old.ReplacedBy || open[0].Replaces != "p1" {
				t.Errorf("replaced by %q, yet open proposals = %+v", old.ReplacedBy, open)
			}
		default:
			t.Errorf("old proposal is %s after the race", old.Status)
		}
	})
}
