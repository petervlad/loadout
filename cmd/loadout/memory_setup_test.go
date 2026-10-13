package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	addedLine   = "added lime's team memory to Claude Code (~/.claude.json)"
	removedLine = "removed lime's team memory from Claude Code (~/.claude.json)"
	limitName   = "CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH"
)

// fakeClaude puts a claude program first on the PATH. It records its
// arguments and keeps the entry in $HOME/.claude.json the way Claude Code
// would, and the home it sees is the test person's, so the real
// ~/.claude.json is never in reach. It returns the calls recorded so far.
func fakeClaude(t *testing.T) (calls func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.log")
	script := `#!/bin/sh
echo "$*" >> "$FAKE_CLAUDE_LOG"
case "$2" in
add-json) printf '{"mcpServers":{"%s":%s}}\n' "$5" "$6" > "$HOME/.claude.json" ;;
remove) printf '{"mcpServers":{}}\n' > "$HOME/.claude.json" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil { //nolint:gosec // a test program that must run
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CLAUDE_LOG", log)
	return func() []string {
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

func (p *person) readHome(rel string) string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.home, filepath.FromSlash(rel)))
	if err != nil {
		p.t.Fatal(err)
	}
	return string(data)
}

func (p *person) writeHome(rel, text string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.home, filepath.FromSlash(rel)), []byte(text), 0o600); err != nil {
		p.t.Fatal(err)
	}
}

// noSecret fails when text holds the memory token.
func noSecret(t *testing.T, what, token, text string) {
	t.Helper()
	if strings.Contains(text, token) {
		t.Errorf("%s shows the memory token", what)
	}
}

func TestInstallAddsMemoryServer(t *testing.T) {
	forms := []struct {
		name string
		args func(p *person) []string
	}{
		{"team package", func(*person) []string { return []string{"install", "lime/planner"} }},
		{"all", func(*person) []string { return []string{"install", "--all"} }},
		{"folder", func(p *person) []string {
			p.must("", "new", "agent", "lime/helper")
			return []string{"install", "helper"}
		}},
	}
	for _, tt := range forms {
		t.Run(tt.name, func(t *testing.T) {
			calls := fakeClaude(t)
			_, alice, _, token := memoryTeam(t)
			args := tt.args(alice)

			out, errOut, code := alice.runIn("", args...)
			if code != 0 {
				t.Fatalf("loadout %s: exit %d\n%s\n%s", strings.Join(args, " "), code, out, errOut)
			}
			for _, want := range []string{addedLine, "set " + limitName + "=24000 in ~/.claude/settings.json"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s\nstderr: %s", want, out, errOut)
				}
			}
			noSecret(t, "install output", token, out+errOut)

			got := calls()
			if len(got) != 1 || !strings.HasPrefix(got[0], "mcp add-json --scope user loadout-memory-lime {") ||
				!strings.Contains(got[0], `"args":["mcp","--team","lime"]`) {
				t.Errorf("claude was called with %q, want one add-json of loadout-memory-lime with mcp --team lime", got)
			}
			noSecret(t, "the claude command line", token, strings.Join(got, "\n"))
			if state := alice.readHome(".claude.json"); !strings.Contains(state, "loadout-memory-lime") {
				t.Errorf("~/.claude.json = %s", state)
			}
			settings := alice.readHome(".claude/settings.json")
			if !strings.Contains(settings, `"`+limitName+`": "24000"`) {
				t.Errorf("settings.json = %s, want the limit set", settings)
			}
			noSecret(t, "settings.json", token, settings)

			// Again: nothing to add, nothing to say, claude is not asked.
			out = alice.must("", args...)
			if strings.Contains(out, "team memory") || strings.Contains(out, limitName) {
				t.Errorf("second install talks about memory:\n%s", out)
			}
			if again := calls(); len(again) != 1 {
				t.Errorf("claude was called again: %q", again)
			}
		})
	}
}

func TestSyncRemovesItWhenOff(t *testing.T) {
	calls := fakeClaude(t)
	_, alice, _, _ := memoryTeam(t)
	alice.must("", "install", "lime/planner")
	if got := calls(); len(got) != 1 {
		t.Fatalf("install called claude %q, want one add", got)
	}

	// Still on: sync changes nothing.
	if out := alice.must("", "sync"); strings.Contains(out, "team memory") {
		t.Errorf("sync with memory on talks about it:\n%s", out)
	}

	alice.must("", "namespace", "set", "lime", "--memory", "off")
	out := alice.must("", "sync")
	if !strings.Contains(out, removedLine) {
		t.Errorf("sync output lacks %q:\n%s", removedLine, out)
	}
	got := calls()
	if len(got) != 2 || got[1] != "mcp remove --scope user loadout-memory-lime" {
		t.Errorf("claude was called with %q, want a remove of loadout-memory-lime", got)
	}
	if state := alice.readHome(".claude.json"); strings.Contains(state, "loadout-memory-lime") {
		t.Errorf("~/.claude.json still has the entry: %s", state)
	}

	// On again: sync adds it back.
	alice.must("", "namespace", "set", "lime", "--memory", "on")
	if out := alice.must("", "sync"); !strings.Contains(out, addedLine) {
		t.Errorf("sync output lacks %q:\n%s", addedLine, out)
	}
}

func TestSetupNoMemoryOptsOut(t *testing.T) {
	calls := fakeClaude(t)
	_, alice, _, _ := memoryTeam(t)
	alice.must("", "install", "lime/planner")

	out := alice.must("", "setup", "--no-memory")
	if !strings.Contains(out, removedLine) {
		t.Errorf("setup --no-memory output lacks %q:\n%s", removedLine, out)
	}
	if wish := alice.readHome(".loadout/mcp.yaml"); !strings.Contains(wish, "claude") {
		t.Errorf("mcp.yaml = %q, want claude declined", wish)
	}
	before := len(calls())

	// Install and sync now leave the tool alone.
	alice.must("", "new", "agent", "lime/other")
	out = alice.must("", "install", "other")
	alice.must("", "sync")
	if strings.Contains(out, "team memory") || len(calls()) != before {
		t.Errorf("install after --no-memory touched claude: %q\n%s", calls()[before:], out)
	}

	out = alice.must("", "setup", "--memory")
	if !strings.Contains(out, addedLine) {
		t.Errorf("setup --memory output lacks %q:\n%s", addedLine, out)
	}

	if _, errOut, code := alice.runIn("", "setup", "--memory", "--no-memory"); code == 0 || !strings.Contains(errOut, "contradict") {
		t.Errorf("both flags: exit %d, stderr %q, want a refusal", code, errOut)
	}
}

func TestSettingsLimitIsSetOnlyWhenUnset(t *testing.T) {
	fakeClaude(t)
	_, alice, _, _ := memoryTeam(t)
	const own = "{\n  \"env\": {\n    \"" + limitName + "\": \"9000\"\n  },\n  \"model\": \"opus\"\n}\n"
	alice.writeHome(".claude/settings.json", own)

	out := alice.must("", "install", "lime/planner")
	if !strings.Contains(out, addedLine) {
		t.Errorf("install output lacks %q:\n%s", addedLine, out)
	}
	if strings.Contains(out, limitName) {
		t.Errorf("install says it set a limit the person had set:\n%s", out)
	}
	if got := alice.readHome(".claude/settings.json"); got != own {
		t.Errorf("settings.json changed:\n%s", got)
	}
}

func TestNoLimitWhenNoServerIsAdded(t *testing.T) {
	fakeClaude(t)
	_, alice, _, _ := memoryTeam(t)
	alice.must("", "namespace", "set", "lime", "--memory", "off")
	out := alice.must("", "install", "lime/planner")
	// settings.json may exist, since login set up the session hook in it.
	if data, err := os.ReadFile(filepath.Join(alice.home, ".claude", "settings.json")); err == nil && strings.Contains(string(data), limitName) || strings.Contains(out, limitName) {
		t.Error("install set the description limit although no memory server was added")
	}
}
