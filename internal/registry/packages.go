package registry

import (
	"context"
	"fmt"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// PackageFootprint is what the registry keeps of one package: what
// `loadout package delete` lists before it deletes, and counts after.
type PackageFootprint struct {
	ID       pack.ID
	Releases []semver.Version // lowest first
	// Kind is the kind of the newest release; empty without one.
	Kind      pack.Kind
	Proposals int      // every status
	Open      []string // ids of open proposals, oldest first
	Reviews   int      // reports attached to its proposals
	Prompts   int      // prompt rows, removed ones included
	// Courses are the namespaces whose wiki course it is, by name.
	Courses []string
	// Deletions are the ids of its open requests to be deleted, oldest
	// first; they close as gone when the package is deleted another way.
	Deletions []string
}

// PackageFootprint returns what DeletePackage would remove, or ErrNotFound
// when the package has no release and no proposal. It takes no principal:
// it is not reachable over HTTP.
func (s *Service) PackageFootprint(ctx context.Context, id pack.ID) (PackageFootprint, error) {
	fp, err := s.store.PackageFootprint(ctx, id)
	if err != nil {
		return PackageFootprint{}, fmt.Errorf("package %s: %w", id, err)
	}
	return fp, nil
}

// DeletePackage removes every release and proposal of id with the
// proposals' reviews and prompts, clears id as any namespace's course, and
// returns what it removed. Callers check who may: only the operator on the
// registry host does, since nothing can undo it. Its open requests to be
// deleted close as gone.
func (s *Service) DeletePackage(ctx context.Context, id pack.ID) (PackageFootprint, error) {
	fp, err := s.store.DeletePackage(ctx, id, s.now().UTC())
	if err != nil {
		return PackageFootprint{}, fmt.Errorf("delete package %s: %w", id, err)
	}
	s.log.Info("package deleted", "package", id.String(),
		"releases", len(fp.Releases), "proposals", fp.Proposals, "reviews", fp.Reviews,
		"prompts", fp.Prompts, "courses", len(fp.Courses))
	return fp, nil
}
