package registry_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

// snaps is a registry.Snapshots that records its labels and can fail.
type snaps struct {
	mu     sync.Mutex
	err    error
	labels []string
}

func (s *snaps) Take(_ context.Context, label string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	s.labels = append(s.labels, label)
	return label + ".db", nil
}

func (s *snaps) taken() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.labels...)
}

type deletionFixture struct {
	store *registrytest.MemStore
	svc   *registry.DeletionService
	snaps *snaps
}

// newDeletionFixture has lime/planner (a team of acme) at 1.0.0 and
// acme/shared at 1.0.0. withSnaps false builds a registry without a backup
// folder.
func newDeletionFixture(t *testing.T, o registry.Owners, withSnaps bool) *deletionFixture {
	t.Helper()
	store := registrytest.NewMemStore()
	f := &deletionFixture{store: store, snaps: &snaps{}}
	now := func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	opts := []registry.DeletionOption{}
	if o != nil {
		opts = append(opts, registry.WithDeletionOwners(o))
	}
	if withSnaps {
		opts = append(opts, registry.WithDeletionSnapshots(f.snaps))
	}
	f.svc = registry.NewDeletionService(store, store, now, slog.New(slog.DiscardHandler), opts...)
	for _, id := range [][2]string{{"lime", "planner"}, {"acme", "shared"}} {
		if _, err := store.PutRelease(t.Context(), registrytest.Release(t, id[0], id[1], "1.0.0"), []byte("b")); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func pkgID(t *testing.T, team, name string) pack.ID {
	t.Helper()
	id, err := pack.NewID(team, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *deletionFixture) there(t *testing.T, team, name string) bool {
	t.Helper()
	_, err := f.store.PackageFootprint(t.Context(), pkgID(t, team, name))
	return err == nil
}

func TestDeletionPropose(t *testing.T) {
	var (
		member     = person("u-member", registry.ScopeRead, limeLayers)
		maintainer = person("u-maint", registry.ScopePublish, limeLayers)
		machine    = registry.Principal{TokenID: "ci", Team: "lime", Scope: registry.ScopePublish, Layers: limeLayers, Label: "ci", CreatedBy: "u-maint"}
		hostToken  = registry.Principal{TokenID: "host", Team: "lime", Scope: registry.ScopeRead, Layers: limeLayers, Label: "olga"}
		stranger   = person("u-zeta", registry.ScopePublish, []registry.Layer{{Namespace: "zeta", Kind: registry.KindTeam}})
		blank      = "  \n"
	)
	tests := []struct {
		name    string
		pr      registry.Principal
		pkg     [2]string
		reason  string
		wantErr error
	}{
		{"a member", member, [2]string{"lime", "planner"}, "unused", nil},
		{"a maintainer", maintainer, [2]string{"lime", "planner"}, "unused", nil},
		{"a team asks about its project's package", member, [2]string{"acme", "shared"}, "unused", nil},
		{"a host-made token", hostToken, [2]string{"lime", "planner"}, "unused", nil},
		{"a machine token", machine, [2]string{"lime", "planner"}, "unused", registry.ErrForbidden},
		{"a stranger", stranger, [2]string{"lime", "planner"}, "unused", registry.ErrForbidden},
		{"no reason", member, [2]string{"lime", "planner"}, "", registry.ErrInvalid},
		{"a blank reason", member, [2]string{"lime", "planner"}, blank, registry.ErrInvalid},
		{"a reason too long", member, [2]string{"lime", "planner"}, strings.Repeat("x", registry.MaxNoteSize+1), registry.ErrInvalid},
		{"a reason that is not text", member, [2]string{"lime", "planner"}, "\xff\xfe", registry.ErrInvalid},
		{"a package that is not there", member, [2]string{"lime", "nope"}, "unused", registry.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeletionFixture(t, nil, true)
			d, err := f.svc.Propose(t.Context(), tc.pr, pkgID(t, tc.pkg[0], tc.pkg[1]), tc.reason)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Propose() error = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if !strings.HasPrefix(d.ID, "del-") || d.Status != registry.DeletionOpen || d.Newest.String() != "1.0.0" ||
				d.Reason != strings.TrimSpace(tc.reason) || d.Author.TokenID != tc.pr.TokenID {
				t.Errorf("Propose() = %+v", d)
			}
			if !f.there(t, tc.pkg[0], tc.pkg[1]) {
				t.Error("asking deleted the package")
			}
		})
	}

	t.Run("one open request at a time, and a new one after it closes", func(t *testing.T) {
		f := newDeletionFixture(t, nil, true)
		first, err := f.svc.Propose(t.Context(), member, pkgID(t, "lime", "planner"), "unused")
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.Propose(t.Context(), maintainer, pkgID(t, "lime", "planner"), "again")
		if !errors.Is(err, registry.ErrExists) || !strings.Contains(err.Error(), first.ID) {
			t.Errorf("second Propose() error = %v, want ErrExists naming %s", err, first.ID)
		}
		if _, err := f.svc.Withdraw(t.Context(), member, first.ID, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Propose(t.Context(), maintainer, pkgID(t, "lime", "planner"), "again"); err != nil {
			t.Errorf("Propose() after a withdrawal error = %v, want nil", err)
		}
	})
}

func TestDeletionApprove(t *testing.T) {
	own := owners{"u-owner": {"acme", "lime"}, "u-zeta-owner": {"zeta"}}
	var (
		member     = person("u-member", registry.ScopeRead, limeLayers)
		author     = person("u-author", registry.ScopeRead, limeLayers)
		maintainer = person("u-maint", registry.ScopePublish, limeLayers)
		owner      = person("u-owner", registry.ScopePublish, limeLayers)
		otherOwner = person("u-zeta-owner", registry.ScopePublish, []registry.Layer{{Namespace: "zeta", Kind: registry.KindProject}})
		machine    = registry.Principal{TokenID: "ci", Team: "lime", Scope: registry.ScopePublish, Layers: limeLayers, Label: "ci", CreatedBy: "u-maint"}
		withWhy    = registry.ApproveInput{Note: "hotfix, nobody else is around", AsOwner: true}
		plain      = registry.ApproveInput{Note: "agreed"}
	)
	tests := []struct {
		name     string
		author   registry.Principal
		approver registry.Principal
		in       registry.ApproveInput
		wantErr  error
		wantHint string
		wantSelf bool
	}{
		{"a maintainer, someone else's", author, maintainer, plain, nil, "", false},
		{"a maintainer, without a note", author, maintainer, registry.ApproveInput{}, nil, "", false},
		{"an owner, someone else's", author, owner, plain, nil, "", false},
		{"an owner, someone else's, as owner is ignored", author, owner, withWhy, nil, "", false},
		{"a member", author, member, plain, registry.ErrForbidden, "publish token", false},
		{"the author, a member", author, author, plain, registry.ErrForbidden, "publish token", false},
		{"a maintainer, own", maintainer, maintainer, plain, registry.ErrForbidden, "ask a teammate", false},
		{"an owner, own, as owner with a note", owner, owner, withWhy, nil, "", true},
		{"an owner, own, without as owner", owner, owner, plain, registry.ErrForbidden, "--as-owner", false},
		{"an owner, own, without a note", owner, owner, registry.ApproveInput{AsOwner: true}, registry.ErrInvalid, "", false},
		{"an owner, own, a blank note", owner, owner, registry.ApproveInput{AsOwner: true, Note: " "}, registry.ErrInvalid, "", false},
		{"a machine token", author, machine, plain, registry.ErrForbidden, "machine token", false},
		{"an owner of another project", author, otherOwner, plain, registry.ErrNotFound, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeletionFixture(t, own, true)
			d, err := f.svc.Propose(t.Context(), tc.author, pkgID(t, "lime", "planner"), "unused")
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.svc.Approve(t.Context(), tc.approver, d.ID, tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Approve() error = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				if tc.wantHint != "" && !strings.Contains(err.Error(), tc.wantHint) {
					t.Errorf("Approve() error = %q, want it to contain %q", err, tc.wantHint)
				}
				if tc.wantHint == "" && strings.Contains(err.Error(), "--as-owner") {
					t.Errorf("Approve() error = %q offers --as-owner to someone who may not use it", err)
				}
				if !f.there(t, "lime", "planner") || len(f.snaps.taken()) != 0 {
					t.Errorf("a refused approval deleted the package or took a copy (copies %v)", f.snaps.taken())
				}
				return
			}
			if f.there(t, "lime", "planner") {
				t.Error("the package is still there after the approval")
			}
			if got.Status != registry.DeletionApproved || got.SelfApproved != tc.wantSelf || got.DecidedBy.UserID != tc.approver.UserID || len(got.Removed.Releases) != 1 {
				t.Errorf("Approve() = %+v", got)
			}
			if want := []string{"before-delete-lime-planner"}; strings.Join(f.snaps.taken(), ",") != strings.Join(want, ",") {
				t.Errorf("copies taken = %v, want %v", f.snaps.taken(), want)
			}
			if tc.wantSelf && got.DecisionNote != strings.TrimSpace(tc.in.Note) {
				t.Errorf("note = %q, want %q", got.DecisionNote, tc.in.Note)
			}
			// The other package is untouched and a second approval is refused.
			if !f.there(t, "acme", "shared") {
				t.Error("another package was deleted")
			}
			if _, err := f.svc.Approve(t.Context(), tc.approver, d.ID, tc.in); !errors.Is(err, registry.ErrProposalClosed) {
				t.Errorf("second Approve() error = %v, want ErrProposalClosed", err)
			}
		})
	}

	t.Run("a registry without owners keeps four eyes for everyone", func(t *testing.T) {
		f := newDeletionFixture(t, nil, true)
		d, err := f.svc.Propose(t.Context(), owner, pkgID(t, "lime", "planner"), "unused")
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.Approve(t.Context(), owner, d.ID, withWhy)
		if !errors.Is(err, registry.ErrForbidden) || strings.Contains(err.Error(), "--as-owner") {
			t.Errorf("Approve() error = %v, want the plain four-eyes refusal", err)
		}
	})

	t.Run("without a backup folder nothing is approved", func(t *testing.T) {
		f := newDeletionFixture(t, own, false)
		d, err := f.svc.Propose(t.Context(), author, pkgID(t, "lime", "planner"), "unused")
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.Approve(t.Context(), maintainer, d.ID, plain)
		if !errors.Is(err, registry.ErrForbidden) || !strings.Contains(err.Error(), "LOADOUT_BACKUP_DIR") {
			t.Errorf("Approve() error = %v, want ErrForbidden naming LOADOUT_BACKUP_DIR", err)
		}
		if !f.there(t, "lime", "planner") {
			t.Error("the package was deleted without a copy")
		}
	})

	t.Run("a failed copy deletes nothing and leaves the request open", func(t *testing.T) {
		f := newDeletionFixture(t, own, true)
		f.snaps.err = errors.New("disk full")
		d, err := f.svc.Propose(t.Context(), author, pkgID(t, "lime", "planner"), "unused")
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.Approve(t.Context(), maintainer, d.ID, plain)
		if !errors.Is(err, registry.ErrBackupFailed) || !strings.Contains(err.Error(), "nothing was deleted") {
			t.Fatalf("Approve() error = %v, want ErrBackupFailed saying nothing was deleted", err)
		}
		if !f.there(t, "lime", "planner") {
			t.Error("the package was deleted without a copy")
		}
		if got, err := f.svc.Deletion(t.Context(), maintainer, d.ID); err != nil || got.Status != registry.DeletionOpen {
			t.Errorf("Deletion() = %+v, %v; want it still open", got, err)
		}
	})

	t.Run("a release after the request refuses the approval, with no copy", func(t *testing.T) {
		f := newDeletionFixture(t, own, true)
		d, err := f.svc.Propose(t.Context(), author, pkgID(t, "lime", "planner"), "unused")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PutRelease(t.Context(), registrytest.Release(t, "lime", "planner", "1.1.0"), []byte("b")); err != nil {
			t.Fatal(err)
		}
		st, err := f.svc.Standing(t.Context(), d)
		if err != nil || !st.Stale || st.Newest.String() != "1.1.0" {
			t.Errorf("Standing() = %+v, %v; want stale at 1.1.0", st, err)
		}
		_, err = f.svc.Approve(t.Context(), maintainer, d.ID, plain)
		if !errors.Is(err, registry.ErrBehind) || !strings.Contains(err.Error(), "1.1.0") {
			t.Fatalf("Approve() error = %v, want ErrBehind naming 1.1.0", err)
		}
		if !f.there(t, "lime", "planner") || len(f.snaps.taken()) != 0 {
			t.Errorf("a stale approval deleted the package or took a copy (copies %v)", f.snaps.taken())
		}
		if _, err := f.svc.Withdraw(t.Context(), author, d.ID, ""); err != nil {
			t.Errorf("Withdraw() of a stale request error = %v, want nil", err)
		}
	})

	t.Run("a package deleted another way closes the request as gone", func(t *testing.T) {
		f := newDeletionFixture(t, own, true)
		d, err := f.svc.Propose(t.Context(), author, pkgID(t, "lime", "planner"), "unused")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DeletePackage(t.Context(), d.Package, time.Now()); err != nil {
			t.Fatal(err)
		}
		_, err = f.svc.Approve(t.Context(), maintainer, d.ID, plain)
		if !errors.Is(err, registry.ErrProposalClosed) || !strings.Contains(err.Error(), "gone") {
			t.Errorf("Approve() error = %v, want ErrProposalClosed saying it is gone", err)
		}
	})

	t.Run("an unknown id is not found", func(t *testing.T) {
		f := newDeletionFixture(t, own, true)
		if _, err := f.svc.Approve(t.Context(), maintainer, "del-nope", plain); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Approve() error = %v, want ErrNotFound", err)
		}
	})
}

func TestDeletionRejectWithdraw(t *testing.T) {
	var (
		member     = person("u-member", registry.ScopeRead, limeLayers)
		author     = person("u-author", registry.ScopeRead, limeLayers)
		maintainer = person("u-maint", registry.ScopePublish, limeLayers)
		machine    = registry.Principal{TokenID: "ci", Team: "lime", Scope: registry.ScopePublish, Layers: limeLayers, Label: "ci", CreatedBy: "u-maint"}
		stranger   = person("u-zeta", registry.ScopePublish, []registry.Layer{{Namespace: "zeta", Kind: registry.KindTeam}})
	)
	tests := []struct {
		name    string
		act     func(*registry.DeletionService, string) (registry.Deletion, error)
		want    error
		wantSt  registry.DeletionStatus
		wantErr string
	}{
		{"a maintainer rejects", func(s *registry.DeletionService, id string) (registry.Deletion, error) {
			return s.Reject(t.Context(), maintainer, id, "still used")
		}, nil, registry.DeletionRejected, ""},
		{"the author cannot reject", func(s *registry.DeletionService, id string) (registry.Deletion, error) {
			return s.Reject(t.Context(), author, id, "")
		}, registry.ErrForbidden, "", "withdraw"},
		{"a member cannot reject", func(s *registry.DeletionService, id string) (registry.Deletion, error) {
			return s.Reject(t.Context(), member, id, "")
		}, registry.ErrForbidden, "", "publish token"},
		{"a machine token cannot reject", func(s *registry.DeletionService, id string) (registry.Deletion, error) {
			return s.Reject(t.Context(), machine, id, "")
		}, registry.ErrForbidden, "", "machine token"},
		{"the author withdraws", func(s *registry.DeletionService, id string) (registry.Deletion, error) {
			return s.Withdraw(t.Context(), author, id, "changed my mind")
		}, nil, registry.DeletionWithdrawn, ""},
		{"a maintainer cannot withdraw", func(s *registry.DeletionService, id string) (registry.Deletion, error) {
			return s.Withdraw(t.Context(), maintainer, id, "")
		}, registry.ErrForbidden, "", "only the author"},
		{"a stranger cannot even see it", func(s *registry.DeletionService, id string) (registry.Deletion, error) {
			return s.Withdraw(t.Context(), stranger, id, "")
		}, registry.ErrNotFound, "", ""},
		{"a note too long", func(s *registry.DeletionService, id string) (registry.Deletion, error) {
			return s.Reject(t.Context(), maintainer, id, strings.Repeat("x", registry.MaxNoteSize+1))
		}, registry.ErrInvalid, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeletionFixture(t, nil, true)
			d, err := f.svc.Propose(t.Context(), author, pkgID(t, "lime", "planner"), "unused")
			if err != nil {
				t.Fatal(err)
			}
			got, err := tc.act(f.svc, d.ID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if err != nil {
				if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if got.Status != tc.wantSt || got.DecidedAt.IsZero() {
				t.Errorf("result = %+v, want %s", got, tc.wantSt)
			}
			if !f.there(t, "lime", "planner") {
				t.Error("closing the request deleted the package")
			}
			if _, err := f.svc.Reject(t.Context(), maintainer, d.ID, ""); !errors.Is(err, registry.ErrProposalClosed) {
				t.Errorf("Reject() of a closed request error = %v, want ErrProposalClosed", err)
			}
		})
	}
}

func TestDeletionList(t *testing.T) {
	f := newDeletionFixture(t, nil, true)
	member := person("u-member", registry.ScopeRead, limeLayers)
	d, err := f.svc.Propose(t.Context(), member, pkgID(t, "lime", "planner"), "unused")
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Deletions(t.Context(), member, "", registry.DeletionOpen)
	if err != nil || len(got) != 1 || got[0].ID != d.ID {
		t.Errorf("Deletions() = %+v, %v; want %s", got, err, d.ID)
	}
	// A team reads the requests of its project, as it reads its proposals.
	if got, err := f.svc.Deletions(t.Context(), member, "acme", ""); err != nil || len(got) != 0 {
		t.Errorf("Deletions(acme) = %+v, %v; want none", got, err)
	}
	if _, err := f.svc.Deletions(t.Context(), member, "zeta", ""); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Deletions(zeta) error = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.Deletions(t.Context(), member, "", "bogus"); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Deletions(bogus status) error = %v, want ErrInvalid", err)
	}
	if _, err := f.svc.Deletion(t.Context(), person("u-zeta", registry.ScopeRead, []registry.Layer{{Namespace: "zeta", Kind: registry.KindTeam}}), d.ID); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Deletion() by a stranger error = %v, want ErrNotFound", err)
	}
}

func TestRecordDelete(t *testing.T) {
	f := newDeletionFixture(t, nil, true)
	by := registry.Actor{TokenID: "web:u-admin", Label: "ada@example.com on the web", UserID: "u-admin"}
	if _, err := f.svc.RecordDelete(t.Context(), by, pkgID(t, "lime", "planner"), " "); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("RecordDelete() without a reason error = %v, want ErrInvalid", err)
	}
	if !f.there(t, "lime", "planner") {
		t.Fatal("a refused record deleted the package")
	}
	fp, err := f.svc.RecordDelete(t.Context(), by, pkgID(t, "lime", "planner"), "wrong team")
	if err != nil || len(fp.Releases) != 1 {
		t.Fatalf("RecordDelete() = %+v, %v", fp, err)
	}
	ds, err := f.store.Deletions(t.Context(), registry.DeletionFilter{Team: "lime", Limit: 10})
	if err != nil || len(ds) != 1 || ds[0].Reason != "wrong team" || ds[0].Author != by || !ds[0].SelfApproved || ds[0].Status != registry.DeletionApproved {
		t.Errorf("Deletions() = %+v, %v; want one approved record by the admin", ds, err)
	}
	if _, err := f.svc.RecordDelete(t.Context(), by, pkgID(t, "lime", "planner"), "again"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("RecordDelete() of a deleted package error = %v, want ErrNotFound", err)
	}
}

func TestDeletionFootprint(t *testing.T) {
	f := newDeletionFixture(t, nil, true)
	member := person("u-member", registry.ScopeRead, limeLayers)
	stranger := person("u-zeta", registry.ScopeRead, []registry.Layer{{Namespace: "zeta", Kind: registry.KindTeam}})
	machine := registry.Principal{TokenID: "ci", Team: "lime", Scope: registry.ScopeRead, Layers: limeLayers, CreatedBy: "u-member"}
	fp, err := f.svc.Footprint(t.Context(), member, pkgID(t, "lime", "planner"))
	if err != nil || len(fp.Releases) != 1 {
		t.Errorf("Footprint() = %+v, %v; want one release", fp, err)
	}
	if _, err := f.svc.Footprint(t.Context(), member, pkgID(t, "acme", "shared")); err != nil {
		t.Errorf("Footprint() of the project's package error = %v, want nil", err)
	}
	for name, tc := range map[string]struct {
		pr   registry.Principal
		id   pack.ID
		want error
	}{
		"a stranger":      {stranger, pkgID(t, "lime", "planner"), registry.ErrForbidden},
		"a machine token": {machine, pkgID(t, "lime", "planner"), registry.ErrForbidden},
		"a missing one":   {member, pkgID(t, "lime", "nope"), registry.ErrNotFound},
	} {
		if _, err := f.svc.Footprint(t.Context(), tc.pr, tc.id); !errors.Is(err, tc.want) {
			t.Errorf("Footprint() for %s error = %v, want %v", name, err, tc.want)
		}
	}
}
