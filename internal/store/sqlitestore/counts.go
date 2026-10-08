package sqlitestore

import (
	"context"
	"fmt"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

var (
	_ registry.CountStore = (*Store)(nil)
	_ account.CountStore  = (*Store)(nil)
)

// Counts implements registry.CountStore. Sizes use length() of the blobs,
// which SQLite answers from the record header without reading them.
func (s *Store) Counts(ctx context.Context, now time.Time) (registry.Counts, error) {
	c := registry.Counts{
		Namespaces: map[registry.NamespaceKind]int{},
		Packages:   map[pack.Kind]int{},
		Proposals:  map[registry.ProposalStatus]int{},
	}
	groups := []struct {
		query string
		add   func(key string, n int)
	}{
		{
			`SELECT kind, COUNT(*) FROM namespaces GROUP BY kind`,
			func(k string, n int) { c.Namespaces[registry.NamespaceKind(k)] = n },
		},
		{
			`SELECT r.kind, COUNT(*) FROM releases r
			WHERE r.seq = (SELECT MAX(seq) FROM releases WHERE team = r.team AND name = r.name)
			GROUP BY r.kind`,
			func(k string, n int) { c.Packages[pack.Kind(k)] = n },
		},
		{
			`SELECT status, COUNT(*) FROM proposals GROUP BY status`,
			func(k string, n int) { c.Proposals[registry.ProposalStatus(k)] = n },
		},
	}
	for _, g := range groups {
		if err := s.eachCount(ctx, g.query, g.add); err != nil {
			return registry.Counts{}, err
		}
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM releases),
			(SELECT COALESCE(SUM(length(bundle)), 0) FROM releases),
			(SELECT COALESCE(SUM(length(bundle)), 0) FROM proposals),
			(SELECT COUNT(*) FROM proposal_reviews),
			(SELECT COALESCE(SUM(length(body) + length(data)), 0) FROM proposal_reviews),
			(SELECT COUNT(*) FROM tokens WHERE user_id = ''),
			(SELECT COUNT(*) FROM tokens WHERE user_id <> '' AND (expires_at IS NULL OR expires_at > ?))`,
		formatTime(now))
	if err := row.Scan(&c.Releases, &c.ReleaseBytes, &c.ProposalBytes, &c.Reviews, &c.ReviewBytes, &c.MachineTokens, &c.LoginTokens); err != nil {
		return registry.Counts{}, fmt.Errorf("count registry: %w", err)
	}
	return c, nil
}

// AccountCounts implements account.CountStore.
func (s *Store) AccountCounts(ctx context.Context, now time.Time) (account.Counts, error) {
	c := account.Counts{Members: map[account.Role]int{}}
	err := s.eachCount(ctx, `SELECT role, COUNT(*) FROM memberships GROUP BY role`,
		func(k string, n int) { c.Members[account.Role(k)] = n })
	if err != nil {
		return account.Counts{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM users WHERE email_verified_at IS NOT NULL),
			(SELECT COUNT(*) FROM users WHERE is_admin <> 0),
			(SELECT COUNT(*) FROM sessions WHERE expires_at > ?)`,
		formatTime(now))
	if err := row.Scan(&c.Users, &c.Verified, &c.Admins, &c.Sessions); err != nil {
		return account.Counts{}, fmt.Errorf("count accounts: %w", err)
	}
	return c, nil
}

// eachCount runs a "key, count" query and hands every row to add.
func (s *Store) eachCount(ctx context.Context, query string, add func(key string, n int)) error {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("count: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key string
			n   int
		)
		if err := rows.Scan(&key, &n); err != nil {
			return fmt.Errorf("count: %w", err)
		}
		add(key, n)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("count: %w", err)
	}
	return nil
}
