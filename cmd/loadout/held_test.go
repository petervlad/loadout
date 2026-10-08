package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// held is Bob after heldSetup: his registry, his agent file and what
// loadout wrote into it before he edited it.
type held struct {
	bob       *person
	registry  *httptest.Server
	agentFile string
	original  []byte
}

// heldSetup has Alice publish lime/planner 0.1.0 and Bob install it with
// policy; Alice then publishes 0.2.0 and Bob has edited his copy.
func heldSetup(t *testing.T, policy string) held {
	t.Helper()
	return heldSetupAt(t, policy, "dev")
}

// heldSetupAt is heldSetup with the registry built as build.
func heldSetupAt(t *testing.T, policy, build string) held {
	t.Helper()
	alice := &person{session: newSession(t)}
	bob := &person{session: newSession(t)}
	srv := startRegistryAt(t, alice, build)
	publishToken := createToken(t, alice, "publish")
	readToken := createToken(t, alice, "read")
	alice.must(publishToken+"\n", "remote", "add", "office", srv.URL, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")
	bob.must(readToken+"\n", "remote", "add", "office", srv.URL, "--token-stdin")
	bob.must("", "install", "lime/planner", "--policy", policy)
	agentFile := filepath.Join(bob.home, ".claude", "agents", "planner.md")
	original, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentFile, []byte("Bob's own planner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alice.must("", "publish", "planner", "--bump", "minor")
	return held{bob: bob, registry: srv, agentFile: agentFile, original: original}
}

// TestHeldBackUpdateIsRemembered: an auto update Bob's edit holds back
// fails his cron with exit 2 and the ways forward; the record clears once
// he forces the update.
func TestHeldBackUpdateIsRemembered(t *testing.T) {
	h := heldSetup(t, "auto")
	bob := h.bob
	out, _, code := bob.runIn("", "sync")
	for _, want := range []string{
		"lime/planner 0.2.0 is held back.",
		"Edited since loadout wrote it:\n  ~/.claude/agents/planner.md",
		"loadout install lime/planner --force",
		"loadout policy lime/planner pin",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sync output lacks %q:\n%s", want, out)
		}
	}
	if code != 2 {
		t.Errorf("sync exit = %d, want 2", code)
	}
	if !strings.Contains(out, "Tip: loadout setup --hooks") {
		t.Errorf("sync output lacks the hooks tip:\n%s", out)
	}
	bob.must("", "setup", "--no-hooks")
	if out, _, _ := bob.runIn("", "sync"); strings.Contains(out, "Tip:") {
		t.Errorf("sync still tips after the person declined the hooks:\n%s", out)
	}
	if strings.Contains(out, "use --force to overwrite") {
		t.Errorf("sync still prints the install's --force hint:\n%s", out)
	}
	if data, _ := os.ReadFile(h.agentFile); string(data) != "Bob's own planner\n" {
		t.Error("sync overwrote Bob's edit")
	}
	if out := bob.must("", "status"); !strings.Contains(out, "As of the last sync") || !strings.Contains(out, "0.2.0 held back: you edited planner.md (claude)") {
		t.Errorf("status = %q, want the held update below the table", out)
	}

	if out := bob.must("", "notices"); out != "lime/planner: 0.2.0 held back: you edited planner.md (claude)\n" {
		t.Errorf("notices = %q", out)
	}

	// Bob edits again and takes the release: nothing is behind.
	if err := os.WriteFile(h.agentFile, []byte("again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bob.must("", "install", "lime/planner", "--force")
	if out := bob.must("", "status"); strings.Contains(out, "As of the last sync") {
		t.Errorf("status after --force = %q, want nothing behind", out)
	}
}

// TestSessionHookIsOffline: with the registry gone, the session hook still
// says what the last sync found, from local files only, and stops once the
// edit is reverted.
func TestSessionHookIsOffline(t *testing.T) {
	h := heldSetup(t, "auto")
	bob := h.bob
	bob.runIn("", "sync")
	h.registry.Close()
	hookIn := `{"cwd":"` + bob.wd + `","source":"startup","hook_event_name":"SessionStart"}`
	out, errOut, code := bob.runIn(hookIn, "notices", "--hook", "claude")
	if code != 0 || !strings.Contains(out, `"systemMessage":"loadout: 1 update is held back by your edits (lime/planner 0.2.0). Run loadout status."`) || strings.Count(out, "\n") != 1 {
		t.Errorf("notices --hook claude: exit %d\n%s\n%s", code, out, errOut)
	}
	if out, _, code := bob.runIn(hookIn, "notices", "--hook", "copilot"); code != 0 || !strings.Contains(out, `"additionalContext":"loadout could not update lime/planner to 0.2.0`) {
		t.Errorf("notices --hook copilot: exit %d, %q", code, out)
	}
	if out, _, code := bob.runIn(`{"source":"resume"}`, "notices", "--hook", "claude"); code != 0 || out != "" {
		t.Errorf("a resumed session: exit %d, %q; want silence", code, out)
	}
	if out, _, code := bob.runIn("not json", "notices", "--hook", "copilot"); code != 0 || out != "" {
		t.Errorf("bad hook input: exit %d, %q; want silence and exit 0", code, out)
	}

	// Bob restores loadout's text: nothing is held any more.
	if err := os.WriteFile(h.agentFile, h.original, 0o644); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
	if out, _, _ := bob.runIn(hookIn, "notices", "--hook", "claude"); out != "" {
		t.Errorf("notices after the edit was reverted = %q, want nothing", out)
	}
	if out := bob.must("", "status"); !strings.Contains(out, "0.2.0 available: loadout install lime/planner") {
		t.Errorf("status after the revert = %q, want available", out)
	}
}

// TestNotifyWarnsAboutEdits: a notify install with edits hears of the
// update and of the edits in its way, and sync still exits 0.
func TestNotifyWarnsAboutEdits(t *testing.T) {
	out := heldSetup(t, "notify").bob.must("", "sync")
	if !strings.Contains(out, "update available, you edited 1 file") || !strings.Contains(out, "lime/planner 0.2.0 is out, and your edits would hold it back.") {
		t.Errorf("sync output = %q", out)
	}
}

// TestSessionHooks: setup --hooks adds loadout's hook to a settings.json
// that holds the person's own hooks without touching them, a second run
// changes nothing, and --no-hooks gives back the person's exact file.
// Copilot's file appears only once ~/.copilot exists.
func TestSessionHooks(t *testing.T) {
	s := newSession(t)
	settings := filepath.Join(s.home, ".claude", "settings.json")
	original := "{\n  \"model\": \"opus\",\n  \"hooks\": {\n    \"SessionStart\": [\n      {\n        \"matcher\": \"startup\",\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"echo hello\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"
	if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if out := s.mustRun("setup", "--hooks"); !strings.Contains(out, "set up the session hook in Claude Code (~/.claude/settings.json)") || strings.Contains(out, "Copilot") {
		t.Errorf("setup --hooks = %q", out)
	}
	with := readFile(t, settings)
	if !strings.Contains(with, `"command": "echo hello"`) || !strings.Contains(with, "notices --hook claude 2>/dev/null || true") || !strings.HasPrefix(with, "{\n  \"model\": \"opus\",") {
		t.Errorf("settings.json after setup:\n%s", with)
	}
	if exists(filepath.Join(s.home, ".copilot")) {
		t.Error("setup --hooks made ~/.copilot")
	}
	if readFile(t, settings+".loadout.bak") != original {
		t.Error("no backup of the original settings.json")
	}
	if out := s.mustRun("setup", "--hooks"); !strings.Contains(out, "the session hook in Claude Code is set up") || readFile(t, settings) != with {
		t.Errorf("setup --hooks again = %q, and the file changed: %v", out, readFile(t, settings) != with)
	}

	// Copilot appears later.
	if err := os.Mkdir(filepath.Join(s.home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := s.mustRun("setup", "--hooks"); !strings.Contains(out, "set up the session hook in GitHub Copilot (~/.copilot/hooks/loadout.json)") {
		t.Errorf("setup --hooks with Copilot = %q", out)
	}

	if out := s.mustRun("setup", "--no-hooks"); !strings.Contains(out, "removed the session hook from Claude Code") || !strings.Contains(out, "removed the session hook from GitHub Copilot") {
		t.Errorf("setup --no-hooks = %q", out)
	}
	if got := readFile(t, settings); got != original {
		t.Errorf("settings.json after --no-hooks =\n%s\nwant the original back", got)
	}
	if exists(filepath.Join(s.home, ".copilot", "hooks", "loadout.json")) {
		t.Error("Copilot's hook file is still there")
	}
}

// TestInstallMerge: Bob edits a line, Alice changes another, and install
// --merge keeps both; when they change the same line the file holds
// markers, status and the hook say so, and resolving them clears it.
func TestInstallMerge(t *testing.T) {
	alice := &person{session: newSession(t)}
	bob := &person{session: newSession(t)}
	url := startServer(t, alice)
	publishToken := createToken(t, alice, "publish")
	readToken := createToken(t, alice, "read")
	alice.must(publishToken+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	agentMD := filepath.Join(alice.wd, "planner", "agent.md")
	head, _, _ := strings.Cut(readFile(t, agentMD), "---\nYou are")
	prompt := "Line one.\nLine two.\nLine three.\nLine four.\nLine five.\n"
	alice.publishPrompt(t, agentMD, head, prompt, "")
	bob.must(readToken+"\n", "remote", "add", "office", url, "--token-stdin")
	bob.must("", "install", "lime/planner")
	bobAgent := filepath.Join(bob.home, ".claude", "agents", "planner.md")
	bob.replaceIn(t, bobAgent, "Line five.", "Line five, Bob's way.")

	prompt = strings.Replace(prompt, "Line one.", "Line one, better.", 1)
	alice.publishPrompt(t, agentMD, head, prompt, "minor")
	out := bob.must("", "sync")
	if !strings.Contains(out, "loadout install lime/planner --merge") {
		t.Errorf("sync does not offer --merge first:\n%s", out)
	}
	out = bob.must("", "install", "lime/planner", "--merge")
	if !strings.Contains(out, "updated lime/planner 0.1.0 to 0.2.0 (user scope), keeping your edits:") || !strings.Contains(out, "merged") {
		t.Errorf("install --merge = %q", out)
	}
	got := readFile(t, bobAgent)
	if !strings.Contains(got, "Line one, better.") || !strings.Contains(got, "Line five, Bob's way.") || !strings.Contains(got, "lime/planner@0.2.0") {
		t.Errorf("merged agent:\n%s", got)
	}
	if out := bob.must("", "sync"); !strings.Contains(out, "up to date") {
		t.Errorf("sync after the merge = %q, want up to date", out)
	}
	if _, errOut, code := bob.runIn("", "install", "lime/planner", "--merge", "--force"); code == 0 || !strings.Contains(errOut, "choose one") {
		t.Errorf("--merge --force: exit %d, %q", code, errOut)
	}
	for _, tc := range []struct {
		who  *person
		args []string
		want string
	}{
		{bob, []string{"install", "loadout/loadout-setup", "--merge"}, "loadout setup --skills --force"},
		{alice, []string{"install", "./planner", "--merge"}, "a folder install has no release to merge from"},
		{alice, []string{"install", "lime/planner", "--merge"}, "nothing to merge"},
	} {
		if _, errOut, code := tc.who.runIn("", tc.args...); code == 0 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, %q; want %q", tc.args, code, errOut, tc.want)
		}
	}

	// The same line on both sides: markers, and the notices say so.
	bob.replaceIn(t, bobAgent, "Line three.", "Line three, Bob.")
	prompt = strings.Replace(prompt, "Line three.", "Line three, Alice.", 1)
	alice.publishPrompt(t, agentMD, head, prompt, "minor")
	out = bob.must("", "install", "lime/planner", "--merge")
	if !strings.Contains(out, "1 conflict") || !strings.Contains(out, "1 file has conflict markers") {
		t.Errorf("install --merge with a conflict = %q", out)
	}
	got = readFile(t, bobAgent)
	if !strings.Contains(got, "<<<<<<< yours (edited 0.2.0)\nLine three, Bob.\n||||||| installed 0.2.0\nLine three.\n=======\nLine three, Alice.\n>>>>>>> release 0.3.0\n") {
		t.Errorf("conflicted agent:\n%s", got)
	}
	if out := bob.must("", "status"); !strings.Contains(out, "0.3.0 merged: resolve the conflicts in planner.md (claude)") {
		t.Errorf("status = %q, want the unresolved conflict", out)
	}
	hookIn := `{"cwd":"` + bob.wd + `","source":"startup"}`
	if out, _, _ := bob.runIn(hookIn, "notices", "--hook", "claude"); !strings.Contains(out, "merge conflicts to resolve in planner.md (claude) (lime/planner 0.3.0)") {
		t.Errorf("hook = %q", out)
	}

	// Bob resolves them.
	resolved := strings.Replace(got, "<<<<<<< yours (edited 0.2.0)\nLine three, Bob.\n||||||| installed 0.2.0\nLine three.\n=======\nLine three, Alice.\n>>>>>>> release 0.3.0\n", "Line three, Bob and Alice.\n", 1)
	if err := os.WriteFile(bobAgent, []byte(resolved), 0o644); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
	if out := bob.must("", "status"); strings.Contains(out, "As of the last sync") {
		t.Errorf("status after resolving = %q", out)
	}
	if out, _, _ := bob.runIn(hookIn, "notices", "--hook", "claude"); out != "" {
		t.Errorf("hook after resolving = %q", out)
	}
	if out := bob.must("", "sync"); !strings.Contains(out, "up to date") {
		t.Errorf("sync after resolving = %q", out)
	}
}

// publishPrompt writes the agent's prompt and publishes it, bumped by
// bump when it is not empty.
func (p *person) publishPrompt(t *testing.T, agentMD, head, prompt, bump string) {
	t.Helper()
	if err := os.WriteFile(agentMD, []byte(head+"---\n"+prompt), 0o644); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
	args := []string{"publish", "planner"}
	if bump != "" {
		args = append(args, "--bump", bump)
	}
	p.must("", args...)
}

// replaceIn changes from to to in the file at path, as a person would.
func (p *person) replaceIn(t *testing.T, path, from, to string) {
	t.Helper()
	data := readFile(t, path)
	if !strings.Contains(data, from) {
		t.Fatalf("%s has no %q:\n%s", path, from, data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(data, from, to, 1)), 0o644); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
}

// TestModelChangeLeavesAnEditedSkillAlone: new team model settings change
// no file of a skill, so Alice's edit to one holds nothing back: sync exits
// 0, says nothing is held, and status and the hook stay quiet.
func TestModelChangeLeavesAnEditedSkillAlone(t *testing.T) {
	_, base, aliceWeb, olgaWeb := ownedRegistry(t)
	olga := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, olga, olgaWeb, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	olga.must("", "new", "skill", "lime/checklist")
	olga.must("", "publish", "checklist")
	for _, policy := range []string{"auto", "notify"} {
		scope := map[string]string{"auto": "user", "notify": "project"}[policy]
		if scope == "project" {
			if err := os.Mkdir(filepath.Join(alice.wd, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		alice.must("", "install", "lime/checklist", "--policy", policy, "--scope", scope)
	}
	for _, skill := range []string{filepath.Join(alice.home, ".claude", "skills", "checklist", "SKILL.md"), filepath.Join(alice.wd, ".claude", "skills", "checklist", "SKILL.md")} {
		alice.replaceIn(t, skill, "# checklist", "# checklist, Alice's way")
	}
	olga.must("", "namespace", "set", "acme", "--model", "claude:balanced=opus")

	out, errOut, code := alice.runIn("", "sync")
	if code != 0 || strings.Contains(out, "held back") || strings.Contains(out, "model settings") {
		t.Errorf("sync: exit %d, want 0 and nothing held\n%s\n%s", code, out, errOut)
	}
	if out := alice.must("", "status"); strings.Contains(out, "As of the last sync") {
		t.Errorf("status = %q, want nothing behind", out)
	}
	if out, _, _ := alice.runIn(`{"cwd":"`+alice.wd+`","source":"startup"}`, "notices", "--hook", "claude"); out != "" {
		t.Errorf("hook = %q, want silence", out)
	}
}

// TestMergeAgainKeepsUnresolvedConflicts: Bob leaves the markers of one
// merge in his file and tries to merge the next release too. The second
// merge refuses, naming the file and the ways out, changes nothing, and
// status and the hook keep reporting the markers until he resolves them.
func TestMergeAgainKeepsUnresolvedConflicts(t *testing.T) {
	alice := &person{session: newSession(t)}
	bob := &person{session: newSession(t)}
	url := startServer(t, alice)
	publishToken := createToken(t, alice, "publish")
	readToken := createToken(t, alice, "read")
	alice.must(publishToken+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	agentMD := filepath.Join(alice.wd, "planner", "agent.md")
	head, _, _ := strings.Cut(readFile(t, agentMD), "---\nYou are")
	prompt := "Line one.\nLine two.\nLine three.\n"
	alice.publishPrompt(t, agentMD, head, prompt, "")
	bob.must(readToken+"\n", "remote", "add", "office", url, "--token-stdin")
	bob.must("", "install", "lime/planner")
	bobAgent := filepath.Join(bob.home, ".claude", "agents", "planner.md")

	bob.replaceIn(t, bobAgent, "Line three.", "Line three, Bob.")
	alice.publishPrompt(t, agentMD, head, strings.Replace(prompt, "Line three.", "Line three, Alice.", 1), "minor")
	if out := bob.must("", "install", "lime/planner", "--merge"); !strings.Contains(out, "1 conflict") {
		t.Fatalf("first merge = %q, want a conflict", out)
	}
	conflicted := readFile(t, bobAgent)

	// 0.3.0 changes only the description; Bob tries to merge it unresolved.
	head = strings.Replace(head, "description: ", "description: Now with a plan. ", 1)
	alice.publishPrompt(t, agentMD, head, strings.Replace(prompt, "Line three.", "Line three, Alice.", 1), "minor")
	_, errOut, code := bob.runIn("", "install", "lime/planner", "--merge")
	if code == 0 || !strings.Contains(errOut, "still has conflict markers from the last loadout install --merge in planner.md (claude)") ||
		!strings.Contains(errOut, "loadout install lime/planner --force") {
		t.Errorf("second merge: exit %d, %q; want a refusal naming the file and the ways out", code, errOut)
	}
	if readFile(t, bobAgent) != conflicted {
		t.Error("the refused merge changed the file")
	}
	if out := bob.must("", "status"); !strings.Contains(out, "resolve the conflicts in planner.md") {
		t.Errorf("status = %q, want the markers still reported", out)
	}
	if hook, _, _ := bob.runIn(`{"cwd":"`+bob.wd+`","source":"startup"}`, "notices", "--hook", "claude"); !strings.Contains(hook, "merge conflicts to resolve") {
		t.Errorf("hook = %q, want the markers still reported", hook)
	}

	// Resolved, the merge goes through and nothing is left to report.
	resolved := strings.Replace(conflicted, "<<<<<<< yours (edited 0.1.0)\nLine three, Bob.\n||||||| installed 0.1.0\nLine three.\n=======\nLine three, Alice.\n>>>>>>> release 0.2.0\n", "Line three, Bob and Alice.\n", 1)
	if err := os.WriteFile(bobAgent, []byte(resolved), 0o644); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
	if out := bob.must("", "install", "lime/planner", "--merge"); !strings.Contains(out, "to 0.3.0") {
		t.Errorf("merge after resolving = %q", out)
	}
	if out := bob.must("", "status"); strings.Contains(out, "resolve the conflicts") {
		t.Errorf("status after resolving and merging = %q", out)
	}
}
