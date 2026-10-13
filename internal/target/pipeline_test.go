package target_test

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
)

var update = flag.Bool("update", false, "rewrite golden files")

func readPipelineFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "pipeline", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func templatePack(t *testing.T) *pack.Pack {
	t.Helper()
	return newPack(t, map[string]string{
		pack.ManifestFile: "name: acme/dev-loop\nkind: pipeline\nversion: 1.2.0\ndescription: The dev loop.\n",
		pack.PipelineFile: readPipelineFile(t, "dev-loop.yaml"),
	})
}

// bindingPack is the Lime binding, with extra lines appended to it.
func bindingPack(t *testing.T, extra string) *pack.Pack {
	t.Helper()
	return newPack(t, map[string]string{
		pack.ManifestFile: "name: lime/dev-loop\nkind: pipeline\nversion: 1.0.0\ndescription: Lime's dev loop.\n",
		pack.PipelineFile: readPipelineFile(t, "lime-binding.yaml") + extra,
	})
}

func def(name string, tools ...agent.Tool) agent.Definition {
	return agent.Definition{Name: name, Description: "Does " + name + ".", Tools: tools, Prompt: "You are " + name + "."}
}

// limeAgents are the four bound agents, each with a shell and no agent tool.
func limeAgents() []agent.Definition {
	tools := []agent.Tool{agent.ToolRead, agent.ToolEdit, agent.ToolShell}
	return []agent.Definition{def("lime-planner", tools...), def("lime-dev", tools...), def("lime-code-review", tools...), def("lime-test", tools...)}
}

func renderBinding(t *testing.T, a target.Renderer, p *pack.Pack, kind target.ScopeKind, opts target.RenderOptions) target.Unit {
	t.Helper()
	u, err := target.Render(a, p, target.Scope{Kind: kind, Root: "/r"}, stamp(p), opts)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	return u
}

func limeOptions(t *testing.T, agents []agent.Definition) target.RenderOptions {
	t.Helper()
	return target.RenderOptions{Requires: []*pack.Pack{templatePack(t)}, Agents: agents}
}

// frontmatterOf returns the YAML between the first two --- lines.
func frontmatterOf(t *testing.T, data []byte) []byte {
	t.Helper()
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		t.Fatalf("no frontmatter:\n%s", data)
	}
	head, _, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		t.Fatalf("frontmatter is not closed:\n%s", data)
	}
	return head
}

func TestRenderConductorGolden(t *testing.T) {
	tests := []struct {
		adapter  target.Renderer
		scope    target.ScopeKind
		wantPath string
	}{
		{claudecode.New(), target.ScopeUser, ".claude/agents/lime-dev-loop.md"},
		{claudecode.New(), target.ScopeProject, ".claude/agents/lime-dev-loop.md"},
		{copilot.New(), target.ScopeUser, ".copilot/agents/lime-dev-loop.md"},
		{copilot.New(), target.ScopeProject, ".github/agents/lime-dev-loop.agent.md"},
	}
	for _, tc := range tests {
		name := "conductor-" + tc.adapter.Name() + "-" + string(tc.scope)
		t.Run(name, func(t *testing.T) {
			p := bindingPack(t, "")
			u := renderBinding(t, tc.adapter, p, tc.scope, limeOptions(t, limeAgents()))
			if u.Root != tc.wantPath || len(u.Files) != 1 || u.Files[0].Path != tc.wantPath {
				t.Fatalf("Root = %q, files %v; want the one file %q", u.Root, paths(u), tc.wantPath)
			}
			if len(u.Warnings) != 0 {
				t.Errorf("Warnings = %q, want none", u.Warnings)
			}
			data := u.Files[0].Data
			golden := filepath.Join("testdata", name+".golden.md")
			if *update {
				if err := os.WriteFile(golden, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden file (run with -update to create it): %v", err)
			}
			if diff := cmp.Diff(string(want), string(data)); diff != "" {
				t.Errorf("conductor mismatch (-want +got):\n%s", diff)
			}

			// Copilot 1.0.91 refused hand-written frontmatter with an unquoted
			// ": ", so every header must survive a real YAML parser.
			var fm struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			if err := yaml.Unmarshal(frontmatterOf(t, data), &fm); err != nil {
				t.Fatalf("frontmatter does not parse: %v\n%s", err, data)
			}
			if fm.Name != "lime-dev-loop" || !strings.Contains(fm.Description, "lime-planner") {
				t.Errorf("frontmatter = %+v, want the conductor's name and description", fm)
			}
		})
	}
}

func TestRenderConductorTools(t *testing.T) {
	delegating := def("lime-dev", agent.ToolRead, agent.ToolAgent)
	tests := []struct {
		name     string
		agents   func() []agent.Definition
		wantTool string
		wantWarn string
	}{
		{"limited to the bound agents", limeAgents, "tools: Read, Grep, Glob, Agent(lime-planner, lime-dev, lime-code-review, lime-test)\n", ""},
		{"an agent that delegates", func() []agent.Definition {
			a := limeAgents()
			a[1] = delegating
			return a
		}, "tools: Read, Grep, Glob, Agent\n", ""},
		{"an agent with every tool", func() []agent.Definition {
			a := limeAgents()
			a[3].Tools = nil
			return a
		}, "tools: Read, Grep, Glob, Agent\n", ""},
		{
			"an unknown agent", func() []agent.Definition { return limeAgents()[:3] },
			"tools: Read, Grep, Glob, Agent(lime-planner, lime-dev, lime-code-review, lime-test)\n", "definition of lime-test is not available",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := renderBinding(t, claudecode.New(), bindingPack(t, ""), target.ScopeUser, limeOptions(t, tc.agents()))
			if !strings.Contains(string(u.Files[0].Data), tc.wantTool) {
				t.Errorf("want %q in:\n%s", tc.wantTool, u.Files[0].Data)
			}
			switch joined := strings.Join(u.Warnings, "\n"); {
			case tc.wantWarn == "" && joined != "":
				t.Errorf("Warnings = %q, want none", u.Warnings)
			case !strings.Contains(joined, tc.wantWarn):
				t.Errorf("Warnings = %q, want %q", u.Warnings, tc.wantWarn)
			}
		})
	}
}

func TestRenderConductorCopilotTools(t *testing.T) {
	u := renderBinding(t, copilot.New(), bindingPack(t, ""), target.ScopeUser, limeOptions(t, limeAgents()))
	if want := "tools: [read, search, agent]\n"; !strings.Contains(string(u.Files[0].Data), want) {
		t.Errorf("want %q in:\n%s", want, u.Files[0].Data)
	}
}

func TestRenderConductorRunFolderWarning(t *testing.T) {
	noShell := limeAgents()
	noShell[0].Tools = []agent.Tool{agent.ToolRead, agent.ToolEdit}
	tests := []struct {
		name    string
		adapter target.Renderer
		agents  []agent.Definition
		want    string // empty: no warning
	}{
		{"copilot, planner without a shell", copilot.New(), noShell, "lime-planner starts the run and has no shell tool"},
		{"copilot, planner with a shell", copilot.New(), limeAgents(), ""},
		{"copilot, definition missing", copilot.New(), limeAgents()[1:], ""},
		{"claude, planner without a shell", claudecode.New(), noShell, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := renderBinding(t, tc.adapter, bindingPack(t, ""), target.ScopeUser, limeOptions(t, tc.agents))
			joined := strings.Join(u.Warnings, "\n")
			if tc.want == "" && joined != "" || !strings.Contains(joined, tc.want) {
				t.Errorf("Warnings = %q, want %q", u.Warnings, tc.want)
			}
		})
	}
}

func TestRenderConductorModel(t *testing.T) {
	tests := []struct {
		name  string
		extra string
		want  string
	}{
		{"balanced by default", "", "model: sonnet\n"},
		{"a binding overrides it", "model: deep\n", "model: opus\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := renderBinding(t, claudecode.New(), bindingPack(t, tc.extra), target.ScopeUser, limeOptions(t, limeAgents()))
			if !strings.Contains(string(u.Files[0].Data), tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, u.Files[0].Data)
			}
		})
	}
}

func TestRenderConductorRenamed(t *testing.T) {
	opts := limeOptions(t, limeAgents())
	opts.Name = "candidate-dev-loop"
	u := renderBinding(t, claudecode.New(), bindingPack(t, ""), target.ScopeUser, opts)
	if u.Root != ".claude/agents/candidate-dev-loop.md" || !strings.Contains(string(u.Files[0].Data), "name: candidate-dev-loop\n") {
		t.Errorf("Root = %q:\n%s", u.Root, u.Files[0].Data)
	}
}

func TestRenderPipelineErrors(t *testing.T) {
	oldMajor := newPack(t, map[string]string{
		pack.ManifestFile: "name: acme/dev-loop\nkind: pipeline\nversion: 2.0.0\ndescription: The dev loop.\n",
		pack.PipelineFile: readPipelineFile(t, "dev-loop.yaml"),
	})
	sameName := newPack(t, map[string]string{
		pack.ManifestFile: "name: lime/dev\nkind: pipeline\nversion: 1.0.0\ndescription: d\n",
		pack.PipelineFile: readPipelineFile(t, "lime-binding.yaml"),
	})
	tests := []struct {
		name    string
		pack    *pack.Pack
		opts    target.RenderOptions
		wantErr string
		wantIs  error
	}{
		{"conductor named like a bound agent", sameName, limeOptions(t, limeAgents()), "also one of the agents it binds", nil},
		{"a template", templatePack(t), target.RenderOptions{}, "installs through a team's binding", target.ErrNotStandalone},
		{"template not provided", bindingPack(t, ""), target.RenderOptions{}, "template acme/dev-loop@1 was not provided", nil},
		{"template at another major", bindingPack(t, ""), target.RenderOptions{Requires: []*pack.Pack{oldMajor}}, "template acme/dev-loop@1 was not provided", nil},
		{"requirement is a binding", bindingPack(t, ""), target.RenderOptions{Requires: []*pack.Pack{bindingPack(t, "")}}, "was not provided", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := target.Render(claudecode.New(), tc.pack, target.Scope{Kind: target.ScopeUser}, stamp(tc.pack), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Render() error = %v, want %q", err, tc.wantErr)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("Render() error = %v, want it to wrap %v", err, tc.wantIs)
			}
		})
	}
}

// noDelegation is a Renderer that cannot start a named agent.
type noDelegation struct{ target.Renderer }

func TestRenderPipelineNeedsDelegator(t *testing.T) {
	p := bindingPack(t, "")
	_, err := target.Render(noDelegation{claudecode.New()}, p, target.Scope{Kind: target.ScopeUser}, stamp(p), limeOptions(t, limeAgents()))
	if err == nil || !strings.Contains(err.Error(), "cannot start a named agent") {
		t.Errorf("Render() error = %v, want a sentence about starting a named agent", err)
	}
}
