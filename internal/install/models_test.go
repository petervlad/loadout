package install

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/tier"
)

func deepPack(t *testing.T) *pack.Pack {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: lime/planner\nkind: agent\nversion: 1.0.0\ndescription: Plans.\n"),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: Plans.\nmodel: deep\n---\nYou plan.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func alphaDeep(t *testing.T, model string) tier.Table {
	t.Helper()
	tb, err := tier.New(map[tier.Key]tier.Choice{{Tool: "alpha", Hint: agent.ModelDeep}: {Model: model}})
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func TestSameVersionNewTableRewritesTheFile(t *testing.T) {
	f := newFixture(t)
	p := deepPack(t)
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	models := alphaDeep(t, "big-model")
	if changed, err := f.in.Rerenders(t.Context(), p, Options{Scope: f.scope, Models: models}); err != nil || !changed {
		t.Errorf("Rerenders() = %v, %v; want true", changed, err)
	}
	res, err := f.in.Install(t.Context(), p, Options{Scope: f.scope, Models: models})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unchanged {
		t.Error("a new model for the agent's hint was reported Unchanged")
	}
	if got := readFile(t, f.path("alpha/planner.txt")); !strings.Contains(got, "model: big-model") {
		t.Errorf("alpha file = %q, want the new model", got)
	}
	if got := readFile(t, f.path("beta/planner.txt")); strings.Contains(got, "model:") {
		t.Errorf("beta file = %q, want no model (the table names only alpha)", got)
	}
	if e, _ := f.locks.lock.Find(p.ID(), f.scope); e.Models != models.Digest() {
		t.Errorf("lock Models = %q, want %q", e.Models, models.Digest())
	}
	if changed, err := f.in.Rerenders(t.Context(), p, Options{Scope: f.scope, Models: models}); err != nil || changed {
		t.Errorf("Rerenders() after the install = %v, %v; want false", changed, err)
	}
}

func TestNewTableThatDoesNotTouchThePackageOnlyUpdatesTheLock(t *testing.T) {
	tests := []struct {
		name string
		pack func(*testing.T) *pack.Pack
	}{
		{"skill", func(t *testing.T) *pack.Pack { t.Helper(); return makeSkill(t, "lime/notes") }},
		{"agent without a hint", func(t *testing.T) *pack.Pack { t.Helper(); return makePack(t, "1.0.0", "v1") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			p := tc.pack(t)
			if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
				t.Fatal(err)
			}
			models := alphaDeep(t, "big-model")
			if changed, err := f.in.Rerenders(t.Context(), p, Options{Scope: f.scope, Models: models}); err != nil || changed {
				t.Errorf("Rerenders() = %v, %v; want false", changed, err)
			}
			res, err := f.in.Install(t.Context(), p, Options{Scope: f.scope, Models: models})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Unchanged {
				t.Error("install was not Unchanged")
			}
			if e, _ := f.locks.lock.Find(p.ID(), f.scope); e.Models != models.Digest() {
				t.Errorf("lock Models = %q, want %q", e.Models, models.Digest())
			}
		})
	}
}

func TestNewTableNeverOverwritesAnEditedFile(t *testing.T) {
	f := newFixture(t)
	p := deepPack(t)
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path("alpha/planner.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.in.Install(t.Context(), p, Options{Scope: f.scope, Models: alphaDeep(t, "big-model")})
	if _, ok := errors.AsType[*ConflictError](err); !ok {
		t.Fatalf("Install() error = %v, want a conflict", err)
	}
	if got := readFile(t, f.path("alpha/planner.txt")); got != "mine\n" {
		t.Errorf("edited file = %q, want it kept", got)
	}
}

func TestRerendersOfAPackageNotInstalled(t *testing.T) {
	f := newFixture(t)
	if changed, err := f.in.Rerenders(t.Context(), deepPack(t), Options{Scope: f.scope, Models: alphaDeep(t, "x")}); err != nil || changed {
		t.Errorf("Rerenders() = %v, %v; want false", changed, err)
	}
}
