package registrytest

import (
	"context"
	"fmt"
	"slices"

	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/wiki"
)

// WikiRevision implements registry.WikiStore.
func (m *MemStore) WikiRevision(_ context.Context, namespace string, seq int) (wiki.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	revs := m.wikis[namespace]
	if seq == 0 && len(revs) > 0 {
		return revs[len(revs)-1], nil
	}
	for _, r := range revs {
		if r.Seq == seq {
			return r, nil
		}
	}
	return wiki.Revision{}, fmt.Errorf("wiki of %s: revision %d: %w", namespace, seq, registry.ErrNotFound)
}

// WikiRevisions implements registry.WikiStore.
func (m *MemStore) WikiRevisions(_ context.Context, namespace string) ([]wiki.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.namespaces[namespace]; !ok {
		return nil, fmt.Errorf("namespace %s: %w", namespace, registry.ErrNotFound)
	}
	var out []wiki.Info
	for _, r := range slices.Backward(m.wikis[namespace]) {
		out = append(out, r.Info)
	}
	return out, nil
}

// PutWikiRevision implements registry.WikiStore.
func (m *MemStore) PutWikiRevision(_ context.Context, rev wiki.Revision, base, keep int) (wiki.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns := rev.Namespace
	if _, ok := m.namespaces[ns]; !ok {
		return wiki.Info{}, fmt.Errorf("save wiki of %s: %w", ns, registry.ErrNotFound)
	}
	revs := m.wikis[ns]
	newest := 0
	if len(revs) > 0 {
		newest = revs[len(revs)-1].Seq
	}
	if newest != base {
		return wiki.Info{}, fmt.Errorf("save wiki of %s on revision %d: revision %d exists: %w", ns, base, newest, registry.ErrStale)
	}
	rev.Seq, rev.Format, rev.Size = base+1, rev.Doc.Format(), rev.Doc.Size()
	rev.CreatedAt = rev.CreatedAt.UTC()
	revs = append(revs, rev)
	if keep > 0 && len(revs) > keep {
		revs = slices.Clone(revs[len(revs)-keep:])
	}
	m.wikis[ns] = revs
	return rev.Info, nil
}
