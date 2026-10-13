package sqlitestore_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/petervlad/loadout/internal/registry/registrytest"
)

func TestSnapshotTo(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "registry.db"), nil)
	ctx := t.Context()
	if _, err := s.PutRelease(ctx, registrytest.Release(t, "lime", "planner", "0.1.0"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := s.SnapshotTo(ctx, copyPath); err != nil {
		t.Fatal(err)
	}
	if err := s.SnapshotTo(ctx, copyPath); err == nil {
		t.Error("SnapshotTo() onto an existing copy: error = nil, want a refusal")
	}

	raw, err := sql.Open("sqlite", copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var n int
	if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM releases WHERE team = 'lime' AND name = 'planner'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("copy has %d releases (%v), want 1", n, err)
	}

	// Rows added later are not in the earlier copy.
	if _, err := s.PutRelease(ctx, registrytest.Release(t, "lime", "later", "0.1.0"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM releases`).Scan(&n); err != nil || n != 1 {
		t.Errorf("copy has %d releases after a later publish (%v), want 1", n, err)
	}
}
