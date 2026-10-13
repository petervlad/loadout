// Package registrytest provides an in-memory registry store for tests and
// the contract suites every Store and TokenStore implementation must pass.
package registrytest

import (
	"bytes"
	"cmp"
	"context"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/tier"
	"github.com/petervlad/loadout/internal/wiki"
)

// MemStore is an in-memory registry.Store, registry.TokenStore,
// registry.NamespaceStore, registry.ProposalStore and registry.WikiStore. The zero value is not
// usable; use NewMemStore.
type MemStore struct {
	mu         sync.Mutex
	releases   []stored
	seq        int64
	tokens     map[string]tokenRecord // by hex hash
	namespaces map[string]registry.Namespace
	tiers      map[string]tier.Table
	// proposals are kept in creation order.
	proposals []storedProposal
	// deletions are kept in creation order.
	deletions []registry.Deletion
	// wikis holds each namespace's kept revisions, oldest first.
	wikis map[string][]wiki.Revision
}

type stored struct {
	rel    registry.Release
	bundle []byte
}

type tokenRecord struct {
	token registry.Token
	order int
}

var (
	_ registry.Store          = (*MemStore)(nil)
	_ registry.TokenStore     = (*MemStore)(nil)
	_ registry.NamespaceStore = (*MemStore)(nil)
	_ registry.WikiStore      = (*MemStore)(nil)
)

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{
		tokens:     map[string]tokenRecord{},
		namespaces: map[string]registry.Namespace{},
		tiers:      map[string]tier.Table{},
		wikis:      map[string][]wiki.Revision{},
	}
}

// PutRelease implements registry.Store.
func (m *MemStore) PutRelease(_ context.Context, rel registry.Release, b []byte) (registry.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkAboveLatest(rel.ID, rel.Version); err != nil {
		return registry.Release{}, err
	}
	m.seq++
	rel.Seq = m.seq
	m.releases = append(m.releases, stored{rel: rel, bundle: bytes.Clone(b)})
	return rel, nil
}

// checkAboveLatest returns ErrVersionConflict unless v is higher than every
// release of id. m.mu must be held.
func (m *MemStore) checkAboveLatest(id pack.ID, v semver.Version) error {
	for _, s := range m.releases {
		if s.rel.ID == id && s.rel.Version.Compare(v) >= 0 {
			return fmt.Errorf("%w: %s@%s is not higher than the release %s", registry.ErrVersionConflict, id, v, s.rel.Version)
		}
	}
	return nil
}

// Release implements registry.Store.
func (m *MemStore) Release(_ context.Context, id pack.ID, v semver.Version) (registry.Release, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.releases {
		if s.rel.ID == id && s.rel.Version.Compare(v) == 0 {
			return s.rel, bytes.Clone(s.bundle), nil
		}
	}
	return registry.Release{}, nil, registry.ErrNotFound
}

// Releases implements registry.Store.
func (m *MemStore) Releases(_ context.Context, id pack.ID) ([]registry.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []registry.Release
	for _, s := range m.releases {
		if s.rel.ID == id {
			out = append(out, s.rel)
		}
	}
	if len(out) == 0 {
		return nil, registry.ErrNotFound
	}
	slices.SortFunc(out, func(a, b registry.Release) int { return a.Version.Compare(b.Version) })
	return out, nil
}

// Packages implements registry.Store.
func (m *MemStore) Packages(_ context.Context, team string) ([]registry.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	latest := map[pack.ID]registry.Release{}
	for _, s := range m.releases {
		if s.rel.ID.Team() != team {
			continue
		}
		if cur, ok := latest[s.rel.ID]; !ok || s.rel.Version.Compare(cur.Version) > 0 {
			latest[s.rel.ID] = s.rel
		}
	}
	out := make([]registry.Release, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b registry.Release) int { return cmp.Compare(a.ID.Name(), b.ID.Name()) })
	return out, nil
}

// Changes implements registry.Store.
func (m *MemStore) Changes(_ context.Context, team string, since int64, limit int) ([]registry.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []registry.Release
	for _, s := range m.releases { // already in Seq order
		if s.rel.ID.Team() == team && s.rel.Seq > since {
			out = append(out, s.rel)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// CreateToken implements registry.TokenStore.
func (m *MemStore) CreateToken(_ context.Context, t registry.Token, hash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := hex.EncodeToString(hash)
	if _, ok := m.tokens[key]; ok {
		return fmt.Errorf("token %s: hash: %w", t.ID, registry.ErrExists)
	}
	for _, r := range m.tokens {
		if r.token.ID == t.ID {
			return fmt.Errorf("token %s: %w", t.ID, registry.ErrExists)
		}
	}
	m.tokens[key] = tokenRecord{token: t, order: len(m.tokens)}
	return nil
}

// TokenByHash implements registry.TokenStore.
func (m *MemStore) TokenByHash(_ context.Context, hash []byte) (registry.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.tokens[hex.EncodeToString(hash)]
	if !ok {
		return registry.Token{}, registry.ErrNotFound
	}
	return rec.token, nil
}

// Tokens implements registry.TokenStore.
func (m *MemStore) Tokens(_ context.Context, team string) ([]registry.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var recs []tokenRecord
	for _, r := range m.tokens {
		if r.token.Team == team {
			recs = append(recs, r)
		}
	}
	slices.SortFunc(recs, func(a, b tokenRecord) int { return cmp.Compare(a.order, b.order) })
	out := make([]registry.Token, len(recs))
	for i, r := range recs {
		out[i] = r.token
	}
	return out, nil
}

// RevokeToken implements registry.TokenStore.
func (m *MemStore) RevokeToken(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.tokens {
		if r.token.ID == id {
			delete(m.tokens, k)
			return nil
		}
	}
	return registry.ErrNotFound
}

// RevokeUserTokens implements registry.TokenStore.
func (m *MemStore) RevokeUserTokens(_ context.Context, userID, team string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, r := range m.tokens {
		if userID != "" && r.token.UserID == userID && (team == "" || r.token.Team == team) {
			delete(m.tokens, k)
			n++
		}
	}
	return n, nil
}

// CreateNamespace implements registry.NamespaceStore.
func (m *MemStore) CreateNamespace(_ context.Context, ns registry.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.namespaces[ns.Name]; ok {
		return fmt.Errorf("namespace %s: %w", ns.Name, registry.ErrExists)
	}
	m.namespaces[ns.Name] = ns
	return nil
}

// Namespace implements registry.NamespaceStore.
func (m *MemStore) Namespace(_ context.Context, name string) (registry.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns, ok := m.namespaces[name]
	if !ok {
		return registry.Namespace{}, registry.ErrNotFound
	}
	return ns, nil
}

// SetRequireReview implements registry.NamespaceStore.
func (m *MemStore) SetRequireReview(_ context.Context, name string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns, ok := m.namespaces[name]
	if !ok {
		return fmt.Errorf("namespace %s: %w", name, registry.ErrNotFound)
	}
	ns.RequireReview = on
	m.namespaces[name] = ns
	return nil
}

// SetWiki implements registry.NamespaceStore.
func (m *MemStore) SetWiki(_ context.Context, name string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns, ok := m.namespaces[name]
	if !ok {
		return fmt.Errorf("namespace %s: %w", name, registry.ErrNotFound)
	}
	ns.Wiki = on
	m.namespaces[name] = ns
	return nil
}

// SetCourse implements registry.NamespaceStore.
func (m *MemStore) SetCourse(_ context.Context, name string, course pack.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ns, ok := m.namespaces[name]
	if !ok {
		return fmt.Errorf("namespace %s: %w", name, registry.ErrNotFound)
	}
	ns.Course = course
	m.namespaces[name] = ns
	return nil
}

// ModelTiers implements registry.NamespaceStore.
func (m *MemStore) ModelTiers(_ context.Context, name string) (tier.Table, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.namespaces[name]; !ok {
		return tier.Table{}, fmt.Errorf("namespace %s: %w", name, registry.ErrNotFound)
	}
	return m.tiers[name], nil
}

// SetModelTiers implements registry.NamespaceStore. A table is immutable,
// so it is kept as it is.
func (m *MemStore) SetModelTiers(_ context.Context, name string, t tier.Table) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.namespaces[name]; !ok {
		return fmt.Errorf("namespace %s: %w", name, registry.ErrNotFound)
	}
	m.tiers[name] = t
	return nil
}

// DeleteNamespace implements registry.NamespaceStore.
func (m *MemStore) DeleteNamespace(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.namespaces[name]; !ok {
		return fmt.Errorf("namespace %s: %w", name, registry.ErrNotFound)
	}
	for _, ns := range m.namespaces {
		if ns.Parent == name {
			return fmt.Errorf("namespace %s: child %s: %w", name, ns.Name, registry.ErrNotEmpty)
		}
	}
	for _, s := range m.releases {
		if s.rel.ID.Team() == name {
			return fmt.Errorf("namespace %s: releases: %w", name, registry.ErrNotEmpty)
		}
	}
	for _, p := range m.proposals {
		if p.p.Package.Team() == name {
			return fmt.Errorf("namespace %s: proposals: %w", name, registry.ErrNotEmpty)
		}
	}
	for k, r := range m.tokens {
		if r.token.Team == name {
			delete(m.tokens, k)
		}
	}
	delete(m.namespaces, name)
	delete(m.tiers, name)
	delete(m.wikis, name)
	return nil
}

// Namespaces implements registry.NamespaceStore.
func (m *MemStore) Namespaces(_ context.Context) ([]registry.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Collect(maps.Values(m.namespaces))
	slices.SortFunc(out, func(a, b registry.Namespace) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}
