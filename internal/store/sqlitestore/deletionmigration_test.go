package sqlitestore_test

import (
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// TestDeletionMigration: a database from before deletion requests opens,
// keeps its releases and proposals, and has no requests; the table works
// at once.
func TestDeletionMigration(t *testing.T) {
	s := openAt(t, 15,
		stmt{q: `INSERT INTO releases (team, name, version, major, minor, patch, digest, description, size, published_at, bundle, kind)
			VALUES ('lime', 'planner', '1.0.0', 1, 0, 0, 'sha256:x', 'about', 3, ?, x'00', 'agent')`, args: []any{t0}},
	)
	ctx := t.Context()
	id, err := pack.NewID("lime", "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PackageFootprint(ctx, id); err != nil {
		t.Fatalf("PackageFootprint() after the migration: %v", err)
	}
	ds, err := s.Deletions(ctx, registry.DeletionFilter{Team: "lime", Limit: 10})
	if err != nil || len(ds) != 0 {
		t.Fatalf("Deletions() = %+v, %v; want none", ds, err)
	}
	req := registry.Deletion{ID: "del-a", Package: id, Reason: "why", Author: registry.Actor{TokenID: "t"}}
	if err := s.CreateDeletion(ctx, req); err != nil {
		t.Fatalf("CreateDeletion() error: %v", err)
	}
	if got, err := s.Deletion(ctx, "del-a"); err != nil || got.Status != registry.DeletionOpen {
		t.Errorf("Deletion() = %+v, %v; want an open request", got, err)
	}
}
