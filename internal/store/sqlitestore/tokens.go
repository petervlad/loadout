package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/registry"
)

const authColumns = "id, team, scope, label, created_at, user_id, created_by_user_id, expires_at"

// CreateToken implements registry.TokenStore.
func (s *Store) CreateToken(ctx context.Context, t registry.Token, hash []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tokens (id, hash, team, scope, label, created_at, user_id, created_by_user_id, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, hash, t.Team, string(t.Scope), t.Label, formatTime(t.CreatedAt), t.UserID, nullString(t.CreatedBy), nullTime(t.ExpiresAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("create token %s: %w", t.ID, registry.ErrExists)
	}
	if err != nil {
		return fmt.Errorf("create token %s: %w", t.ID, err)
	}
	return nil
}

// TokenByHash implements registry.TokenStore.
func (s *Store) TokenByHash(ctx context.Context, hash []byte) (registry.Token, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+authColumns+` FROM tokens WHERE hash = ?`, hash)
	t, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Token{}, fmt.Errorf("token by hash: %w", registry.ErrNotFound)
	}
	if err != nil {
		return registry.Token{}, fmt.Errorf("token by hash: %w", err)
	}
	return t, nil
}

// Tokens implements registry.TokenStore.
func (s *Store) Tokens(ctx context.Context, team string) ([]registry.Token, error) {
	// rowid breaks ties between tokens created in the same instant.
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+authColumns+` FROM tokens
		WHERE team = ?
		ORDER BY created_at, rowid`,
		team)
	if err != nil {
		return nil, fmt.Errorf("tokens of %s: %w", team, err)
	}
	defer rows.Close()
	var out []registry.Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("tokens of %s: %w", team, err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tokens of %s: %w", team, err)
	}
	return out, nil
}

// RevokeToken implements registry.TokenStore.
func (s *Store) RevokeToken(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("revoke token %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke token %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("revoke token %s: %w", id, registry.ErrNotFound)
	}
	return nil
}

// RevokeUserTokens implements registry.TokenStore.
func (s *Store) RevokeUserTokens(ctx context.Context, userID, team string) (int, error) {
	if userID == "" {
		return 0, nil // tokens without a user are never matched
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM tokens WHERE user_id = ? AND (? = '' OR team = ?)`,
		userID, team, team)
	if err != nil {
		return 0, fmt.Errorf("revoke tokens of user %s: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke tokens of user %s: %w", userID, err)
	}
	return int(n), nil
}

func scanToken(sc scanner) (registry.Token, error) {
	var (
		t              registry.Token
		scope, created string
		createdBy      sql.NullString
		expires        sql.NullString
	)
	if err := sc.Scan(&t.ID, &t.Team, &scope, &t.Label, &created, &t.UserID, &createdBy, &expires); err != nil {
		return registry.Token{}, err
	}
	at, err := parseTime(created)
	if err != nil {
		return registry.Token{}, fmt.Errorf("stored token %s: %w", t.ID, err)
	}
	t.Scope = registry.TokenScope(scope)
	t.CreatedAt = at
	t.CreatedBy = createdBy.String
	if expires.Valid {
		if t.ExpiresAt, err = parseTime(expires.String); err != nil {
			return registry.Token{}, fmt.Errorf("stored token %s: %w", t.ID, err)
		}
	}
	return t, nil
}
