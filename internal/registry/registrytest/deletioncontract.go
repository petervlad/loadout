package registrytest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
)

// DeletionBackend is a store that keeps requests to delete packages with
// what an approval deletes: the releases, the proposals with their reviews
// and prompts, and the namespaces whose course the package may be.
type DeletionBackend interface {
	DeleteBackend
	registry.DeletionStore
}

// RunDeletionContract runs the behaviour of registry.DeletionStore every
// implementation must have. newStore must return a fresh, empty store for
// each call.
func RunDeletionContract(t *testing.T, newStore func(t *testing.T) DeletionBackend) {
	t.Helper()
	ctx := t.Context()
	var (
		alice = registry.Actor{TokenID: "tok-alice", Label: "alice", UserID: "u-alice"}
		bob   = registry.Actor{TokenID: "tok-bob", Label: "bob", UserID: "u-bob"}
		at    = func(n int) time.Time { return proposedAt.Add(time.Duration(n) * time.Minute) }
	)
	id := func(team, name string) pack.ID {
		t.Helper()
		i, err := pack.NewID(team, name)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	base := func(s string) registry.Base {
		t.Helper()
		b, err := registry.ParseBase(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	request := func(did string, pkg pack.ID, newest string, n int) registry.Deletion {
		return registry.Deletion{ID: did, Package: pkg, Reason: "unused since spring", Newest: base(newest), Author: alice, CreatedAt: at(n)}
	}
	put := func(t *testing.T, s DeletionBackend, team, name, version string) {
		t.Helper()
		if _, err := s.PutRelease(ctx, Release(t, team, name, version), []byte("bundle")); err != nil {
			t.Fatal(err)
		}
	}
	create := func(t *testing.T, s DeletionBackend, d registry.Deletion) {
		t.Helper()
		if err := s.CreateDeletion(ctx, d); err != nil {
			t.Fatalf("CreateDeletion(%s) error: %v", d.ID, err)
		}
	}
	decision := func(status registry.ProposalStatus, by registry.Actor, n int) registry.Decision {
		return registry.Decision{Status: status, By: by, At: at(n), Note: "because"}
	}
	status := func(t *testing.T, s DeletionBackend, did string) registry.Deletion {
		t.Helper()
		d, err := s.Deletion(ctx, did)
		if err != nil {
			t.Fatalf("Deletion(%s) error: %v", did, err)
		}
		return d
	}
	is := func(t *testing.T, what string, err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("%s error = %v, want %v", what, err, want)
		}
	}
	packageThere := func(t *testing.T, s DeletionBackend, pkg pack.ID) bool {
		t.Helper()
		_, err := s.PackageFootprint(ctx, pkg)
		return err == nil
	}

	t.Run("a request is stored open and read back", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "lime", "planner", "1.0.0")
		want := request("del-a", id("lime", "planner"), "1.0.0", 1)
		create(t, s, want)
		got := status(t, s, "del-a")
		want.Status = registry.DeletionOpen
		if d := cmp.Diff(want, got, cmp.AllowUnexported(registry.Base{}, want.Package, semver.Version{})); d != "" {
			t.Errorf("Deletion() mismatch (-want +got):\n%s", d)
		}
		_, err := s.Deletion(ctx, "del-nope")
		is(t, "Deletion(unknown)", err, registry.ErrNotFound)
	})

	t.Run("a package has one open request at a time", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "lime", "planner", "1.0.0")
		put(t, s, "lime", "other", "1.0.0")
		create(t, s, request("del-a", id("lime", "planner"), "1.0.0", 1))
		is(t, "second CreateDeletion()", s.CreateDeletion(ctx, request("del-b", id("lime", "planner"), "1.0.0", 2)), registry.ErrExists)
		is(t, "CreateDeletion() with a used id", s.CreateDeletion(ctx, request("del-a", id("lime", "other"), "1.0.0", 2)), registry.ErrExists)
		create(t, s, request("del-c", id("lime", "other"), "1.0.0", 3))
		if _, err := s.DecideDeletion(ctx, "del-a", decision(registry.ProposalRejected, bob, 4)); err != nil {
			t.Fatal(err)
		}
		create(t, s, request("del-d", id("lime", "planner"), "1.0.0", 5)) // the open slot is free again
	})

	t.Run("the footprint lists the open request", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "lime", "planner", "1.0.0")
		create(t, s, request("del-a", id("lime", "planner"), "1.0.0", 1))
		fp, err := s.PackageFootprint(ctx, id("lime", "planner"))
		if err != nil || len(fp.Deletions) != 1 || fp.Deletions[0] != "del-a" {
			t.Errorf("PackageFootprint().Deletions = %v, %v; want [del-a]", fp.Deletions, err)
		}
	})

	t.Run("a list is one namespace's, newest first, by status", func(t *testing.T) {
		s := newStore(t)
		for _, n := range []string{"a", "b", "c"} {
			put(t, s, "lime", n, "1.0.0")
		}
		put(t, s, "acme", "a", "1.0.0")
		create(t, s, request("del-1", id("lime", "a"), "1.0.0", 1))
		create(t, s, request("del-2", id("lime", "b"), "1.0.0", 2))
		create(t, s, request("del-3", id("lime", "c"), "1.0.0", 3))
		create(t, s, request("del-x", id("acme", "a"), "1.0.0", 4))
		if _, err := s.DecideDeletion(ctx, "del-2", decision(registry.ProposalWithdrawn, alice, 5)); err != nil {
			t.Fatal(err)
		}
		ids := func(f registry.DeletionFilter) []string {
			t.Helper()
			ds, err := s.Deletions(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, d := range ds {
				out = append(out, d.ID)
			}
			return out
		}
		for _, tt := range []struct {
			name string
			f    registry.DeletionFilter
			want []string
		}{
			{"every status", registry.DeletionFilter{Team: "lime", Limit: 10}, []string{"del-3", "del-2", "del-1"}},
			{"open", registry.DeletionFilter{Team: "lime", Status: registry.DeletionOpen, Limit: 10}, []string{"del-3", "del-1"}},
			{"withdrawn", registry.DeletionFilter{Team: "lime", Status: registry.DeletionWithdrawn, Limit: 10}, []string{"del-2"}},
			{"limit keeps the newest", registry.DeletionFilter{Team: "lime", Limit: 2}, []string{"del-3", "del-2"}},
			{"another namespace", registry.DeletionFilter{Team: "acme", Limit: 10}, []string{"del-x"}},
			{"none", registry.DeletionFilter{Team: "zeta", Limit: 10}, nil},
		} {
			if d := cmp.Diff(tt.want, ids(tt.f)); d != "" {
				t.Errorf("%s: Deletions() mismatch (-want +got):\n%s", tt.name, d)
			}
		}
	})

	t.Run("reject and withdraw close an open request once", func(t *testing.T) {
		for _, st := range []registry.ProposalStatus{registry.ProposalRejected, registry.ProposalWithdrawn} {
			s := newStore(t)
			put(t, s, "lime", "planner", "1.0.0")
			create(t, s, request("del-a", id("lime", "planner"), "1.0.0", 1))
			got, err := s.DecideDeletion(ctx, "del-a", decision(st, bob, 2))
			if err != nil {
				t.Fatalf("DecideDeletion(%s) error: %v", st, err)
			}
			if got.Status != registry.DeletionStatus(st) || got.DecidedBy != bob || got.DecisionNote != "because" || !got.DecidedAt.Equal(at(2)) || got.SelfApproved {
				t.Errorf("DecideDeletion(%s) = %+v", st, got)
			}
			if !packageThere(t, s, id("lime", "planner")) {
				t.Errorf("%s deleted the package", st)
			}
			_, err = s.DecideDeletion(ctx, "del-a", decision(st, bob, 3))
			is(t, "second DecideDeletion()", err, registry.ErrProposalClosed)
			_, err = s.ApproveDeletion(ctx, "del-a", registry.Decision{By: bob, At: at(3), Latest: base("1.0.0")})
			is(t, "ApproveDeletion() of a closed request", err, registry.ErrProposalClosed)
		}
		s := newStore(t)
		put(t, s, "lime", "planner", "1.0.0")
		create(t, s, request("del-a", id("lime", "planner"), "1.0.0", 1))
		_, err := s.DecideDeletion(ctx, "del-a", decision(registry.ProposalApproved, bob, 2))
		is(t, "DecideDeletion(approved)", err, registry.ErrInvalid)
		_, err = s.DecideDeletion(ctx, "del-nope", decision(registry.ProposalRejected, bob, 2))
		is(t, "DecideDeletion(unknown)", err, registry.ErrNotFound)
	})

	t.Run("approval deletes the package and the request keeps what went", func(t *testing.T) {
		s := newStore(t)
		for _, ns := range []registry.Namespace{
			{Name: "acme", Kind: registry.KindProject, CreatedAt: proposedAt},
			{Name: "lime", Parent: "acme", Kind: registry.KindTeam, CreatedAt: proposedAt},
		} {
			if err := s.CreateNamespace(ctx, ns); err != nil {
				t.Fatal(err)
			}
		}
		planner := id("lime", "planner")
		if err := s.SetCourse(ctx, "acme", planner); err != nil {
			t.Fatal(err)
		}
		put(t, s, "lime", "planner", "0.1.0")
		put(t, s, "lime", "planner", "0.2.0")
		put(t, s, "lime", "other", "1.0.0")
		p := Proposal(t, "p1", "lime", "planner", "0.3.0")
		p.Kind = pack.KindAgent
		if err := s.CreateProposal(ctx, p, []byte("candidate")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddReview(ctx, "p1", registry.Review{Author: alice, CreatedAt: proposedAt, Size: 4}, []byte("text"), nil, registry.MaxReviews); err != nil {
			t.Fatal(err)
		}
		create(t, s, request("del-a", planner, "0.2.0", 1))
		create(t, s, request("del-o", id("lime", "other"), "1.0.0", 2))

		dec := decision(registry.ProposalApproved, bob, 3)
		dec.Latest = base("0.2.0")
		got, err := s.ApproveDeletion(ctx, "del-a", dec)
		if err != nil {
			t.Fatalf("ApproveDeletion() error: %v", err)
		}
		wantRemoved := registry.PackageFootprint{
			ID: planner, Releases: []semver.Version{mustVersion(t, "0.1.0"), mustVersion(t, "0.2.0")},
			Proposals: 1, Open: []string{"p1"}, Reviews: 1, Prompts: 2, Courses: []string{"acme"},
		}
		if got.Status != registry.DeletionApproved || got.DecidedBy != bob || got.DecisionNote != "because" || !got.DecidedAt.Equal(at(3)) || got.SelfApproved {
			t.Errorf("ApproveDeletion() = %+v", got)
		}
		if d := cmp.Diff(wantRemoved, got.Removed, cmp.AllowUnexported(planner, semver.Version{})); d != "" {
			t.Errorf("Removed mismatch (-want +got):\n%s", d)
		}
		if again := status(t, s, "del-a"); cmp.Diff(got, again, cmp.AllowUnexported(registry.Base{}, planner, semver.Version{})) != "" {
			t.Errorf("the stored request differs from the one approved: %+v vs %+v", again, got)
		}
		if packageThere(t, s, planner) {
			t.Error("the package is still there after the approval")
		}
		if _, err := s.Proposal(ctx, "p1"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Proposal(p1) error = %v, want ErrNotFound", err)
		}
		if ns, err := s.Namespace(ctx, "acme"); err != nil || !ns.Course.IsZero() {
			t.Errorf("course after the approval = %v, %v; want none", ns.Course, err)
		}
		if !packageThere(t, s, id("lime", "other")) {
			t.Error("another package was deleted")
		}
		if o := status(t, s, "del-o"); o.Status != registry.DeletionOpen {
			t.Errorf("another package's request = %s, want open", o.Status)
		}
		_, err = s.ApproveDeletion(ctx, "del-a", dec)
		is(t, "second ApproveDeletion()", err, registry.ErrProposalClosed)

		// The name is free, and a new request for it is allowed.
		put(t, s, "lime", "planner", "1.0.0")
		create(t, s, request("del-b", planner, "1.0.0", 4))
		if old := status(t, s, "del-a"); old.Status != registry.DeletionApproved {
			t.Errorf("the record changed after republishing: %s", old.Status)
		}
	})

	t.Run("a package that only has proposals is approved with no newest release", func(t *testing.T) {
		s := newStore(t)
		p := Proposal(t, "p1", "lime", "draft", "0.1.0")
		p.Kind = pack.KindAgent
		if err := s.CreateProposal(ctx, p, []byte("candidate")); err != nil {
			t.Fatal(err)
		}
		create(t, s, request("del-a", id("lime", "draft"), "", 1))
		got, err := s.ApproveDeletion(ctx, "del-a", decision(registry.ProposalApproved, bob, 2))
		if err != nil || got.Status != registry.DeletionApproved || got.Removed.Proposals != 1 {
			t.Errorf("ApproveDeletion() = %+v, %v", got, err)
		}
	})

	t.Run("a release after the request makes the approval stale", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "lime", "planner", "1.0.0")
		create(t, s, request("del-a", id("lime", "planner"), "1.0.0", 1))
		put(t, s, "lime", "planner", "1.1.0")
		dec := decision(registry.ProposalApproved, bob, 2)
		dec.Latest = base("1.0.0")
		_, err := s.ApproveDeletion(ctx, "del-a", dec)
		is(t, "ApproveDeletion()", err, registry.ErrStale)
		if !packageThere(t, s, id("lime", "planner")) {
			t.Error("a stale approval deleted the package")
		}
		if d := status(t, s, "del-a"); d.Status != registry.DeletionOpen {
			t.Errorf("a stale approval closed the request as %s", d.Status)
		}
		if _, err := s.DecideDeletion(ctx, "del-a", decision(registry.ProposalWithdrawn, alice, 3)); err != nil {
			t.Errorf("withdrawing a stale request: %v", err)
		}
	})

	t.Run("a direct delete closes open requests as gone", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "lime", "planner", "1.0.0")
		create(t, s, request("del-a", id("lime", "planner"), "1.0.0", 1))
		fp, err := s.DeletePackage(ctx, id("lime", "planner"), at(5))
		if err != nil || len(fp.Deletions) != 1 || fp.Deletions[0] != "del-a" {
			t.Fatalf("DeletePackage() = %+v, %v; want the footprint to list del-a", fp, err)
		}
		got := status(t, s, "del-a")
		if got.Status != registry.DeletionGone || !got.DecidedAt.Equal(at(5)) || got.DecidedBy != (registry.Actor{}) {
			t.Errorf("request after a direct delete = %+v, want gone at %v, decided by nobody", got, at(5))
		}
		_, err = s.ApproveDeletion(ctx, "del-a", registry.Decision{By: bob, At: at(6), Latest: base("1.0.0")})
		is(t, "ApproveDeletion() of a gone request", err, registry.ErrProposalClosed)
		_, err = s.DecideDeletion(ctx, "del-a", decision(registry.ProposalWithdrawn, alice, 6))
		is(t, "DecideDeletion() of a gone request", err, registry.ErrProposalClosed)
		put(t, s, "lime", "planner", "1.0.0")
		create(t, s, request("del-b", id("lime", "planner"), "1.0.0", 7))
	})

	t.Run("a recorded delete deletes and keeps the record", func(t *testing.T) {
		s := newStore(t)
		put(t, s, "lime", "planner", "0.1.0")
		put(t, s, "lime", "planner", "0.2.0")
		put(t, s, "lime", "other", "1.0.0")
		create(t, s, request("del-open", id("lime", "planner"), "0.2.0", 1))
		rec := registry.Deletion{ID: "del-rec", Package: id("lime", "planner"), Reason: "wrong team", Author: bob, CreatedAt: at(2)}
		fp, err := s.RecordDeletion(ctx, rec)
		if err != nil {
			t.Fatalf("RecordDeletion() error: %v", err)
		}
		if len(fp.Releases) != 2 {
			t.Errorf("RecordDeletion() = %+v, want two releases", fp)
		}
		if packageThere(t, s, id("lime", "planner")) {
			t.Error("the package is still there")
		}
		got := status(t, s, "del-rec")
		if got.Status != registry.DeletionApproved || !got.SelfApproved || got.Reason != "wrong team" || got.Author != bob || got.DecidedBy != bob ||
			!got.DecidedAt.Equal(at(2)) || got.Newest.String() != "0.2.0" || len(got.Removed.Releases) != 2 {
			t.Errorf("recorded request = %+v", got)
		}
		if open := status(t, s, "del-open"); open.Status != registry.DeletionGone {
			t.Errorf("the open request = %s, want gone", open.Status)
		}
		if !packageThere(t, s, id("lime", "other")) {
			t.Error("another package was deleted")
		}

		_, err = s.RecordDeletion(ctx, registry.Deletion{ID: "del-none", Package: id("lime", "planner"), Reason: "again", Author: bob, CreatedAt: at(3)})
		is(t, "RecordDeletion() of a missing package", err, registry.ErrNotFound)
		if _, err := s.Deletion(ctx, "del-none"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("a failed RecordDeletion left a record: %v", err)
		}
	})
}

func mustVersion(t *testing.T, s string) semver.Version {
	t.Helper()
	v, err := semver.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
