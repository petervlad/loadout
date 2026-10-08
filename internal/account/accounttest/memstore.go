// Package accounttest provides an in-memory account store for tests and the
// contract suite every account.Store implementation must pass.
package accounttest

import (
	"bytes"
	"cmp"
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
)

// MemStore is an in-memory account.Store. The zero value is not usable; use
// NewMemStore.
type MemStore struct {
	namespaces registry.NamespaceStore

	mu         sync.Mutex
	users      map[string]account.User // by id
	members    map[memberKey]account.Membership
	sessions   map[string]account.Session // by hex hash
	challenges map[string]account.Challenge
	failures   []codeFailure
}

type codeFailure struct {
	user    string
	purpose account.Purpose
	at      time.Time
}

type memberKey struct{ user, namespace string }

var _ account.Store = (*MemStore)(nil)

// NewMemStore returns an empty store. Memberships may only name namespaces
// that exist in namespaces, as with the SQLite store.
func NewMemStore(namespaces registry.NamespaceStore) *MemStore {
	return &MemStore{
		namespaces: namespaces,
		users:      map[string]account.User{},
		members:    map[memberKey]account.Membership{},
		sessions:   map[string]account.Session{},
		challenges: map[string]account.Challenge{},
	}
}

// CreateUser implements account.UserStore.
func (m *MemStore) CreateUser(_ context.Context, u account.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.ID]; ok {
		return fmt.Errorf("user %s: %w", u.ID, registry.ErrExists)
	}
	if err := m.checkUnique(u); err != nil {
		return err
	}
	m.users[u.ID] = u
	return nil
}

// checkUnique returns ErrExists if another user has u's email or Google
// subject. m.mu must be held.
func (m *MemStore) checkUnique(u account.User) error {
	for _, o := range m.users {
		if o.ID == u.ID {
			continue
		}
		if o.Email == u.Email || (u.GoogleSubject != "" && o.GoogleSubject == u.GoogleSubject) {
			return fmt.Errorf("user %s: email or google subject: %w", u.ID, registry.ErrExists)
		}
	}
	return nil
}

// UserByID implements account.UserStore.
func (m *MemStore) UserByID(_ context.Context, id string) (account.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return account.User{}, registry.ErrNotFound
	}
	return u, nil
}

// UserByEmail implements account.UserStore.
func (m *MemStore) UserByEmail(_ context.Context, email string) (account.User, error) {
	return m.find(func(u account.User) bool { return u.Email == email })
}

// UserByGoogle implements account.UserStore.
func (m *MemStore) UserByGoogle(_ context.Context, subject string) (account.User, error) {
	return m.find(func(u account.User) bool { return subject != "" && u.GoogleSubject == subject })
}

func (m *MemStore) find(match func(account.User) bool) (account.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if match(u) {
			return u, nil
		}
	}
	return account.User{}, registry.ErrNotFound
}

// SetPassword implements account.UserStore.
func (m *MemStore) SetPassword(_ context.Context, id, hash string) error {
	return m.update(id, func(u *account.User) error { u.PasswordHash = hash; return nil })
}

// MarkVerified implements account.UserStore.
func (m *MemStore) MarkVerified(_ context.Context, id string, at time.Time) error {
	return m.update(id, func(u *account.User) error {
		if !u.Verified() {
			u.EmailVerifiedAt = at
		}
		return nil
	})
}

// LinkGoogle implements account.UserStore.
func (m *MemStore) LinkGoogle(_ context.Context, id, subject, name string) error {
	return m.update(id, func(u *account.User) error {
		if u.GoogleSubject != "" && u.GoogleSubject != subject {
			return fmt.Errorf("user %s: linked to another account: %w", id, registry.ErrExists)
		}
		u.GoogleSubject = subject
		if u.Name == "" {
			u.Name = name
		}
		return m.checkUnique(*u)
	})
}

// SetAdmin implements account.UserStore.
func (m *MemStore) SetAdmin(_ context.Context, id string, on bool) error {
	return m.update(id, func(u *account.User) error { u.IsAdmin = on; return nil })
}

// update changes one user under the lock; change's error leaves it as it
// was.
func (m *MemStore) update(id string, change func(*account.User) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("user %s: %w", id, registry.ErrNotFound)
	}
	if err := change(&u); err != nil {
		return err
	}
	m.users[id] = u
	return nil
}

// DeleteUser implements account.UserStore.
func (m *MemStore) DeleteUser(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[id]; !ok {
		return fmt.Errorf("user %s: %w", id, registry.ErrNotFound)
	}
	delete(m.users, id)
	for k := range m.members {
		if k.user == id {
			delete(m.members, k)
		}
	}
	for k, s := range m.sessions {
		if s.UserID == id {
			delete(m.sessions, k)
		}
	}
	for k, c := range m.challenges {
		if c.UserID == id {
			delete(m.challenges, k)
		}
	}
	m.failures = slices.DeleteFunc(m.failures, func(f codeFailure) bool { return f.user == id })
	return nil
}

// Admins implements account.UserStore.
func (m *MemStore) Admins(_ context.Context) ([]account.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []account.User
	for _, u := range m.users {
		if u.IsAdmin {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b account.User) int { return cmp.Compare(a.Email, b.Email) })
	return out, nil
}

// PutMember implements account.MemberStore.
func (m *MemStore) PutMember(ctx context.Context, ms account.Membership) error {
	// Checked before locking: the namespace store has its own lock.
	if _, err := m.namespaces.Namespace(ctx, ms.Namespace); err != nil {
		return fmt.Errorf("namespace %s: %w", ms.Namespace, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[ms.UserID]; !ok {
		return fmt.Errorf("user %s: %w", ms.UserID, registry.ErrNotFound)
	}
	k := memberKey{ms.UserID, ms.Namespace}
	if old, ok := m.members[k]; ok {
		ms.CreatedAt = old.CreatedAt
	}
	ms.Email = ""
	m.members[k] = ms
	return nil
}

// PutMembers implements account.MemberStore: everything is checked before
// anything is written.
func (m *MemStore) PutMembers(ctx context.Context, ms []account.Membership) error {
	for _, x := range ms {
		if _, err := m.namespaces.Namespace(ctx, x.Namespace); err != nil {
			return fmt.Errorf("namespace %s: %w", x.Namespace, err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range ms {
		if _, ok := m.users[x.UserID]; !ok {
			return fmt.Errorf("user %s: %w", x.UserID, registry.ErrNotFound)
		}
	}
	for _, x := range ms {
		k := memberKey{x.UserID, x.Namespace}
		if old, ok := m.members[k]; ok {
			x.CreatedAt = old.CreatedAt
		}
		x.Email = ""
		m.members[k] = x
	}
	return nil
}

// RemoveMember implements account.MemberStore.
func (m *MemStore) RemoveMember(_ context.Context, userID, namespace string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memberKey{userID, namespace}
	if _, ok := m.members[k]; !ok {
		return fmt.Errorf("member %s of %s: %w", userID, namespace, registry.ErrNotFound)
	}
	delete(m.members, k)
	return nil
}

// Membership implements account.MemberStore.
func (m *MemStore) Membership(_ context.Context, userID, namespace string) (account.Membership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms, ok := m.members[memberKey{userID, namespace}]
	if !ok {
		return account.Membership{}, registry.ErrNotFound
	}
	ms.Email = m.users[userID].Email
	return ms, nil
}

// Members implements account.MemberStore.
func (m *MemStore) Members(_ context.Context, namespace string) ([]account.Membership, error) {
	out := m.list(func(ms account.Membership) bool { return ms.Namespace == namespace })
	slices.SortFunc(out, func(a, b account.Membership) int { return cmp.Compare(a.Email, b.Email) })
	return out, nil
}

// UserMemberships implements account.MemberStore.
func (m *MemStore) UserMemberships(_ context.Context, userID string) ([]account.Membership, error) {
	out := m.list(func(ms account.Membership) bool { return ms.UserID == userID })
	slices.SortFunc(out, func(a, b account.Membership) int { return cmp.Compare(a.Namespace, b.Namespace) })
	return out, nil
}

func (m *MemStore) list(match func(account.Membership) bool) []account.Membership {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []account.Membership
	for _, ms := range m.members {
		if match(ms) {
			ms.Email = m.users[ms.UserID].Email
			out = append(out, ms)
		}
	}
	return out
}

// CreateSession implements account.SessionStore.
func (m *MemStore) CreateSession(_ context.Context, s account.Session, hash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := hex.EncodeToString(hash)
	if _, ok := m.sessions[k]; ok {
		return registry.ErrExists
	}
	if _, ok := m.users[s.UserID]; !ok {
		return fmt.Errorf("user %s: %w", s.UserID, registry.ErrNotFound)
	}
	m.sessions[k] = s
	return nil
}

// SessionByHash implements account.SessionStore.
func (m *MemStore) SessionByHash(_ context.Context, hash []byte) (account.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[hex.EncodeToString(hash)]
	if !ok {
		return account.Session{}, registry.ErrNotFound
	}
	return s, nil
}

// DeleteSession implements account.SessionStore.
func (m *MemStore) DeleteSession(_ context.Context, hash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, hex.EncodeToString(hash))
	return nil
}

// DeleteUserSessions implements account.SessionStore.
func (m *MemStore) DeleteUserSessions(_ context.Context, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, k)
			n++
		}
	}
	return n, nil
}

// DeleteExpiredSessions implements account.SessionStore.
func (m *MemStore) DeleteExpiredSessions(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, s := range m.sessions {
		if s.ExpiresAt.Before(now) {
			delete(m.sessions, k)
			n++
		}
	}
	return n, nil
}

// CreateChallenge implements account.ChallengeStore.
func (m *MemStore) CreateChallenge(_ context.Context, c account.Challenge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.challenges[c.ID]; ok {
		return registry.ErrExists
	}
	for _, o := range m.challenges {
		if bytes.Equal(o.LinkHash, c.LinkHash) {
			return registry.ErrExists
		}
	}
	if _, ok := m.users[c.UserID]; !ok {
		return fmt.Errorf("user %s: %w", c.UserID, registry.ErrNotFound)
	}
	c.CodeHash, c.LinkHash = bytes.Clone(c.CodeHash), bytes.Clone(c.LinkHash)
	m.challenges[c.ID] = c
	return nil
}

// Challenge implements account.ChallengeStore.
func (m *MemStore) Challenge(_ context.Context, id string) (account.Challenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	if !ok {
		return account.Challenge{}, registry.ErrNotFound
	}
	return c, nil
}

// ChallengeByLink implements account.ChallengeStore.
func (m *MemStore) ChallengeByLink(_ context.Context, linkHash []byte) (account.Challenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.challenges {
		if bytes.Equal(c.LinkHash, linkHash) {
			return c, nil
		}
	}
	return account.Challenge{}, registry.ErrNotFound
}

// AddAttempt implements account.ChallengeStore.
func (m *MemStore) AddAttempt(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	if !ok {
		return 0, registry.ErrNotFound
	}
	c.Attempts++
	m.challenges[id] = c
	return c.Attempts, nil
}

// UseChallenge implements account.ChallengeStore.
func (m *MemStore) UseChallenge(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	if !ok || c.Used {
		return registry.ErrNotFound
	}
	c.Used = true
	m.challenges[id] = c
	return nil
}

// DeleteExpiredChallenges implements account.ChallengeStore.
func (m *MemStore) DeleteExpiredChallenges(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, c := range m.challenges {
		if c.ExpiresAt.Before(now) {
			delete(m.challenges, k)
			n++
		}
	}
	return n, nil
}

// CancelChallenges implements account.ChallengeStore.
func (m *MemStore) CancelChallenges(_ context.Context, userID string, purpose account.Purpose) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, c := range m.challenges {
		if c.UserID == userID && !c.Used && (purpose == "" || c.Purpose == purpose) {
			c.Used = true
			m.challenges[k] = c
			n++
		}
	}
	return n, nil
}

// AddCodeFailure implements account.ChallengeStore.
func (m *MemStore) AddCodeFailure(_ context.Context, userID string, purpose account.Purpose, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return fmt.Errorf("user %s: %w", userID, registry.ErrNotFound)
	}
	m.failures = append(m.failures, codeFailure{user: userID, purpose: purpose, at: at})
	return nil
}

// CodeFailures implements account.ChallengeStore.
func (m *MemStore) CodeFailures(_ context.Context, userID string, purpose account.Purpose, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, f := range m.failures {
		if f.user == userID && f.purpose == purpose && !f.at.Before(since) {
			n++
		}
	}
	return n, nil
}

// DeleteCodeFailures implements account.ChallengeStore.
func (m *MemStore) DeleteCodeFailures(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.failures[:0]
	for _, f := range m.failures {
		if !f.at.Before(before) {
			kept = append(kept, f)
		}
	}
	n := len(m.failures) - len(kept)
	m.failures = kept
	return n, nil
}
