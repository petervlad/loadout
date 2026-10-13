package pack

import (
	"strings"
	"testing"
)

func loadAgent(t *testing.T, agentMD string) *Pack {
	t.Helper()
	p, err := Load(writePackage(t, map[string]string{ManifestFile: testManifest, AgentFile: agentMD}))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return p
}

func TestLintFindsHandoffsAtTheirFileLine(t *testing.T) {
	agentMD := "---\nname: planner\ndescription: d\n---\n\nPlan it.\n\nNext: run lime-dev\n"
	p := loadAgent(t, agentMD)
	got := p.Lint()
	if len(got) != 1 {
		t.Fatalf("Lint() = %v, want one finding", got)
	}
	f := got[0]
	if f.Path != AgentFile || f.Line != 8 || f.Rule != RuleHandoff || f.Text != "Next: run lime-dev" {
		t.Errorf("finding = %+v, want agent.md:8 handoff", f)
	}
	for _, want := range []string{"warning: agent.md:8", "Verdict", "pipeline"} {
		if !strings.Contains(f.String(), want) {
			t.Errorf("String() = %q, want it to contain %q", f.String(), want)
		}
	}
	if strings.Contains(f.String(), "\u2014") {
		t.Errorf("String() has an em-dash: %q", f.String())
	}
}

// A description that repeats the prompt's first line must not move the
// reported line number into the header.
func TestLintLineIgnoresTheHeader(t *testing.T) {
	agentMD := "---\nname: planner\ndescription: \"Next: run lime-dev\"\n---\nNext: run lime-dev\n"
	got := loadAgent(t, agentMD).Lint()
	if len(got) != 1 || got[0].Line != 5 {
		t.Errorf("Lint() = %+v, want one finding at line 5", got)
	}
}

func TestLintIsQuietForCleanAgentsAndOtherKinds(t *testing.T) {
	agentMD := "---\nname: planner\ndescription: d\n---\nPlan it. End with a Verdict line.\n"
	p := loadAgent(t, agentMD)
	if got := p.Lint(); len(got) != 0 {
		t.Errorf("Lint() = %v, want none", got)
	}
}
