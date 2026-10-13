package release

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
)

// fakeRegistry serves a fixed list of releases.
type fakeRegistry struct {
	versions []string
	err      error
}

func (f fakeRegistry) Releases(context.Context, pack.ID) ([]api.Release, error) {
	if f.err != nil {
		return nil, f.err
	}
	rels := make([]api.Release, len(f.versions))
	for i, v := range f.versions {
		rels[i] = api.Release{Version: v}
	}
	return rels, nil
}

type fakeInstalls []lock.Entry

func (f fakeInstalls) Entries(context.Context) ([]lock.Entry, error) { return f, nil }

func mustID(t *testing.T, s string) pack.ID {
	t.Helper()
	id, err := pack.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func entry(id pack.ID, v semver.Version, kind target.ScopeKind) lock.Entry {
	return lock.Entry{ID: id, Version: v, Scope: target.Scope{Kind: kind}}
}

func versions(entries []lock.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = string(e.Scope.Kind) + "@" + e.Version.String()
	}
	return out
}

func TestNext(t *testing.T) {
	id := mustID(t, "lime/planner")
	tests := []struct {
		name  string
		reg   fakeRegistry
		local semver.Version
		part  semver.Part
		want  semver.Version
	}{
		{"local is newer", fakeRegistry{versions: []string{"1.0.0", "1.1.0"}}, semver.New(1, 2, 0), semver.Patch, semver.New(1, 2, 1)},
		{"published is newer", fakeRegistry{versions: []string{"1.0.0", "2.0.0"}}, semver.New(1, 2, 0), semver.Minor, semver.New(2, 1, 0)},
		{"no releases", fakeRegistry{}, semver.New(0, 1, 0), semver.Major, semver.New(1, 0, 0)},
		{"never published", fakeRegistry{err: fmt.Errorf("releases: %w", api.ErrNotFound)}, semver.New(0, 1, 0), semver.Patch, semver.New(0, 1, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Next(t.Context(), tt.reg, id, tt.local, tt.part)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Next() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNextRegistryError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Next(t.Context(), fakeRegistry{err: boom}, mustID(t, "lime/planner"), semver.New(1, 0, 0), semver.Patch); !errors.Is(err, boom) {
		t.Errorf("Next() error = %v, want %v", err, boom)
	}
}

func TestInstalled(t *testing.T) {
	id, other := mustID(t, "lime/planner"), mustID(t, "lime/other")
	svc := New(fakeInstalls{
		entry(id, semver.New(1, 1, 0), target.ScopeUser),
		entry(other, semver.New(9, 0, 0), target.ScopeUser),
		entry(id, semver.New(1, 3, 0), target.ScopeProject),
	})
	got, err := svc.Installed(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"project@1.3.0", "user@1.1.0"}, versions(got)); diff != "" {
		t.Errorf("Installed() mismatch (-want +got):\n%s", diff)
	}
	if _, err := svc.Installed(t.Context(), mustID(t, "lime/ghost")); !errors.Is(err, install.ErrNotInstalled) {
		t.Errorf("Installed(ghost) error = %v, want ErrNotInstalled", err)
	}
}

func TestPlanRollback(t *testing.T) {
	id := mustID(t, "lime/planner")
	reg := fakeRegistry{versions: []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0"}}
	v := func(s string) *semver.Version {
		p, err := semver.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return &p
	}
	tests := []struct {
		name     string
		installs []lock.Entry
		to       *semver.Version
		want     string
		rollback []string
		keep     []string
	}{
		{
			name:     "previous release",
			installs: []lock.Entry{entry(id, semver.New(1, 3, 0), target.ScopeUser)},
			want:     "1.2.0",
			rollback: []string{"user@1.3.0"},
		},
		{
			name: "every scope at the same version",
			installs: []lock.Entry{
				entry(id, semver.New(1, 3, 0), target.ScopeUser),
				entry(id, semver.New(1, 3, 0), target.ScopeProject),
			},
			want:     "1.2.0",
			rollback: []string{"user@1.3.0", "project@1.3.0"},
		},
		{
			name: "previous of the newest install, older ones kept",
			installs: []lock.Entry{
				entry(id, semver.New(1, 1, 0), target.ScopeUser),
				entry(id, semver.New(1, 3, 0), target.ScopeProject),
			},
			want:     "1.2.0",
			rollback: []string{"project@1.3.0"},
			keep:     []string{"user@1.1.0"},
		},
		{
			name: "explicit version between installs",
			installs: []lock.Entry{
				entry(id, semver.New(1, 3, 0), target.ScopeUser),
				entry(id, semver.New(1, 0, 0), target.ScopeProject),
			},
			to:       v("1.1.0"),
			want:     "1.1.0",
			rollback: []string{"user@1.3.0"},
			keep:     []string{"project@1.0.0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PlanRollback(t.Context(), reg, tt.installs, tt.to)
			if err != nil {
				t.Fatal(err)
			}
			if got.Version.String() != tt.want {
				t.Errorf("PlanRollback().Version = %s, want %s", got.Version, tt.want)
			}
			if diff := cmp.Diff(tt.rollback, versions(got.Rollback)); diff != "" {
				t.Errorf("PlanRollback().Rollback mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.keep, versions(got.Keep), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("PlanRollback().Keep mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPlanRollbackRefuses(t *testing.T) {
	id := mustID(t, "lime/planner")
	reg := fakeRegistry{versions: []string{"1.0.0", "1.1.0"}}
	installs := []lock.Entry{
		entry(id, semver.New(1, 0, 0), target.ScopeUser),
		entry(id, semver.New(1, 1, 0), target.ScopeProject),
	}

	forward := semver.New(1, 1, 0)
	_, err := PlanRollback(t.Context(), reg, installs, &forward)
	notOlder, ok := errors.AsType[*NotOlderError](err)
	if !ok {
		t.Fatalf("PlanRollback(1.1.0) error = %v, want NotOlderError", err)
	}
	if notOlder.Installed != semver.New(1, 1, 0) {
		t.Errorf("NotOlderError.Installed = %s, want the newest install 1.1.0", notOlder.Installed)
	}

	oldest := []lock.Entry{entry(id, semver.New(1, 0, 0), target.ScopeUser)}
	if _, err := PlanRollback(t.Context(), reg, oldest, nil); !errors.Is(err, ErrNoOlder) {
		t.Errorf("PlanRollback(oldest) error = %v, want ErrNoOlder", err)
	}
	if _, err := PlanRollback(t.Context(), reg, nil, nil); !errors.Is(err, install.ErrNotInstalled) {
		t.Errorf("PlanRollback(none) error = %v, want ErrNotInstalled", err)
	}
}

// fetchingRegistry also serves a bundle for every version it lists.
type fetchingRegistry struct {
	fakeRegistry
	fetched string
}

func (f *fetchingRegistry) Fetch(_ context.Context, _ pack.ID, version string) (*pack.Pack, error) {
	f.fetched = version
	return nil, nil
}

func TestResolveTakesTheHighestOfTheMajor(t *testing.T) {
	id := mustID(t, "acme/dev-loop")
	tests := []struct {
		name     string
		versions []string
		major    int
		want     string
		wantErr  error
	}{
		{"highest of the major", []string{"1.0.0", "1.2.0", "1.10.0", "2.0.0"}, 1, "1.10.0", nil},
		{"an older major", []string{"1.0.0", "2.0.0", "2.1.0"}, 1, "1.0.0", nil},
		{"no such major", []string{"1.0.0"}, 2, "", api.ErrNotFound},
		{"no releases", nil, 1, "", api.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fetchingRegistry{fakeRegistry{versions: tc.versions}, ""}
			_, err := Resolve(t.Context(), reg, pack.Requirement{ID: id, Major: tc.major})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Resolve() error = %v, want %v", err, tc.wantErr)
			}
			if reg.fetched != tc.want {
				t.Errorf("fetched %q, want %q", reg.fetched, tc.want)
			}
		})
	}
}
