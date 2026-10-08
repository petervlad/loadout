package accounttest

import (
	"context"
	"time"

	"github.com/petervlad/loadout/internal/account"
)

var _ account.CountStore = (*MemStore)(nil)

// AccountCounts implements account.CountStore.
func (m *MemStore) AccountCounts(_ context.Context, now time.Time) (account.Counts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := account.Counts{Users: len(m.users), Members: map[account.Role]int{}}
	for _, u := range m.users {
		if u.Verified() {
			c.Verified++
		}
		if u.IsAdmin {
			c.Admins++
		}
	}
	for _, ms := range m.members {
		c.Members[ms.Role]++
	}
	for _, s := range m.sessions {
		if s.ExpiresAt.After(now) {
			c.Sessions++
		}
	}
	return c, nil
}
