package target_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/skill"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
)

func newPack(t *testing.T, files map[string]string) *pack.Pack {
	t.Helper()
	out := make(map[string][]byte, len(files))
	for k, v := range files {
		out[k] = []byte(v)
	}
	p, err := pack.FromFiles(out)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func agentPack(t *testing.T) *pack.Pack {
	t.Helper()
	return newPack(t, map[string]string{
		pack.ManifestFile: "name: lime/planner\nkind: agent\nversion: 1.0.0\ndescription: Plans.\n",
		pack.AgentFile:    "---\nname: planner\ndescription: Plans.\n---\nYou plan.\n",
	})
}

func skillPack(t *testing.T) *pack.Pack {
	t.Helper()
	return newPack(t, map[string]string{
		pack.ManifestFile:      "name: lime/notes\nkind: skill\nversion: 1.0.0\ndescription: Notes.\n",
		skill.File:             "---\nname: notes\ndescription: Write notes. Use when asked.\n---\nWrite them.\n",
		"scripts/run.sh":       "echo hi\n",
		"references/format.md": "# Format\n",
	})
}

func kbPack(t *testing.T) *pack.Pack {
	t.Helper()
	return newPack(t, map[string]string{
		pack.ManifestFile: "name: lime/ops\nkind: kb\nversion: 1.0.0\ndescription: How we run production.\n",
		"README.md":       "# Overview\n",
		"runbooks/a.md":   "# Restart\n",
	})
}

func stamp(p *pack.Pack) target.Stamp {
	return target.Stamp{ID: p.ID(), Version: p.Version(), Digest: p.Digest()}
}

func paths(u target.Unit) []string {
	out := make([]string, len(u.Files))
	for i, f := range u.Files {
		out[i] = f.Path
	}
	return out
}

func TestRender(t *testing.T) {
	claude, cop := claudecode.New(), copilot.New()
	tests := []struct {
		name      string
		adapter   target.Renderer
		pack      func(*testing.T) *pack.Pack
		scope     target.ScopeKind
		wantRoot  string
		wantPaths []string
	}{
		{"claude agent user", claude, agentPack, target.ScopeUser, ".claude/agents/planner.md", []string{".claude/agents/planner.md"}},
		{"claude agent project", claude, agentPack, target.ScopeProject, ".claude/agents/planner.md", []string{".claude/agents/planner.md"}},
		{"copilot agent user", cop, agentPack, target.ScopeUser, ".copilot/agents/planner.md", []string{".copilot/agents/planner.md"}},
		{"copilot agent project", cop, agentPack, target.ScopeProject, ".github/agents/planner.agent.md", []string{".github/agents/planner.agent.md"}},
		{"claude skill user", claude, skillPack, target.ScopeUser, ".claude/skills/notes", []string{".claude/skills/notes/SKILL.md", ".claude/skills/notes/references/format.md", ".claude/skills/notes/scripts/run.sh"}},
		{"claude skill project", claude, skillPack, target.ScopeProject, ".claude/skills/notes", []string{".claude/skills/notes/SKILL.md", ".claude/skills/notes/references/format.md", ".claude/skills/notes/scripts/run.sh"}},
		{"copilot skill user", cop, skillPack, target.ScopeUser, ".copilot/skills/notes", []string{".copilot/skills/notes/SKILL.md", ".copilot/skills/notes/references/format.md", ".copilot/skills/notes/scripts/run.sh"}},
		{"copilot skill project shares claude's folder", cop, skillPack, target.ScopeProject, ".claude/skills/notes", []string{".claude/skills/notes/SKILL.md", ".claude/skills/notes/references/format.md", ".claude/skills/notes/scripts/run.sh"}},
		{"claude kb user", claude, kbPack, target.ScopeUser, ".claude/skills/ops", []string{".claude/skills/ops/SKILL.md", ".claude/skills/ops/README.md", ".claude/skills/ops/runbooks/a.md"}},
		{"claude kb project", claude, kbPack, target.ScopeProject, ".claude/skills/ops", []string{".claude/skills/ops/SKILL.md", ".claude/skills/ops/README.md", ".claude/skills/ops/runbooks/a.md"}},
		{"copilot kb user", cop, kbPack, target.ScopeUser, ".copilot/skills/ops", []string{".copilot/skills/ops/SKILL.md", ".copilot/skills/ops/README.md", ".copilot/skills/ops/runbooks/a.md"}},
		{"copilot kb project", cop, kbPack, target.ScopeProject, ".claude/skills/ops", []string{".claude/skills/ops/SKILL.md", ".claude/skills/ops/README.md", ".claude/skills/ops/runbooks/a.md"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.pack(t)
			u, err := target.Render(tc.adapter, p, target.Scope{Kind: tc.scope, Root: "/r"}, stamp(p), target.RenderOptions{})
			if err != nil {
				t.Fatalf("Render() error: %v", err)
			}
			if u.Root != tc.wantRoot {
				t.Errorf("Root = %q, want %q", u.Root, tc.wantRoot)
			}
			if diff := cmp.Diff(tc.wantPaths, paths(u)); diff != "" {
				t.Errorf("paths mismatch (-want +got):\n%s", diff)
			}
			main := string(u.Files[0].Data)
			if !strings.Contains(main, target.ManagedNote(stamp(p))) {
				t.Errorf("%s has no managed note:\n%s", u.Files[0].Path, main)
			}
			if p.Kind() == pack.KindAgent {
				return
			}
			s, err := skill.Parse(u.Files[0].Data, nil)
			if err != nil {
				t.Fatalf("rendered SKILL.md does not parse: %v\n%s", err, main)
			}
			if s.Name != p.ID().Name() {
				t.Errorf("rendered SKILL.md names %q, want %q", s.Name, p.ID().Name())
			}
			// The note sits between the frontmatter and the instructions.
			if !strings.Contains(main, "---\n\n"+target.ManagedNote(stamp(p))+"\n\n") {
				t.Errorf("managed note is not right after the frontmatter:\n%s", main)
			}
		})
	}
}

func TestRenderKeepsSupportingFiles(t *testing.T) {
	p := skillPack(t)
	u, err := target.Render(claudecode.New(), p, target.Scope{Kind: target.ScopeUser}, stamp(p), target.RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range u.Files[1:] {
		rel := strings.TrimPrefix(f.Path, u.Root+"/")
		if want := p.Files()[rel]; string(f.Data) != string(want) {
			t.Errorf("%s = %q, want the package's %s unchanged (%q)", f.Path, f.Data, rel, want)
		}
	}
}

func TestRenderRenamed(t *testing.T) {
	tests := []struct {
		name     string
		adapter  target.Renderer
		pack     func(*testing.T) *pack.Pack
		wantRoot string
		wantName string // in the rendered main file
	}{
		{"claude agent", claudecode.New(), agentPack, ".claude/agents/candidate-planner.md", "name: candidate-planner"},
		{"copilot agent", copilot.New(), agentPack, ".github/agents/candidate-planner.agent.md", "name: candidate-planner"},
		{"claude skill", claudecode.New(), skillPack, ".claude/skills/candidate-planner", "name: candidate-planner"},
		{"copilot kb", copilot.New(), kbPack, ".claude/skills/candidate-planner", "name: candidate-planner"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.pack(t)
			u, err := target.Render(tc.adapter, p, target.Scope{Kind: target.ScopeProject, Root: "/r"}, stamp(p), target.RenderOptions{Name: "candidate-planner"})
			if err != nil {
				t.Fatalf("Render() error: %v", err)
			}
			if u.Root != tc.wantRoot || !strings.HasPrefix(u.Files[0].Path, tc.wantRoot) {
				t.Errorf("Root = %q, first file %q; want %q", u.Root, u.Files[0].Path, tc.wantRoot)
			}
			if !strings.Contains(string(u.Files[0].Data), tc.wantName+"\n") {
				t.Errorf("main file does not carry the new name:\n%s", u.Files[0].Data)
			}
			if again, _ := tc.pack(t).Skill(); again != nil && again.Name != p.ID().Name() {
				t.Errorf("renaming changed the package's skill to %q", again.Name)
			}
		})
	}
}

func TestRenderErrors(t *testing.T) {
	p := skillPack(t)
	if _, err := target.Render(claudecode.New(), p, target.Scope{Kind: target.ScopeUser}, stamp(p), target.RenderOptions{Name: "Not Valid"}); err == nil {
		t.Error("Render() with an invalid name: error = nil")
	}
	if _, err := target.Render(copilot.New(), p, target.Scope{Kind: "global"}, stamp(p), target.RenderOptions{}); err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("Render() in an unknown scope: error = %v, want unknown scope", err)
	}
}

func TestSkillsDir(t *testing.T) {
	tests := []struct {
		adapter target.Renderer
		scope   target.ScopeKind
		want    string
	}{
		{claudecode.New(), target.ScopeUser, ".claude/skills"},
		{claudecode.New(), target.ScopeProject, ".claude/skills"},
		{copilot.New(), target.ScopeUser, ".copilot/skills"},
		{copilot.New(), target.ScopeProject, ".claude/skills"},
	}
	for _, tc := range tests {
		t.Run(tc.adapter.Name()+" "+string(tc.scope), func(t *testing.T) {
			got, err := tc.adapter.SkillsDir(tc.scope)
			if err != nil || got != tc.want {
				t.Errorf("SkillsDir() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
