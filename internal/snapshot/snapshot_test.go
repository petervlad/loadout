package snapshot_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/snapshot"
)

// copier writes a marker, or fails after writing part of a file.
type copier struct{ fail bool }

func (c copier) SnapshotTo(_ context.Context, path string) error {
	if err := os.WriteFile(path, []byte("db"), 0o600); err != nil {
		return err
	}
	if c.fail {
		return errors.New("disk full")
	}
	return nil
}

func TestNewRefusesBadFolders(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, dir string }{
		{"relative", "backups"},
		{"missing", filepath.Join(t.TempDir(), "nope")},
		{"a file", file},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := snapshot.New(tt.dir, copier{}, nil); err == nil {
				t.Errorf("New(%q) error = nil, want one", tt.dir)
			}
		})
	}
}

func TestTake(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 6, 10, 15, 0, 0, time.UTC)
	d, err := snapshot.New(dir, copier{}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}

	name, err := d.Take(t.Context(), "before-delete-lime-planner")
	if err != nil {
		t.Fatal(err)
	}
	if want := "before-delete-lime-planner-20261006T101500Z.db"; name != want {
		t.Errorf("Take() = %q, want %q", name, want)
	}
	if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("copy = %v, %v, want a 0600 file", info, err)
	}
	// The same second and label would overwrite: refused.
	if _, err := d.Take(t.Context(), "before-delete-lime-planner"); err == nil {
		t.Error("Take() twice in one second: error = nil, want a refusal")
	}
	for _, bad := range []string{"", "../x", "Lime", "a/b", "a.b", strings.Repeat("a", 201)} {
		if _, err := d.Take(t.Context(), bad); err == nil {
			t.Errorf("Take(%q) error = nil, want a refusal", bad)
		}
	}
}

func TestTakeRemovesAPartialCopy(t *testing.T) {
	dir := t.TempDir()
	d, err := snapshot.New(dir, copier{fail: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Take(t.Context(), "before-delete-lime-x"); err == nil {
		t.Fatal("Take() error = nil, want the copier's error")
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("folder = %v, want empty after a failed copy", left)
	}
}
