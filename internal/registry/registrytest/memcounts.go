package registrytest

import (
	"context"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

var _ registry.CountStore = (*MemStore)(nil)

// Counts implements registry.CountStore.
func (m *MemStore) Counts(_ context.Context, now time.Time) (registry.Counts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := registry.Counts{
		Namespaces: map[registry.NamespaceKind]int{},
		Packages:   map[pack.Kind]int{},
		Proposals:  map[registry.ProposalStatus]int{},
	}
	for _, ns := range m.namespaces {
		c.Namespaces[ns.Kind]++
	}
	// Releases are kept in Seq order, so the last one of a package is its
	// latest.
	latest := map[pack.ID]pack.Kind{}
	for _, s := range m.releases {
		latest[s.rel.ID] = s.rel.Kind
		c.Releases++
		c.ReleaseBytes += int64(len(s.bundle))
	}
	for _, k := range latest {
		c.Packages[k]++
	}
	for _, s := range m.proposals {
		c.Proposals[s.p.Status]++
		c.ProposalBytes += int64(len(s.bundle))
		for i := range s.texts {
			c.Reviews++
			c.ReviewBytes += int64(len(s.texts[i]) + len(s.datas[i]))
		}
	}
	for _, r := range m.tokens {
		switch {
		case r.token.UserID == "":
			c.MachineTokens++
		case r.token.ExpiresAt.IsZero() || r.token.ExpiresAt.After(now):
			c.LoginTokens++
		}
	}
	return c, nil
}
