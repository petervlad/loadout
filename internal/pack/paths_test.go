package pack

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/skill"
)

func TestCheckPath(t *testing.T) {
	deepest := strings.Repeat("d/", MaxDepth) + "f.md"
	tests := []struct {
		path string
		want string // empty for a good path
	}{
		{"SKILL.md", ""},
		{"references/format.md", ""},
		{"a-b_c.d/E.F-g_1.txt", ""},
		{deepest, ""},
		{strings.Repeat("n", 128), ""},
		{"", "empty file path"},
		{"/abs.md", "empty path segment"},
		{"dir/", "empty path segment"},
		{"a//b.md", "empty path segment"},
		{".", "leaves the package"},
		{"..", "leaves the package"},
		{"a/../b.md", "leaves the package"},
		{"a/./b.md", "leaves the package"},
		{"../escape.md", "leaves the package"},
		{".hidden", "hidden"},
		{"a/.git/config", "hidden"},
		{"d/" + deepest, "folders deep"},
		{strings.Repeat("n", 129), "longer than 128"},
		{"a b.md", "only letters"},
		{`a\b.md`, "only letters"},
		{"C:/x.md", "only letters"},
		{"caf\u00e9.md", "only letters"},
		{"a\x00b", "only letters"},
		{"a:b", "only letters"},
		{"~/x", "only letters"},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.path), func(t *testing.T) {
			err := CheckPath(tc.path)
			if tc.want == "" {
				if err != nil {
					t.Errorf("CheckPath() error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("CheckPath() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCheckFiles(t *testing.T) {
	tests := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{name: "good", files: map[string][]byte{"a.md": []byte("x"), "b/c.md": []byte("\u00e9 ok")}},
		{name: "empty file", files: map[string][]byte{"a.md": nil}},
		{name: "case only", files: map[string][]byte{"README.md": nil, "readme.md": nil}, want: "differ only in case"},
		{name: "case only in a folder", files: map[string][]byte{"Docs/a.md": nil, "docs/a.md": nil}, want: "differ only in case"},
		{name: "file where a folder goes", files: map[string][]byte{"docs": nil, "docs/a.md": nil}, want: "docs is a file, but docs/a.md puts a folder in its place"},
		{name: "file where a deeper folder goes", files: map[string][]byte{"a/b": nil, "a/b/c/d.md": nil}, want: "a/b is a file"},
		{name: "file where a folder goes, other case", files: map[string][]byte{"Docs": nil, "docs/a.md": nil}, want: "Docs is a file"},
		{name: "nul byte", files: map[string][]byte{"a.md": []byte("a\x00b")}, want: "not a UTF-8 text file"},
		{name: "invalid utf-8", files: map[string][]byte{"a.md": {0xff, 0xfe, 'a'}}, want: "not a UTF-8 text file"},
		{name: "binary", files: map[string][]byte{"logo.png": {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0}}, want: "not a UTF-8 text file"},
		{name: "bad path", files: map[string][]byte{"../a.md": nil}, want: "leaves the package"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFiles(tc.files)
			if tc.want == "" {
				if err != nil {
					t.Errorf("checkFiles() error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("checkFiles() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLimitsCheck(t *testing.T) {
	l := Limits{MaxFiles: 3, MaxFileSize: 10, MaxTotal: 15}
	tests := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"within", map[string][]byte{"a.md": make([]byte, 10), "b.md": make([]byte, 5)}, ""},
		{"too many files", manyFiles(4), "4 files, the limit for a test is 3"},
		{"file too large", map[string][]byte{"a.md": make([]byte, 11)}, "a.md is 11 bytes, the limit per file for a test is 10"},
		{"too much in all", map[string][]byte{"a.md": make([]byte, 10), "b.md": make([]byte, 6)}, "16 bytes in all, the limit for a test is 15"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := l.check(tc.files, "a test")
			if tc.want == "" {
				if err != nil {
					t.Errorf("check() error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("check() error = %v, want ErrTooLarge with %q", err, tc.want)
			}
		})
	}
}

func TestKindLimits(t *testing.T) {
	agentMain := "---\nname: planner\ndescription: d\n---\n"
	agentManifest := "name: lime/planner\nkind: agent\nversion: 0.1.0\ndescription: d\n"
	skillFiles := func(extra map[string][]byte) map[string][]byte {
		files := map[string][]byte{ManifestFile: []byte(skillManifest), skill.File: []byte(skillMain)}
		maps.Copy(files, extra)
		return files
	}
	docs := func(n int) map[string][]byte {
		out := make(map[string][]byte, n)
		for i := range n {
			out[fmt.Sprintf("references/%02d/doc%d.md", i%50, i)] = []byte("# Doc\n")
		}
		return out
	}
	tests := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"small agent", map[string][]byte{ManifestFile: []byte(agentManifest), AgentFile: []byte(agentMain + "p\n")}, ""},
		{
			"agent over 1 MB",
			map[string][]byte{ManifestFile: []byte(agentManifest), AgentFile: []byte(agentMain + strings.Repeat("p", 1<<20))},
			"the limit for an agent is 1048576",
		},
		{"skill of 3,000 small files", skillFiles(docs(3000)), ""},
		{"skill file over 2 MB", skillFiles(map[string][]byte{"references/big.md": []byte(strings.Repeat("x", 2<<20+1))}), "the limit per file for a skill is 2097152"},
		{"skill over 5,000 files", skillFiles(docs(5000)), "5002 files, the limit for a skill is 5000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromFiles(tc.files)
			if tc.want == "" {
				if err != nil {
					t.Errorf("FromFiles() error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("FromFiles() error = %v, want ErrTooLarge with %q", err, tc.want)
			}
		})
	}
}

func TestMaxLimitsCoverEveryKind(t *testing.T) {
	m := MaxLimits()
	for _, k := range Kinds() {
		l := k.Limits()
		if l.MaxFiles == 0 || l.MaxFiles > m.MaxFiles || l.MaxFileSize > m.MaxFileSize || l.MaxTotal > m.MaxTotal {
			t.Errorf("%s limits = %+v, want non-zero and within MaxLimits %+v", k, l, m)
		}
	}
}

func manyFiles(n int) map[string][]byte {
	out := make(map[string][]byte, n)
	for i := range n {
		out[fmt.Sprintf("f%d.md", i)] = nil
	}
	return out
}
