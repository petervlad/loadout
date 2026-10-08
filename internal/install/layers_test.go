package install

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
)

// layerPack returns ns/planner, which renders to the same files as every
// other namespace's planner.
func layerPack(t *testing.T, ns, version string, targets ...string) *pack.Pack {
	t.Helper()
	manifest := "name: " + ns + "/planner\nkind: agent\nversion: " + version + "\ndescription: d\n"
	if len(targets) > 0 {
		manifest += "targets: [" + strings.Join(targets, ", ") + "]\n"
	}
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte(manifest),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: d\n---\nfrom " + ns + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f fixture) entry(t *testing.T, id pack.ID) lock.Entry {
	t.Helper()
	l, err := f.locks.Load()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := l.Find(id, f.scope)
	if !ok {
		t.Fatalf("%s is not in the lock", id)
	}
	return e
}

func paths[T any](items []T, path func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, path(it))
	}
	slices.Sort(out)
	return out
}

func filePaths(e lock.Entry) []string {
	return paths(e.Files, func(f lock.FileRecord) string { return f.Path })
}

func shadowPaths(e lock.Entry) []string {
	return paths(e.Shadowed, func(s lock.Shadow) string { return s.Path })
}

func TestLayerPrecedence(t *testing.T) {
	both := []string{"alpha/planner.txt", "beta/planner.txt"}
	tests := []struct {
		name string
		// first is installed, then second; each is namespace, depth, targets.
		firstNS, secondNS       string
		firstDepth, secondDepth int
		firstTargets            []string
		wantOnDisk              map[string]string // path -> namespace whose content it has
		wantFirstFiles          []string
		wantFirstShadowed       []string
		wantSecondFiles         []string
		wantSecondShadowed      []string
		wantTookOver            bool
	}{
		{
			name:    "closer installed after farther takes the files over",
			firstNS: "acme", firstDepth: 1, secondNS: "lime", secondDepth: 0,
			wantOnDisk:     map[string]string{"alpha/planner.txt": "lime", "beta/planner.txt": "lime"},
			wantFirstFiles: []string{}, wantFirstShadowed: both,
			wantSecondFiles: both, wantSecondShadowed: []string{},
			wantTookOver: true,
		},
		{
			name:    "farther installed after closer is shadowed",
			firstNS: "lime", firstDepth: 0, secondNS: "acme", secondDepth: 1,
			wantOnDisk:     map[string]string{"alpha/planner.txt": "lime", "beta/planner.txt": "lime"},
			wantFirstFiles: both, wantFirstShadowed: []string{},
			wantSecondFiles: []string{}, wantSecondShadowed: both,
		},
		{
			name:    "farther fills the targets the closer does not have",
			firstNS: "lime", firstDepth: 0, firstTargets: []string{"alpha"}, secondNS: "acme", secondDepth: 1,
			wantOnDisk:     map[string]string{"alpha/planner.txt": "lime", "beta/planner.txt": "acme"},
			wantFirstFiles: []string{"alpha/planner.txt"}, wantFirstShadowed: []string{},
			wantSecondFiles: []string{"beta/planner.txt"}, wantSecondShadowed: []string{"alpha/planner.txt"},
		},
		{
			name:    "grandparent loses to parent",
			firstNS: "acme", firstDepth: 2, secondNS: "lime", secondDepth: 1,
			wantOnDisk:     map[string]string{"alpha/planner.txt": "lime", "beta/planner.txt": "lime"},
			wantFirstFiles: []string{}, wantFirstShadowed: both,
			wantSecondFiles: both, wantSecondShadowed: []string{},
			wantTookOver: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			first := layerPack(t, tc.firstNS, "1.0.0", tc.firstTargets...)
			second := layerPack(t, tc.secondNS, "1.0.0")
			if _, err := f.in.Install(t.Context(), first, Options{Scope: f.scope, Depth: tc.firstDepth}); err != nil {
				t.Fatalf("Install(%s) error: %v", first.ID(), err)
			}
			res, err := f.in.Install(t.Context(), second, Options{Scope: f.scope, Depth: tc.secondDepth})
			if err != nil {
				t.Fatalf("Install(%s) error: %v", second.ID(), err)
			}
			if got := len(res.TookOver) > 0; got != tc.wantTookOver {
				t.Errorf("TookOver = %v, want some: %v", res.TookOver, tc.wantTookOver)
			}
			for p, ns := range tc.wantOnDisk {
				if got := readFile(t, f.path(p)); !strings.Contains(got, "from "+ns) {
					t.Errorf("%s = %q, want %s's version", p, got, ns)
				}
			}
			fe, se := f.entry(t, first.ID()), f.entry(t, second.ID())
			check := func(what string, got, want []string) {
				t.Helper()
				if !slices.Equal(got, want) {
					t.Errorf("%s = %v, want %v", what, got, want)
				}
			}
			check("first files", filePaths(fe), tc.wantFirstFiles)
			check("first shadowed", shadowPaths(fe), tc.wantFirstShadowed)
			check("second files", filePaths(se), tc.wantSecondFiles)
			check("second shadowed", shadowPaths(se), tc.wantSecondShadowed)

			// Status stays truthful: every file is ok or shadowed.
			statuses, err := f.in.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, st := range statuses {
				for _, file := range st.Files {
					if file.State != StateOK && file.State != StateShadowed {
						t.Errorf("status of %s %s = %s, want ok or shadowed", st.Entry.ID, file.Path, file.State)
					}
				}
			}
		})
	}
}

func TestShadowedUpgradeNeverClobbersTheCloserFile(t *testing.T) {
	f := newFixture(t)
	lime, acme := layerPack(t, "lime", "1.0.0"), layerPack(t, "acme", "1.0.0")
	if _, err := f.in.Install(t.Context(), lime, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(t.Context(), acme, Options{Scope: f.scope, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	res, err := f.in.Install(t.Context(), acme, Options{Scope: f.scope, Depth: 1})
	if err != nil || !res.Unchanged {
		t.Errorf("reinstall of a shadowed package = %+v, %v; want unchanged", res, err)
	}

	res, err = f.in.Install(t.Context(), layerPack(t, "acme", "2.0.0"), Options{Scope: f.scope, Depth: 1})
	if err != nil {
		t.Fatalf("Install(acme 2.0.0) error: %v", err)
	}
	if !res.Entry.FullyShadowed() || !slices.Equal(res.ShadowedBy(), []pack.ID{lime.ID()}) {
		t.Errorf("Install(acme 2.0.0) = %+v, want fully shadowed by lime/planner", res.Entry)
	}
	for _, p := range []string{"alpha/planner.txt", "beta/planner.txt"} {
		if got := readFile(t, f.path(p)); !strings.Contains(got, "from lime") {
			t.Errorf("%s = %q, want lime's version kept", p, got)
		}
	}
}

func TestTakeOverKeepsEditedFiles(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), layerPack(t, "acme", "1.0.0"), Options{Scope: f.scope, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path("alpha/planner.txt"), []byte("my tweak"), 0o644); err != nil {
		t.Fatal(err)
	}
	lime := layerPack(t, "lime", "1.0.0")
	_, err := f.in.Install(t.Context(), lime, Options{Scope: f.scope})
	ce, ok := errors.AsType[*ConflictError](err)
	if !ok || len(ce.Conflicts) != 1 || ce.Conflicts[0].Reason != ReasonModified {
		t.Fatalf("Install(lime) over an edited acme file error = %v, want one modified conflict", err)
	}
	if got := readFile(t, f.path("alpha/planner.txt")); got != "my tweak" {
		t.Errorf("edited file = %q, want it kept", got)
	}
	if _, err := f.in.Install(t.Context(), lime, Options{Scope: f.scope, Force: true}); err != nil {
		t.Fatalf("Install(lime, Force) error: %v", err)
	}
	if got := readFile(t, f.path("alpha/planner.txt")); !strings.Contains(got, "from lime") {
		t.Errorf("after --force file = %q, want lime's version", got)
	}
}

func TestUninstallingTheCloserUnshadows(t *testing.T) {
	f := newFixture(t)
	lime, acme := layerPack(t, "lime", "1.0.0"), layerPack(t, "acme", "1.0.0")
	if _, err := f.in.Install(t.Context(), lime, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(t.Context(), acme, Options{Scope: f.scope, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	removed, err := f.in.Uninstall(t.Context(), lime.ID(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || !slices.Equal(removed[0].Unshadowed, []pack.ID{acme.ID()}) {
		t.Errorf("Uninstall(lime) = %+v, want acme/planner unshadowed", removed)
	}
	statuses, err := f.in.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State() != StateMissing {
		t.Errorf("status after the closer is gone = %+v, want acme/planner missing", statuses)
	}

	res, err := f.in.Install(t.Context(), acme, Options{Scope: f.scope, Depth: 1})
	if err != nil || res.Unchanged {
		t.Fatalf("reinstall acme = %+v, %v; want files written", res, err)
	}
	if got := readFile(t, f.path("alpha/planner.txt")); !strings.Contains(got, "from acme") {
		t.Errorf("after reinstall file = %q, want acme's version restored", got)
	}
	if e := f.entry(t, acme.ID()); len(e.Shadowed) != 0 || len(e.Files) != 2 {
		t.Errorf("acme entry = %+v, want two files and no shadows", e)
	}
}
