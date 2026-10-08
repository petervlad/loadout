package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// useCopilot makes p a Copilot CLI user as well.
func useCopilot(t *testing.T, p *person) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(p.home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // path is inside the test temp dir
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestTeamSharesASkillAndAKnowledgeBase(t *testing.T) {
	alice := &person{session: newSession(t)}
	bob := &person{session: newSession(t)}
	useCopilot(t, bob)

	url := startServer(t, alice)
	publishToken := createToken(t, alice, "publish")
	readToken := createToken(t, alice, "read")
	alice.must(publishToken+"\n", "remote", "add", "office", url, "--token-stdin")
	bob.must(readToken+"\n", "remote", "add", "office", url, "--token-stdin")

	// Alice scaffolds a skill with a supporting file and a KB with a
	// document in a folder, and publishes both.
	if out := alice.must("", "new", "skill", "lime/checklist"); !strings.Contains(out, "SKILL.md") || !strings.Contains(out, "references") {
		t.Errorf("new skill output = %q, want SKILL.md and references/ as next steps", out)
	}
	extra := filepath.Join(alice.wd, "checklist", "references", "extra.md")
	writeFile(t, extra, "# Extra\n\nOnly for big releases.\n")
	alice.must("", "publish", "checklist")

	if out := alice.must("", "new", "kb", "lime/runbooks"); !strings.Contains(out, "documents") {
		t.Errorf("new kb output = %q, want a hint to add documents", out)
	}
	writeFile(t, filepath.Join(alice.wd, "runbooks", "ops", "deploy.md"), "# Deploying the API\n\nRun make deploy.\n")
	alice.must("", "publish", "runbooks")

	list := bob.must("", "list")
	for _, want := range []string{`lime/checklist\s+skill\s`, `lime/runbooks\s+kb\s`} {
		if !regexp.MustCompile(want).MatchString(list) {
			t.Errorf("list does not match %s:\n%s", want, list)
		}
	}

	// Bob installs both; each lands as a skill folder for both his tools.
	bob.must("", "install", "lime/checklist", "--policy", "auto")
	bob.must("", "install", "lime/runbooks")
	for _, tool := range []string{".claude", ".copilot"} {
		skills := filepath.Join(bob.home, tool, "skills")
		main := readFile(t, filepath.Join(skills, "checklist", "SKILL.md"))
		if !strings.Contains(main, "managed by loadout: lime/checklist@0.1.0") {
			t.Errorf("%s skill SKILL.md has no managed-by note:\n%s", tool, main)
		}
		if !exists(filepath.Join(skills, "checklist", "references", "extra.md")) {
			t.Errorf("%s skill is missing references/extra.md", tool)
		}
		index := readFile(t, filepath.Join(skills, "runbooks", "SKILL.md"))
		for _, want := range []string{"managed by loadout: lime/runbooks@0.1.0", "ops/deploy.md", "Deploying the API", "README.md"} {
			if !strings.Contains(index, want) {
				t.Errorf("%s kb index is missing %q:\n%s", tool, want, index)
			}
		}
		if got := readFile(t, filepath.Join(skills, "runbooks", "ops", "deploy.md")); !strings.Contains(got, "Run make deploy.") {
			t.Errorf("%s kb document = %q", tool, got)
		}
	}

	// status shows each skill folder once per tool, and rolls up edits;
	// --files lists every file.
	if err := os.WriteFile(filepath.Join(bob.home, ".claude", "skills", "runbooks", "ops", "deploy.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := bob.must("", "status")
	if n := strings.Count(status, "\n"); n != 5 {
		t.Errorf("status has %d lines, want a header and 4 folder lines:\n%s", n-1, status)
	}
	if !regexp.MustCompile(`lime/runbooks\s.*claude\s+modified \(1 of 3 files\)\s+\S+/\.claude/skills/runbooks\n`).MatchString(status) {
		t.Errorf("status does not roll up the edit:\n%s", status)
	}
	if !regexp.MustCompile(`lime/runbooks\s.*copilot\s+ok\s`).MatchString(status) {
		t.Errorf("status does not show the intact copilot copy as ok:\n%s", status)
	}
	if files := bob.must("", "status", "--files"); !strings.Contains(files, filepath.Join("runbooks", "ops", "deploy.md")) {
		t.Errorf("status --files does not list the files:\n%s", files)
	}
	if err := os.WriteFile(filepath.Join(bob.home, ".claude", "skills", "runbooks", "ops", "deploy.md"), []byte("# Deploying the API\n\nRun make deploy.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Alice drops the supporting file; diff shows it, and Bob's auto sync
	// removes it from both tools.
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	alice.must("", "publish", "checklist", "--bump", "patch")
	diff := alice.must("", "diff", "lime/checklist", "0.1.0", "0.1.1")
	if !strings.Contains(diff, "checklist@0.1.0/references/extra.md") || !strings.Contains(diff, "-Only for big releases.") {
		t.Errorf("diff does not show the removed file:\n%s", diff)
	}
	if out := bob.must("", "sync"); !regexp.MustCompile(`lime/checklist\s.*updated`).MatchString(out) {
		t.Errorf("sync = %q, want lime/checklist updated", out)
	}
	for _, tool := range []string{".claude", ".copilot"} {
		dir := filepath.Join(bob.home, tool, "skills", "checklist")
		if exists(filepath.Join(dir, "references", "extra.md")) {
			t.Errorf("%s: sync kept the removed references/extra.md", tool)
		}
		if !exists(filepath.Join(dir, "references", "README.md")) {
			t.Errorf("%s: sync removed references/README.md, which is still in the package", tool)
		}
		if main := readFile(t, filepath.Join(dir, "SKILL.md")); !strings.Contains(main, "@0.1.1") {
			t.Errorf("%s: SKILL.md is not 0.1.1:\n%s", tool, main)
		}
	}

	// Uninstall removes the whole skill folder.
	bob.must("", "uninstall", "lime/checklist")
	for _, tool := range []string{".claude", ".copilot"} {
		if dir := filepath.Join(bob.home, tool, "skills", "checklist"); exists(dir) {
			t.Errorf("uninstall left %s", dir)
		}
		if !exists(filepath.Join(bob.home, tool, "skills", "runbooks", "SKILL.md")) {
			t.Errorf("uninstalling the skill touched the kb in %s", tool)
		}
	}
}

func TestProjectScopeWritesOneSkillForBothTools(t *testing.T) {
	s := newSession(t)
	s.mustRun("new", "skill", "lime/checklist")
	pkg := filepath.Join(s.wd, "checklist")

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.wd = repo
	out := s.mustRun("install", pkg, "--scope", "project", "--target", "claude,copilot")

	dir := filepath.Join(repo, ".claude", "skills", "checklist")
	if !strings.Contains(out, dir) {
		t.Errorf("install output = %q, want the skill folder %s", out, dir)
	}
	if main := readFile(t, filepath.Join(dir, "SKILL.md")); !strings.Contains(main, "managed by loadout: lime/checklist@0.1.0") {
		t.Errorf("project SKILL.md has no managed-by note:\n%s", main)
	}
	for _, other := range []string{".github", ".copilot", ".agents"} {
		if exists(filepath.Join(repo, other)) {
			t.Errorf("project install wrote a second copy under %s", other)
		}
	}
	if status := s.mustRun("status"); !strings.Contains(status, "claude,copilot") {
		t.Errorf("status = %q, want the files shared by claude,copilot", status)
	}
}
