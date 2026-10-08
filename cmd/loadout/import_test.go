package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPublishInstall(t *testing.T) {
	alice := &person{session: newSession(t)}
	bob := &person{session: newSession(t)}
	useCopilot(t, bob)

	url := startServer(t, alice)
	publishToken := createToken(t, alice, "publish")
	readToken := createToken(t, alice, "read")
	alice.must(publishToken+"\n", "remote", "add", "office", url, "--token-stdin")
	bob.must(readToken+"\n", "remote", "add", "office", url, "--token-stdin")

	// Alice has a Claude Code agent and a skill with Claude-only fields.
	writeFile(t, filepath.Join(alice.wd, ".claude", "agents", "reviewer.md"), `---
name: reviewer
description: Review a change. Use when asked for a review.
tools: Read, Grep, Glob, Bash, Skill, TodoWrite
model: opus
color: green
---
You review changes and list findings by severity.
`)
	writeFile(t, filepath.Join(alice.wd, ".claude", "skills", "triage", "SKILL.md"), `---
name: triage
description: Find why a build failed. Use when a build is red.
allowed-tools: Bash(curl:*) Read
---
Fetch the build log and read it from the end.
`)
	writeFile(t, filepath.Join(alice.wd, ".claude", "skills", "triage", "references", "errors.md"), "# Known errors\n")

	out := alice.must("", "import", "agent", ".claude/agents/reviewer.md", "--team", "lime", "--out", "packages")
	for _, want := range []string{
		"imported " + filepath.Join(".claude", "agents", "reviewer.md") + " as lime/reviewer (agent) in " + filepath.Join("packages", "reviewer"),
		"note: dropped tool TodoWrite",
		"note: dropped field color",
		"loadout publish " + filepath.Join("packages", "reviewer"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("import agent output is missing %q:\n%s", want, out)
		}
	}
	out = alice.must("", "import", "skill", ".claude/skills/triage", "--team", "lime", "--out", "packages")
	if !strings.Contains(out, "lime/triage (skill)") || !strings.Contains(out, "kept fields allowed-tools") {
		t.Errorf("import skill output = %q", out)
	}

	// Import never overwrites, and refuses unknown tools unless told.
	if _, errOut, code := alice.runIn("", "import", "skill", ".claude/skills/triage", "--team", "lime", "--out", "packages"); code == 0 || !strings.Contains(errOut, "already exists") || !strings.Contains(errOut, "--name") {
		t.Errorf("second import: exit %d, stderr %q, want already exists and a hint to use --name", code, errOut)
	}
	// An agent and a skill of one name: the second is imported under
	// another name.
	writeFile(t, filepath.Join(alice.wd, ".claude", "agents", "triage.md"), "---\ndescription: Triage builds.\ntools: Read, LS\n---\nTriage.\n")
	if out := alice.must("", "import", "agent", ".claude/agents/triage.md", "--team", "lime", "--out", "packages", "--name", "triage-agent"); !strings.Contains(out, "lime/triage-agent (agent)") {
		t.Errorf("import agent --name output = %q", out)
	}
	writeFile(t, filepath.Join(alice.wd, "jira.md"), "---\ndescription: Jira helper.\ntools: Read, mcp__jira__search\n---\nUse Jira.\n")
	if _, errOut, code := alice.runIn("", "import", "agent", "jira.md", "--team", "lime"); code == 0 || !strings.Contains(errOut, "mcp__jira__search") || !strings.Contains(errOut, "--drop-unknown-tools") {
		t.Errorf("import with an unknown tool: exit %d, stderr %q, want the tool and the flag named", code, errOut)
	}

	alice.must("", "publish", filepath.Join("packages", "reviewer"))
	alice.must("", "publish", filepath.Join("packages", "triage"))

	// Bob installs both into Claude Code and Copilot.
	bob.must("", "install", "lime/reviewer")
	bob.must("", "install", "lime/triage")

	claudeAgent := readFile(t, filepath.Join(bob.home, ".claude", "agents", "reviewer.md"))
	for _, want := range []string{"tools: Read, Grep, Glob, Bash, Skill\n", "model: opus\n", "You review changes"} {
		if !strings.Contains(claudeAgent, want) {
			t.Errorf("Claude Code agent is missing %q:\n%s", want, claudeAgent)
		}
	}
	copilotAgent := readFile(t, filepath.Join(bob.home, ".copilot", "agents", "reviewer.md"))
	if !strings.Contains(copilotAgent, "tools: [read, search, execute]\n") {
		t.Errorf("Copilot agent tools are wrong:\n%s", copilotAgent)
	}
	for _, tool := range []string{".claude", ".copilot"} {
		dir := filepath.Join(bob.home, tool, "skills", "triage")
		if main := readFile(t, filepath.Join(dir, "SKILL.md")); !strings.Contains(main, "allowed-tools: Bash(curl:*) Read\n") {
			t.Errorf("%s SKILL.md lost allowed-tools:\n%s", tool, main)
		}
		if !exists(filepath.Join(dir, "references", "errors.md")) {
			t.Errorf("%s skill is missing references/errors.md", tool)
		}
	}
}
