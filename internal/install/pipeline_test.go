package install

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
)

func pipelinePack(t *testing.T, id, version, file string) *pack.Pack {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "pipeline", "testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + id + "\nkind: pipeline\nversion: " + version + "\ndescription: d\n"),
		pack.PipelineFile: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func agentNamed(t *testing.T, team, name string) *pack.Pack {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + team + "/" + name + "\nkind: agent\nversion: 1.0.0\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: " + name + "\ndescription: d\ntools: [read, shell]\n---\nprompt\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func claudeFixture(t *testing.T) fixture {
	t.Helper()
	locks := &memLocks{}
	return fixture{
		in:    New(locks, slog.New(slog.DiscardHandler), claudecode.New()),
		locks: locks,
		scope: target.Scope{Kind: target.ScopeUser, Root: t.TempDir()},
	}
}

func TestInstallBindingRecordsTemplateAndWarnsAboutMissingAgents(t *testing.T) {
	f := claudeFixture(t)
	ctx := t.Context()
	tmpl := pipelinePack(t, "acme/dev-loop", "1.2.0", "dev-loop.yaml")
	binding := pipelinePack(t, "lime/dev-loop", "1.0.0", "lime-binding.yaml")

	var defs []agent.Definition
	for _, name := range []string{"lime-planner", "lime-dev", "lime-code-review", "lime-test"} {
		def, _ := agentNamed(t, "lime", name).Agent()
		defs = append(defs, def)
	}
	opts := Options{Scope: f.scope, Requires: []*pack.Pack{tmpl}, Agents: defs}
	res, err := f.in.Install(ctx, binding, opts)
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	if len(res.Warnings) != 4 || !strings.Contains(strings.Join(res.Warnings, "\n"), "needs the agent lime-planner, which is not installed here") {
		t.Errorf("Warnings = %q, want one per missing agent", res.Warnings)
	}
	want := []lock.Requirement{{ID: tmpl.ID(), Version: tmpl.Version(), Digest: tmpl.Digest()}}
	if got := res.Entry.Requires; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Entry.Requires = %+v, want %+v", got, want)
	}
	entries, _ := f.in.Entries(ctx)
	if len(entries) != 1 || len(entries[0].Requires) != 1 {
		t.Errorf("lock entries = %+v, want the requirement kept", entries)
	}

	for _, name := range []string{"lime-planner", "lime-dev", "lime-code-review", "lime-test"} {
		if _, err := f.in.Install(ctx, agentNamed(t, "lime", name), Options{Scope: f.scope}); err != nil {
			t.Fatal(err)
		}
	}
	res, err = f.in.Install(ctx, binding, opts)
	if err != nil || !res.Unchanged || len(res.Warnings) != 0 {
		t.Errorf("Install() again = unchanged %v, warnings %q, %v; want unchanged and silent", res.Unchanged, res.Warnings, err)
	}
}

func TestInstallTemplateIsRefused(t *testing.T) {
	f := claudeFixture(t)
	_, err := f.in.Install(t.Context(), pipelinePack(t, "acme/dev-loop", "1.2.0", "dev-loop.yaml"), Options{Scope: f.scope})
	if !errors.Is(err, target.ErrNotStandalone) || !strings.Contains(err.Error(), "installs through a team's binding") {
		t.Errorf("Install(template) = %v, want ErrNotStandalone with the reason", err)
	}
	if entries, _ := f.in.Entries(t.Context()); len(entries) != 0 {
		t.Errorf("lock = %+v, want nothing recorded", entries)
	}
}

func TestMergeOfAConductorIsRefused(t *testing.T) {
	f := claudeFixture(t)
	binding := pipelinePack(t, "lime/dev-loop", "1.0.0", "lime-binding.yaml")
	_, err := f.in.Merge(t.Context(), binding, binding, Options{Scope: f.scope})
	if !errors.Is(err, ErrMergeGenerated) {
		t.Errorf("Merge() = %v, want ErrMergeGenerated", err)
	}
}
