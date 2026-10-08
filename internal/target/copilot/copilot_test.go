package copilot

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

var _ install.Renderer = (*Adapter)(nil)

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
	defs := []struct {
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
	scopes := []struct {
		kind     target.ScopeKind
		wantPath string
	}{
		{kind: target.ScopeUser, wantPath: ".copilot/agents/planner.md"},
		{kind: target.ScopeProject, wantPath: ".github/agents/planner.agent.md"},
	}
	for _, d := range defs {
		for _, s := range scopes {
			name := d.name + "-" + string(s.kind)
			t.Run(name, func(t *testing.T) {
				f, err := New().RenderAgent(d.def, tier.Choice{}, target.Scope{Kind: s.kind, Root: "/root"}, testStamp(t))
				if err != nil {
					t.Fatalf("Render() error: %v", err)
				}
				if f.Path != s.wantPath {
					t.Errorf("Render() path = %q, want %q", f.Path, s.wantPath)
				}
				golden := filepath.Join("testdata", name+".golden.md")
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
}

func TestRenderUnknownScope(t *testing.T) {
	def := agent.Definition{Name: "planner", Description: "Plans.", Prompt: "You plan."}
	for _, kind := range []target.ScopeKind{"", "global"} {
		f, err := New().RenderAgent(def, tier.Choice{}, target.Scope{Kind: kind, Root: "/root"}, testStamp(t))
		if err == nil {
			t.Errorf("Render(scope %q) = %q, want error", kind, f.Path)
		}
		if dir, err := New().SkillsDir(kind); err == nil {
			t.Errorf("SkillsDir(%q) = %q, want error", kind, dir)
		}
	}
}

func TestEveryToolIsMapped(t *testing.T) {
	// The documented aliases, see the package comment for the source.
	documented := []string{"execute", "read", "edit", "search", "agent", "web", "todo"}
	// Tools Copilot does on its own, with nothing to grant.
	nothingToGrant := []agent.Tool{agent.ToolSkill}
	for _, tool := range agent.Tools() {
		got, err := mapTools([]agent.Tool{tool})
		if err != nil {
			t.Errorf("mapTools(%q) error: %v", tool, err)
			continue
		}
		if slices.Contains(nothingToGrant, tool) {
			if len(got) != 0 {
				t.Errorf("mapTools(%q) = %v, want nothing to grant", tool, got)
			}
			continue
		}
		if len(got) == 0 {
			t.Errorf("mapTools(%q) = %v, want at least one alias", tool, got)
		}
		for _, n := range got {
			if !slices.Contains(documented, n) {
				t.Errorf("mapTools(%q) = %v, want only documented aliases %v", tool, got, documented)
			}
		}
	}
}

func TestMapTools(t *testing.T) {
	tests := []struct {
		tool agent.Tool
		want []string
	}{
		{agent.ToolRead, []string{"read"}},
		{agent.ToolSearch, []string{"search"}},
		{agent.ToolEdit, []string{"edit"}},
		{agent.ToolShell, []string{"execute"}},
		{agent.ToolWeb, []string{"web"}},
		{agent.ToolAgent, []string{"agent"}},
		{agent.ToolSkill, nil},
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

func TestRenderToolsThatGrantNothing(t *testing.T) {
	// An agent that asks only for skills must not get every tool: Copilot
	// reads a missing tools list as all tools and tools: [] as none.
	tests := []struct {
		name  string
		tools []agent.Tool
		want  string
	}{
		{"only skill", []agent.Tool{agent.ToolSkill}, "tools: []\n"},
		{"skill and read", []agent.Tool{agent.ToolSkill, agent.ToolRead}, "tools: [read]\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def := agent.Definition{Name: "planner", Description: "Plans.", Tools: tc.tools, Prompt: "You plan."}
			f, err := New().RenderAgent(def, tier.Choice{}, target.Scope{Kind: target.ScopeUser, Root: "/root"}, testStamp(t))
			if err != nil {
				t.Fatalf("Render() error: %v", err)
			}
			if !strings.Contains(string(f.Data), tc.want) {
				t.Errorf("Render() =\n%s\nwant a line %q", f.Data, tc.want)
			}
		})
	}
}

func TestMapToolsDeduplicates(t *testing.T) {
	got, err := mapTools([]agent.Tool{agent.ToolEdit, agent.ToolShell, agent.ToolEdit, agent.ToolRead, agent.ToolShell})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"edit", "execute", "read"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mapTools() mismatch (-want +got):\n%s", diff)
	}
}

func TestMapToolsUnknown(t *testing.T) {
	if got, err := mapTools([]agent.Tool{"teleport"}); err == nil {
		t.Errorf("mapTools(teleport) = %v, want error", got)
	}
}
