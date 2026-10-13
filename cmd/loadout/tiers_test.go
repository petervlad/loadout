package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestModelTiers: a project owner sets which Copilot model the team's agents
// run; a member's install writes it, and when the owner changes it, sync
// rewrites the file for an auto install and reports it for a notify one.
func TestModelTiers(t *testing.T) {
	_, base, aliceWeb, olgaWeb := ownedRegistry(t)
	olga := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, olga, olgaWeb, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}

	// A team maintainer does not change the project's table; the project's owner does.
	if _, errOut, code := alice.runIn("", "namespace", "set", "acme", "--model", "copilot:balanced=GPT-5"); code == 0 || !strings.Contains(errOut, "needs a maintainer or an owner") {
		t.Errorf("team maintainer sets the project's model: exit %d, %s; want refused", code, errOut)
	}
	if _, errOut, code := olga.runIn("", "namespace", "set", "acme", "--model", "codex:deep=x"); code == 0 || !strings.Contains(errOut, "unknown tool") {
		t.Errorf("unknown tool: exit %d, %s; want refused", code, errOut)
	}
	out := olga.must("", "namespace", "set", "acme", "--model", "copilot:balanced=Claude Sonnet 5", "--effort", "copilot:balanced=high")
	if !strings.Contains(out, "Claude Sonnet 5") {
		t.Errorf("namespace set output = %s", out)
	}

	alice.must("", "install", "lime/planner", "--target", "claude,copilot", "--policy", "auto")
	alice.must("", "install", "web/styles", "--target", "copilot", "--policy", "notify")
	planner := filepath.Join(alice.home, ".copilot", "agents", "planner.md")
	for _, want := range []string{`model: Claude Sonnet 5 (copilot)`, "models: [claude-sonnet-5]", "reasoning-effort: high"} {
		if got := readText(t, planner); !strings.Contains(got, want) {
			t.Errorf("copilot agent lacks %q:\n%s", want, got)
		}
	}
	if got := readText(t, filepath.Join(alice.home, ".claude", "agents", "planner.md")); !strings.Contains(got, "model: sonnet\n") || strings.Contains(got, "effort") {
		t.Errorf("claude agent = %s, want the built-in sonnet and no effort", got)
	}

	// The resolved table names where each value comes from.
	tiers := alice.must("", "namespace", "tiers", "lime")
	for _, want := range []string{"copilot  balanced  Claude Sonnet 5  high    acme", "claude   deep      opus             -       built-in", "copilot  fast      -"} {
		if !strings.Contains(tiers, want) {
			t.Errorf("namespace tiers lime lacks %q:\n%s", want, tiers)
		}
	}

	// Nothing changed: sync leaves both alone.
	if out := alice.must("", "sync"); strings.Contains(out, "model settings") {
		t.Errorf("sync without a change = %s", out)
	}

	olga.must("", "namespace", "set", "acme", "--model", "copilot:balanced=GPT-5.3-Codex")
	out = alice.must("", "sync")
	if !strings.Contains(out, "model settings applied") || !strings.Contains(out, "model settings changed") || !strings.Contains(out, "loadout install web/styles") {
		t.Errorf("sync after the change = %s, want planner applied and styles reported", out)
	}
	if got := readText(t, planner); !strings.Contains(got, "models: [gpt-5.3-codex]") || !strings.Contains(got, "reasoning-effort: high") {
		t.Errorf("copilot agent after sync:\n%s", got)
	}
	if got := readText(t, filepath.Join(alice.home, ".copilot", "agents", "styles.md")); strings.Contains(got, "gpt-5.3-codex") {
		t.Errorf("notify install was rewritten:\n%s", got)
	}
	if out := alice.must("", "sync"); strings.Contains(out, "model settings applied") {
		t.Errorf("second sync applied again: %s", out)
	}

	// Clearing the project's values brings back the person's own choice.
	olga.must("", "namespace", "set", "acme", "--model", "copilot:balanced=", "--effort", "copilot:balanced=")
	alice.must("", "sync")
	if got := readText(t, planner); strings.Contains(got, "model") || strings.Contains(got, "effort") {
		t.Errorf("copilot agent after clearing:\n%s", got)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // inside the test temp dir
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
