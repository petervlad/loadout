package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/skill"
)

func TestSkills(t *testing.T) {
	out := t.TempDir()
	src := filepath.Join("testdata", "skills", "zuul-triage")
	results, err := Skills([]string{src}, Options{Team: "lime", Out: out, Version: semver.New(1, 0, 0)})
	if err != nil {
		t.Fatalf("Skills() error: %v", err)
	}
	r := results[0]
	if r.ID.String() != "lime/zuul-triage" || r.Kind != pack.KindSkill {
		t.Errorf("Skills() = %s %s, want lime/zuul-triage skill", r.ID, r.Kind)
	}
	if diff := cmp.Diff([]string{"kept fields allowed-tools, metadata, compatibility as they are; loadout does not read them"}, r.Notices); diff != "" {
		t.Errorf("Skills() notices mismatch (-want +got):\n%s", diff)
	}
	p, err := pack.Load(r.Dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if p.Version() != semver.New(1, 0, 0) {
		t.Errorf("version = %s, want 1.0.0", p.Version())
	}
	if got, want := p.Manifest().Description, "Find out why a CI build failed, e.g. a POST_FAILURE or a timeout."; got != want {
		t.Errorf("manifest description = %q, want %q", got, want)
	}
	// SKILL.md is copied byte for byte, the hidden file is left behind.
	files := p.Files()
	if want := readTestdata(t, "skills/zuul-triage/SKILL.md"); string(files[skill.File]) != string(want) {
		t.Errorf("SKILL.md =\n%s\nwant it copied as it is:\n%s", files[skill.File], want)
	}
	wantPaths := []string{pack.ManifestFile, skill.File, "references/errors.md"}
	if diff := cmp.Diff(wantPaths, p.Paths()); diff != "" {
		t.Errorf("Paths() mismatch (-want +got):\n%s", diff)
	}
}

func TestSkillsErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{"no SKILL.md", func(t *testing.T, dir string) {
			t.Helper()
			writeTestFile(t, filepath.Join(dir, "README.md"), "# x\n")
		}, "no SKILL.md"},
		{"invalid SKILL.md", func(t *testing.T, dir string) {
			t.Helper()
			writeTestFile(t, filepath.Join(dir, skill.File), "---\nname: a\n---\nx\n")
		}, "description is empty"},
		{"already a package", func(t *testing.T, dir string) {
			t.Helper()
			writeTestFile(t, filepath.Join(dir, skill.File), "---\nname: a\ndescription: d\n---\nx\n")
			writeTestFile(t, filepath.Join(dir, pack.ManifestFile), "name: lime/a\n")
		}, "already a loadout package"},
		{"symlink", func(t *testing.T, dir string) {
			t.Helper()
			writeTestFile(t, filepath.Join(dir, skill.File), "---\nname: a\ndescription: d\n---\nx\n")
			if err := os.Symlink(filepath.Join(dir, skill.File), filepath.Join(dir, "copy.md")); err != nil {
				t.Skipf("cannot create a symlink here: %v", err)
			}
		}, "symbolic link"},
		{"binary file", func(t *testing.T, dir string) {
			t.Helper()
			writeTestFile(t, filepath.Join(dir, skill.File), "---\nname: a\ndescription: d\n---\nx\n")
			writeTestFile(t, filepath.Join(dir, "logo.png"), "\x89PNG\x00")
		}, "not a UTF-8 text file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			out := t.TempDir()
			if _, err := Skills([]string{dir}, Options{Team: "lime", Out: out}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Skills() error = %v, want %q", err, tc.want)
			}
			if entries, _ := os.ReadDir(out); len(entries) != 0 {
				t.Errorf("Skills() wrote %d folders, want none", len(entries))
			}
		})
	}
}

func TestKB(t *testing.T) {
	out := t.TempDir()
	src := filepath.Join("testdata", "kb", "runbooks")
	r, err := KB(src, KBOptions{Options: Options{Team: "lime", Out: out}, Description: "How the team runs its services."})
	if err != nil {
		t.Fatalf("KB() error: %v", err)
	}
	if r.ID.String() != "lime/runbooks" || r.Kind != pack.KindKB || r.Dir != filepath.Join(out, "runbooks") {
		t.Errorf("KB() = %+v", r)
	}
	p, err := pack.Load(r.Dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	b, _ := p.KB()
	if len(b.Docs()) != 2 {
		t.Errorf("KB docs = %v, want 2", b.Docs())
	}

	named, err := KB(src, KBOptions{Options: Options{Team: "lime", Out: out, Name: "ops-runbooks"}, Description: "Runbooks."})
	if err != nil {
		t.Fatalf("KB(--name) error: %v", err)
	}
	if named.ID.String() != "lime/ops-runbooks" {
		t.Errorf("KB(--name) id = %s, want lime/ops-runbooks", named.ID)
	}

	for _, tc := range []struct {
		name string
		opts KBOptions
		want string
	}{
		{"no description", KBOptions{Options: Options{Team: "lime", Out: t.TempDir()}}, "description is empty"},
		{"bad team", KBOptions{Options: Options{Team: "Lime", Out: t.TempDir()}, Description: "d"}, "team name"},
		{"exists", KBOptions{Options: Options{Team: "lime", Out: out}, Description: "d"}, "already exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := KB(src, tc.opts); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("KB() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSummary(t *testing.T) {
	long := strings.Repeat("word ", 60)
	tests := []struct {
		name, in, want string
	}{
		{"one sentence", "Plans work.", "Plans work."},
		{"first of two", "Plans work. Use when asked.", "Plans work."},
		{"folded lines", "Plans\n  the team's\twork. Use when asked.", "Plans the team's work."},
		{"question", "Why is CI red? Use when asked.", "Why is CI red?"},
		{"abbreviation", "Checks files, e.g. YAML. Use when asked.", "Checks files, e.g. YAML."},
		{"no full stop", "Plans work", "Plans work"},
		{"version number", "Runs tool 1.2 checks. More.", "Runs tool 1.2 checks."},
		{"too long", long, strings.TrimSpace(strings.Repeat("word ", 39)) + "…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := summary(tc.in); got != tc.want {
				t.Errorf("summary(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
