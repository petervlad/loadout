package registrytest

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

var _ registry.DeletionStore = (*MemStore)(nil)

// findDeletion returns the stored request with id, or nil. m.mu must be held.
func (m *MemStore) findDeletion(id string) *registry.Deletion {
	for i := range m.deletions {
		if m.deletions[i].ID == id {
			return &m.deletions[i]
		}
	}
	return nil
}

// openDeletion is findDeletion for a request that must be open. m.mu must
// be held.
func (m *MemStore) openDeletion(id string) (*registry.Deletion, error) {
	d := m.findDeletion(id)
	if d == nil {
		return nil, fmt.Errorf("deletion %s: %w", id, registry.ErrNotFound)
	}
	if d.Status != registry.DeletionOpen {
		return nil, fmt.Errorf("deletion %s: %w: it is %s", id, registry.ErrProposalClosed, d.Status)
	}
	return d, nil
}

func cloneDeletion(d registry.Deletion) registry.Deletion {
	d.Removed.Releases = slices.Clone(d.Removed.Releases)
	d.Removed.Open = slices.Clone(d.Removed.Open)
	d.Removed.Courses = slices.Clone(d.Removed.Courses)
	d.Removed.Deletions = slices.Clone(d.Removed.Deletions)
	return d
}

// CreateDeletion implements registry.DeletionStore.
func (m *MemStore) CreateDeletion(_ context.Context, d registry.Deletion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.deletions {
		if o.ID == d.ID || (o.Package == d.Package && o.Status == registry.DeletionOpen) {
			return fmt.Errorf("create deletion %s: %w: %s already has an open request", d.ID, registry.ErrExists, d.Package)
		}
	}
	d.Status = registry.DeletionOpen
	d.DecidedAt, d.DecidedBy, d.DecisionNote, d.SelfApproved, d.Removed = time.Time{}, registry.Actor{}, "", false, registry.PackageFootprint{}
	m.deletions = append(m.deletions, d)
	return nil
}

// Deletion implements registry.DeletionStore.
func (m *MemStore) Deletion(_ context.Context, id string) (registry.Deletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.findDeletion(id)
	if d == nil {
		return registry.Deletion{}, fmt.Errorf("deletion %s: %w", id, registry.ErrNotFound)
	}
	return cloneDeletion(*d), nil
}

// Deletions implements registry.DeletionStore.
func (m *MemStore) Deletions(_ context.Context, f registry.DeletionFilter) ([]registry.Deletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []registry.Deletion
	for _, d := range slices.Backward(m.deletions) { // newest first
		if d.Package.Team() == f.Team && (f.Status == "" || d.Status == f.Status) {
			out = append(out, cloneDeletion(d))
		}
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func closeDeletion(d *registry.Deletion, status registry.DeletionStatus, dec registry.Decision) {
	d.Status, d.DecidedAt, d.DecidedBy, d.DecisionNote = status, dec.At.UTC(), dec.By, dec.Note
	d.SelfApproved = dec.SelfApproved
}

// DecideDeletion implements registry.DeletionStore.
func (m *MemStore) DecideDeletion(_ context.Context, id string, dec registry.Decision) (registry.Deletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if dec.Status != registry.ProposalRejected && dec.Status != registry.ProposalWithdrawn {
		return registry.Deletion{}, fmt.Errorf("decide deletion %s: %w: cannot close a request as %q", id, registry.ErrInvalid, dec.Status)
	}
	d, err := m.openDeletion(id)
	if err != nil {
		return registry.Deletion{}, err
	}
	closeDeletion(d, registry.DeletionStatus(dec.Status), dec)
	return cloneDeletion(*d), nil
}

// ApproveDeletion implements registry.DeletionStore.
func (m *MemStore) ApproveDeletion(_ context.Context, id string, dec registry.Decision) (registry.Deletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, err := m.openDeletion(id)
	if err != nil {
		return registry.Deletion{}, err
	}
	if latest := m.latest(d.Package); !latest.Equal(dec.Latest) {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: %w: the newest release of %s is %q, not %q", id, registry.ErrStale, d.Package, latest, dec.Latest)
	}
	fp, err := m.removePackage(d.Package, dec.At)
	if err != nil {
		return registry.Deletion{}, err
	}
	fp.Deletions = nil // the request itself was the only one open
	closeDeletion(d, registry.DeletionApproved, dec)
	d.Removed = fp
	return cloneDeletion(*d), nil
}

// RecordDeletion implements registry.DeletionStore.
func (m *MemStore) RecordDeletion(_ context.Context, d registry.Deletion) (registry.PackageFootprint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d.Newest = m.latest(d.Package)
	fp, err := m.removePackage(d.Package, d.CreatedAt)
	if err != nil {
		return registry.PackageFootprint{}, err
	}
	d.Status, d.SelfApproved, d.Removed = registry.DeletionApproved, true, fp
	d.DecidedAt, d.DecidedBy = d.CreatedAt.UTC(), d.Author
	m.deletions = append(m.deletions, cloneDeletion(d))
	return fp, nil
}
