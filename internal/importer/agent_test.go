package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestConvertRealAgents(t *testing.T) {
	// The frontmatter of agents a team keeps for Claude Code and Copilot.
	tests := []struct {
		file  string
		name  string
		tools []agent.Tool
	}{
		{"covered-repos.md", "covered-repos", []agent.Tool{agent.ToolRead, agent.ToolSearch}},
		{"onboarding-buddy.md", "onboarding-buddy", []agent.Tool{agent.ToolRead, agent.ToolSearch, agent.ToolShell, agent.ToolEdit}},
		{"onboarding-factory.md", "onboarding-factory", []agent.Tool{agent.ToolRead, agent.ToolSearch, agent.ToolShell, agent.ToolEdit, agent.ToolAgent, agent.ToolSkill}},
		{"setup-doctor.md", "setup-doctor", []agent.Tool{agent.ToolRead, agent.ToolSearch, agent.ToolShell}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			def, notices, err := convertAgent(tc.file, readTestdata(t, "agents/"+tc.file), false)
			if err != nil {
				t.Fatalf("convertAgent() error: %v", err)
			}
			if def.Name != tc.name || def.Model != agent.ModelBalanced {
				t.Errorf("convertAgent() name, model = %q, %q, want %q, balanced", def.Name, def.Model, tc.name)
			}
			if diff := cmp.Diff(tc.tools, def.Tools); diff != "" {
				t.Errorf("convertAgent() tools mismatch (-want +got):\n%s", diff)
			}
			if strings.Contains(def.Description, "\n") || !strings.HasSuffix(def.Description, ".") {
				t.Errorf("convertAgent() description = %q, want the folded text on one line", def.Description)
			}
			if len(notices) != 0 {
				t.Errorf("convertAgent() notices = %q, want none", notices)
			}
		})
	}
}

func TestConvertCopilotAgent(t *testing.T) {
	def, notices, err := convertAgent("copilot-reviewer.agent.md", readTestdata(t, "agents/copilot-reviewer.agent.md"), true)
	if err != nil {
		t.Fatalf("convertAgent() error: %v", err)
	}
	want := agent.Definition{
		Name:        "copilot-reviewer",
		Description: "Review a pull request for correctness and the team's conventions. Use when asked to review a PR.",
		Tools:       []agent.Tool{agent.ToolRead, agent.ToolSearch, agent.ToolShell},
		Prompt:      "You review pull requests. Read the diff, run the tests, and list findings by severity.",
	}
	if diff := cmp.Diff(want, def); diff != "" {
		t.Errorf("convertAgent() mismatch (-want +got):\n%s", diff)
	}
	wantNotices := []string{
		"dropped tool github/*: loadout has no equivalent",
		"dropped tool todo: the AI tool manages it on its own",
		`model "Claude Sonnet 4.5" is a vendor model name; left unset, so each person's tool picks the model (hints are fast, balanced and deep)`,
		"dropped field target: loadout agents have no equivalent",
		`name "Code Reviewer" is not a valid package name; used "copilot-reviewer" from the file name`,
	}
	if diff := cmp.Diff(wantNotices, notices); diff != "" {
		t.Errorf("convertAgent() notices mismatch (-want +got):\n%s", diff)
	}
}

func TestConvertAgentFields(t *testing.T) {
	agentFile := func(header string) []byte {
		return []byte("---\ndescription: Plans work.\n" + header + "---\nYou plan.\n")
	}
	tests := []struct {
		name        string
		file        string
		header      string
		wantName    string
		wantTools   []agent.Tool
		wantModel   agent.Model
		wantNotices []string
	}{
		{name: "name from the file", file: "dir/planner.md", wantName: "planner"},
		{name: "name from a Copilot file", file: "planner.agent.md", wantName: "planner"},
		{name: "frontmatter name wins", file: "x.md", header: "name: planner\n", wantName: "planner"},
		{name: "no tools line", file: "p.md", wantName: "p"},
		{name: "empty tools value", file: "p.md", header: "tools:\n", wantName: "p"},
		{
			name: "claude comma string", file: "p.md", header: "tools: Read, Write, Edit, MultiEdit, WebFetch, WebSearch, Task, Skill\n", wantName: "p",
			wantTools: []agent.Tool{agent.ToolRead, agent.ToolEdit, agent.ToolWeb, agent.ToolAgent, agent.ToolSkill},
		},
		{
			name: "copilot aliases and synonyms", file: "p.md", header: "tools: [shell, powershell, custom-agent, NotebookRead, NotebookEdit, Glob]\n", wantName: "p",
			wantTools: []agent.Tool{agent.ToolShell, agent.ToolAgent, agent.ToolRead, agent.ToolEdit, agent.ToolSearch},
		},
		{
			name: "case insensitive", file: "p.md", header: "tools: [READ, Execute]\n", wantName: "p",
			wantTools: []agent.Tool{agent.ToolRead, agent.ToolShell},
		},
		{
			name: "claude TodoWrite", file: "p.md", header: "tools: Read, TodoWrite\n", wantName: "p",
			wantTools: []agent.Tool{agent.ToolRead}, wantNotices: []string{"dropped tool TodoWrite: the AI tool manages it on its own"},
		},
		{
			name: "copilot every tool", file: "p.md", header: "tools: ['*']\n", wantName: "p",
			wantNotices: []string{"tools * allow every tool; the agent gets every tool"},
		},
		{name: "haiku", file: "p.md", header: "model: haiku\n", wantName: "p", wantModel: agent.ModelFast},
		{name: "opus", file: "p.md", header: "model: opus\n", wantName: "p", wantModel: agent.ModelDeep},
		{name: "a hint already", file: "p.md", header: "model: balanced\n", wantName: "p", wantModel: agent.ModelBalanced},
		{name: "inherit", file: "p.md", header: "model: inherit\n", wantName: "p"},
		{
			name: "vendor model", file: "p.md", header: "model: claude-opus-4-1\n", wantName: "p",
			wantNotices: []string{`model "claude-opus-4-1" is a vendor model name; left unset, so each person's tool picks the model (hints are fast, balanced and deep)`},
		},
		{
			name: "list of models", file: "p.md", header: "model: [GPT-5, Claude Sonnet 4]\n", wantName: "p",
			wantNotices: []string{"model is a list of vendor models; left unset, so each person's tool picks the model (hints are fast, balanced and deep)"},
		},
		{
			name: "claude extras", file: "p.md", header: "color: blue\npermissionMode: plan\n", wantName: "p",
			wantNotices: []string{"dropped field color: loadout agents have no equivalent", "dropped field permissionMode: loadout agents have no equivalent"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def, notices, err := convertAgent(tc.file, agentFile(tc.header), false)
			if err != nil {
				t.Fatalf("convertAgent() error: %v", err)
			}
			if def.Name != tc.wantName || def.Model != tc.wantModel {
				t.Errorf("convertAgent() name, model = %q, %q, want %q, %q", def.Name, def.Model, tc.wantName, tc.wantModel)
			}
			if diff := cmp.Diff(tc.wantTools, def.Tools); diff != "" {
				t.Errorf("convertAgent() tools mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantNotices, notices); diff != "" {
				t.Errorf("convertAgent() notices mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConvertAgentErrors(t *testing.T) {
	tests := []struct {
		name string
		data string
		drop bool
		want string
	}{
		{"no frontmatter", "You plan.", false, "frontmatter"},
		{"no description", "---\nname: p\n---\nYou plan.", false, "description is empty"},
		{"no prompt", "---\nname: p\ndescription: d\n---\n", false, "prompt"},
		{"not a mapping", "---\n- a\n---\nx", false, "want fields"},
		{"tools not text", "---\ndescription: d\ntools: {a: b}\n---\nx", false, "tools: want a list"},
		{"empty tools list", "---\ndescription: d\ntools: []\n---\nx", false, "tools is an empty list"},
		{"only dropped tools", "---\ndescription: d\ntools: TodoWrite\n---\nx", false, "no tool is left after dropping TodoWrite"},
		{"only unknown tools, dropped", "---\ndescription: d\ntools: [mcp__jira__search]\n---\nx", true, "no tool is left after dropping mcp__jira__search"},
		{"disallowed tools", "---\ndescription: d\ndisallowedTools: Bash\n---\nx", false, "cannot deny"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := convertAgent("p.md", []byte(tc.data), tc.drop)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("convertAgent() error = %v, want %q", err, tc.want)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "p.md: ") {
				t.Errorf("convertAgent() error = %v, want it to name the file", err)
			}
		})
	}
}

func TestConvertAgentInvalidNames(t *testing.T) {
	// Neither the frontmatter nor the file name is usable.
	_, _, err := convertAgent("My Agent.md", []byte("---\nname: Bad Name\ndescription: d\n---\nx"), false)
	if err == nil || !strings.Contains(err.Error(), `"Bad Name" must be lowercase`) {
		t.Errorf("convertAgent() error = %v, want the frontmatter name refused", err)
	}
}

func TestConvertAgentUnknownTools(t *testing.T) {
	data := []byte("---\ndescription: d\ntools: Read, mcp__jira__search, Teleport\n---\nx")
	_, _, err := convertAgent("p.md", data, false)
	e, ok := errors.AsType[*UnknownToolsError](err)
	if !ok {
		t.Fatalf("convertAgent() error = %v, want *UnknownToolsError", err)
	}
	if diff := cmp.Diff([]string{"mcp__jira__search", "Teleport"}, e.Tools); diff != "" {
		t.Errorf("UnknownToolsError.Tools mismatch (-want +got):\n%s", diff)
	}
	if !strings.Contains(err.Error(), "mcp__jira__search, Teleport") {
		t.Errorf("error = %v, want the tools named", err)
	}
}

func TestAgentsWritesPackages(t *testing.T) {
	out := t.TempDir()
	files, err := filepath.Glob(filepath.Join("testdata", "agents", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := Agents(files, AgentOptions{Options: Options{Team: "lime", Out: out}, DropUnknownTools: true})
	if err != nil {
		t.Fatalf("Agents() error: %v", err)
	}
	if len(results) != len(files) {
		t.Fatalf("Agents() = %d results, want %d", len(results), len(files))
	}
	for _, r := range results {
		p, err := pack.Load(r.Dir)
		if err != nil {
			t.Fatalf("Load(%s) error: %v", r.Dir, err)
		}
		if p.ID() != r.ID || p.Kind() != pack.KindAgent || p.Version() != semver.New(0, 1, 0) {
			t.Errorf("Load(%s) = %s %s %s, want %s agent 0.1.0", r.Dir, p.ID(), p.Kind(), p.Version(), r.ID)
		}
		if r.Dir != filepath.Join(out, r.ID.Name()) {
			t.Errorf("Result.Dir = %s, want %s", r.Dir, filepath.Join(out, r.ID.Name()))
		}
		if d := p.Manifest().Description; strings.Contains(d, "Use when") {
			t.Errorf("manifest description = %q, want only the first sentence", d)
		}
	}

	// A second run refuses to overwrite and writes nothing.
	if _, err := Agents(files, AgentOptions{Options: Options{Team: "lime", Out: out}, DropUnknownTools: true}); !errorsAs[*CollisionError](err) {
		t.Errorf("second Agents() error = %v, want *CollisionError", err)
	}
}

func TestAgentsWritesNothingWhenOneFails(t *testing.T) {
	out := t.TempDir()
	bad := filepath.Join(t.TempDir(), "bad.md")
	if err := os.WriteFile(bad, []byte("---\ndescription: d\ntools: Teleport\n---\nx"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []string{filepath.Join("testdata", "agents", "setup-doctor.md"), bad}
	if _, err := Agents(files, AgentOptions{Options: Options{Team: "lime", Out: out}}); err == nil {
		t.Fatal("Agents() error = nil")
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("Agents() wrote %d folders, want none", len(entries))
	}
}

func TestAgentsRefusesTwoAgentsWithOneName(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, f := range []string{"a.md", "b.agent.md"} {
		p := filepath.Join(dir, f)
		if err := os.WriteFile(p, []byte("---\nname: planner\ndescription: d\n---\nx"), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	out := t.TempDir()
	if _, err := Agents(files, AgentOptions{Options: Options{Team: "lime", Out: out}}); err == nil || !strings.Contains(err.Error(), "would both become") {
		t.Errorf("Agents() error = %v, want the clash named", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("Agents() wrote %d folders, want none", len(entries))
	}
}

func errorsAs[T error](err error) bool {
	_, ok := errors.AsType[T](err)
	return ok
}
