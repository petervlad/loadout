package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var builtinSkills = []string{"loadout-setup", "loadout-author", "loadout-review", "loadout-admin"}

func (p *person) claudeSkill(name string) string {
	return filepath.Join(p.home, ".claude", "skills", name, "SKILL.md")
}

// TestLoginInstallsLoadoutSkills: signing in gives the person's AI agent
// loadout's own skills, for the user, wherever login runs; an uninstalled
// one stays uninstalled until setup --skills.
func TestLoginInstallsLoadoutSkills(t *testing.T) {
	_, base, alice := accountRegistry(t)
	p := &person{session: newSession(t)}
	// Login runs in a repository with its own .claude folder: the skills
	// still go to the home folder, never into the repository.
	for _, dir := range []string{".git", ".claude"} {
		if err := os.Mkdir(filepath.Join(p.wd, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	out, errOut, code := loginAs(t, p, alice, "--remote", base)
	if code != 0 {
		t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
	}
	if want := "added loadout's skills for your AI agent (claude): " + strings.Join(builtinSkills, ", "); !strings.Contains(out, want) {
		t.Errorf("login output = %q, want %q", out, want)
	}
	// Login also sets up the session hook, in one line that says how to
	// remove it, in the home folder only.
	if want := "set up session hooks (~/.claude/settings.json)"; !strings.Contains(out, want) || !strings.Contains(out, "loadout setup --no-hooks") {
		t.Errorf("login output = %q, want %q", out, want)
	}
	if !strings.Contains(readFile(t, filepath.Join(p.home, ".claude", "settings.json")), "notices --hook claude") || exists(filepath.Join(p.wd, ".claude", "settings.json")) {
		t.Error("login did not set up the hook in ~/.claude/settings.json, or wrote one into the repository")
	}
	for _, name := range builtinSkills {
		if !strings.Contains(readFile(t, p.claudeSkill(name)), "managed by loadout: loadout/"+name+"@") {
			t.Errorf("%s is not installed as a loadout package", name)
		}
	}
	if exists(filepath.Join(p.wd, ".claude", "skills")) || exists(filepath.Join(p.home, ".copilot")) {
		t.Error("skills went into the repository, or ~/.copilot was created")
	}
	status := p.must("", "status")
	if !strings.Contains(status, "loadout/loadout-setup") || !strings.Contains(status, " auto ") {
		t.Errorf("status = %q, want the skills with updates auto", status)
	}

	// Signing in again changes nothing.
	if out, _, _ = loginAs(t, p, alice, "--remote", base); strings.Contains(out, "loadout's skills") || strings.Contains(out, "session hooks") {
		t.Errorf("second login output = %q, want nothing about the skills or hooks", out)
	}
	// After --no-hooks, login leaves them out.
	p.must("", "setup", "--no-hooks")
	if exists(filepath.Join(p.home, ".claude", "settings.json")) {
		t.Error("--no-hooks left the settings.json that only held loadout's hook")
	}
	if out, _, _ = loginAs(t, p, alice, "--remote", base); strings.Contains(out, "session hooks") || exists(filepath.Join(p.home, ".claude", "settings.json")) {
		t.Errorf("login after --no-hooks = %q, want no hooks", out)
	}

	// The person uninstalls one; login does not bring it back.
	if out := p.must("", "uninstall", "loadout/loadout-review"); !strings.Contains(out, "loadout setup --skills") {
		t.Errorf("uninstall output = %q, want how to bring it back", out)
	}
	if out, _, _ = loginAs(t, p, alice, "--remote", base); exists(p.claudeSkill("loadout-review")) {
		t.Errorf("login reinstalled an uninstalled skill: %q", out)
	}
	if out := p.must("", "sync"); exists(p.claudeSkill("loadout-review")) {
		t.Errorf("sync reinstalled an uninstalled skill: %q", out)
	}

	// setup --skills brings it back.
	if out := p.must("", "setup", "--skills"); !strings.Contains(out, "added loadout's skills for your AI agent (claude): loadout-review") {
		t.Errorf("setup output = %q", out)
	}
	if !exists(p.claudeSkill("loadout-review")) {
		t.Error("setup --skills did not install loadout-review")
	}
	if out := p.must("", "setup", "--skills"); !strings.Contains(out, "loadout's skills are up to date (claude)") {
		t.Errorf("second setup output = %q", out)
	}
	if _, errOut, code := p.runIn("", "setup"); code == 0 || !strings.Contains(errOut, "pass --skills") {
		t.Errorf("setup without --skills: exit %d, %q", code, errOut)
	}
}

// TestLoginWithoutAnAITool: on a machine with neither ~/.claude nor
// ~/.copilot, login succeeds, says so in one line and creates neither;
// the next sync after Claude Code appears adds the skills.
func TestLoginWithoutAnAITool(t *testing.T) {
	_, base, alice := accountRegistry(t)
	p := &person{session: newSession(t)}
	if err := os.Remove(filepath.Join(p.home, ".claude")); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := loginAs(t, p, alice, "--remote", base)
	if code != 0 {
		t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "No Claude Code or Copilot found here") || !strings.Contains(out, "loadout setup --skills --target claude") || strings.Count(out, "Claude Code or Copilot") != 1 {
		t.Errorf("login output = %q, want one line about the missing tools", out)
	}
	for _, dir := range []string{".claude", ".copilot"} {
		if exists(filepath.Join(p.home, dir)) {
			t.Errorf("login created ~/%s", dir)
		}
	}
	if out := p.must("", "sync"); strings.Contains(out, "loadout's skills") || exists(filepath.Join(p.home, ".claude")) {
		t.Errorf("sync without a tool = %q", out)
	}
	if _, errOut, code := p.runIn("", "setup", "--skills"); code == 0 || !strings.Contains(errOut, "--target claude") {
		t.Errorf("setup --skills without a tool: exit %d, %q", code, errOut)
	}

	// Claude Code is installed later: the next sync adds the skills.
	if err := os.Mkdir(filepath.Join(p.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := p.must("", "sync"); !strings.Contains(out, "added loadout's skills for your AI agent (claude)") {
		t.Errorf("sync after claude appeared = %q", out)
	}
	if !exists(p.claudeSkill("loadout-author")) {
		t.Error("sync did not add the skills")
	}
}

// TestSetupSkillsWithATarget: setup --skills --target creates the tool's
// folder when the person asks, without any registry.
func TestSetupSkillsWithATarget(t *testing.T) {
	s := newSession(t)
	if err := os.Remove(filepath.Join(s.home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if out := s.mustRun("setup", "--skills", "--target", "copilot"); !strings.Contains(out, "added loadout's skills for your AI agent (copilot)") {
		t.Errorf("setup output = %q", out)
	}
	if !exists(filepath.Join(s.home, ".copilot", "skills", "loadout-setup", "SKILL.md")) || exists(filepath.Join(s.home, ".claude")) {
		t.Error("want the skills in ~/.copilot only")
	}
	// sync keeps them (they come from the CLI, not a registry).
	if out := s.mustRun("sync"); !strings.Contains(out, "nothing installed from a registry") {
		t.Errorf("sync = %q", out)
	}
}

// TestLoginLeavesABadSettingsFileAlone: a settings.json loadout cannot
// edit safely (JSON with a comment, or a link into a dotfiles repository)
// never fails the sign-in and is never changed; login says why in one line
// and points to setup --hooks, which prints the entry to paste by hand.
func TestLoginLeavesABadSettingsFileAlone(t *testing.T) {
	tests := []struct {
		name string
		make func(t *testing.T, settings string)
		want string
	}{
		{name: "not valid JSON", want: "not valid JSON", make: func(t *testing.T, settings string) {
			t.Helper()
			if err := os.WriteFile(settings, []byte("{\n  // mine\n  \"model\": \"opus\"\n}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a symbolic link", want: "is a symbolic link", make: func(t *testing.T, settings string) {
			t.Helper()
			dotfile := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(dotfile, []byte("{\"model\": \"opus\"}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dotfile, settings); err != nil {
				t.Fatal(err)
			}
		}},
	}
	_, base, alice := accountRegistry(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &person{session: newSession(t)}
			settings := filepath.Join(p.home, ".claude", "settings.json")
			tc.make(t, settings)
			before := readFile(t, settings)
			out, errOut, code := loginAs(t, p, alice, "--remote", base)
			if code != 0 {
				t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
			}
			if !strings.Contains(out, "left ~/.claude/settings.json alone") || !strings.Contains(out, tc.want) || !strings.Contains(out, "loadout setup --hooks") {
				t.Errorf("login output = %q, want why the file was left alone", out)
			}
			if readFile(t, settings) != before || exists(settings+".loadout.bak") {
				t.Error("login changed the settings file, or backed it up")
			}
			out, _, code = p.runIn("", "setup", "--hooks")
			if code != 1 || !strings.Contains(out, "To set the hook up by hand") || !strings.Contains(out, "notices --hook claude") {
				t.Errorf("setup --hooks: exit %d, %q; want 1 and the entry to paste", code, out)
			}
			if readFile(t, settings) != before {
				t.Error("setup --hooks changed the settings file")
			}
		})
	}
}

var hostSkills = []string{"loadout-host-setup", "loadout-host-provision"}

// TestSetupHostSkills: the skills that set up a registry are installed only
// when asked for; login and sync never add them, sync keeps them in step,
// and uninstall says how to bring one back.
func TestSetupHostSkills(t *testing.T) {
	s := newSession(t)
	if out := s.mustRun("setup", "--skills"); !strings.Contains(out, "added loadout's skills") {
		t.Fatalf("setup --skills = %q", out)
	}
	for _, name := range hostSkills {
		if exists(filepath.Join(s.home, ".claude", "skills", name)) {
			t.Fatalf("setup --skills added %s", name)
		}
	}
	out := s.mustRun("setup", "--host-skills")
	for _, name := range hostSkills {
		if !strings.Contains(out, name) || !exists(filepath.Join(s.home, ".claude", "skills", name, "SKILL.md")) {
			t.Errorf("setup --host-skills did not install %s: %q", name, out)
		}
	}
	if !strings.Contains(out, "ask your AI agent to set up a loadout registry") {
		t.Errorf("setup --host-skills = %q, want what to ask", out)
	}
	// The provision skill carries the deploy files.
	if !exists(filepath.Join(s.home, ".claude", "skills", "loadout-host-provision", "assets", "deploy.sh")) {
		t.Error("the provision skill has no deploy.sh")
	}
	if out := s.mustRun("setup", "--host-skills"); !strings.Contains(out, "are up to date") {
		t.Errorf("second setup --host-skills = %q", out)
	}
	if out := s.mustRun("uninstall", "loadout/loadout-host-setup"); !strings.Contains(out, "loadout setup --host-skills") {
		t.Errorf("uninstall = %q, want how to bring it back", out)
	}
	if out := s.mustRun("sync"); exists(filepath.Join(s.home, ".claude", "skills", "loadout-host-setup")) {
		t.Errorf("sync added an uninstalled host skill: %q", out)
	}
	s.mustRun("setup", "--host-skills")
	if !exists(filepath.Join(s.home, ".claude", "skills", "loadout-host-setup", "SKILL.md")) {
		t.Error("setup --host-skills did not bring the skill back")
	}
}

// TestInstallKeepsLoadoutSkillsInStep: install brings loadout's own skills
// back in step, as sync does, so a person who only installs a package (a
// pipeline, say) also gets the skills that explain it.
func TestInstallKeepsLoadoutSkillsInStep(t *testing.T) {
	_, base, alice := accountRegistry(t)
	p := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, p, alice, "--remote", base); code != 0 {
		t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
	}
	// The lock says an older CLI installed loadout-author: another digest.
	lockPath := filepath.Join(p.home, ".loadout", "lock.yaml")
	old := regexp.MustCompile(`(id: loadout/loadout-author\n\s+version: \S+\n\s+digest: )\S+`)
	lockFile := readFile(t, lockPath)
	if !old.MatchString(lockFile) {
		t.Fatalf("no loadout-author entry in the lock:\n%s", lockFile)
	}
	const stale = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if err := os.WriteFile(lockPath, []byte(old.ReplaceAllString(lockFile, "${1}"+stale)), 0o600); err != nil {
		t.Fatal(err)
	}
	out := p.must("", "install", "lime/planner")
	if strings.Contains(readFile(t, lockPath), stale) {
		t.Errorf("install did not bring loadout-author in step: %q", out)
	}
}
