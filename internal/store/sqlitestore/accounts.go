package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
)

var _ account.Store = (*Store)(nil)

const userColumns = "id, email, name, password_hash, google_subject, email_verified_at, created_at, is_admin"

// CreateUser implements account.UserStore.
func (s *Store) CreateUser(ctx context.Context, u account.User) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (`+userColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, nullString(u.PasswordHash), nullString(u.GoogleSubject), nullTime(u.EmailVerifiedAt), formatTime(u.CreatedAt), u.IsAdmin)
	if isUniqueViolation(err) {
		return fmt.Errorf("create user %s: %w", u.ID, registry.ErrExists)
	}
	if err != nil {
		return fmt.Errorf("create user %s: %w", u.ID, err)
	}
	return nil
}

// UserByID implements account.UserStore.
func (s *Store) UserByID(ctx context.Context, id string) (account.User, error) {
	return s.userWhere(ctx, "id = ?", id)
}

// UserByEmail implements account.UserStore.
func (s *Store) UserByEmail(ctx context.Context, email string) (account.User, error) {
	return s.userWhere(ctx, "email = ?", email)
}

// UserByGoogle implements account.UserStore.
func (s *Store) UserByGoogle(ctx context.Context, subject string) (account.User, error) {
	return s.userWhere(ctx, "google_subject = ?", subject)
}

func (s *Store) userWhere(ctx context.Context, cond string, arg any) (account.User, error) {
	// cond is one of the constant conditions of the callers above.
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE `+cond, arg) //nolint:gosec // G202: see above
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return account.User{}, fmt.Errorf("user: %w", registry.ErrNotFound)
	}
	if err != nil {
		return account.User{}, fmt.Errorf("user: %w", err)
	}
	return u, nil
}

// SetPassword implements account.UserStore.
func (s *Store) SetPassword(ctx context.Context, id, hash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, nullString(hash), id)
	return affectedOne(res, err, "set password of user "+id)
}

// MarkVerified implements account.UserStore.
func (s *Store) MarkVerified(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET email_verified_at = COALESCE(email_verified_at, ?) WHERE id = ?`,
		formatTime(at), id)
	return affectedOne(res, err, "verify user "+id)
}

// LinkGoogle implements account.UserStore.
func (s *Store) LinkGoogle(ctx context.Context, id, subject, name string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET google_subject = ?, name = CASE WHEN name = '' THEN ? ELSE name END
		WHERE id = ? AND (google_subject IS NULL OR google_subject = ?)`,
		subject, name, id, subject)
	if isUniqueViolation(err) {
		return fmt.Errorf("link google to user %s: %w", id, registry.ErrExists)
	}
	err = affectedOne(res, err, "link google to user "+id)
	if !errors.Is(err, registry.ErrNotFound) {
		return err
	}
	if _, uerr := s.UserByID(ctx, id); uerr != nil {
		return err
	}
	return fmt.Errorf("link google to user %s: linked to another account: %w", id, registry.ErrExists)
}

// SetAdmin implements account.UserStore.
func (s *Store) SetAdmin(ctx context.Context, id string, on bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET is_admin = ? WHERE id = ?`, on, id)
	return affectedOne(res, err, "set admin of user "+id)
}

// DeleteUser implements account.UserStore. Memberships, sessions and
// challenges go with the user (ON DELETE CASCADE).
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return affectedOne(res, err, "delete user "+id)
}

// Admins implements account.UserStore.
func (s *Store) Admins(ctx context.Context) ([]account.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users WHERE is_admin = 1 ORDER BY email`)
	if err != nil {
		return nil, fmt.Errorf("admins: %w", err)
	}
	defer rows.Close()
	var out []account.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("admins: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admins: %w", err)
	}
	return out, nil
}

func scanUser(sc scanner) (account.User, error) {
	var (
		u                  account.User
		pw, google, verify sql.NullString
		created            string
	)
	if err := sc.Scan(&u.ID, &u.Email, &u.Name, &pw, &google, &verify, &created, &u.IsAdmin); err != nil {
		return account.User{}, err
	}
	u.PasswordHash, u.GoogleSubject = pw.String, google.String
	var err error
	if u.CreatedAt, err = parseTime(created); err != nil {
		return account.User{}, fmt.Errorf("stored user %s: %w", u.ID, err)
	}
	if verify.Valid {
		if u.EmailVerifiedAt, err = parseTime(verify.String); err != nil {
			return account.User{}, fmt.Errorf("stored user %s: %w", u.ID, err)
		}
	}
	return u, nil
}

// PutMember implements account.MemberStore.
func (s *Store) PutMember(ctx context.Context, m account.Membership) error {
	return putMember(ctx, s.db, m)
}

// PutMembers implements account.MemberStore: one transaction, so a failed
// membership leaves none of the others behind.
func (s *Store) PutMembers(ctx context.Context, ms []account.Membership) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put members: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op after Commit
	for _, m := range ms {
		if err := putMember(ctx, tx, m); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("put members: %w", err)
	}
	return nil
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func putMember(ctx context.Context, db execer, m account.Membership) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO memberships (user_id, namespace, role, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, namespace) DO UPDATE SET role = excluded.role`,
		m.UserID, m.Namespace, string(m.Role), formatTime(m.CreatedAt))
	if isForeignKeyViolation(err) {
		return fmt.Errorf("put member %s in %s: user or namespace: %w", m.UserID, m.Namespace, registry.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("put member %s in %s: %w", m.UserID, m.Namespace, err)
	}
	return nil
}

// RemoveMember implements account.MemberStore.
func (s *Store) RemoveMember(ctx context.Context, userID, namespace string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memberships WHERE user_id = ? AND namespace = ?`, userID, namespace)
	return affectedOne(res, err, "remove member "+userID+" from "+namespace)
}

const memberSelect = `
	SELECT m.user_id, u.email, m.namespace, m.role, m.created_at
	FROM memberships m JOIN users u ON u.id = m.user_id`

// Membership implements account.MemberStore.
func (s *Store) Membership(ctx context.Context, userID, namespace string) (account.Membership, error) {
	row := s.db.QueryRowContext(ctx, memberSelect+` WHERE m.user_id = ? AND m.namespace = ?`, userID, namespace)
	m, err := scanMember(row)
	if errors.Is(err, sql.ErrNoRows) {
		return account.Membership{}, fmt.Errorf("member %s of %s: %w", userID, namespace, registry.ErrNotFound)
	}
	if err != nil {
		return account.Membership{}, fmt.Errorf("member %s of %s: %w", userID, namespace, err)
	}
	return m, nil
}

// Members implements account.MemberStore.
func (s *Store) Members(ctx context.Context, namespace string) ([]account.Membership, error) {
	return s.members(ctx, memberSelect+` WHERE m.namespace = ? ORDER BY u.email`, namespace)
}

// UserMemberships implements account.MemberStore.
func (s *Store) UserMemberships(ctx context.Context, userID string) ([]account.Membership, error) {
	return s.members(ctx, memberSelect+` WHERE m.user_id = ? ORDER BY m.namespace`, userID)
}

func (s *Store) members(ctx context.Context, query string, arg string) ([]account.Membership, error) {
	rows, err := s.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("members: %w", err)
	}
	defer rows.Close()
	var out []account.Membership
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("members: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("members: %w", err)
	}
	return out, nil
}

func scanMember(sc scanner) (account.Membership, error) {
	var (
		m             account.Membership
		role, created string
	)
	if err := sc.Scan(&m.UserID, &m.Email, &m.Namespace, &role, &created); err != nil {
		return account.Membership{}, err
	}
	at, err := parseTime(created)
	if err != nil {
		return account.Membership{}, fmt.Errorf("stored membership %s/%s: %w", m.UserID, m.Namespace, err)
	}
	m.Role, m.CreatedAt = account.Role(role), at
	return m, nil
}

// CreateSession implements account.SessionStore.
func (s *Store) CreateSession(ctx context.Context, sess account.Session, hash []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hash, sess.UserID, formatTime(sess.CreatedAt), formatTime(sess.ExpiresAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("create session: %w", registry.ErrExists)
	}
	if isForeignKeyViolation(err) {
		return fmt.Errorf("create session: user %s: %w", sess.UserID, registry.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionByHash implements account.SessionStore.
func (s *Store) SessionByHash(ctx context.Context, hash []byte) (account.Session, error) {
	var (
		sess             account.Session
		created, expires string
	)
	err := s.db.QueryRowContext(ctx, `SELECT user_id, created_at, expires_at FROM sessions WHERE hash = ?`, hash).
		Scan(&sess.UserID, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return account.Session{}, fmt.Errorf("session: %w", registry.ErrNotFound)
	}
	if err != nil {
		return account.Session{}, fmt.Errorf("session: %w", err)
	}
	if sess.CreatedAt, err = parseTime(created); err != nil {
		return account.Session{}, fmt.Errorf("session: %w", err)
	}
	if sess.ExpiresAt, err = parseTime(expires); err != nil {
		return account.Session{}, fmt.Errorf("session: %w", err)
	}
	return sess, nil
}

// DeleteSession implements account.SessionStore.
func (s *Store) DeleteSession(ctx context.Context, hash []byte) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE hash = ?`, hash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions implements account.SessionStore.
func (s *Store) DeleteUserSessions(ctx context.Context, userID string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("delete sessions of %s: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete sessions of %s: %w", userID, err)
	}
	return int(n), nil
}

// DeleteExpiredSessions implements account.SessionStore.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	return s.deleteExpired(ctx, "sessions", now)
}

const challengeColumns = "id, user_id, purpose, code_hash, link_hash, password_hash, attempts, used, created_at, expires_at"

// CreateChallenge implements account.ChallengeStore.
func (s *Store) CreateChallenge(ctx context.Context, c account.Challenge) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO challenges (`+challengeColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.UserID, string(c.Purpose), c.CodeHash, c.LinkHash, nullString(c.PasswordHash), c.Attempts, c.Used,
		formatTime(c.CreatedAt), formatTime(c.ExpiresAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("create challenge %s: %w", c.ID, registry.ErrExists)
	}
	if isForeignKeyViolation(err) {
		return fmt.Errorf("create challenge %s: user %s: %w", c.ID, c.UserID, registry.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("create challenge %s: %w", c.ID, err)
	}
	return nil
}

// Challenge implements account.ChallengeStore.
func (s *Store) Challenge(ctx context.Context, id string) (account.Challenge, error) {
	return s.challengeWhere(ctx, "id = ?", id)
}

// ChallengeByLink implements account.ChallengeStore.
func (s *Store) ChallengeByLink(ctx context.Context, linkHash []byte) (account.Challenge, error) {
	return s.challengeWhere(ctx, "link_hash = ?", linkHash)
}

func (s *Store) challengeWhere(ctx context.Context, cond string, arg any) (account.Challenge, error) {
	var (
		c                account.Challenge
		purpose          string
		pw               sql.NullString
		created, expires string
	)
	err := s.db.QueryRowContext(ctx, `SELECT `+challengeColumns+` FROM challenges WHERE `+cond, arg).
		Scan(&c.ID, &c.UserID, &purpose, &c.CodeHash, &c.LinkHash, &pw, &c.Attempts, &c.Used, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return account.Challenge{}, fmt.Errorf("challenge: %w", registry.ErrNotFound)
	}
	if err != nil {
		return account.Challenge{}, fmt.Errorf("challenge: %w", err)
	}
	c.Purpose, c.PasswordHash = account.Purpose(purpose), pw.String
	if c.CreatedAt, err = parseTime(created); err != nil {
		return account.Challenge{}, fmt.Errorf("challenge %s: %w", c.ID, err)
	}
	if c.ExpiresAt, err = parseTime(expires); err != nil {
		return account.Challenge{}, fmt.Errorf("challenge %s: %w", c.ID, err)
	}
	return c, nil
}

// AddAttempt implements account.ChallengeStore.
func (s *Store) AddAttempt(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `UPDATE challenges SET attempts = attempts + 1 WHERE id = ? RETURNING attempts`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("challenge %s: %w", id, registry.ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("challenge %s: count attempt: %w", id, err)
	}
	return n, nil
}

// UseChallenge implements account.ChallengeStore.
func (s *Store) UseChallenge(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE challenges SET used = 1 WHERE id = ? AND used = 0`, id)
	return affectedOne(res, err, "use challenge "+id)
}

// DeleteExpiredChallenges implements account.ChallengeStore.
func (s *Store) DeleteExpiredChallenges(ctx context.Context, now time.Time) (int, error) {
	return s.deleteExpired(ctx, "challenges", now)
}

// CancelChallenges implements account.ChallengeStore.
func (s *Store) CancelChallenges(ctx context.Context, userID string, purpose account.Purpose) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE challenges SET used = 1 WHERE user_id = ? AND used = 0 AND (? = '' OR purpose = ?)`,
		userID, string(purpose), string(purpose))
	if err != nil {
		return 0, fmt.Errorf("cancel challenges of %s: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cancel challenges of %s: %w", userID, err)
	}
	return int(n), nil
}

// AddCodeFailure implements account.ChallengeStore.
func (s *Store) AddCodeFailure(ctx context.Context, userID string, purpose account.Purpose, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO code_failures (user_id, purpose, at) VALUES (?, ?, ?)`,
		userID, string(purpose), formatTime(at))
	if isForeignKeyViolation(err) {
		return fmt.Errorf("code failure of %s: %w", userID, registry.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("code failure of %s: %w", userID, err)
	}
	return nil
}

// CodeFailures implements account.ChallengeStore.
func (s *Store) CodeFailures(ctx context.Context, userID string, purpose account.Purpose, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM code_failures WHERE user_id = ? AND purpose = ? AND at >= ?`,
		userID, string(purpose), formatTime(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("code failures of %s: %w", userID, err)
	}
	return n, nil
}

// DeleteCodeFailures implements account.ChallengeStore.
func (s *Store) DeleteCodeFailures(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM code_failures WHERE at < ?`, formatTime(before))
	if err != nil {
		return 0, fmt.Errorf("delete old code failures: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete old code failures: %w", err)
	}
	return int(n), nil
}

// deleteExpired deletes rows of table (a constant) that expired before now.
func (s *Store) deleteExpired(ctx context.Context, table string, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE expires_at < ?`, formatTime(now)) //nolint:gosec // G202: table is a constant
	if err != nil {
		return 0, fmt.Errorf("delete expired %s: %w", table, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete expired %s: %w", table, err)
	}
	return int(n), nil
}

// affectedOne turns an Exec result into nil, or ErrNotFound when no row
// matched.
func affectedOne(res sql.Result, err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", what, registry.ErrNotFound)
	}
	return nil
}

func isForeignKeyViolation(err error) bool {
	se, ok := errors.AsType[*sqlite.Error](err)
	return ok && se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullTime(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(t), Valid: true}
}
