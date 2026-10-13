package registrytest

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// PackageFootprint implements registry.Store.
func (m *MemStore) PackageFootprint(_ context.Context, id pack.ID) (registry.PackageFootprint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.footprint(id)
}

// DeletePackage implements registry.Store.
func (m *MemStore) DeletePackage(_ context.Context, id pack.ID, at time.Time) (registry.PackageFootprint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removePackage(id, at)
}

// removePackage deletes what the store keeps of id and closes its open
// requests to be deleted as gone. m.mu must be held.
func (m *MemStore) removePackage(id pack.ID, at time.Time) (registry.PackageFootprint, error) {
	fp, err := m.footprint(id)
	if err != nil {
		return registry.PackageFootprint{}, err
	}
	m.releases = slices.DeleteFunc(m.releases, func(s stored) bool { return s.rel.ID == id })
	m.proposals = slices.DeleteFunc(m.proposals, func(s storedProposal) bool { return s.p.Package == id })
	for name, ns := range m.namespaces {
		if ns.Course == id {
			ns.Course = pack.ID{}
			m.namespaces[name] = ns
		}
	}
	for i := range m.deletions {
		if d := &m.deletions[i]; d.Package == id && d.Status == registry.DeletionOpen {
			d.Status, d.DecidedAt = registry.DeletionGone, at.UTC()
		}
	}
	return fp, nil
}

// footprint reads what the store keeps of id. m.mu must be held.
func (m *MemStore) footprint(id pack.ID) (registry.PackageFootprint, error) {
	fp := registry.PackageFootprint{ID: id}
	var rels []registry.Release
	for _, s := range m.releases {
		if s.rel.ID == id {
			rels = append(rels, s.rel)
		}
	}
	slices.SortFunc(rels, func(a, b registry.Release) int { return a.Version.Compare(b.Version) })
	for _, r := range rels {
		fp.Releases = append(fp.Releases, r.Version)
		fp.Kind = r.Kind
	}
	for _, s := range m.proposals { // creation order
		if s.p.Package != id {
			continue
		}
		fp.Proposals++
		if s.p.Status == registry.ProposalOpen {
			fp.Open = append(fp.Open, s.p.ID)
		}
		fp.Reviews += len(s.texts)
		fp.Prompts += len(s.p.Prompts) + len(s.p.RemovedPrompts)
	}
	for name, ns := range m.namespaces {
		if ns.Course == id {
			fp.Courses = append(fp.Courses, name)
		}
	}
	slices.Sort(fp.Courses)
	for _, d := range m.deletions { // creation order
		if d.Package == id && d.Status == registry.DeletionOpen {
			fp.Deletions = append(fp.Deletions, d.ID)
		}
	}
	if len(fp.Releases) == 0 && fp.Proposals == 0 {
		return registry.PackageFootprint{}, fmt.Errorf("%w: no release or proposal", registry.ErrNotFound)
	}
	return fp, nil
}
