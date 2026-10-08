package registrytest

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/registry"
)

// ProposalBackend is a store that keeps both releases and proposals, which
// approving needs in one transaction.
type ProposalBackend interface {
	registry.Store
	registry.ProposalStore
}

var proposedAt = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// Proposal returns an open proposal for tests.
func Proposal(t *testing.T, id, team, name, version string) registry.Proposal {
	t.Helper()
	rel := Release(t, team, name, version)
	author := registry.Actor{TokenID: "author", Label: "alice", UserID: "u-alice"}
	return registry.Proposal{
		ID:          id,
		Package:     rel.ID,
		Version:     rel.Version,
		Digest:      rel.Digest,
		Description: rel.Description,
		Size:        rel.Size,
		Author:      author,
		Note:        "why " + id,
		Prompts: []registry.Prompt{
			{Seq: 1, Text: "plan ticket 1", Author: author, CreatedAt: proposedAt},
			{Seq: 2, Text: "plan ticket 2", Author: author, CreatedAt: proposedAt},
		},
		Status:    registry.ProposalOpen,
		CreatedAt: proposedAt,
	}
}

// BaseAt returns the Base naming release version, for tests.
func BaseAt(t *testing.T, version string) registry.Base {
	t.Helper()
	b, err := registry.ParseBase(version)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RunProposalStoreContract runs the behaviour every registry.ProposalStore
// must have. newStore must return a fresh, empty store for each call.
func RunProposalStoreContract(t *testing.T, newStore func(t *testing.T) ProposalBackend) {
	t.Helper()
	reviewer := registry.Actor{TokenID: "reviewer", Label: "bob"}
	decider := registry.Actor{TokenID: "reviewer", Label: "bob", UserID: "u-bob"}
	decision := func(status registry.ProposalStatus) registry.Decision {
		return registry.Decision{Status: status, By: decider, At: proposedAt.Add(time.Hour), Note: "note"}
	}

	t.Run("create, get and bundle", func(t *testing.T) {
		s := newStore(t)
		in := Proposal(t, "p1", "lime", "planner", "0.2.0")
		if err := s.CreateProposal(t.Context(), in, []byte("candidate")); err != nil {
			t.Fatalf("CreateProposal() error: %v", err)
		}
		got, err := s.Proposal(t.Context(), "p1")
		if err != nil {
			t.Fatalf("Proposal() error: %v", err)
		}
		if diff := cmp.Diff(in, got, cmp.AllowUnexported(in.Package, in.Version)); diff != "" {
			t.Errorf("Proposal() mismatch (-want +got):\n%s", diff)
		}
		data, err := s.ProposalBundle(t.Context(), "p1")
		if err != nil || !bytes.Equal(data, []byte("candidate")) {
			t.Errorf("ProposalBundle() = %q, %v; want %q", data, err, "candidate")
		}
		if _, err := s.Releases(t.Context(), in.Package); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("a proposal created a release: Releases() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("base and links read back through every proposal query", func(t *testing.T) {
		s := newStore(t)
		based := Proposal(t, "p1", "lime", "planner", "0.3.0")
		based.Base = BaseAt(t, "0.1.0")
		// Only a decision or a replacement sets these; creating ignores them.
		based.ApprovedOver, based.Replaces, based.ReplacedBy = BaseAt(t, "0.2.0"), "p0", "p9"
		first := Proposal(t, "p2", "lime", "reviewer", "0.1.0") // before any release: no base
		for _, p := range []registry.Proposal{based, first} {
			if err := s.CreateProposal(t.Context(), p, []byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		want := map[string]registry.Base{"p1": BaseAt(t, "0.1.0"), "p2": {}}
		check := func(what string, got registry.Proposal) {
			t.Helper()
			if !got.Base.Equal(want[got.ID]) || !got.ApprovedOver.IsZero() || got.Replaces != "" || got.ReplacedBy != "" {
				t.Errorf("%s %s = base %q, over %q, replaces %q, replaced by %q; want base %q and no links",
					what, got.ID, got.Base, got.ApprovedOver, got.Replaces, got.ReplacedBy, want[got.ID])
			}
		}
		for id := range want {
			got, err := s.Proposal(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			check("Proposal()", got)
		}
		list, err := s.Proposals(t.Context(), registry.ProposalFilter{Team: "lime", Limit: 10})
		if err != nil || len(list) != 2 {
			t.Fatalf("Proposals() = %+v, %v", list, err)
		}
		for _, p := range list {
			check("Proposals()", p)
		}
		closed, err := s.DecideProposal(t.Context(), "p2", decision(registry.ProposalWithdrawn))
		if err != nil {
			t.Fatal(err)
		}
		check("DecideProposal()", closed)
		approved, _, err := s.ApproveProposal(t.Context(), "p1", decision(registry.ProposalApproved))
		if err != nil {
			t.Fatal(err)
		}
		check("ApproveProposal()", approved)
	})

	t.Run("not found", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.Proposal(t.Context(), "nope"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Proposal() error = %v, want ErrNotFound", err)
		}
		if _, err := s.ProposalBundle(t.Context(), "nope"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("ProposalBundle() error = %v, want ErrNotFound", err)
		}
		if _, err := s.DecideProposal(t.Context(), "nope", decision(registry.ProposalRejected)); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("DecideProposal() error = %v, want ErrNotFound", err)
		}
		if _, _, err := s.ApproveProposal(t.Context(), "nope", decision(registry.ProposalApproved)); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("ApproveProposal() error = %v, want ErrNotFound", err)
		}
		if _, _, err := s.ReviewText(t.Context(), "nope", 1); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("ReviewText() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("one open proposal per version", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateProposal(t.Context(), Proposal(t, "p1", "lime", "planner", "0.2.0"), []byte("a")); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateProposal(t.Context(), Proposal(t, "p2", "lime", "planner", "0.2.0"), []byte("b")); !errors.Is(err, registry.ErrVersionConflict) {
			t.Errorf("second open proposal error = %v, want ErrVersionConflict", err)
		}
		if err := s.CreateProposal(t.Context(), Proposal(t, "p3", "lime", "planner", "0.3.0"), []byte("c")); err != nil {
			t.Errorf("another version error = %v, want nil", err)
		}
		if _, err := s.DecideProposal(t.Context(), "p1", decision(registry.ProposalWithdrawn)); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateProposal(t.Context(), Proposal(t, "p4", "lime", "planner", "0.2.0"), []byte("d")); err != nil {
			t.Errorf("proposal after withdrawing the open one error = %v, want nil", err)
		}
	})

	t.Run("list filters, newest first", func(t *testing.T) {
		s := newStore(t)
		for i, p := range []registry.Proposal{
			Proposal(t, "p1", "lime", "planner", "0.2.0"),
			Proposal(t, "p2", "lime", "reviewer", "1.0.0"),
			Proposal(t, "p3", "other", "planner", "0.2.0"),
			Proposal(t, "p4", "lime", "planner", "0.3.0"),
		} {
			p.CreatedAt = proposedAt.Add(time.Duration(i) * time.Minute)
			if err := s.CreateProposal(t.Context(), p, []byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.DecideProposal(t.Context(), "p4", decision(registry.ProposalRejected)); err != nil {
			t.Fatal(err)
		}
		planner := Release(t, "lime", "planner", "0.1.0").ID
		tests := []struct {
			name   string
			filter registry.ProposalFilter
			want   []string
		}{
			{"team", registry.ProposalFilter{Team: "lime", Limit: 10}, []string{"p4", "p2", "p1"}},
			{"open", registry.ProposalFilter{Team: "lime", Status: registry.ProposalOpen, Limit: 10}, []string{"p2", "p1"}},
			{"package", registry.ProposalFilter{Team: "lime", Package: planner, Limit: 10}, []string{"p4", "p1"}},
			{"rejected", registry.ProposalFilter{Team: "lime", Status: registry.ProposalRejected, Limit: 10}, []string{"p4"}},
			{"limit", registry.ProposalFilter{Team: "lime", Limit: 1}, []string{"p4"}},
			{"other team", registry.ProposalFilter{Team: "nobody", Limit: 10}, nil},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				props, err := s.Proposals(t.Context(), tc.filter)
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, p := range props {
					got = append(got, p.ID)
				}
				if !slices.Equal(got, tc.want) {
					t.Errorf("Proposals(%+v) = %v, want %v", tc.filter, got, tc.want)
				}
			})
		}
	})

	t.Run("decide closes once", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateProposal(t.Context(), Proposal(t, "p1", "lime", "planner", "0.2.0"), []byte("x")); err != nil {
			t.Fatal(err)
		}
		d := decision(registry.ProposalRejected)
		got, err := s.DecideProposal(t.Context(), "p1", d)
		if err != nil {
			t.Fatalf("DecideProposal() error: %v", err)
		}
		if got.Status != registry.ProposalRejected || got.DecidedBy != decider || !got.DecidedAt.Equal(d.At) || got.DecisionNote != "note" {
			t.Errorf("DecideProposal() = %+v, want rejected by %v at %v", got, reviewer, d.At)
		}
		if again, err := s.Proposal(t.Context(), "p1"); err != nil || again.Status != registry.ProposalRejected || !again.DecidedAt.Equal(d.At) {
			t.Errorf("Proposal() after reject = %+v, %v", again, err)
		}
		for _, status := range []registry.ProposalStatus{registry.ProposalWithdrawn, registry.ProposalRejected} {
			if _, err := s.DecideProposal(t.Context(), "p1", decision(status)); !errors.Is(err, registry.ErrProposalClosed) {
				t.Errorf("DecideProposal(%s) on closed error = %v, want ErrProposalClosed", status, err)
			}
		}
		if _, _, err := s.ApproveProposal(t.Context(), "p1", decision(registry.ProposalApproved)); !errors.Is(err, registry.ErrProposalClosed) {
			t.Errorf("ApproveProposal() on closed error = %v, want ErrProposalClosed", err)
		}
	})

	t.Run("approve publishes atomically", func(t *testing.T) {
		s := newStore(t)
		before, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", "0.1.0"), []byte("old"))
		if err != nil {
			t.Fatal(err)
		}
		in := Proposal(t, "p1", "lime", "planner", "0.2.0")
		if err := s.CreateProposal(t.Context(), in, []byte("candidate")); err != nil {
			t.Fatal(err)
		}
		d := decision(registry.ProposalApproved)
		d.Latest = BaseAt(t, "0.1.0")
		p, rel, err := s.ApproveProposal(t.Context(), "p1", d)
		if err != nil {
			t.Fatalf("ApproveProposal() error: %v", err)
		}
		if p.Status != registry.ProposalApproved || p.DecidedBy != decider {
			t.Errorf("ApproveProposal() proposal = %+v, want approved by %v", p, reviewer)
		}
		if rel.Seq <= before.Seq || rel.Version != in.Version || rel.Digest != in.Digest || !rel.PublishedAt.Equal(d.At) {
			t.Errorf("ApproveProposal() release = %+v, want %s with Seq > %d published at %v", rel, in.Version, before.Seq, d.At)
		}
		got, data, err := s.Release(t.Context(), in.Package, in.Version)
		if err != nil || !bytes.Equal(data, []byte("candidate")) || got.Seq != rel.Seq || got.Description != in.Description || got.Size != in.Size {
			t.Errorf("Release() after approve = %+v, %q, %v; want the candidate", got, data, err)
		}
		changes, err := s.Changes(t.Context(), "lime", before.Seq, 10)
		if err != nil || len(changes) != 1 || changes[0].Version != in.Version {
			t.Errorf("Changes() after approve = %+v, %v; want the approved version", changes, err)
		}
	})

	t.Run("approve records where a release came from and whether review was skipped", func(t *testing.T) {
		for _, self := range []bool{false, true} {
			t.Run(fmt.Sprint("self approved ", self), func(t *testing.T) {
				s := newStore(t)
				direct, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", "0.1.0"), []byte("old"))
				if err != nil {
					t.Fatal(err)
				}
				in := Proposal(t, "p1", "lime", "planner", "0.2.0")
				if err := s.CreateProposal(t.Context(), in, []byte("candidate")); err != nil {
					t.Fatal(err)
				}
				d := decision(registry.ProposalApproved)
				d.SelfApproved = self
				d.Latest = BaseAt(t, "0.1.0")
				p, rel, err := s.ApproveProposal(t.Context(), "p1", d)
				if err != nil {
					t.Fatalf("ApproveProposal() error: %v", err)
				}
				if p.SelfApproved != self || rel.SelfApproved != self || rel.Proposal != "p1" || rel.PublishedBy != decider {
					t.Errorf("ApproveProposal() = proposal self %v, release %+v; want self %v from p1", p.SelfApproved, rel, self)
				}
				if got, err := s.Proposal(t.Context(), "p1"); err != nil || got.SelfApproved != self {
					t.Errorf("Proposal() after approve: self %v, %v; want %v", got.SelfApproved, err, self)
				}
				if got, err := s.Proposals(t.Context(), registry.ProposalFilter{Team: "lime", Limit: 10}); err != nil || len(got) != 1 || got[0].SelfApproved != self {
					t.Errorf("Proposals() after approve = %+v, %v; want self %v", got, err, self)
				}
				// Every release query reads the record back.
				check := func(what string, got registry.Release) {
					t.Helper()
					// The approver released it; the proposal leads to the author.
					if got.Proposal != "p1" || got.SelfApproved != self || got.PublishedBy != decider {
						t.Errorf("%s = proposal %q, self %v, by %+v; want p1, %v, by %+v", what, got.Proposal, got.SelfApproved, got.PublishedBy, self, decider)
					}
				}
				got, _, err := s.Release(t.Context(), in.Package, in.Version)
				if err != nil {
					t.Fatal(err)
				}
				check("Release()", got)
				rels, err := s.Releases(t.Context(), in.Package)
				if err != nil || len(rels) != 2 {
					t.Fatalf("Releases() = %+v, %v", rels, err)
				}
				check("Releases()[1]", rels[1])
				if rels[0].Proposal != "" || rels[0].SelfApproved || rels[0].PublishedBy != (registry.Actor{}) {
					t.Errorf("Releases()[0], published directly = %+v; want no proposal", rels[0])
				}
				pkgs, err := s.Packages(t.Context(), "lime")
				if err != nil || len(pkgs) != 1 {
					t.Fatalf("Packages() = %+v, %v", pkgs, err)
				}
				check("Packages()[0]", pkgs[0])
				changes, err := s.Changes(t.Context(), "lime", direct.Seq, 10)
				if err != nil || len(changes) != 1 {
					t.Fatalf("Changes() = %+v, %v", changes, err)
				}
				check("Changes()[0]", changes[0])
			})
		}
	})

	t.Run("approve refuses a version no longer above the latest", func(t *testing.T) {
		for _, published := range []string{"0.2.0", "0.3.0"} {
			t.Run(published, func(t *testing.T) {
				s := newStore(t)
				if err := s.CreateProposal(t.Context(), Proposal(t, "p1", "lime", "planner", "0.2.0"), []byte("candidate")); err != nil {
					t.Fatal(err)
				}
				if _, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", published), []byte("published")); err != nil {
					t.Fatal(err)
				}
				if _, _, err := s.ApproveProposal(t.Context(), "p1", decision(registry.ProposalApproved)); !errors.Is(err, registry.ErrVersionConflict) {
					t.Fatalf("ApproveProposal() error = %v, want ErrVersionConflict", err)
				}
				p, err := s.Proposal(t.Context(), "p1")
				if err != nil || p.Status != registry.ProposalOpen {
					t.Errorf("after a failed approve Proposal() = %+v, %v; want still open", p, err)
				}
				rels, err := s.Releases(t.Context(), p.Package)
				if err != nil || len(rels) != 1 {
					t.Errorf("after a failed approve Releases() = %+v, %v; want only the published one", rels, err)
				}
			})
		}
	})

	t.Run("approve checks the latest release it saw and records approving over it", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", "0.1.0"), []byte("old")); err != nil {
			t.Fatal(err)
		}
		in := Proposal(t, "p1", "lime", "planner", "0.3.0")
		in.Base = BaseAt(t, "0.1.0")
		if err := s.CreateProposal(t.Context(), in, []byte("candidate")); err != nil {
			t.Fatal(err)
		}
		// 0.2.0 lands after the approver read 0.1.0 as the latest.
		if _, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", "0.2.0"), []byte("newer")); err != nil {
			t.Fatal(err)
		}
		for _, latest := range []registry.Base{BaseAt(t, "0.1.0"), {}} {
			d := decision(registry.ProposalApproved)
			d.Latest = latest
			if _, _, err := s.ApproveProposal(t.Context(), "p1", d); !errors.Is(err, registry.ErrStale) {
				t.Fatalf("ApproveProposal(latest %q) error = %v, want ErrStale", latest, err)
			}
		}
		if p, err := s.Proposal(t.Context(), "p1"); err != nil || p.Status != registry.ProposalOpen {
			t.Fatalf("after a stale approve Proposal() = %+v, %v; want still open", p, err)
		}
		if rels, err := s.Releases(t.Context(), in.Package); err != nil || len(rels) != 2 {
			t.Fatalf("after a stale approve Releases() = %d, %v; want 0.1.0 and 0.2.0 only", len(rels), err)
		}

		d := decision(registry.ProposalApproved)
		d.Latest, d.Over = BaseAt(t, "0.2.0"), BaseAt(t, "0.2.0")
		p, _, err := s.ApproveProposal(t.Context(), "p1", d)
		if err != nil {
			t.Fatalf("ApproveProposal(over 0.2.0) error: %v", err)
		}
		check := func(what string, got registry.Proposal) {
			t.Helper()
			if got.ApprovedOver.String() != "0.2.0" || got.Base.String() != "0.1.0" {
				t.Errorf("%s = base %q, approved over %q; want 0.1.0 approved over 0.2.0", what, got.Base, got.ApprovedOver)
			}
		}
		check("ApproveProposal()", p)
		got, err := s.Proposal(t.Context(), "p1")
		if err != nil {
			t.Fatal(err)
		}
		check("Proposal()", got)
		list, err := s.Proposals(t.Context(), registry.ProposalFilter{Team: "lime", Limit: 10})
		if err != nil || len(list) != 1 {
			t.Fatalf("Proposals() = %+v, %v", list, err)
		}
		check("Proposals()", list[0])
	})

	t.Run("concurrent approvals publish once", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateProposal(t.Context(), Proposal(t, "p1", "lime", "planner", "0.2.0"), []byte("candidate")); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				_, _, err := s.ApproveProposal(t.Context(), "p1", decision(registry.ProposalApproved))
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		ok := 0
		for err := range errs {
			switch {
			case err == nil:
				ok++
			case !errors.Is(err, registry.ErrProposalClosed) && !errors.Is(err, registry.ErrVersionConflict):
				t.Errorf("concurrent ApproveProposal() error: %v", err)
			}
		}
		if ok != 1 {
			t.Errorf("%d concurrent approvals succeeded, want 1", ok)
		}
	})

	runPromptContract(t, newStore)
	runReplaceContract(t, newStore, decision)

	t.Run("reviews", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateProposal(t.Context(), Proposal(t, "p1", "lime", "planner", "0.2.0"), []byte("x")); err != nil {
			t.Fatal(err)
		}
		data := []byte(`{"prompts":[]}`)
		for i := 1; i <= 2; i++ {
			text := fmt.Sprintf("report %d", i)
			var d []byte // the first is text only, the second has data
			if i == 2 {
				d = data
			}
			r, err := s.AddReview(t.Context(), "p1", registry.Review{Author: decider, CreatedAt: proposedAt, Size: len(text), DataSize: len(d)}, []byte(text), d, 2)
			if err != nil {
				t.Fatalf("AddReview(%d) error: %v", i, err)
			}
			if r.Seq != i {
				t.Errorf("AddReview(%d) Seq = %d, want %d", i, r.Seq, i)
			}
		}
		if _, err := s.AddReview(t.Context(), "p1", registry.Review{Author: decider, CreatedAt: proposedAt, Size: 1}, []byte("x"), nil, 2); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("AddReview() over the limit error = %v, want ErrInvalid", err)
		}
		r, text, err := s.ReviewText(t.Context(), "p1", 2)
		if err != nil || string(text) != "report 2" || r.Seq != 2 || r.Author != decider || r.Size != len("report 2") || !r.CreatedAt.Equal(proposedAt) {
			t.Errorf("ReviewText(2) = %+v, %q, %v; want report 2", r, text, err)
		}
		if _, _, err := s.ReviewText(t.Context(), "p1", 3); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("ReviewText(3) error = %v, want ErrNotFound", err)
		}
		r, got, err := s.ReviewData(t.Context(), "p1", 2)
		if err != nil || string(got) != string(data) || r.Seq != 2 || r.DataSize != len(data) || r.Size != len("report 2") || r.Author != decider {
			t.Errorf("ReviewData(2) = %+v, %q, %v; want %q", r, got, err, data)
		}
		for _, seq := range []int{1, 3} {
			if _, _, err := s.ReviewData(t.Context(), "p1", seq); !errors.Is(err, registry.ErrNotFound) {
				t.Errorf("ReviewData(%d) error = %v, want ErrNotFound", seq, err)
			}
		}
		p, err := s.Proposal(t.Context(), "p1")
		if err != nil || len(p.Reviews) != 2 || p.Reviews[0].Seq != 1 || p.Reviews[1].Author != decider || p.Reviews[0].DataSize != 0 || p.Reviews[1].DataSize != len(data) {
			t.Errorf("Proposal().Reviews = %+v, %v; want two reviews by %v, the second with data", p.Reviews, err, decider)
		}
		// A listing carries the same summaries, so a list can count reports.
		if list, err := s.Proposals(t.Context(), registry.ProposalFilter{Team: "lime", Limit: 10}); err != nil || len(list) != 1 || cmp.Diff(p.Reviews, list[0].Reviews) != "" {
			t.Errorf("Proposals().Reviews = %+v, %v; want %+v", list, err, p.Reviews)
		}

		if _, err := s.DecideProposal(t.Context(), "p1", decision(registry.ProposalWithdrawn)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddReview(t.Context(), "p1", registry.Review{Author: decider, CreatedAt: proposedAt, Size: 1}, []byte("x"), nil, 5); !errors.Is(err, registry.ErrProposalClosed) {
			t.Errorf("AddReview() on a closed proposal error = %v, want ErrProposalClosed", err)
		}
	})
}
