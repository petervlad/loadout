package memory

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/petervlad/loadout/internal/registry"
)

// Record is one entry as a person reads it on the web: the entry whatever
// its status, every version, and what the person may do with it.
type Record struct {
	Entry Entry
	// Versions are oldest first.
	Versions []Version
	// CanRetract is true for an active entry the person wrote or moderates.
	CanRetract bool
	// CanMove is true for an active entry of a team that the person
	// moderates, while the project above has memory on.
	CanMove bool
}

// History returns entry id for a person who reads it through ns, retracted or
// not, with its history. An entry outside the layers ns reads is ErrNoMemory.
// It counts no use, since a person looking at the history is not an agent
// relying on the text. CanRetract and CanMove repeat the checks Retract and
// Move make, so a page offers only what the service will accept.
func (s *Service) History(ctx context.Context, c Caller, ns string, id int64) (Record, error) {
	a, err := s.resolve(ctx, c, ns)
	if err != nil {
		return Record{}, err
	}
	e, err := s.store.Get(ctx, id)
	if errors.Is(err, registry.ErrNotFound) {
		return Record{}, ErrNoMemory
	}
	if err != nil {
		return Record{}, fmt.Errorf("memory %d: %w", id, err)
	}
	if !slices.Contains(a.readable, e.Namespace) {
		return Record{}, ErrNoMemory
	}
	versions, err := s.store.History(ctx, id)
	if err != nil {
		return Record{}, fmt.Errorf("history of memory %d: %w", id, err)
	}
	rec := Record{Entry: e, Versions: versions}
	if !e.Active() {
		return rec, nil
	}
	// The person's rights where the entry lives, which can differ from ns:
	// a member of a team reads the project's entries and moderates none.
	own, err := s.resolve(ctx, c, e.Namespace)
	if err != nil {
		return rec, nil //nolint:nilerr // no rights there means no buttons, not a failure
	}
	rec.CanRetract = e.AuthorID == c.UserID || own.Moderate
	if project := parentOf(own.Layers, e.Namespace); own.Moderate && project != "" {
		rec.CanMove = slices.Contains(own.readable, project)
	}
	return rec, nil
}
