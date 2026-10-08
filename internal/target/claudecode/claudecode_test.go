package claudecode

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

var update = flag.Bool("update", false, "rewrite golden files")

func testStamp(t *testing.T) target.Stamp {
	t.Helper()
	id, err := pack.NewID("lime", "planner")
	if err != nil {
		t.Fatal(err)
	}
	return target.Stamp{ID: id, Version: semver.New(1, 3, 0), Digest: "sha256:0123456789abcdef"}
}

func TestRenderGolden(t *testing.T) {
	tests := []struct {
		name string
		def  agent.Definition
	}{
		{
			name: "full",
			def: agent.Definition{
				Name:        "planner",
				Description: "Turn a ticket into a plan. Use when the user says: plan this.",
				Model:       agent.ModelDeep,
				Tools:       []agent.Tool{agent.ToolRead, agent.ToolSearch, agent.ToolShell},
				Prompt:      "You are the team's planner.\n\nReturn an ordered plan.",
			},
		},
		{
			name: "minimal",
			def: agent.Definition{
				Name:        "planner",
				Description: "Plans.",
				Prompt:      "You plan.",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := New().RenderAgent(tc.def, target.Choose(New(), tier.Table{}, tc.def.Model), target.Scope{Kind: target.ScopeUser}, testStamp(t))
			if err != nil {
				t.Fatalf("Render() error: %v", err)
			}
			if f.Path != ".claude/agents/planner.md" {
				t.Errorf("Render() path = %q, want %q", f.Path, ".claude/agents/planner.md")
			}
			golden := filepath.Join("testdata", tc.name+".golden.md")
			if *update {
				if err := os.WriteFile(golden, f.Data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden file (run with -update to create it): %v", err)
			}
			if diff := cmp.Diff(string(want), string(f.Data)); diff != "" {
				t.Errorf("Render() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEveryToolIsMapped(t *testing.T) {
	// Every canonical tool has a Claude Code name: an empty mapping would
	// render an agent without the tool it asked for.
	for _, tool := range agent.Tools() {
		got, err := mapTools([]agent.Tool{tool})
		if err != nil {
			t.Errorf("mapTools(%q) error: %v", tool, err)
		}
		if len(got) == 0 {
			t.Errorf("mapTools(%q) = %v, want at least one name", tool, got)
		}
	}
}

func TestMapTools(t *testing.T) {
	tests := []struct {
		tool agent.Tool
		want []string
	}{
		{agent.ToolRead, []string{"Read"}},
		{agent.ToolSearch, []string{"Grep", "Glob"}},
		{agent.ToolEdit, []string{"Edit", "Write"}},
		{agent.ToolShell, []string{"Bash"}},
		{agent.ToolWeb, []string{"WebFetch", "WebSearch"}},
		{agent.ToolAgent, []string{"Agent"}},
		{agent.ToolSkill, []string{"Skill"}},
	}
	for _, tc := range tests {
		got, err := mapTools([]agent.Tool{tc.tool})
		if err != nil {
			t.Fatalf("mapTools(%q) error: %v", tc.tool, err)
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("mapTools(%q) mismatch (-want +got):\n%s", tc.tool, diff)
		}
	}
}

func TestMapToolsDeduplicates(t *testing.T) {
	got, err := mapTools([]agent.Tool{agent.ToolEdit, agent.ToolRead, agent.ToolEdit})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Edit", "Write", "Read"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mapTools() mismatch (-want +got):\n%s", diff)
	}
}
