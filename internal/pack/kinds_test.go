package pack

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/skill"
)

const (
	skillManifest = "name: lime/release-notes\nkind: skill\nversion: 0.1.0\ndescription: Writes release notes.\n"
	skillMain     = "---\nname: release-notes\ndescription: Write release notes. Use when asked for them.\n---\nCollect the merged PRs.\n"
	kbManifest    = "name: lime/ops-kb\nkind: kb\nversion: 0.1.0\ndescription: How the team runs production.\n"
)

// writeTree writes files, which may sit in folders, into a new folder.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fromStrings(t *testing.T, files map[string]string) (*Pack, error) {
	t.Helper()
	out := make(map[string][]byte, len(files))
	for k, v := range files {
		out[k] = []byte(v)
	}
	return FromFiles(out)
}

func TestSkillPackage(t *testing.T) {
	p, err := fromStrings(t, map[string]string{
		ManifestFile:           skillManifest,
		skill.File:             skillMain,
		"references/format.md": "# Format\n",
		"scripts/collect.sh":   "echo hi\n",
	})
	if err != nil {
		t.Fatalf("FromFiles() error: %v", err)
	}
	if p.Kind() != KindSkill {
		t.Errorf("Kind() = %q", p.Kind())
	}
	s, ok := p.Skill()
	if !ok || s.Name != "release-notes" {
		t.Fatalf("Skill() = %+v, %v", s, ok)
	}
	if diff := cmp.Diff(map[string][]byte{"references/format.md": []byte("# Format\n"), "scripts/collect.sh": []byte("echo hi\n")}, s.Files()); diff != "" {
		t.Errorf("Skill().Files() mismatch (-want +got):\n%s", diff)
	}
	if _, ok := p.Agent(); ok {
		t.Error("Agent() ok for a skill package")
	}
	if _, ok := p.KB(); ok {
		t.Error("KB() ok for a skill package")
	}
}

func TestKBPackage(t *testing.T) {
	p, err := fromStrings(t, map[string]string{
		ManifestFile:          kbManifest,
		"README.md":           "# Overview\n",
		"runbooks/restart.md": "# Restart\n",
	})
	if err != nil {
		t.Fatalf("FromFiles() error: %v", err)
	}
	b, ok := p.KB()
	if !ok || b.Name != "ops-kb" || b.Description != "How the team runs production." || len(b.Docs()) != 2 {
		t.Fatalf("KB() = %+v, %v", b, ok)
	}
	s, ok := p.Skill()
	if !ok || s.Name != "ops-kb" || !strings.Contains(s.Body, "runbooks/restart.md") {
		t.Errorf("Skill() = %+v, %v; want the knowledge base as a skill", s, ok)
	}
}

func TestKindErrors(t *testing.T) {
	agentManifest := "name: lime/planner\nkind: agent\nversion: 0.1.0\ndescription: d\n"
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"missing kind", map[string]string{ManifestFile: strings.Replace(testManifest, "kind: agent\n", "", 1), AgentFile: testAgent}, `kind "" is not supported: want agent, skill, kb`},
		{"unknown kind", map[string]string{ManifestFile: strings.Replace(testManifest, "kind: agent", "kind: command", 1), AgentFile: testAgent}, `kind "command"`},
		{"agent with a nested file", map[string]string{ManifestFile: agentManifest, AgentFile: "---\nname: planner\ndescription: d\n---\np\n", "references/a.md": "x"}, `unexpected file "references/a.md"`},
		{"agent as a skill", map[string]string{ManifestFile: strings.Replace(agentManifest, "kind: agent", "kind: skill", 1), AgentFile: "x"}, "missing SKILL.md"},
		{"skill without SKILL.md", map[string]string{ManifestFile: skillManifest, "README.md": "x"}, "missing SKILL.md"},
		{"skill name mismatch", map[string]string{ManifestFile: strings.Replace(skillManifest, "release-notes", "notes", 1), skill.File: skillMain}, "must match"},
		{"invalid skill", map[string]string{ManifestFile: skillManifest, skill.File: "---\nname: release-notes\n---\nb\n"}, "SKILL.md: description is empty"},
		{"skill file twice", map[string]string{ManifestFile: skillManifest, skill.File: skillMain, "skill.md": "x"}, "differ only in case"},
		{"kb with SKILL.md", map[string]string{ManifestFile: kbManifest, skill.File: skillMain}, "writes a knowledge base's SKILL.md"},
		{"kb without documents", map[string]string{ManifestFile: kbManifest}, "at least one document"},
		{"kb with a script", map[string]string{ManifestFile: kbManifest, "a.md": "x", "run.sh": "x"}, "run.sh: not a document type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fromStrings(t, tc.files)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("FromFiles() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestKindNames(t *testing.T) {
	tests := []struct {
		kind     Kind
		valid    bool
		main     string
		describe string
	}{
		{KindAgent, true, AgentFile, "agent"},
		{KindSkill, true, skill.File, "skill"},
		{KindKB, true, "", "KB"},
		{"widget", false, "", "widget"},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			if tc.kind.Valid() != tc.valid || tc.kind.MainFile() != tc.main || tc.kind.Describe() != tc.describe {
				t.Errorf("Valid, MainFile, Describe = %v, %q, %q; want %v, %q, %q", tc.kind.Valid(), tc.kind.MainFile(), tc.kind.Describe(), tc.valid, tc.main, tc.describe)
			}
		})
	}
	for _, k := range Kinds() {
		if !k.Valid() {
			t.Errorf("Kinds() lists %q, which is not valid", k)
		}
	}
}

func TestPathsAndMain(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		wantPaths []string
		wantMain  string
		wantData  string // a substring of the main file
	}{
		{
			name:      "agent",
			files:     map[string]string{ManifestFile: testManifest, AgentFile: testAgent},
			wantPaths: []string{ManifestFile, AgentFile},
			wantMain:  AgentFile,
			wantData:  "You plan.",
		},
		{
			name:      "skill",
			files:     map[string]string{ManifestFile: skillManifest, skill.File: skillMain, "assets/a.txt": "a", "Z.md": "z", "b.md": "b"},
			wantPaths: []string{ManifestFile, skill.File, "Z.md", "assets/a.txt", "b.md"},
			wantMain:  skill.File,
			wantData:  "Collect the merged PRs.",
		},
		{
			name:      "kb",
			files:     map[string]string{ManifestFile: kbManifest, "z.md": "# Z\n", "a/b.md": "# B\n", "README.md": "# R\n"},
			wantPaths: []string{ManifestFile, "README.md", "a/b.md", "z.md"},
			wantMain:  skill.File,
			wantData:  "| [a/b.md](a/b.md) | B |",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := fromStrings(t, tc.files)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.wantPaths, p.Paths()); diff != "" {
				t.Errorf("Paths() mismatch (-want +got):\n%s", diff)
			}
			name, data, err := p.Main()
			if err != nil {
				t.Fatalf("Main() error: %v", err)
			}
			if name != tc.wantMain || !strings.Contains(string(data), tc.wantData) {
				t.Errorf("Main() = %q,\n%s\nwant %q containing %q", name, data, tc.wantMain, tc.wantData)
			}
		})
	}
}

func TestLoadNested(t *testing.T) {
	dir := writeTree(t, map[string]string{
		ManifestFile:              skillManifest,
		skill.File:                skillMain,
		"references/deep/a/b.md":  "b",
		"references/.DS_Store":    "junk",
		".git/config":             "junk",
		".github/workflows/ci.md": "junk",
		".hidden.md":              "junk",
	})
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := []string{ManifestFile, skill.File, "references/deep/a/b.md"}
	if diff := cmp.Diff(want, p.Paths()); diff != "" {
		t.Errorf("Paths() mismatch (-want +got):\n%s", diff)
	}
	fromFiles, err := fromStrings(t, map[string]string{ManifestFile: skillManifest, skill.File: skillMain, "references/deep/a/b.md": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest() != fromFiles.Digest() {
		t.Errorf("Load() digest %s differs from FromFiles() digest %s for the same files", p.Digest(), fromFiles.Digest())
	}
}

func TestLoadRefusesSymlinks(t *testing.T) {
	tests := []struct {
		name string
		link func(t *testing.T, dir string) error
	}{
		{"file", func(_ *testing.T, dir string) error {
			return os.Symlink(filepath.Join(dir, skill.File), filepath.Join(dir, "copy.md"))
		}},
		{"folder", func(t *testing.T, dir string) error {
			t.Helper()
			return os.Symlink(t.TempDir(), filepath.Join(dir, "references"))
		}},
		{"outside", func(t *testing.T, dir string) error {
			t.Helper()
			outside := filepath.Join(t.TempDir(), "secret.md")
			if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
				return err
			}
			return os.Symlink(outside, filepath.Join(dir, "secret.md"))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, map[string]string{ManifestFile: skillManifest, skill.File: skillMain})
			if err := tc.link(t, dir); err != nil {
				t.Skipf("cannot create a symlink here: %v", err)
			}
			if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("Load() error = %v, want the symlink refused", err)
			}
		})
	}
}

func TestReadFilesStopsAtTheLimits(t *testing.T) {
	small := Limits{MaxFiles: 3, MaxFileSize: 10, MaxTotal: 15}
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"within", map[string]string{"a.md": "12345", "b/c.md": "12345"}, ""},
		{"too many files", map[string]string{"a.md": "", "b.md": "", "c/d.md": "", "e.md": ""}, "more than 3 files"},
		{"file too large", map[string]string{"a.md": strings.Repeat("x", 11)}, "a.md is 11 bytes"},
		{"too much in all", map[string]string{"a.md": strings.Repeat("x", 8), "b.md": strings.Repeat("x", 8)}, "over 15 bytes in all"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.OpenRoot(writeTree(t, tc.files))
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			_, err = readFiles(root, small)
			if tc.want == "" {
				if err != nil {
					t.Errorf("readFiles() error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("readFiles() error = %v, want ErrTooLarge with %q", err, tc.want)
			}
		})
	}
}

func TestLoadStopsAtTheLargestLimits(t *testing.T) {
	big := writeTree(t, map[string]string{ManifestFile: kbManifest, "big.md": strings.Repeat("x", MaxLimits().MaxFileSize+1)})
	if _, err := Load(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Load() of a large file: error = %v, want ErrTooLarge", err)
	}
}

func TestScaffoldEveryKind(t *testing.T) {
	id, err := NewID("lime", "helper")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range Kinds() {
		t.Run(string(kind), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "helper")
			made, err := Scaffold(dir, id, kind)
			if err != nil {
				t.Fatalf("Scaffold() error: %v", err)
			}
			p, err := Load(dir)
			if err != nil {
				t.Fatalf("Load(scaffold) error: %v", err)
			}
			if p.Kind() != kind || p.ID() != id || p.Digest() != made.Digest() {
				t.Errorf("Load(scaffold) = %s %s %s, want %s %s %s", p.Kind(), p.ID(), p.Digest(), kind, id, made.Digest())
			}
			if _, _, err := p.Main(); err != nil {
				t.Errorf("Main() error: %v", err)
			}
		})
	}
	if _, err := Scaffold(filepath.Join(t.TempDir(), "x"), id, "widget"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("Scaffold(widget) error = %v, want not supported", err)
	}
}

func TestDigestIsStable(t *testing.T) {
	files := map[string][]byte{"a": []byte("1"), "b/c": []byte("2")}
	want := digestOf(files)
	for range 20 {
		again := map[string][]byte{"b/c": []byte("2"), "a": []byte("1")}
		if got := digestOf(again); got != want {
			t.Fatalf("digestOf() = %s, then %s for the same files", want, got)
		}
	}
	for name, other := range map[string]map[string][]byte{
		"moved to a folder": {"a": []byte("1"), "b/b/c": []byte("2")},
		"path and content":  {"a1": []byte(""), "b/c": []byte("2")},
		"renamed":           {"A": []byte("1"), "b/c": []byte("2")},
	} {
		if digestOf(other) == want {
			t.Errorf("%s: same digest as the original", name)
		}
	}
}
