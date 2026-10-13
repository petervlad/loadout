package update

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// bindingRegistry serves lime/dev-loop, a binding of acme/dev-loop@1, and the
// template's releases.
type bindingRegistry struct {
	fakeRegistry
	template []semver.Version
	// templateErr answers the template's Releases.
	templateErr error
	resolved    int
}

func (r *bindingRegistry) Releases(ctx context.Context, id pack.ID) ([]Release, error) {
	if id.String() == "lime/dev-loop" {
		rels, err := r.fakeRegistry.Releases(ctx, id)
		for i := range rels {
			rels[i].Kind = pack.KindPipeline
		}
		return rels, err
	}
	if r.templateErr != nil {
		return nil, r.templateErr
	}
	var out []Release
	for _, v := range r.template {
		out = append(out, Release{Version: v, Kind: pack.KindPipeline})
	}
	return out, nil
}

func (r *bindingRegistry) Fetch(_ context.Context, id pack.ID, v semver.Version) (*pack.Pack, error) {
	return pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + id.String() + "\nkind: pipeline\nversion: " + v.String() + "\ndescription: d\n"),
		pack.PipelineFile: []byte("uses: acme/dev-loop@1\nbind:\n  plan: lime-planner\nwork_folder: explorations/\n"),
	})
}

func (r *bindingRegistry) Needs(context.Context, *pack.Pack) (Needed, error) {
	r.resolved++
	return Needed{}, nil
}

func requirement(t *testing.T, version string) lock.Requirement {
	t.Helper()
	id, err := pack.NewID("acme", "dev-loop")
	if err != nil {
		t.Fatal(err)
	}
	v, err := semver.Parse(version)
	if err != nil {
		t.Fatal(err)
	}
	return lock.Requirement{ID: id, Version: v}
}

func mustVersion(t *testing.T, v string) semver.Version {
	t.Helper()
	out, err := semver.Parse(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func versions(t *testing.T, vs ...string) []semver.Version {
	t.Helper()
	var out []semver.Version
	for _, v := range vs {
		out = append(out, mustVersion(t, v))
	}
	return out
}

// conductor returns the installed binding rendered from template 1.0.0.
func conductor(t *testing.T, policy lock.Policy) lock.Entry {
	t.Helper()
	e := entry(t, "dev-loop", "1.0.0", policy, "office")
	e.Requires = []lock.Requirement{requirement(t, "1.0.0")}
	return e
}

func syncBinding(t *testing.T, e lock.Entry, reg *bindingRegistry) (Result, *fakeInstaller, *recordingNotifier) {
	t.Helper()
	reg.latest = map[string]semver.Version{"lime/dev-loop": e.Version}
	inst := &fakeInstaller{entries: []lock.Entry{e}, rerenders: true}
	notes := &recordingNotifier{}
	svc := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, notes, &memMemory{}, slog.New(slog.DiscardHandler))
	results, err := svc.Sync(t.Context())
	if err != nil || len(results) != 1 {
		t.Fatalf("Sync() = %+v, %v", results, err)
	}
	return results[0], inst, notes
}

func TestSyncRendersTheConductorAgainOnATemplateMinor(t *testing.T) {
	tests := []struct {
		name        string
		policy      lock.Policy
		want        Outcome
		wantInstall bool
		wantNotes   int
	}{
		{"auto renders it again", lock.PolicyAuto, OutcomeTemplateUpdated, true, 0},
		{"notify reports it", lock.PolicyNotify, OutcomeTemplateChanged, false, 1},
		{"pin does nothing", lock.PolicyPin, OutcomeUpToDate, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := &bindingRegistry{template: versions(t, "1.0.0", "1.1.0")}
			r, inst, notes := syncBinding(t, conductor(t, tc.policy), reg)
			if r.Outcome != tc.want {
				t.Fatalf("outcome = %q, want %q", r.Outcome, tc.want)
			}
			if got := len(inst.installed) == 1; got != tc.wantInstall {
				t.Errorf("installed %v, want install: %v", inst.installed, tc.wantInstall)
			}
			if tc.wantInstall && reg.resolved != 1 {
				t.Errorf("resolved the template %d times, want once", reg.resolved)
			}
			if len(notes.got) != tc.wantNotes {
				t.Fatalf("notifications = %+v, want %d", notes.got, tc.wantNotes)
			}
			if tc.wantNotes == 1 {
				if n := notes.got[0]; n.Template != "acme/dev-loop@1.1.0" || n.ModelsChanged {
					t.Errorf("notification = %+v, want the template 1.1.0 and no model change", n)
				}
			}
		})
	}
}

func TestSyncHoldsATemplateUpdateBackWhenTheConductorWasEdited(t *testing.T) {
	reg := &bindingRegistry{template: versions(t, "1.0.0", "1.1.0")}
	e := conductor(t, lock.PolicyAuto)
	reg.latest = map[string]semver.Version{"lime/dev-loop": e.Version}
	inst := &fakeInstaller{
		entries: []lock.Entry{e}, rerenders: true,
		installErr: &install.ConflictError{Conflicts: []install.Conflict{{Path: "/home/a/.claude/agents/dev-loop.md", Reason: install.ReasonModified}}},
	}
	svc := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, &recordingNotifier{}, &memMemory{}, slog.New(slog.DiscardHandler))
	results, err := svc.Sync(t.Context())
	if err != nil || len(results) != 1 {
		t.Fatalf("Sync() = %+v, %v", results, err)
	}
	if r := results[0]; r.Outcome != OutcomeBlocked || !r.TemplateOnly() || len(r.Held) == 0 {
		t.Errorf("result = %q template %q held %v, want blocked by the edit", r.Outcome, r.Template, r.Held)
	}
}

func TestSyncPrintsOneLineForANewTemplateMajor(t *testing.T) {
	for _, policy := range []lock.Policy{lock.PolicyAuto, lock.PolicyNotify, lock.PolicyPin} {
		t.Run(string(policy), func(t *testing.T) {
			reg := &bindingRegistry{template: versions(t, "1.0.0", "2.0.0")}
			r, inst, notes := syncBinding(t, conductor(t, policy), reg)
			want := "acme/dev-loop 2.0.0 is out. lime/dev-loop uses major 1. Move it with `uses: acme/dev-loop@2` and publish."
			if !slices.Equal(r.Notes, []string{want}) {
				t.Errorf("notes = %q, want %q", r.Notes, want)
			}
			if r.Outcome != OutcomeUpToDate || len(inst.installed) != 0 || len(notes.got) != 0 || r.Behind() {
				t.Errorf("a new major changed state: %q, installed %v, notified %v", r.Outcome, inst.installed, notes.got)
			}
		})
	}
}

func TestSyncRendersAConductorWithoutRecordedRequirements(t *testing.T) {
	// The side file was missing or damaged: nothing records the template.
	e := conductor(t, lock.PolicyAuto)
	e.Requires = nil
	reg := &bindingRegistry{template: versions(t, "1.0.0")}
	r, inst, _ := syncBinding(t, e, reg)
	if r.Outcome != OutcomeTemplateUpdated || len(inst.installed) != 1 {
		t.Errorf("outcome = %q, installed %v; want the conductor rendered again", r.Outcome, inst.installed)
	}

	e.Policy = lock.PolicyNotify
	reg = &bindingRegistry{template: versions(t, "1.0.0")}
	if r, inst, _ = syncBinding(t, e, reg); r.Outcome != OutcomeTemplateChanged || len(inst.installed) != 0 {
		t.Errorf("notify: outcome = %q, installed %v; want it reported only", r.Outcome, inst.installed)
	}
}

func TestSyncSaysWhenTheTemplateMajorIsGone(t *testing.T) {
	for name, reg := range map[string]*bindingRegistry{
		"no release in the major": {template: versions(t, "2.0.0")},
		"template unknown":        {templateErr: api.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			r, inst, _ := syncBinding(t, conductor(t, lock.PolicyAuto), reg)
			if r.Outcome != OutcomeUpToDate || len(inst.installed) != 0 {
				t.Errorf("outcome = %q, installed %v; want the conductor left alone", r.Outcome, inst.installed)
			}
			if i := slices.IndexFunc(r.Notes, func(n string) bool { return strings.Contains(n, "no longer lists") }); i < 0 {
				t.Errorf("notes = %q, want a line saying the major is gone", r.Notes)
			}
		})
	}
}

func TestSyncFailsWhenTheTemplateCannotBeListed(t *testing.T) {
	reg := &bindingRegistry{templateErr: errors.New("timeout")}
	r, inst, _ := syncBinding(t, conductor(t, lock.PolicyAuto), reg)
	if r.Outcome != OutcomeFailed || len(inst.installed) != 0 {
		t.Errorf("outcome = %q, installed %v; want a failed check", r.Outcome, inst.installed)
	}
}

func TestSyncInstallsANewBindingVersionWithTheTemplateResolved(t *testing.T) {
	reg := &bindingRegistry{template: versions(t, "1.0.0", "1.1.0")}
	e := conductor(t, lock.PolicyAuto)
	inst := &fakeInstaller{entries: []lock.Entry{e}}
	reg.latest = map[string]semver.Version{"lime/dev-loop": semver.New(1, 1, 0)}
	svc := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, &recordingNotifier{}, &memMemory{}, slog.New(slog.DiscardHandler))
	results, err := svc.Sync(t.Context())
	if err != nil || len(results) != 1 || results[0].Outcome != OutcomeUpdated {
		t.Fatalf("Sync() = %+v, %v; want updated", results, err)
	}
	if reg.resolved != 1 {
		t.Errorf("resolved the template %d times, want once", reg.resolved)
	}
}
