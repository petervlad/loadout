package registrytest

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// CountBackend is a store that keeps everything registry.CountStore counts.
type CountBackend interface {
	registry.Store
	registry.TokenStore
	registry.NamespaceStore
	registry.ProposalStore
	registry.CountStore
}

// RunCountStoreContract runs the behaviour every registry.CountStore must
// have. newStore must return a fresh, empty store for each call.
func RunCountStoreContract(t *testing.T, newStore func(t *testing.T) CountBackend) {
	t.Helper()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	t.Run("empty", func(t *testing.T) {
		got, err := newStore(t).Counts(t.Context(), now)
		if err != nil {
			t.Fatalf("Counts() error: %v", err)
		}
		if got.Releases != 0 || got.ReleaseBytes != 0 || len(got.Namespaces) != 0 || len(got.Packages) != 0 || got.MachineTokens != 0 {
			t.Errorf("Counts() of an empty store = %+v, want zeros", got)
		}
	})

	t.Run("counts everything", func(t *testing.T) {
		s := newStore(t)
		ctx := t.Context()
		for _, ns := range []registry.Namespace{
			{Name: "acme", Kind: registry.KindProject, CreatedAt: now},
			{Name: "lime", Parent: "acme", Kind: registry.KindTeam, CreatedAt: now},
			{Name: "koala", Parent: "acme", Kind: registry.KindTeam, CreatedAt: now},
		} {
			if err := s.CreateNamespace(ctx, ns); err != nil {
				t.Fatal(err)
			}
		}
		put := func(team, name, version string, kind pack.Kind, bundle string) {
			t.Helper()
			rel := Release(t, team, name, version)
			rel.Kind = kind
			if _, err := s.PutRelease(ctx, rel, []byte(bundle)); err != nil {
				t.Fatal(err)
			}
		}
		// planner was an agent and became a skill: it counts once, as a
		// skill.
		put("lime", "planner", "1.0.0", pack.KindAgent, "aaaa")
		put("lime", "planner", "1.1.0", pack.KindSkill, "bbbbbb")
		put("lime", "reviewer", "1.0.0", pack.KindAgent, "cc")
		put("acme", "docs", "2.0.0", pack.KindKB, "dddddddddd")

		if err := s.CreateProposal(ctx, Proposal(t, "p1", "lime", "reviewer", "1.1.0"), []byte("12345")); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateProposal(ctx, Proposal(t, "p2", "lime", "planner", "1.2.0"), []byte("123")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddReview(ctx, "p1", registry.Review{Author: registry.Actor{TokenID: "r", Label: "bob"}, CreatedAt: now, Size: 4, DataSize: 2}, []byte("text"), []byte("{}"), 5); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DecideProposal(ctx, "p2", registry.Decision{Status: registry.ProposalRejected, By: registry.Actor{TokenID: "r", Label: "bob"}, At: now, Note: "no"}); err != nil {
			t.Fatal(err)
		}

		for i, tok := range []registry.Token{
			{ID: "t1", Team: "lime", Scope: registry.ScopePublish, Label: "ci", CreatedAt: now},
			{ID: "t2", Team: "acme", Scope: registry.ScopeRead, Label: "ci", CreatedAt: now, CreatedBy: "u-alice"},
			{ID: "t3", Team: registry.AccountNamespace, Scope: registry.ScopePublish, Label: "alice", CreatedAt: now, UserID: "u-alice", ExpiresAt: now.Add(time.Hour)},
			{ID: "t4", Team: registry.AccountNamespace, Scope: registry.ScopePublish, Label: "bob", CreatedAt: now, UserID: "u-bob", ExpiresAt: now.Add(-time.Hour)},
		} {
			if err := s.CreateToken(ctx, tok, []byte{byte(i), 1, 2, 3}); err != nil {
				t.Fatal(err)
			}
		}

		got, err := s.Counts(ctx, now)
		if err != nil {
			t.Fatalf("Counts() error: %v", err)
		}
		want := registry.Counts{
			Namespaces:    map[registry.NamespaceKind]int{registry.KindProject: 1, registry.KindTeam: 2},
			Packages:      map[pack.Kind]int{pack.KindSkill: 1, pack.KindAgent: 1, pack.KindKB: 1},
			Releases:      4,
			ReleaseBytes:  22,
			Proposals:     map[registry.ProposalStatus]int{registry.ProposalOpen: 1, registry.ProposalRejected: 1},
			ProposalBytes: 8,
			Reviews:       1,
			ReviewBytes:   6,
			MachineTokens: 2,
			LoginTokens:   1,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Counts() mismatch (-want +got):\n%s", diff)
		}
	})
}
