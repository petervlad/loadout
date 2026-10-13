package install

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
)

// An older setup left a linked skill folder, an unmanaged agent file and a
// twin in ~/.agents. Without Adopt the install stops; with it they are moved
// to the backup, the link's target is untouched and the package installs.
func TestAdoptMovesOldSetupAside(t *testing.T) {
	home, root, repo := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(repo, "SKILL.md"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".claude/skills", ".copilot/skills", ".agents/skills"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(repo, filepath.Join(root, dir, "notes")); err != nil {
			t.Fatal(err)
		}
	}
	in := New(&memLocks{}, slog.New(slog.DiscardHandler), claudecode.New(), copilot.New())
	scope := target.Scope{Kind: target.ScopeUser, Root: root}
	p := skillPack(t, "lime", "1.0.0")

	if _, err := in.Install(t.Context(), p, Options{Scope: scope}); err == nil {
		t.Fatal("Install() without Adopt succeeded over a linked folder")
	}
	if _, err := in.Install(t.Context(), p, Options{Scope: scope, Adopt: true}); err != nil {
		t.Fatalf("Install() with Adopt error: %v", err)
	}

	if got := readFile(t, filepath.Join(repo, "SKILL.md")); got != "old\n" {
		t.Errorf("the link's target was changed: %q", got)
	}
	for _, dir := range []string{".claude/skills", ".copilot/skills"} {
		got := readFile(t, filepath.Join(root, dir, "notes", "SKILL.md"))
		if !strings.Contains(got, "from lime") {
			t.Errorf("%s/notes/SKILL.md = %q, want the installed skill", dir, got)
		}
	}
	if exists(t, filepath.Join(root, ".agents/skills/notes")) {
		t.Error("the ~/.agents twin is still there, Copilot would list the skill twice")
	}
	backups, err := filepath.Glob(filepath.Join(home, ".loadout", "backup", "*", ".agents", "skills", "notes"))
	if err != nil || len(backups) != 1 {
		t.Errorf("backup of the ~/.agents twin = %v, %v, want one", backups, err)
	}
}
