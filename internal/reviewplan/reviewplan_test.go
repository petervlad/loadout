package reviewplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/release"
	"github.com/petervlad/loadout/internal/semver"
)

// The real services the CLI wires in.
var (
	_ Registry = (*client.Client)(nil)
	_ Installs = (*release.Service)(nil)
)

// fakeRegistry serves releases, proposals and a package listing from
// memory. Packages are made on demand from id and version, so each version
// has its own digest.
type fakeRegistry struct {
	t         *testing.T
	releases  map[string][]string // id -> versions, lowest first
	proposals []api.Proposal      // any status, any order
	packages  []string            // the view's listing as "id@version", closest layer first
	kinds     map[string]string   // kind of a listed package id, agent when absent
	fetched   []string
	failFetch string // "id@version" whose Fetch fails
}

func (f *fakeRegistry) Releases(_ context.Context, id pack.ID) ([]api.Release, error) {
	vs, ok := f.releases[id.String()]
	if !ok {
		return nil, fmt.Errorf("releases: %w", api.ErrNotFound)
	}
	out := make([]api.Release, len(vs))
	for i, v := range vs {
		out[i] = f.release(id.String(), v)
	}
	return out, nil
}

func (f *fakeRegistry) release(id, version string) api.Release {
	return api.Release{ID: id, Version: version, Digest: string(makePack(f.t, id, version).Digest())}
}

func (f *fakeRegistry) Fetch(_ context.Context, id pack.ID, version string) (*pack.Pack, error) {
	key := id.String() + "@" + version
	f.fetched = append(f.fetched, key)
	if key == f.failFetch {
		return nil, errors.New("connection reset")
	}
	return makePack(f.t, id.String(), version), nil
}

func (f *fakeRegistry) Proposals(_ context.Context, pkg, status string) ([]api.Proposal, error) {
	var out []api.Proposal
	for _, p := range f.proposals {
		if p.Package == pkg && p.Status == status {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeRegistry) Proposal(_ context.Context, id string) (api.Proposal, error) {
	for _, p := range f.proposals {
		if p.ID == id {
			return p, nil
		}
	}
	return api.Proposal{}, fmt.Errorf("proposal: %w", api.ErrNotFound)
}

func (f *fakeRegistry) FetchProposal(_ context.Context, p api.Proposal) (*pack.Pack, error) {
	f.fetched = append(f.fetched, "proposal "+p.ID)
	return makeProposalPack(f.t, p), nil
}

func (f *fakeRegistry) Packages(context.Context) ([]api.Release, error) {
	out := make([]api.Release, len(f.packages))
	for i, s := range f.packages {
		id, v, _ := strings.Cut(s, "@")
		out[i] = f.release(id, v)
		out[i].Kind = f.kinds[id]
	}
	return out, nil
}

type fakeInstalls []lock.Entry

func (f fakeInstalls) Installed(_ context.Context, id pack.ID) ([]lock.Entry, error) {
	var out []lock.Entry
	for _, e := range f {
		if e.ID == id {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w", id, install.ErrNotInstalled)
	}
	return out, nil
}

func makePack(t *testing.T, id, version string) *pack.Pack {
	t.Helper()
	return makeAgent(t, id, version, "release")
}

func makeProposalPack(t *testing.T, p api.Proposal) *pack.Pack {
	t.Helper()
	return makeAgent(t, p.Package, p.Version, "proposal "+p.ID)
}

func makeAgent(t *testing.T, id, version, body string) *pack.Pack {
	t.Helper()
	_, name, _ := strings.Cut(id, "/")
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + id + "\nkind: agent\nversion: " + version + "\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: " + name + "\ndescription: d\n---\n" + body + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func installed(id, version, remote string) lock.Entry {
	v, err := semver.Parse(version)
	if err != nil {
		panic(err)
	}
	pid, err := pack.ParseID(id)
	if err != nil {
		panic(err)
	}
	return lock.Entry{ID: pid, Version: v, Remote: remote}
}

func proposal(id, version string, age time.Duration, status string) api.Proposal {
	return api.Proposal{
		ID: id, Package: "lime/planner", Version: version, Status: status,
		CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(-age),
	}
}

// describe turns a side into "source version [proposal]" for comparison.
func describe(s Side) string {
	if s.Pack == nil {
		return "none"
	}
	out := string(s.Source) + " " + s.Version()
	if s.ProposalID != "" {
		out += " " + s.ProposalID
	}
	return out
}

const (
	propA = "aaaaaaaaaaaa"
	propB = "bbbbbbbbbbbb"
	propC = "cccccccccccc"
)

func TestResolveSides(t *testing.T) {
	released := map[string][]string{"lime/planner": {"1.1.0", "1.2.0", "1.3.0"}}
	openA := proposal(propA, "1.4.0", 2*time.Hour, api.StatusOpen)
	openB := proposal(propB, "1.5.0", time.Hour, api.StatusOpen)
	closedC := proposal(propC, "1.4.1", 0, "rejected")
	behindA := openA
	behindA.Base, behindA.Latest = "1.2.0", "1.3.0"

	tests := []struct {
		name      string
		releases  map[string][]string
		proposals []api.Proposal
		installs  fakeInstalls
		from, to  string
		wantFrom  string
		wantTo    string
		wantProp  string // id of Plan.Proposal
		wantNote  string // a substring of one note
		wantErr   string
	}{
		{
			name: "installed version against the open proposal", releases: released,
			proposals: []api.Proposal{openA}, installs: fakeInstalls{installed("lime/planner", "1.2.0", "work")},
			wantFrom: "installed 1.2.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
		},
		{
			name: "nothing installed compares with the latest release", releases: released,
			proposals: []api.Proposal{openA},
			wantFrom:  "release 1.3.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
			wantNote: "You have not installed lime/planner; comparing with the latest release 1.3.0.",
		},
		{
			name: "a behind proposal says so and what the report compares with", releases: released,
			proposals: []api.Proposal{behindA}, installs: fakeInstalls{installed("lime/planner", "1.2.0", "work")},
			wantFrom: "installed 1.2.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
			wantNote: "Proposal " + propA + " is based on 1.2.0; 1.3.0 was released since, so approving it would release 1.4.0 without 1.3.0's changes. The report compares it with what you have installed, 1.2.0.",
		},
		{
			name: "a behind proposal against the latest release", releases: released,
			proposals: []api.Proposal{behindA},
			wantFrom:  "release 1.3.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
			wantNote: "The report compares it with the release 1.3.0.",
		},
		{
			name: "folder install compares with the latest release", releases: released,
			proposals: []api.Proposal{openA}, installs: fakeInstalls{installed("lime/planner", "1.2.0", "")},
			wantFrom: "release 1.3.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
			wantNote: "comes from a folder",
		},
		{
			name: "installed version the registry no longer has", releases: released,
			proposals: []api.Proposal{openA}, installs: fakeInstalls{installed("lime/planner", "1.0.0", "work")},
			wantFrom: "release 1.3.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
			wantNote: "Your installed lime/planner 1.0.0 is no longer on the registry",
		},
		{
			name: "several scopes take the highest", releases: released,
			proposals: []api.Proposal{openA},
			installs: fakeInstalls{
				installed("lime/planner", "1.1.0", "work"),
				installed("lime/planner", "1.2.0", "work"),
				installed("lime/planner", "1.2.0", ""),
			},
			wantFrom: "installed 1.2.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
			wantNote: "different versions in different places; comparing with the highest, 1.2.0.",
		},
		{
			name: "other packages' installs are ignored", releases: released,
			proposals: []api.Proposal{openA}, installs: fakeInstalls{installed("lime/other", "1.2.0", "work")},
			wantFrom: "release 1.3.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
		},
		{
			name: "several open proposals take the newest", releases: released,
			proposals: []api.Proposal{openA, openB, closedC},
			wantFrom:  "release 1.3.0", wantTo: "proposal 1.5.0 " + propB, wantProp: propB,
			wantNote: "has 2 open proposals; reviewing the newest, " + propB,
		},
		{
			name: "no open proposal reviews the latest release", releases: released,
			proposals: []api.Proposal{closedC}, installs: fakeInstalls{installed("lime/planner", "1.1.0", "work")},
			wantFrom: "installed 1.1.0", wantTo: "release 1.3.0",
			wantNote: "has no open proposal; reviewing the latest release 1.3.0.",
		},
		{
			name:      "no release yet runs the proposal alone",
			proposals: []api.Proposal{openA},
			wantFrom:  "none", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
			wantNote: "has no release yet, so only the version under review runs.",
		},
		{
			name:    "nothing to review",
			wantErr: "lime/planner has no release and no open proposal to review",
		},
		{
			name: "equal sides", releases: released,
			installs: fakeInstalls{installed("lime/planner", "1.3.0", "work")},
			wantErr:  "both sides are lime/planner 1.3.0; pass --from",
		},
		{
			name: "from and to as versions", releases: released, proposals: []api.Proposal{openA},
			from: "1.1.0", to: "1.2.0",
			wantFrom: "release 1.1.0", wantTo: "release 1.2.0",
		},
		{
			name: "a version that is only a proposal", releases: released, proposals: []api.Proposal{openA, openB},
			to:       "1.4.0",
			wantFrom: "release 1.3.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
		},
		{
			name: "from and to as proposal ids, closed ones too", releases: released,
			proposals: []api.Proposal{openA, openB, closedC},
			from:      propA, to: propC,
			wantFrom: "proposal 1.4.0 " + propA, wantTo: "proposal 1.4.1 " + propC, wantProp: propC,
		},
		{
			name: "latest", releases: released, proposals: []api.Proposal{openA},
			installs: fakeInstalls{installed("lime/planner", "1.1.0", "work")},
			from:     "latest",
			wantFrom: "release 1.3.0", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
		},
		{
			name: "from latest without a release runs the proposal alone", proposals: []api.Proposal{openA},
			from:     "latest",
			wantFrom: "none", wantTo: "proposal 1.4.0 " + propA, wantProp: propA,
			wantNote: "has no release yet, so only the version under review runs.",
		},
		{
			name: "to latest without a release", proposals: []api.Proposal{openA},
			to: "latest", wantErr: "lime/planner has no release yet",
		},
		{
			name: "unknown version", releases: released, proposals: []api.Proposal{openA},
			to: "2.0.0", wantErr: "lime/planner has no release or open proposal 2.0.0",
		},
		{
			name: "unknown proposal id", releases: released,
			to: "0123456789ab", wantErr: "there is no proposal 0123456789ab",
		},
		{
			name: "proposal of another package", releases: released,
			proposals: []api.Proposal{{ID: propA, Package: "lime/other", Version: "1.0.0", Status: api.StatusOpen}},
			to:        propA, wantErr: "proposal " + propA + " is for lime/other, not lime/planner",
		},
		{
			name: "neither a version nor an id", releases: released,
			from: "main", wantErr: `"main" is neither a version`,
		},
		{
			name: "same proposal on both sides", releases: released, proposals: []api.Proposal{openA},
			from: propA, wantErr: "both sides are lime/planner 1.4.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &fakeRegistry{t: t, releases: tt.releases, proposals: tt.proposals}
			plan, err := NewPlanner(reg, tt.installs).Resolve(t.Context(), Request{
				Package: plannerID(t), From: tt.from, To: tt.to, NoContext: true,
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Resolve() error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := describe(plan.From); got != tt.wantFrom {
				t.Errorf("From = %s, want %s", got, tt.wantFrom)
			}
			if got := describe(plan.To); got != tt.wantTo {
				t.Errorf("To = %s, want %s", got, tt.wantTo)
			}
			gotProp := ""
			if plan.Proposal != nil {
				gotProp = plan.Proposal.ID
			}
			if gotProp != tt.wantProp {
				t.Errorf("Proposal = %q, want %q", gotProp, tt.wantProp)
			}
			if tt.wantNote != "" && !strings.Contains(strings.Join(plan.Notes, "\n"), tt.wantNote) {
				t.Errorf("Notes = %q, want one containing %q", plan.Notes, tt.wantNote)
			}
			if plan.Context != nil || plan.ContextReleases != nil {
				t.Error("NoContext still produced a context")
			}
		})
	}
}

func TestResolveNoBehindNote(t *testing.T) {
	released := map[string][]string{"lime/planner": {"1.2.0", "1.3.0"}}
	base := proposal(propA, "1.4.0", time.Hour, api.StatusOpen)
	upToDate, olderRegistry := base, base
	upToDate.Base, upToDate.Latest = "1.3.0", "1.3.0"
	for name, p := range map[string]api.Proposal{"up to date": upToDate, "older registry": olderRegistry} {
		t.Run(name, func(t *testing.T) {
			reg := &fakeRegistry{t: t, releases: released, proposals: []api.Proposal{p}}
			plan, err := NewPlanner(reg, nil).Resolve(t.Context(), Request{Package: plannerID(t), NoContext: true})
			if err != nil {
				t.Fatal(err)
			}
			if notes := strings.Join(plan.Notes, "\n"); strings.Contains(notes, "released since") {
				t.Errorf("Notes = %q, want no behind note", notes)
			}
		})
	}
}

func TestResolveWithoutInstalls(t *testing.T) {
	reg := &fakeRegistry{t: t, releases: map[string][]string{"lime/planner": {"1.0.0", "1.1.0"}}}
	plan, err := NewPlanner(reg, nil).Resolve(t.Context(), Request{Package: plannerID(t), From: "1.0.0", NoContext: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := describe(plan.From) + " / " + describe(plan.To); got != "release 1.0.0 / release 1.1.0" {
		t.Errorf("sides = %s", got)
	}
}

func TestResolveRefusesNoPackage(t *testing.T) {
	_, err := NewPlanner(&fakeRegistry{t: t}, nil).Resolve(t.Context(), Request{})
	if err == nil {
		t.Fatal("Resolve() without a package succeeded")
	}
}

func TestResolveContext(t *testing.T) {
	tests := []struct {
		name     string
		packages []string
		want     []string
		wantNote string
	}{
		{
			name:     "every package but the one under review",
			packages: []string{"lime/planner@1.3.0", "lime/lime-kb@2.0.0", "acme/python-standards@1.0.0"},
			want:     []string{"lime/lime-kb@2.0.0", "acme/python-standards@1.0.0"},
		},
		{
			name:     "a closer layer shadows a farther one",
			packages: []string{"lime/style@1.0.0", "lime/planner@1.3.0", "acme/style@3.0.0"},
			want:     []string{"lime/style@1.0.0"},
			wantNote: "Left acme/style out of the context: lime/style has the same name.",
		},
		{
			name:     "the package under review keeps its name",
			packages: []string{"lime/planner@1.3.0", "acme/planner@1.0.0", "acme/other@1.0.0"},
			want:     []string{"acme/other@1.0.0"},
			wantNote: "Left acme/planner out of the context: lime/planner has the same name.",
		},
		{
			name: "an empty team",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &fakeRegistry{
				t:         t,
				releases:  map[string][]string{"lime/planner": {"1.3.0"}},
				proposals: []api.Proposal{proposal(propA, "1.4.0", 0, api.StatusOpen)},
				packages:  tt.packages,
			}
			plan, err := NewPlanner(reg, nil).Resolve(t.Context(), Request{Package: plannerID(t)})
			if err != nil {
				t.Fatal(err)
			}
			var listed []string
			for _, rel := range plan.ContextReleases {
				listed = append(listed, rel.ID+"@"+rel.Version)
			}
			if diff := cmp.Diff(tt.want, listed); diff != "" {
				t.Errorf("ContextReleases (-want +got):\n%s", diff)
			}

			// Nothing is downloaded until the sequence is walked, and then
			// exactly what was listed, in order.
			reg.fetched = nil
			var got []string
			for p, err := range plan.Context {
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, p.ID().String()+"@"+p.Version().String())
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Context (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.want, reg.fetched); diff != "" {
				t.Errorf("fetched (-want +got):\n%s", diff)
			}
			if tt.wantNote != "" && !strings.Contains(strings.Join(plan.Notes, "\n"), tt.wantNote) {
				t.Errorf("Notes = %q, want one containing %q", plan.Notes, tt.wantNote)
			}
		})
	}
}

func TestContextLeavesOutPipelines(t *testing.T) {
	reg := &fakeRegistry{
		t:         t,
		releases:  map[string][]string{"lime/planner": {"1.3.0"}},
		proposals: []api.Proposal{proposal(propA, "1.4.0", 0, api.StatusOpen)},
		packages:  []string{"lime/dev-loop@1.0.0", "acme/dev-loop-template@1.0.0", "lime/lime-kb@2.0.0"},
		kinds:     map[string]string{"lime/dev-loop": "pipeline", "acme/dev-loop-template": "pipeline"},
	}
	plan, err := NewPlanner(reg, nil).Resolve(t.Context(), Request{Package: plannerID(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ContextReleases) != 1 || plan.ContextReleases[0].ID != "lime/lime-kb" {
		t.Errorf("ContextReleases = %+v, want only lime/lime-kb", plan.ContextReleases)
	}
	if notes := strings.Join(plan.Notes, "\n"); strings.Contains(notes, "dev-loop") {
		t.Errorf("Notes = %q, want no note about a pipeline", plan.Notes)
	}
}

func TestContextStopsAtFirstError(t *testing.T) {
	reg := &fakeRegistry{
		t:         t,
		releases:  map[string][]string{"lime/planner": {"1.3.0"}},
		proposals: []api.Proposal{proposal(propA, "1.4.0", 0, api.StatusOpen)},
		packages:  []string{"lime/a@1.0.0", "lime/b@1.0.0", "lime/c@1.0.0"},
		failFetch: "lime/b@1.0.0",
	}
	plan, err := NewPlanner(reg, nil).Resolve(t.Context(), Request{Package: plannerID(t)})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	var gotErr error
	for p, err := range plan.Context {
		if err != nil {
			gotErr = err
			continue
		}
		got = append(got, p.ID().String())
	}
	if diff := cmp.Diff([]string{"lime/a"}, got); diff != "" {
		t.Errorf("packages before the error (-want +got):\n%s", diff)
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "fetch lime/b@1.0.0") {
		t.Errorf("error = %v, want one naming lime/b@1.0.0", gotErr)
	}
}

func TestContextStopsWhenCancelled(t *testing.T) {
	reg := &fakeRegistry{
		t:         t,
		releases:  map[string][]string{"lime/planner": {"1.3.0"}},
		proposals: []api.Proposal{proposal(propA, "1.4.0", 0, api.StatusOpen)},
		packages:  []string{"lime/a@1.0.0"},
	}
	ctx, cancel := context.WithCancel(t.Context())
	plan, err := NewPlanner(reg, nil).Resolve(ctx, Request{Package: plannerID(t)})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for _, err := range plan.Context {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	}
}

// digestLiar lists a digest the content does not have.
type digestLiar struct{ *fakeRegistry }

func (d digestLiar) Packages(ctx context.Context) ([]api.Release, error) {
	rels, err := d.fakeRegistry.Packages(ctx)
	for i := range rels {
		rels[i].Digest = "sha256:0000"
	}
	return rels, err
}

func TestContextChecksTheListedDigest(t *testing.T) {
	reg := digestLiar{&fakeRegistry{
		t:         t,
		releases:  map[string][]string{"lime/planner": {"1.3.0"}},
		proposals: []api.Proposal{proposal(propA, "1.4.0", 0, api.StatusOpen)},
		packages:  []string{"lime/a@1.0.0"},
	}}
	plan, err := NewPlanner(reg, nil).Resolve(t.Context(), Request{Package: plannerID(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range plan.Context {
		if err == nil || !strings.Contains(err.Error(), "the registry listed sha256:0000") {
			t.Errorf("error = %v, want a digest mismatch", err)
		}
	}
}

func plannerID(t *testing.T) pack.ID {
	t.Helper()
	id, err := pack.ParseID("lime/planner")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
