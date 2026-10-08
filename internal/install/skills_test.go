package install

import (
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/skill"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
)

// skillPack returns ns/notes, a skill with the given supporting files. Its
// instructions name ns, so a test can tell whose SKILL.md is on disk.
func skillPack(t *testing.T, ns, version string, files ...string) *pack.Pack {
	t.Helper()
	all := map[string][]byte{
		pack.ManifestFile: []byte("name: " + ns + "/notes\nkind: skill\nversion: " + version + "\ndescription: d\n"),
		skill.File:        []byte("---\nname: notes\ndescription: Take notes.\n---\nfrom " + ns + "\n"),
	}
	for _, name := range files {
		all[name] = []byte(name + " from " + ns + "\n")
	}
	p, err := pack.FromFiles(all)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Lstat(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// checkOnDisk fails for every path whose existence is not as wanted.
func (f fixture) checkOnDisk(t *testing.T, want map[string]bool) {
	t.Helper()
	for _, p := range slices.Sorted(maps.Keys(want)) {
		if got := exists(t, f.path(p)); got != want[p] {
			t.Errorf("%s exists = %v, want %v", p, got, want[p])
		}
	}
}

func TestInstallSkillWritesTheFolder(t *testing.T) {
	f := newFixture(t)
	p := skillPack(t, "lime", "1.0.0", "references/format.md", "scripts/run.sh")
	res, err := f.in.Install(t.Context(), p, Options{Scope: f.scope})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	for _, tool := range []string{"alpha", "beta"} {
		dir := tool + "/skills/notes/"
		if got := readFile(t, f.path(dir+skill.File)); !strings.Contains(got, "from lime") || !strings.Contains(got, "managed by loadout: lime/notes@1.0.0") {
			t.Errorf("%sSKILL.md = %q, want the skill with its note", dir, got)
		}
		if got := readFile(t, f.path(dir+"references/format.md")); got != "references/format.md from lime\n" {
			t.Errorf("%sreferences/format.md = %q", dir, got)
		}
	}
	if len(res.Entry.Files) != 6 {
		t.Fatalf("Entry.Files = %+v, want 3 files for each of 2 targets", res.Entry.Files)
	}
	for _, rec := range res.Entry.Files {
		want := rec.Target + "/skills/notes"
		if rec.Unit != want || !strings.HasPrefix(rec.Path, want+"/") {
			t.Errorf("record %+v, want unit %q", rec, want)
		}
	}
}

func TestAgentFilesHaveNoUnit(t *testing.T) {
	f := newFixture(t)
	res, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range res.Entry.Files {
		if rec.Unit != "" {
			t.Errorf("agent file record %+v has a unit", rec)
		}
	}
}

func TestProjectSkillIsWrittenOnceForBothTools(t *testing.T) {
	locks := &memLocks{}
	in := New(locks, slog.New(slog.DiscardHandler), claudecode.New(), copilot.New())
	scope := target.Scope{Kind: target.ScopeProject, Root: t.TempDir()}
	res, err := in.Install(t.Context(), skillPack(t, "lime", "1.0.0", "references/format.md"), Options{Scope: scope})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	var got []string
	for _, rec := range res.Entry.Files {
		got = append(got, rec.Target+" "+rec.Path)
		if rec.Unit != ".claude/skills/notes" {
			t.Errorf("record %+v, want unit .claude/skills/notes", rec)
		}
	}
	want := []string{"claude,copilot .claude/skills/notes/SKILL.md", "claude,copilot .claude/skills/notes/references/format.md"}
	if !slices.Equal(got, want) {
		t.Errorf("Entry.Files = %v, want %v", got, want)
	}
	if got := res.Entry.Targets(); !slices.Equal(got, []string{"claude", "copilot"}) {
		t.Errorf("Entry.Targets() = %v, want [claude copilot]", got)
	}
	if exists(t, scope.Root+"/.github/skills") || exists(t, scope.Root+"/.copilot") {
		t.Error("a second copy of the skill was written for Copilot")
	}

	// A reinstall recognises the shared files as intact.
	res, err = in.Install(t.Context(), skillPack(t, "lime", "1.0.0", "references/format.md"), Options{Scope: scope})
	if err != nil || !res.Unchanged {
		t.Errorf("reinstall = %+v, %v; want unchanged", res, err)
	}
	statuses, err := in.Status(t.Context())
	if err != nil || len(statuses) != 1 || statuses[0].State() != StateOK {
		t.Errorf("Status() = %+v, %v; want one intact install", statuses, err)
	}

	// Uninstalling removes the one shared folder.
	if _, err := in.Uninstall(t.Context(), res.Entry.ID, "", false); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}
	if exists(t, scope.Root+"/.claude/skills/notes") {
		t.Error("skill folder left after uninstall")
	}
}

func TestSkillUpgradeRemovesDroppedFiles(t *testing.T) {
	f := newFixture(t)
	install := func(p *pack.Pack) {
		t.Helper()
		if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
			t.Fatalf("Install(%s) error: %v", p.Version(), err)
		}
	}
	install(skillPack(t, "lime", "1.0.0", "references/deep/a.md", "references/b.md", "scripts/run.sh"))

	install(skillPack(t, "lime", "1.1.0", "references/b.md", "scripts/run.sh"))
	f.checkOnDisk(t, map[string]bool{
		"alpha/skills/notes/references/deep/a.md": false,
		"alpha/skills/notes/references/deep":      false,
		"alpha/skills/notes/references/b.md":      true,
		"beta/skills/notes/references/deep":       false,
	})

	install(skillPack(t, "lime", "1.2.0"))
	f.checkOnDisk(t, map[string]bool{
		"alpha/skills/notes/references": false,
		"alpha/skills/notes/scripts":    false,
		"alpha/skills/notes/SKILL.md":   true,
		"beta/skills/notes/references":  false,
	})
	if e := f.entry(t, skillPack(t, "lime", "1.2.0").ID()); len(e.Files) != 2 {
		t.Errorf("entry files = %v, want the two SKILL.md files", filePaths(e))
	}
}

func TestSkillUpgradeKeepsAnEditedDroppedFile(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), skillPack(t, "lime", "1.0.0", "references/deep/a.md"), Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	edited := f.path("alpha/skills/notes/references/deep/a.md")
	if err := os.WriteFile(edited, []byte("my notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(t.Context(), skillPack(t, "lime", "1.1.0"), Options{Scope: f.scope}); err != nil {
		t.Fatalf("Install(1.1.0) error: %v", err)
	}
	if got := readFile(t, edited); got != "my notes" {
		t.Errorf("edited file = %q, want it kept", got)
	}
	f.checkOnDisk(t, map[string]bool{"beta/skills/notes/references": false})
}

func TestUninstallSkill(t *testing.T) {
	f := newFixture(t)
	p := skillPack(t, "lime", "1.0.0", "references/deep/a.md", "scripts/run.sh")
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	edited := f.path("beta/skills/notes/scripts/run.sh")
	if err := os.WriteFile(edited, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := f.in.Uninstall(t.Context(), p.ID(), "", false)
	if err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}
	if len(removed) != 1 || !slices.Equal(removed[0].Kept, []string{edited}) {
		t.Errorf("Uninstall() = %+v, want the edited file kept", removed)
	}
	f.checkOnDisk(t, map[string]bool{
		"alpha/skills/notes":               false,
		"alpha/skills":                     true,
		"beta/skills/notes/scripts/run.sh": true,
		"beta/skills/notes/SKILL.md":       false,
		"beta/skills/notes/references":     false,
	})
}

func TestUnmanagedSkillFileConflicts(t *testing.T) {
	f := newFixture(t)
	mine := f.path("alpha/skills/notes/SKILL.md")
	if err := os.MkdirAll(f.path("alpha/skills/notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("my own skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := skillPack(t, "lime", "1.0.0", "references/a.md")
	_, err := f.in.Install(t.Context(), p, Options{Scope: f.scope})
	ce, ok := errors.AsType[*ConflictError](err)
	if !ok || len(ce.Conflicts) != 1 || ce.Conflicts[0].Reason != ReasonUnmanaged || ce.Conflicts[0].Path != mine {
		t.Fatalf("Install() error = %v, want one unmanaged conflict on %s", err, mine)
	}
	f.checkOnDisk(t, map[string]bool{"alpha/skills/notes/references": false, "beta/skills/notes": false})
	if got := readFile(t, mine); got != "my own skill" {
		t.Errorf("SKILL.md = %q, want it untouched", got)
	}

	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope, Force: true}); err != nil {
		t.Fatalf("Install(Force) error: %v", err)
	}
	if got := readFile(t, mine); !strings.Contains(got, "from lime") {
		t.Errorf("after --force SKILL.md = %q, want the package's", got)
	}
}

func TestSameLayerSkillConflicts(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), skillPack(t, "lime", "1.0.0"), Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	_, err := f.in.Install(t.Context(), skillPack(t, "acme", "1.0.0"), Options{Scope: f.scope, Force: true})
	ce, ok := errors.AsType[*ConflictError](err)
	if !ok || ce.Conflicts[0].Reason != ReasonOtherPackage {
		t.Fatalf("Install(acme) error = %v, want another package's files, even with --force", err)
	}
}

func TestCloserSkillShadowsTheWholeFolder(t *testing.T) {
	f := newFixture(t)
	lime := skillPack(t, "lime", "1.0.0", "references/shared.md")
	acme := skillPack(t, "acme", "1.0.0", "references/shared.md", "references/acme-only.md", "scripts/acme.sh")
	if _, err := f.in.Install(t.Context(), lime, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	res, err := f.in.Install(t.Context(), acme, Options{Scope: f.scope, Depth: 1})
	if err != nil {
		t.Fatalf("Install(acme) error: %v", err)
	}
	if !res.Entry.FullyShadowed() || !slices.Equal(res.ShadowedBy(), []pack.ID{lime.ID()}) {
		t.Errorf("Install(acme) entry = %+v, want fully shadowed by lime/notes", res.Entry)
	}
	if got := len(res.Entry.Shadowed); got != 8 {
		t.Errorf("acme has %d shadowed files, want all 4 files for 2 targets", got)
	}
	f.checkOnDisk(t, map[string]bool{
		"alpha/skills/notes/references/acme-only.md": false,
		"alpha/skills/notes/scripts":                 false,
		"beta/skills/notes/references/acme-only.md":  false,
	})
	if got := readFile(t, f.path("alpha/skills/notes/references/shared.md")); !strings.Contains(got, "from lime") {
		t.Errorf("shared.md = %q, want lime's", got)
	}
}

func TestCloserSkillTakesTheFolderOver(t *testing.T) {
	f := newFixture(t)
	acme := skillPack(t, "acme", "1.0.0", "references/shared.md", "references/acme-only.md", "scripts/acme.sh")
	lime := skillPack(t, "lime", "1.0.0", "references/shared.md", "references/lime-only.md")
	if _, err := f.in.Install(t.Context(), acme, Options{Scope: f.scope, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	edited := f.path("beta/skills/notes/scripts/acme.sh")
	if err := os.WriteFile(edited, []byte("tweaked"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := f.in.Install(t.Context(), lime, Options{Scope: f.scope})
	if err != nil {
		t.Fatalf("Install(lime) error: %v", err)
	}
	if !slices.Equal(res.TookOver, []pack.ID{acme.ID()}) {
		t.Errorf("TookOver = %v, want acme/notes", res.TookOver)
	}
	f.checkOnDisk(t, map[string]bool{
		"alpha/skills/notes/references/acme-only.md": false,
		"alpha/skills/notes/scripts":                 false,
		"alpha/skills/notes/references/lime-only.md": true,
		"beta/skills/notes/references/acme-only.md":  false,
		"beta/skills/notes/scripts/acme.sh":          true, // edited, so kept
	})
	for _, p := range []string{"alpha/skills/notes/SKILL.md", "alpha/skills/notes/references/shared.md"} {
		if got := readFile(t, f.path(p)); !strings.Contains(got, "from lime") {
			t.Errorf("%s = %q, want lime's", p, got)
		}
	}
	ae := f.entry(t, acme.ID())
	if len(ae.Files) != 0 || len(ae.Shadowed) != 8 {
		t.Errorf("acme entry files %v, shadowed %v; want every file shadowed", filePaths(ae), shadowPaths(ae))
	}

	// With the closer skill gone, the farther one is missing until it is
	// installed again, and then it comes back whole.
	removed, err := f.in.Uninstall(t.Context(), lime.ID(), "", false)
	if err != nil || !slices.Equal(removed[0].Unshadowed, []pack.ID{acme.ID()}) {
		t.Fatalf("Uninstall(lime) = %+v, %v; want acme unshadowed", removed, err)
	}
	if err := os.Remove(edited); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(t.Context(), acme, Options{Scope: f.scope, Depth: 1}); err != nil {
		t.Fatalf("reinstall acme error: %v", err)
	}
	f.checkOnDisk(t, map[string]bool{
		"alpha/skills/notes/references/acme-only.md": true,
		"alpha/skills/notes/references/lime-only.md": false,
		"beta/skills/notes/scripts/acme.sh":          true,
	})
	if e := f.entry(t, acme.ID()); len(e.Files) != 8 || len(e.Shadowed) != 0 {
		t.Errorf("acme entry = %+v, want every file back", e)
	}
}

func TestCloserSkillForOneTargetLeavesTheOther(t *testing.T) {
	f := newFixture(t)
	acme := skillPack(t, "acme", "1.0.0", "references/acme-only.md")
	if _, err := f.in.Install(t.Context(), acme, Options{Scope: f.scope, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	lime := skillPack(t, "lime", "1.0.0")
	if _, err := f.in.Install(t.Context(), lime, Options{Scope: f.scope, Targets: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	f.checkOnDisk(t, map[string]bool{
		"alpha/skills/notes/references":             false,
		"beta/skills/notes/references/acme-only.md": true,
	})
	ae := f.entry(t, acme.ID())
	want := []string{"beta/skills/notes/SKILL.md", "beta/skills/notes/references/acme-only.md"}
	if got := filePaths(ae); !slices.Equal(got, want) {
		t.Errorf("acme files = %v, want %v", got, want)
	}
	for _, s := range ae.Shadowed {
		if s.Target != "alpha" || s.By != lime.ID() {
			t.Errorf("acme shadow %+v, want alpha files shadowed by lime", s)
		}
	}
	if got := ae.Targets(); !slices.Equal(got, []string{"beta", "alpha"}) {
		t.Errorf("acme Targets() = %v, want beta then alpha", got)
	}
}

func TestStatusOfAShadowedSkill(t *testing.T) {
	tests := []struct {
		name        string
		closerFirst bool
	}{
		{"closer installed first", true},
		{"closer took the folder over", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			lime := skillPack(t, "lime", "1.0.0")
			acme := skillPack(t, "acme", "1.0.0", "references/acme-only.md")
			order := []struct {
				p     *pack.Pack
				depth int
			}{{acme, 1}, {lime, 0}}
			if tc.closerFirst {
				slices.Reverse(order)
			}
			for _, o := range order {
				if _, err := f.in.Install(t.Context(), o.p, Options{Scope: f.scope, Depth: o.depth}); err != nil {
					t.Fatalf("Install(%s) error: %v", o.p.ID(), err)
				}
			}
			statuses, err := f.in.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, st := range statuses {
				for _, file := range st.Files {
					if file.State != StateOK && file.State != StateShadowed {
						t.Errorf("status of %s %s = %s, want ok or shadowed", st.Entry.ID, file.Path, file.State)
					}
					if file.State == StateShadowed && file.ShadowedBy != lime.ID() {
						t.Errorf("%s is shadowed by %s, want lime/notes", file.Path, file.ShadowedBy)
					}
				}
			}

			if _, err := f.in.Uninstall(t.Context(), lime.ID(), "", false); err != nil {
				t.Fatal(err)
			}
			statuses, err = f.in.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(statuses) != 1 || statuses[0].State() != StateMissing {
				t.Errorf("status after the closer is gone = %+v, want acme/notes missing", statuses)
			}
		})
	}
}

func TestSkillFileBecomesAFolder(t *testing.T) {
	tests := []struct {
		name          string
		first, second *pack.Pack
		secondDepth   int
	}{
		{"upgrade", skillPack(t, "lime", "1.0.0", "docs"), skillPack(t, "lime", "1.1.0", "docs/a.md"), 0},
		{"folder becomes a file", skillPack(t, "lime", "1.0.0", "docs/a.md"), skillPack(t, "lime", "1.1.0", "docs"), 0},
		{"take over", skillPack(t, "acme", "1.0.0", "docs"), skillPack(t, "lime", "1.0.0", "docs/a.md"), -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.in.Install(t.Context(), tc.first, Options{Scope: f.scope, Depth: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.in.Install(t.Context(), tc.second, Options{Scope: f.scope, Depth: 1 + tc.secondDepth}); err != nil {
				t.Fatalf("Install(%s %s) error: %v", tc.second.ID(), tc.second.Version(), err)
			}
			statuses, err := f.in.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, st := range statuses {
				if st.Entry.ID == tc.second.ID() && st.State() != StateOK {
					t.Errorf("status of %s = %+v, want ok", st.Entry.ID, st.Files)
				}
			}
		})
	}
}

// A skill folder that is a symlink (a checkout linked in by hand) is never
// written through, not even with --force: the files it points at stay as
// they are.
func TestLinkedSkillFolderIsNeverWrittenThrough(t *testing.T) {
	f := newFixture(t)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "SKILL.md"), []byte("the repo's own skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.path("alpha/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, f.path("alpha/skills/notes")); err != nil {
		t.Fatal(err)
	}
	p := skillPack(t, "lime", "1.0.0", "references/a.md")
	for _, force := range []bool{false, true} {
		_, err := f.in.Install(t.Context(), p, Options{Scope: f.scope, Force: force})
		ce, ok := errors.AsType[*ConflictError](err)
		if !ok || !slices.ContainsFunc(ce.Conflicts, func(c Conflict) bool { return c.Reason == ReasonLinkedFolder }) {
			t.Fatalf("Install(force %v) error = %v, want a linked-folder conflict", force, err)
		}
	}
	if got := readFile(t, filepath.Join(repo, "SKILL.md")); got != "the repo's own skill" {
		t.Errorf("the linked SKILL.md = %q, want it untouched", got)
	}
	if exists(t, filepath.Join(repo, "references")) {
		t.Error("install wrote into the linked folder")
	}
}
