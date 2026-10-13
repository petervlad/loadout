package sqlitestore_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

// TestDeletePackageIsOneTransaction makes the last delete fail and checks
// that the earlier ones, the proposals, reviews and prompts, were rolled
// back with it.
func TestDeletePackageIsOneTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s := open(t, path, nil)
	ctx := t.Context()
	id, err := pack.NewID("lime", "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutRelease(ctx, registrytest.Release(t, "lime", "planner", "0.1.0"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	p := registrytest.Proposal(t, "p1", "lime", "planner", "0.2.0")
	if err := s.CreateProposal(ctx, p, []byte("c")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReview(ctx, "p1", registry.Review{Author: p.Author, CreatedAt: time.Now(), Size: 1}, []byte("r"), nil, registry.MaxReviews); err != nil {
		t.Fatal(err)
	}
	before, err := s.PackageFootprint(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	// A trigger lives in the file, so the store's own connections hit it.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `CREATE TRIGGER refuse_release_delete BEFORE DELETE ON releases
		BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DeletePackage(ctx, id, time.Now()); err == nil || errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("DeletePackage() error = %v, want a failure that is not ErrNotFound", err)
	}
	after, err := s.PackageFootprint(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if before.Proposals != after.Proposals || before.Reviews != after.Reviews || before.Prompts != after.Prompts || len(before.Releases) != len(after.Releases) {
		t.Errorf("a failed delete changed the package: before %+v, after %+v", before, after)
	}
	if _, err := s.Proposal(ctx, "p1"); err != nil {
		t.Errorf("proposal p1 after a failed delete: %v", err)
	}
	if _, _, err := s.ReviewText(ctx, "p1", 1); err != nil {
		t.Errorf("review of p1 after a failed delete: %v", err)
	}
}
