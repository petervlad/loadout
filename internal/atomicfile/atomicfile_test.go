package atomicfile_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/petervlad/loadout/internal/atomicfile"
)

func TestWrite(t *testing.T) {
	tests := []struct {
		name string
		perm fs.FileMode
		old  bool
	}{
		{"new file", 0o644, false},
		{"replace", 0o600, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "f.yaml")
			if tt.old {
				if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := atomicfile.Write(path, []byte("new"), tt.perm); err != nil {
				t.Fatalf("Write() error: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "new" {
				t.Errorf("content = %q, want %q", got, "new")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != tt.perm {
				t.Errorf("perm = %v, want %v", info.Mode().Perm(), tt.perm)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Errorf("folder holds %d entries, want 1 (temp file left behind)", len(entries))
			}
		})
	}
}

func TestWriteInStaysInRoot(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := atomicfile.WriteIn(r, "../escape", []byte("x"), 0o644); err == nil {
		t.Error("WriteIn(../escape) = nil, want an error")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Error("file written outside the root")
	}
}

func TestWriteMissingFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "f")
	if err := atomicfile.Write(path, []byte("x"), 0o644); err == nil {
		t.Error("Write() into a missing folder = nil, want an error")
	}
}
