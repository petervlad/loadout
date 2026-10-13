package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFindLegacy(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".agents/agents/lime-dev.md", "old agent\n")                                     // old copy
	write(".copilot/agents/lime-test.md", "<!-- managed by loadout: lime/lime-test@1 -->") // ours
	write(".agents/agents/unrelated.md", "mine\n")                                         // no such package
	write(".agents/skills/lime-kb/SKILL.md", "old kb\n")                                   // old KB folder
	write(".claude/agents/lime-dev.md", "claude's own\n")                                  // not scanned for copilot
	repo := t.TempDir()
	if err := os.Symlink(repo, filepath.Join(root, ".agents/skills/linked")); err != nil {
		t.Fatal(err)
	}

	wanted := []Wanted{{"lime-dev", true}, {"lime-test", true}, {"lime-kb", false}, {"linked", false}}
	got, err := FindLegacy(root, wanted, []string{"copilot"})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, l := range got {
		rel, _ := filepath.Rel(root, l.Path)
		paths = append(paths, rel)
	}
	want := []string{".agents/agents/lime-dev.md", ".agents/skills/lime-kb", ".agents/skills/linked"}
	if !slices.Equal(paths, want) {
		t.Errorf("FindLegacy(copilot) = %v, want %v", paths, want)
	}
	if got[2].LinkTo != repo {
		t.Errorf("link target = %q, want %q", got[2].LinkTo, repo)
	}

	got, err = FindLegacy(root, wanted, []string{"claude"})
	if err != nil || len(got) != 1 || filepath.Base(got[0].Path) != "lime-dev.md" {
		t.Errorf("FindLegacy(claude) = %v, %v, want only .claude/agents/lime-dev.md", got, err)
	}

	got, err = FindLegacy(root, wanted, []string{"vscode"})
	if err != nil || len(got) != 4 {
		t.Errorf("FindLegacy(vscode) = %v, %v, want the copilot three and .claude/agents/lime-dev.md", got, err)
	}
}

func TestFindDuplicatesKeepsOneCopy(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const ours = "<!-- managed by loadout: lime/x@1 -->"
	// only old copies, in three folders: keep the first (~/.copilot)
	write(".copilot/agents/ops-dev.md", "old")
	write(".agents/agents/ops-dev.md", "old")
	write(".claude/agents/ops-dev.md", "old")
	// a loadout copy beats an old one, whatever the folder
	write(".copilot/agents/lime-dev.md", "old")
	write(".claude/agents/lime-dev.md", ours)
	// loadout's own copy in two tools' folders is not touched
	write(".copilot/agents/lime-docs.md", ours)
	write(".claude/agents/lime-docs.md", ours)
	// a name that exists once is left alone
	write(".agents/skills/solo/SKILL.md", "mine")
	// a skill in two folders
	write(".copilot/skills/ci-api/SKILL.md", "old")
	write(".agents/skills/ci-api/SKILL.md", "old")

	got, err := FindDuplicates(root, []string{"vscode"})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, l := range got {
		rel, _ := filepath.Rel(root, l.Path)
		paths = append(paths, rel)
	}
	want := []string{
		".agents/agents/ops-dev.md", ".agents/skills/ci-api",
		".claude/agents/ops-dev.md", ".copilot/agents/lime-dev.md",
	}
	if !slices.Equal(paths, want) {
		t.Errorf("FindDuplicates(vscode) = %v, want %v", paths, want)
	}
}
