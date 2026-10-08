package pack

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/skill"
)

const (
	testManifest = "name: lime/planner\nkind: agent\nversion: 1.3.0\ndescription: Plans tickets.\ntargets: [claude]\n"
	testAgent    = "---\nname: planner\ndescription: Plans tickets.\ntools: [read]\n---\nYou plan.\n"
)

func writePackage(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoad(t *testing.T) {
	dir := writePackage(t, map[string]string{ManifestFile: testManifest, AgentFile: testAgent, ".DS_Store": "x"})
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got := p.ID().String(); got != "lime/planner" {
		t.Errorf("ID() = %q, want %q", got, "lime/planner")
	}
	if got := p.Version().String(); got != "1.3.0" {
		t.Errorf("Version() = %q, want %q", got, "1.3.0")
	}
	if def, _ := p.Agent(); def.Name != "planner" {
		t.Errorf("Agent().Name = %q, want %q", def.Name, "planner")
	}
	if !strings.HasPrefix(string(p.Digest()), "sha256:") {
		t.Errorf("Digest() = %q, want sha256: prefix", p.Digest())
	}
	if _, ok := p.Files()[".DS_Store"]; ok {
		t.Error("Files() includes a hidden file")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantMsg string
	}{
		{"missing manifest", map[string]string{AgentFile: testAgent}, "missing loadout.yaml"},
		{"missing agent", map[string]string{ManifestFile: testManifest}, "missing agent.md"},
		{"extra file", map[string]string{ManifestFile: testManifest, AgentFile: testAgent, "notes.txt": "x"}, "unexpected file"},
		{"name mismatch", map[string]string{ManifestFile: strings.Replace(testManifest, "planner", "reviewer", 1), AgentFile: testAgent}, "must match"},
		{"bad id", map[string]string{ManifestFile: strings.Replace(testManifest, "lime/planner", "planner", 1), AgentFile: testAgent}, "team/name"},
		{"bad version", map[string]string{ManifestFile: strings.Replace(testManifest, "1.3.0", "1.3", 1), AgentFile: testAgent}, "MAJOR.MINOR.PATCH"},
		{"wrong kind", map[string]string{ManifestFile: strings.Replace(testManifest, "kind: agent", "kind: widget", 1), AgentFile: testAgent}, "not supported"},
		{"unknown field", map[string]string{ManifestFile: testManifest + "owner: bob\n", AgentFile: testAgent}, "owner"},
		{"empty manifest", map[string]string{ManifestFile: "", AgentFile: testAgent}, "empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writePackage(t, tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("Load() error = %v, want it to contain %q", err, tc.wantMsg)
			}
		})
	}
}

func TestDigestIsContentBased(t *testing.T) {
	a, err := Load(writePackage(t, map[string]string{ManifestFile: testManifest, AgentFile: testAgent}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(writePackage(t, map[string]string{ManifestFile: testManifest, AgentFile: testAgent}))
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() != b.Digest() {
		t.Errorf("same content, different digests: %s vs %s", a.Digest(), b.Digest())
	}
	c, err := Load(writePackage(t, map[string]string{ManifestFile: testManifest, AgentFile: testAgent + "More.\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() == c.Digest() {
		t.Error("different content, same digest")
	}
}

func TestPackIsImmutable(t *testing.T) {
	p, err := Load(writePackage(t, map[string]string{ManifestFile: testManifest, AgentFile: testAgent}))
	if err != nil {
		t.Fatal(err)
	}
	p.Files()[AgentFile][0] = 'X'
	def, _ := p.Agent()
	def.Tools[0] = "changed"
	p.Manifest().Targets[0] = "changed"
	again, _ := p.Agent()
	if p.Files()[AgentFile][0] == 'X' || again.Tools[0] == "changed" || p.Manifest().Targets[0] == "changed" {
		t.Error("mutating a returned value changed the Pack")
	}
}

func TestScaffold(t *testing.T) {
	id, err := NewID("lime", "planner")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "planner")
	if _, err := Scaffold(dir, id, KindAgent); err != nil {
		t.Fatalf("Scaffold() error: %v", err)
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(scaffold) error: %v", err)
	}
	if p.ID() != id {
		t.Errorf("scaffold ID = %s, want %s", p.ID(), id)
	}
	if _, err := Scaffold(dir, id, KindAgent); !errors.Is(err, ErrExists) {
		t.Errorf("second Scaffold() error = %v, want ErrExists", err)
	}
}

func TestCreate(t *testing.T) {
	files := map[string][]byte{
		ManifestFile:            []byte(skillManifest),
		skill.File:              []byte(skillMain),
		"references/details.md": []byte("# Details\n"),
	}
	dir := filepath.Join(t.TempDir(), "release-notes")
	p, err := Create(dir, files)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if diff := cmp.Diff(files, p.Files()); diff != "" {
		t.Errorf("Create() files mismatch (-want +got):\n%s", diff)
	}
	if _, err := Create(dir, files); !errors.Is(err, ErrExists) {
		t.Errorf("second Create() error = %v, want ErrExists", err)
	}

	// An invalid package is refused before anything is written.
	bad := filepath.Join(t.TempDir(), "bad")
	if _, err := Create(bad, map[string][]byte{ManifestFile: []byte(skillManifest)}); err == nil {
		t.Error("Create(no SKILL.md) error = nil")
	}
	if _, err := os.Stat(bad); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Create(invalid) left %s behind: %v", bad, err)
	}
}

func TestParseID(t *testing.T) {
	for _, bad := range []string{"planner", "lime/", "/planner", "Lime/planner", "lime/plan/ner"} {
		if _, err := ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) = nil error, want error", bad)
		}
	}
}

func TestSetVersionKeepsComments(t *testing.T) {
	dir := writePackage(t, map[string]string{
		ManifestFile: "# owned by lime\nname: lime/planner\nkind: agent\nversion: 1.3.0 # bumped by publish\ndescription: Plans tickets.\n",
		AgentFile:    testAgent,
	})
	if err := SetVersion(dir, semver.New(1, 4, 0)); err != nil {
		t.Fatalf("SetVersion() error: %v", err)
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() after SetVersion error: %v", err)
	}
	if got := p.Version().String(); got != "1.4.0" {
		t.Errorf("version = %s, want 1.4.0", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# owned by lime") {
		t.Errorf("comment lost:\n%s", data)
	}
	info, err := os.Stat(filepath.Join(dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("manifest perm = %v, want 0644", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("folder holds %d entries, want 2 (temp file left behind)", len(entries))
	}
}

func TestDrawioMustBeUncompressed(t *testing.T) {
	plain := `<mxfile><diagram id="a" name="Page-1"><mxGraphModel><root><mxCell id="0"/></root></mxGraphModel></diagram></mxfile>`
	compressed := `<mxfile><diagram id="a" name="Page-1">7VhNj5swEP01HLcCzEdyTLLp9lBpq+bQ3l0iY9WAGTkh+fs1wQ6</diagram></mxfile>`
	for _, tc := range []struct {
		name, data string
		wantErr    bool
	}{
		{"uncompressed", plain, false},
		{"compressed", compressed, true},
		{"bare model", `<mxGraphModel><root/></mxGraphModel>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFiles(map[string][]byte{"arch/system.drawio": []byte(tc.data)})
			if (err != nil) != tc.wantErr {
				t.Errorf("checkFiles() error = %v, want error %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "Compressed") {
				t.Errorf("error %q does not say how to fix it", err)
			}
			if got := DrawioCompressed([]byte(tc.data)); got != tc.wantErr {
				t.Errorf("DrawioCompressed() = %t, want %t", got, tc.wantErr)
			}
		})
	}
}
