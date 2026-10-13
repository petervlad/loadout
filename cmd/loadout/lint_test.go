package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An agent that names its successor still publishes and proposes, with a
// warning that says what to do instead.
func TestPublishAndProposeWarnAboutHandoffs(t *testing.T) {
	alice, _, _ := team(t)
	editAgent(t, alice, "Next: run lime-dev")

	want := "warning: agent.md:"
	out := alice.must("", "publish", "planner", "--bump", "patch")
	if !strings.Contains(out, "published lime/planner@") || !strings.Contains(out, want) || !strings.Contains(out, "Verdict") {
		t.Errorf("publish output = %q, want the upload line and a handoff warning", out)
	}

	got := alice.must("", "propose", "planner", "--bump", "patch", "--prompt", "Plan X")
	if !strings.Contains(got, "proposed lime/planner@") || !strings.Contains(got, want) {
		t.Errorf("propose output = %q, want the proposal line and a handoff warning", got)
	}

	// A clean agent prints no warning.
	file := filepath.Join(alice.wd, "planner", "agent.md")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	clean := strings.ReplaceAll(string(data), "Next: run lime-dev", "End with a Verdict line.")
	if err := os.WriteFile(file, []byte(clean), 0o644); err != nil { //nolint:gosec // inside the test temp dir
		t.Fatal(err)
	}
	if out := alice.must("", "publish", "planner", "--bump", "patch"); strings.Contains(out, "warning") {
		t.Errorf("publish of a clean agent warned: %q", out)
	}
}
