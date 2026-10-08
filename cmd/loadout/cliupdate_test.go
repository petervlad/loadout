package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/server"
)

const newerLine = "loadout 0.5.0 is out (you have 0.4.0). To update, in your loadout folder: git pull && go install ./cmd/loadout"

// TestRegistryCommandsRememberTheLatestCLI: a command that talks to a
// registry keeps what it said about loadout releases, in a file of its own.
func TestRegistryCommandsRememberTheLatestCLI(t *testing.T) {
	alice := &person{session: newSession(t)}
	url := startRegistryAt(t, alice, "v0.5.0").URL
	token := createToken(t, alice, "read")
	alice.must(token+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "list")

	data, err := os.ReadFile(filepath.Join(alice.home, ".loadout", "cliupdate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "url: "+url) || !strings.Contains(got, "latest: 0.5.0") {
		t.Errorf("cliupdate.yaml:\n%s", got)
	}
}

// joinedBob is Bob with lime/planner installed from Alice's registry,
// built as v0.5.0 with opts, set up by an unknown build that hears nothing.
func joinedBob(t *testing.T, opts ...server.Option) (bob *person, stop func()) {
	t.Helper()
	return joinedBobAt(t, "v0.5.0", opts...)
}

// joinedBobAt is joinedBob with the registry built as build.
func joinedBobAt(t *testing.T, build string, opts ...server.Option) (bob *person, stop func()) {
	t.Helper()
	alice := &person{session: newSession(t)}
	bob = &person{session: newSession(t)}
	srv := startRegistryAt(t, alice, build, opts...)
	publish := createToken(t, alice, "publish")
	read := createToken(t, alice, "read")
	alice.must(publish+"\n", "remote", "add", "office", srv.URL, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")
	bob.must(read+"\n", "remote", "add", "office", srv.URL, "--token-stdin")
	bob.must("", "install", "lime/planner")
	return bob, srv.Close
}

func TestNewerCLIIsAnnounced(t *testing.T) {
	bob, stop := joinedBob(t)
	bob.version, bob.terminal = "v0.4.0", true

	out, errOut, code := bob.runIn("", "sync")
	if code != 0 || !strings.Contains(out, "lime/planner") || strings.Contains(out, "is out") {
		t.Errorf("sync: exit %d, stdout %q; want its table only", code, out)
	}
	if strings.TrimSpace(errOut) != newerLine {
		t.Errorf("sync stderr = %q, want %q", errOut, newerLine)
	}
	if _, errOut, _ := bob.runIn("", "sync"); errOut != "" {
		t.Errorf("a second sync the same day said %q", errOut)
	}

	// Offline, from what the registry said.
	stop()
	if out := bob.must("", "status"); !strings.Contains(out, "loadout 0.5.0 is out (you have 0.4.0; seen on 127.0.0.1:") {
		t.Errorf("status = %q, want the footer", out)
	}
	out = bob.must("", "version")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 2 || lines[0] != "loadout 0.4.0" || !strings.HasPrefix(lines[1], "0.5.0 is out (you have 0.4.0; seen on ") {
		t.Errorf("version = %q", out)
	}
	if out := bob.must("", "notices"); !strings.Contains(out, newerLine) {
		t.Errorf("notices = %q, want the line", out)
	}
	hook, errOut, code := bob.runIn(`{"source":"startup","cwd":"`+bob.wd+`"}`, "notices", "--hook", "claude")
	if code != 0 || errOut != "" {
		t.Fatalf("notices --hook: exit %d, stderr %q", code, errOut)
	}
	var got struct {
		SystemMessage string `json:"systemMessage"`
		Specific      struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(hook), &got); err != nil {
		t.Fatalf("hook output %q: %v", hook, err)
	}
	if !strings.Contains(got.SystemMessage, "loadout 0.5.0 is out") || !strings.Contains(got.Specific.Context, "A newer loadout CLI is available (0.5.0; installed 0.4.0)") {
		t.Errorf("hook output = %+v", got)
	}
	// The hook spoke on the agents' clock: the next session hears nothing.
	if hook, _, _ := bob.runIn(`{"source":"startup"}`, "notices", "--hook", "claude"); hook != "" {
		t.Errorf("a second session heard %q", hook)
	}
}

func TestAgentIsAskedToTellTheUser(t *testing.T) {
	for _, env := range []map[string]string{{"CLAUDECODE": "1"}, {"COPILOT_AGENT_SESSION_ID": "s-1"}} {
		bob, _ := joinedBob(t)
		bob.version, bob.env = "v0.4.0", env
		bob.terminal = true // VS Code's agent runs in a terminal; the variable wins
		_, errOut, code := bob.runIn("", "list")
		if code != 0 || !strings.HasPrefix(errOut, "loadout, for the agent: a newer loadout CLI is available (0.5.0; this is 0.4.0). Tell the user once") {
			t.Errorf("%v: list exit %d, stderr %q", env, code, errOut)
		}
		if _, errOut, _ := bob.runIn("", "list"); errOut != "" {
			t.Errorf("%v: a second list said %q", env, errOut)
		}
	}
}

func TestTooOldCLIIsToldEveryTime(t *testing.T) {
	bob, _ := joinedBob(t, server.WithCLI(semver.New(0, 5, 0), semver.New(0, 3, 0)))
	bob.version = "v0.2.0"
	const tooOld = "needs loadout 0.3.0 or newer and this is 0.2.0, so some commands may fail."
	for range 2 {
		if _, errOut, code := bob.runIn("", "list"); code != 0 || !strings.Contains(errOut, tooOld) {
			t.Errorf("list: exit %d, stderr %q", code, errOut)
		}
	}
	_, errOut, code := bob.runIn("", "install", "lime/nothing")
	if code != 1 {
		t.Errorf("a failing install exits %d, want 1", code)
	}
	if i, j := strings.Index(errOut, "error:"), strings.Index(errOut, tooOld); i < 0 || j < i {
		t.Errorf("failing install stderr %q, want the error, then the warning", errOut)
	}
	// LOADOUT_UPDATE_NOTICE=off keeps this one: it says why commands fail.
	bob.env = map[string]string{"LOADOUT_UPDATE_NOTICE": "off"}
	if _, errOut, _ := bob.runIn("", "list"); !strings.Contains(errOut, tooOld) {
		t.Errorf("quiet list stderr %q, want the warning", errOut)
	}
}

func TestQuietSettingSilencesNews(t *testing.T) {
	bob, _ := joinedBob(t)
	bob.version, bob.terminal = "v0.4.0", true
	bob.env = map[string]string{"LOADOUT_UPDATE_NOTICE": "off"}
	if _, errOut, _ := bob.runIn("", "sync"); errOut != "" {
		t.Errorf("quiet sync said %q", errOut)
	}
	if out := bob.must("", "status"); !strings.Contains(out, "loadout 0.5.0 is out") {
		t.Errorf("quiet status = %q, still want the footer", out)
	}
}

func TestDevBuildIsNotNagged(t *testing.T) {
	bob, _ := joinedBob(t, server.WithCLI(semver.New(0, 5, 0), semver.New(0, 3, 0)))
	bob.version, bob.terminal = "dev", true
	for _, args := range [][]string{{"list"}, {"sync"}, {"status"}} {
		if _, errOut, _ := bob.runIn("", args...); errOut != "" {
			t.Errorf("%v said %q to a dev build", args, errOut)
		}
	}
	if out := bob.must("", "version"); !strings.Contains(out, "The newest release is 0.5.0") {
		t.Errorf("version = %q, want the newest release named", out)
	}
}

// TestNewsLeavesStdoutAndExitCodesAlone: scripts read stdout and exit
// codes, so a registry that advertises a newer loadout changes neither;
// only stderr gains the line. status, notices and version show the news
// in their output on purpose and are left out.
func TestNewsLeavesStdoutAndExitCodesAlone(t *testing.T) {
	quiet, _ := joinedBobAt(t, "dev")
	told, _ := joinedBobAt(t, "v0.5.0", server.WithCLI(semver.New(0, 5, 0), semver.New(0, 3, 0)))
	told.version, quiet.version = "v0.2.0", "v0.2.0"
	told.terminal, quiet.terminal = true, true
	// Ports, paths and minutes differ between the two registries.
	norm := regexp.MustCompile(`127\.0\.0\.1:\d+|/[^ \n]*/\.claude/|\d{4}-\d\d-\d\d \d\d:\d\d`)
	const line = "loadout: 127.0.0.1:"
	for _, args := range [][]string{
		{"list"},
		{"layers"},
		{"sync"},
		{"diff", "lime/planner", "0.1.0", "0.1.0"},
		{"install", "lime/nothing"},
		{"sync", "--no-such-flag"},
		{"no-such-command"},
		{"policy", "lime/planner", "pin"},
		{"sync"},
		{"uninstall", "lime/planner"},
	} {
		qOut, qErr, qCode := quiet.runIn("", args...)
		out, errOut, code := told.runIn("", args...)
		if code != qCode {
			t.Errorf("%v: exit %d with the news, %d without", args, code, qCode)
		}
		if a, b := norm.ReplaceAllString(out, "X"), norm.ReplaceAllString(qOut, "X"); a != b {
			t.Errorf("%v: stdout with the news\n%s\nwithout\n%s", args, out, qOut)
		}
		if strings.Contains(out, "needs loadout") || strings.Contains(out, " is out") {
			t.Errorf("%v: the news reached stdout: %q", args, out)
		}
		// A registry without a release tag says nothing to a release CLI.
		if strings.Contains(qErr, "needs loadout") || strings.Contains(qErr, " is out") {
			t.Errorf("%v: a registry without a release told %q", args, qErr)
		}
		// args[0] == "no-such-command" runs nothing, so it says nothing.
		lines := strings.Split(strings.TrimSuffix(errOut, "\n"), "\n")
		if args[0] != "no-such-command" && !strings.HasPrefix(lines[len(lines)-1], line) {
			t.Errorf("%v: stderr %q, want the too-old line last", args, errOut)
		}
		if rest := strings.Join(slices.DeleteFunc(lines, func(l string) bool { return strings.HasPrefix(l, line) }), "\n"); norm.ReplaceAllString(rest, "X") != norm.ReplaceAllString(strings.TrimSuffix(qErr, "\n"), "X") {
			t.Errorf("%v: stderr besides the line %q, without the news %q", args, rest, qErr)
		}
	}
}

// TestSessionHookSaysHeldBackAndNewerCLI: the hook's one message carries
// both the held-back update and the newer loadout, the CLI part once a day
// on the agents' clock, the held-back part every session. The sync that
// found the held update exits 2 as before, its error first, the line last.
func TestSessionHookSaysHeldBackAndNewerCLI(t *testing.T) {
	h := heldSetupAt(t, "auto", "v0.5.0")
	bob := h.bob
	bob.version, bob.terminal = "v0.4.0", true
	_, errOut, code := bob.runIn("", "sync")
	if code != 2 {
		t.Errorf("sync with a held update exits %d, want 2", code)
	}
	if i, j := strings.Index(errOut, "error: 1 update is held back"), strings.Index(errOut, newerLine); i < 0 || j < i {
		t.Errorf("sync stderr %q, want the error, then the line", errOut)
	}
	hookIn := `{"cwd":"` + bob.wd + `","source":"startup","hook_event_name":"SessionStart"}`
	tests := []struct {
		tool, json string
		cli        bool
	}{
		{"claude", `"systemMessage":"loadout: 1 update is held back by your edits (lime/planner 0.2.0). Run loadout status. loadout 0.5.0 is out (you have 0.4.0).`, true},
		{"copilot", `"additionalContext":"loadout could not update lime/planner to 0.2.0`, false},
	}
	for _, tc := range tests {
		out, errOut, code := bob.runIn(hookIn, "notices", "--hook", tc.tool)
		if code != 0 || errOut != "" || !strings.Contains(out, tc.json) {
			t.Errorf("%s hook: exit %d, stderr %q, stdout %s", tc.tool, code, errOut, out)
		}
		if got := strings.Contains(out, "A newer loadout CLI is available (0.5.0; installed 0.4.0)"); got != tc.cli {
			t.Errorf("%s hook told the agent of the newer loadout: %v, want %v (the agents' clock)\n%s", tc.tool, got, tc.cli, out)
		}
	}
}
