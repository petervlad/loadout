package registrytest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
)

// DeleteBackend is a store that keeps what DeletePackage removes from: the
// releases, the proposals with their reviews and prompts, and the
// namespaces whose course it may be.
type DeleteBackend interface {
	registry.Store
	registry.ProposalStore
	registry.NamespaceStore
}

// RunPackageDeleteContract runs the behaviour of Store.PackageFootprint and
// Store.DeletePackage every implementation must have. newStore must return
// a fresh, empty store for each call.
func RunPackageDeleteContract(t *testing.T, newStore func(t *testing.T) DeleteBackend) {
	t.Helper()
	ctx := t.Context()
	author := registry.Actor{TokenID: "author", Label: "alice", UserID: "u-alice"}
	decide := func(status registry.ProposalStatus) registry.Decision {
		return registry.Decision{Status: status, By: registry.Actor{TokenID: "bob", UserID: "u-bob"}, At: proposedAt.Add(time.Hour)}
	}
	id := func(team, name string) pack.ID {
		t.Helper()
		i, err := pack.NewID(team, name)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	ver := func(s string) semver.Version {
		t.Helper()
		v, err := semver.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	put := func(t *testing.T, s DeleteBackend, team, name, version string, kind pack.Kind) registry.Release {
		t.Helper()
		rel := Release(t, team, name, version)
		rel.Kind = kind
		got, err := s.PutRelease(ctx, rel, []byte("bundle"))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	// propose stores an open proposal made n minutes after proposedAt, so
	// that "oldest first" has something to order by.
	propose := func(t *testing.T, s DeleteBackend, pid, team, name, version string, n int) {
		t.Helper()
		p := Proposal(t, pid, team, name, version)
		p.Kind = pack.KindAgent
		p.CreatedAt = proposedAt.Add(time.Duration(n) * time.Minute)
		for i := range p.Prompts {
			p.Prompts[i].CreatedAt = p.CreatedAt
		}
		if err := s.CreateProposal(ctx, p, []byte("candidate")); err != nil {
			t.Fatal(err)
		}
	}
	review := func(t *testing.T, s DeleteBackend, pid string) {
		t.Helper()
		if _, err := s.AddReview(ctx, pid, registry.Review{Author: author, CreatedAt: proposedAt, Size: 4}, []byte("text"), nil, registry.MaxReviews); err != nil {
			t.Fatal(err)
		}
	}
	notFound := func(t *testing.T, what string, err error) {
		t.Helper()
		if !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("%s error = %v, want ErrNotFound", what, err)
		}
	}
	diff := func(want, got registry.PackageFootprint) string {
		return cmp.Diff(want, got, cmp.AllowUnexported(want.ID, semver.Version{}))
	}
	// gone checks nothing of id is left to find.
	gone := func(t *testing.T, s DeleteBackend, pid pack.ID, proposalIDs ...string) {
		t.Helper()
		_, err := s.Releases(ctx, pid)
		notFound(t, "Releases() after delete", err)
		_, err = s.PackageFootprint(ctx, pid)
		notFound(t, "PackageFootprint() after delete", err)
		for _, p := range proposalIDs {
			_, err := s.Proposal(ctx, p)
			notFound(t, "Proposal("+p+") after delete", err)
			_, _, err = s.ReviewText(ctx, p, 1)
			notFound(t, "ReviewText("+p+") after delete", err)
			_, err = s.ProposalBundle(ctx, p)
			notFound(t, "ProposalBundle("+p+") after delete", err)
		}
		list, err := s.Proposals(ctx, registry.ProposalFilter{Team: pid.Team(), Package: pid, Limit: 50})
		if err != nil || len(list) != 0 {
			t.Errorf("Proposals() after delete = %+v, %v; want none", list, err)
		}
	}

	t.Run("a missing package is not found, by both methods", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "lime", "other", "1.0.0", pack.KindSkill)
		_, err := s.PackageFootprint(ctx, id("lime", "planner"))
		notFound(t, "PackageFootprint()", err)
		_, err = s.DeletePackage(ctx, id("lime", "planner"), proposedAt)
		notFound(t, "DeletePackage()", err)
	})

	t.Run("releases only: all gone, siblings and same names elsewhere stay", func(t *testing.T) {
		s := newStore(t)
		for _, v := range []string{"0.1.0", "0.2.0", "1.0.0"} {
			put(t, s, "lime", "planner", v, pack.KindAgent)
		}
		put(t, s, "lime", "other", "1.0.0", pack.KindSkill)
		put(t, s, "acme", "planner", "1.0.0", pack.KindAgent)
		before, err := s.Changes(ctx, "lime", 0, 50)
		if err != nil || len(before) != 4 {
			t.Fatalf("Changes() = %+v, %v", before, err)
		}
		want := registry.PackageFootprint{ID: id("lime", "planner"), Releases: []semver.Version{ver("0.1.0"), ver("0.2.0"), ver("1.0.0")}, Kind: pack.KindAgent}
		got, err := s.DeletePackage(ctx, id("lime", "planner"), proposedAt)
		if err != nil {
			t.Fatalf("DeletePackage() error: %v", err)
		}
		if d := diff(want, got); d != "" {
			t.Errorf("DeletePackage() mismatch (-want +got):\n%s", d)
		}
		gone(t, s, id("lime", "planner"))
		pkgs, err := s.Packages(ctx, "lime")
		if err != nil || len(pkgs) != 1 || pkgs[0].ID != id("lime", "other") {
			t.Errorf("Packages(lime) = %+v, %v; want only lime/other", pkgs, err)
		}
		changes, err := s.Changes(ctx, "lime", 0, 50)
		if err != nil || len(changes) != 1 || changes[0].ID != id("lime", "other") {
			t.Errorf("Changes(lime) = %+v, %v; want only lime/other", changes, err)
		}
		if _, err := s.Releases(ctx, id("acme", "planner")); err != nil {
			t.Errorf("acme/planner was touched: %v", err)
		}
	})

	t.Run("proposals of every status with reviews, prompts and courses", func(t *testing.T) {
		s := newStore(t)
		for _, ns := range []registry.Namespace{
			{Name: "acme", Kind: registry.KindProject, CreatedAt: proposedAt},
			{Name: "lime", Parent: "acme", Kind: registry.KindTeam, CreatedAt: proposedAt},
			{Name: "koala", Parent: "acme", Kind: registry.KindTeam, CreatedAt: proposedAt},
		} {
			if err := s.CreateNamespace(ctx, ns); err != nil {
				t.Fatal(err)
			}
		}
		planner, other := id("lime", "planner"), id("lime", "other")
		// Courses: planner in two namespaces, another package in the third.
		for ns, course := range map[string]pack.ID{"acme": planner, "koala": planner, "lime": other} {
			if err := s.SetCourse(ctx, ns, course); err != nil {
				t.Fatal(err)
			}
		}
		// approved: its proposal became release 0.1.0.
		propose(t, s, "pa", "lime", "planner", "0.1.0", 1)
		review(t, s, "pa")
		if _, _, err := s.ApproveProposal(ctx, "pa", decide(registry.ProposalApproved)); err != nil {
			t.Fatal(err)
		}
		last := put(t, s, "lime", "planner", "0.2.0", pack.KindSkill)
		propose(t, s, "pr", "lime", "planner", "0.4.0", 2) // rejected, with a review
		review(t, s, "pr")
		if _, err := s.DecideProposal(ctx, "pr", decide(registry.ProposalRejected)); err != nil {
			t.Fatal(err)
		}
		propose(t, s, "pw", "lime", "planner", "0.5.0", 3) // withdrawn
		if _, err := s.DecideProposal(ctx, "pw", decide(registry.ProposalWithdrawn)); err != nil {
			t.Fatal(err)
		}
		propose(t, s, "po", "lime", "planner", "0.3.0", 4) // open, a review, a removed prompt
		review(t, s, "po")
		if err := s.RemovePrompt(ctx, "po", 1, author, proposedAt.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		propose(t, s, "px", "lime", "planner", "0.6.0", 5) // replaced by pn, which stays open
		repl := Proposal(t, "pn", "lime", "planner", "0.6.0")
		repl.CreatedAt = proposedAt.Add(6 * time.Minute)
		if err := s.ReplaceProposal(ctx, "px", repl, []byte("rebased"), decide(registry.ProposalWithdrawn)); err != nil {
			t.Fatal(err)
		}
		// Another package, with the same shapes, that must not change.
		put(t, s, "lime", "other", "1.0.0", pack.KindSkill)
		propose(t, s, "o1", "lime", "other", "1.1.0", 7)
		review(t, s, "o1")
		otherBefore, err := s.PackageFootprint(ctx, other)
		if err != nil {
			t.Fatal(err)
		}

		want := registry.PackageFootprint{
			ID:        planner,
			Releases:  []semver.Version{ver("0.1.0"), ver("0.2.0")},
			Kind:      pack.KindSkill,
			Proposals: 6,                    // pa pr pw po px pn
			Open:      []string{"po", "pn"}, // oldest first
			Reviews:   3,                    // pa pr po
			Prompts:   12,                   // two each, the removed one included
			Courses:   []string{"acme", "koala"},
		}
		listed, err := s.PackageFootprint(ctx, planner)
		if err != nil {
			t.Fatalf("PackageFootprint() error: %v", err)
		}
		if d := diff(want, listed); d != "" {
			t.Errorf("PackageFootprint() mismatch (-want +got):\n%s", d)
		}
		got, err := s.DeletePackage(ctx, planner, proposedAt)
		if err != nil {
			t.Fatalf("DeletePackage() error: %v", err)
		}
		if d := diff(listed, got); d != "" {
			t.Errorf("DeletePackage() differs from the footprint listed before (-want +got):\n%s", d)
		}

		gone(t, s, planner, "pa", "pr", "pw", "po", "px", "pn")
		for ns, want := range map[string]pack.ID{"acme": {}, "koala": {}, "lime": other} {
			got, err := s.Namespace(ctx, ns)
			if err != nil || got.Course != want {
				t.Errorf("course of %s = %q, %v; want %q", ns, got.Course, err, want)
			}
		}
		after, err := s.PackageFootprint(ctx, other)
		if err != nil {
			t.Fatalf("PackageFootprint(other) error: %v", err)
		}
		if d := diff(otherBefore, after); d != "" {
			t.Errorf("another package changed (-before +after):\n%s", d)
		}
		if _, _, err := s.ReviewText(ctx, "o1", 1); err != nil {
			t.Errorf("another package lost its review: %v", err)
		}
		if p, err := s.Proposal(ctx, "o1"); err != nil || len(p.Prompts) != 2 {
			t.Errorf("another package's proposal = %+v, %v; want two prompts", p, err)
		}

		// The name is free again: a seq never steps back, and the open
		// version of a deleted proposal can be proposed once more.
		again := put(t, s, "lime", "planner", "1.0.0", pack.KindAgent)
		if again.Seq <= last.Seq {
			t.Errorf("Seq after republishing = %d, want above the deleted release's %d", again.Seq, last.Seq)
		}
		propose(t, s, "p-again", "lime", "planner", "1.1.0", 8)
		propose(t, s, "po2", "lime", "planner", "1.2.0", 9)
		if _, err := s.DeletePackage(ctx, planner, proposedAt); err != nil {
			t.Fatalf("second DeletePackage() error: %v", err)
		}
		propose(t, s, "po3", "lime", "planner", "1.2.0", 10) // the open index is free
		if open, err := s.Proposals(ctx, registry.ProposalFilter{Team: "lime", Package: planner, Status: registry.ProposalOpen, Limit: 10}); err != nil || len(open) != 1 {
			t.Errorf("open proposals after proposing again = %+v, %v; want one", open, err)
		}
	})

	t.Run("proposals only: a never released package is deleted, not missing", func(t *testing.T) {
		s := newStore(t)
		propose(t, s, "p1", "lime", "draft", "0.1.0", 1)
		fp, err := s.DeletePackage(ctx, id("lime", "draft"), proposedAt)
		if err != nil {
			t.Fatalf("DeletePackage() error: %v", err)
		}
		want := registry.PackageFootprint{ID: id("lime", "draft"), Proposals: 1, Open: []string{"p1"}, Prompts: 2}
		if d := diff(want, fp); d != "" {
			t.Errorf("DeletePackage() mismatch (-want +got):\n%s", d)
		}
		gone(t, s, id("lime", "draft"), "p1")
	})

	t.Run("the footprint of a published package lists no proposals", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "lime", "planner", "0.1.0", pack.KindKB)
		fp, err := s.PackageFootprint(ctx, id("lime", "planner"))
		if err != nil {
			t.Fatal(err)
		}
		if fp.Proposals != 0 || len(fp.Open) != 0 || fp.Reviews != 0 || fp.Prompts != 0 || len(fp.Courses) != 0 || fp.Kind != pack.KindKB {
			t.Errorf("PackageFootprint() = %+v, want releases and a kb kind only", fp)
		}
		if !slices.Equal(fp.Releases, []semver.Version{ver("0.1.0")}) {
			t.Errorf("Releases = %v, want 0.1.0", fp.Releases)
		}
	})
}
