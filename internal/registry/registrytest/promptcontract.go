package registrytest

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/registry"
)

// runPromptContract covers the prompt methods of a registry.ProposalStore.
func runPromptContract(t *testing.T, newStore func(t *testing.T) ProposalBackend) {
	t.Helper()
	bob := registry.Actor{TokenID: "reviewer", Label: "bob", UserID: "u-bob"}
	later := proposedAt.Add(time.Hour)
	// create stores p1 with the two prompts of Proposal and returns it.
	create := func(t *testing.T, s ProposalBackend) registry.Proposal {
		t.Helper()
		p := Proposal(t, "p1", "lime", "planner", "0.2.0")
		if err := s.CreateProposal(t.Context(), p, []byte("x")); err != nil {
			t.Fatal(err)
		}
		return p
	}
	seqs := func(ps []registry.Prompt) []int {
		var out []int
		for _, p := range ps {
			out = append(out, p.Seq)
		}
		return out
	}

	t.Run("prompts: add, edit and remove keep their numbers", func(t *testing.T) {
		s := newStore(t)
		p := create(t, s)
		added, err := s.AddPrompt(t.Context(), "p1", registry.Prompt{Text: "plan ticket 3", Author: bob, CreatedAt: later}, 20, 60)
		if err != nil {
			t.Fatalf("AddPrompt() error: %v", err)
		}
		want3 := registry.Prompt{Seq: 3, Text: "plan ticket 3", Author: bob, CreatedAt: later}
		if diff := cmp.Diff(want3, added); diff != "" {
			t.Errorf("AddPrompt() mismatch (-want +got):\n%s", diff)
		}

		edited, err := s.EditPrompt(t.Context(), "p1", 3, "plan ticket 3, citing it", later.Add(time.Minute))
		if err != nil {
			t.Fatalf("EditPrompt() error: %v", err)
		}
		want3.Text, want3.EditedAt = "plan ticket 3, citing it", later.Add(time.Minute)
		if diff := cmp.Diff(want3, edited); diff != "" {
			t.Errorf("EditPrompt() mismatch (-want +got):\n%s", diff)
		}

		if err := s.RemovePrompt(t.Context(), "p1", 1, p.Author, later.Add(2*time.Minute)); err != nil {
			t.Fatalf("RemovePrompt() error: %v", err)
		}
		again, err := s.AddPrompt(t.Context(), "p1", registry.Prompt{Text: "plan ticket 4", Author: bob, CreatedAt: later}, 20, 60)
		if err != nil || again.Seq != 4 {
			t.Fatalf("AddPrompt() after a removal = %+v, %v; want Seq 4, never a reused number", again, err)
		}

		got, err := s.Proposal(t.Context(), "p1")
		if err != nil {
			t.Fatal(err)
		}
		removed := p.Prompts[0]
		removed.RemovedAt, removed.RemovedBy = later.Add(2*time.Minute), p.Author
		want := []registry.Prompt{p.Prompts[1], want3, again}
		if diff := cmp.Diff(want, got.Prompts); diff != "" {
			t.Errorf("Proposal().Prompts mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]registry.Prompt{removed}, got.RemovedPrompts); diff != "" {
			t.Errorf("Proposal().RemovedPrompts mismatch (-want +got):\n%s", diff)
		}
		if texts := got.PromptTexts(); !slices.Equal(texts, []string{"plan ticket 2", "plan ticket 3, citing it", "plan ticket 4"}) {
			t.Errorf("PromptTexts() = %q", texts)
		}
		list, err := s.Proposals(t.Context(), registry.ProposalFilter{Team: "lime", Limit: 10})
		if err != nil || len(list) != 1 || !slices.Equal(seqs(list[0].Prompts), []int{2, 3, 4}) || !slices.Equal(seqs(list[0].RemovedPrompts), []int{1}) {
			t.Errorf("Proposals() = %+v, %v; want prompts 2, 3, 4 and removed 1", list, err)
		}

		for _, seq := range []int{1, 99} {
			if _, err := s.EditPrompt(t.Context(), "p1", seq, "x", later); !errors.Is(err, registry.ErrNotFound) {
				t.Errorf("EditPrompt(%d) error = %v, want ErrNotFound", seq, err)
			}
			if err := s.RemovePrompt(t.Context(), "p1", seq, bob, later); !errors.Is(err, registry.ErrNotFound) {
				t.Errorf("RemovePrompt(%d) error = %v, want ErrNotFound", seq, err)
			}
		}
	})

	t.Run("prompts: limits on active prompts and on rows", func(t *testing.T) {
		s := newStore(t)
		create(t, s)
		const limit, maxRows = 3, 5
		add := func() error {
			_, err := s.AddPrompt(t.Context(), "p1", registry.Prompt{Text: "q", Author: bob, CreatedAt: later}, limit, maxRows)
			return err
		}
		remove := func(seq int) {
			t.Helper()
			if err := s.RemovePrompt(t.Context(), "p1", seq, bob, later); err != nil {
				t.Fatalf("RemovePrompt(%d) error: %v", seq, err)
			}
		}
		if err := add(); err != nil { // 3 active, 3 rows
			t.Fatal(err)
		}
		if err := add(); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("AddPrompt() over %d active error = %v, want ErrInvalid", limit, err)
		}
		remove(1)
		if err := add(); err != nil { // 3 active, 4 rows
			t.Fatalf("AddPrompt() after a removal error: %v", err)
		}
		remove(2)
		if err := add(); err != nil { // 3 active, 5 rows
			t.Fatalf("AddPrompt() at %d rows error: %v", maxRows-1, err)
		}
		remove(3)
		if err := add(); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("AddPrompt() at %d rows error = %v, want ErrInvalid", maxRows, err)
		}
	})

	t.Run("prompts: the last one stays", func(t *testing.T) {
		s := newStore(t)
		p := Proposal(t, "p1", "lime", "planner", "0.2.0")
		p.Prompts = p.Prompts[:1]
		if err := s.CreateProposal(t.Context(), p, []byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := s.RemovePrompt(t.Context(), "p1", 1, p.Author, later); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("RemovePrompt(last) error = %v, want ErrInvalid", err)
		}
		if got, err := s.Proposal(t.Context(), "p1"); err != nil || len(got.Prompts) != 1 || got.RemovedPrompts != nil {
			t.Errorf("Proposal() after refusing = %+v, %v; want the prompt still active", got, err)
		}
	})

	t.Run("prompts: closed or missing proposals", func(t *testing.T) {
		s := newStore(t)
		create(t, s)
		if _, err := s.DecideProposal(t.Context(), "p1", registry.Decision{Status: registry.ProposalWithdrawn, By: bob, At: later}); err != nil {
			t.Fatal(err)
		}
		for id, want := range map[string]error{"p1": registry.ErrProposalClosed, "nope": registry.ErrNotFound} {
			if _, err := s.AddPrompt(t.Context(), id, registry.Prompt{Text: "q", Author: bob, CreatedAt: later}, 20, 60); !errors.Is(err, want) {
				t.Errorf("AddPrompt(%s) error = %v, want %v", id, err, want)
			}
			if _, err := s.EditPrompt(t.Context(), id, 1, "q", later); !errors.Is(err, want) {
				t.Errorf("EditPrompt(%s) error = %v, want %v", id, err, want)
			}
			if err := s.RemovePrompt(t.Context(), id, 1, bob, later); !errors.Is(err, want) {
				t.Errorf("RemovePrompt(%s) error = %v, want %v", id, err, want)
			}
		}
	})

	t.Run("prompts: concurrent adds stay under the limit", func(t *testing.T) {
		s := newStore(t)
		create(t, s)
		const limit = 5
		var (
			wg  sync.WaitGroup
			mu  sync.Mutex
			got []int
		)
		for i := range 10 {
			wg.Go(func() {
				p, err := s.AddPrompt(t.Context(), "p1", registry.Prompt{Text: fmt.Sprint("q", i), Author: bob, CreatedAt: later}, limit, 60)
				switch {
				case err == nil:
					mu.Lock()
					got = append(got, p.Seq)
					mu.Unlock()
				case !errors.Is(err, registry.ErrInvalid):
					t.Errorf("concurrent AddPrompt() error: %v", err)
				}
			})
		}
		wg.Wait()
		slices.Sort(got)
		if !slices.Equal(got, []int{3, 4, 5}) {
			t.Errorf("concurrent adds got numbers %v, want 3, 4, 5", got)
		}
	})
}
