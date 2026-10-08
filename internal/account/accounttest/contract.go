package accounttest

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
)

// Backend is a store under test: the account store and the namespace
// store memberships refer to. With SQLite both are one database.
type Backend struct {
	Store      account.Store
	Namespaces registry.NamespaceStore
}

var at = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func hash(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// User returns a user for tests.
func User(id, email string) account.User {
	return account.User{ID: id, Email: email, CreatedAt: at}
}

func mustCreate(t *testing.T, s account.Store, users ...account.User) {
	t.Helper()
	for _, u := range users {
		if err := s.CreateUser(t.Context(), u); err != nil {
			t.Fatalf("CreateUser(%s) error: %v", u.ID, err)
		}
	}
}

func mustNamespace(t *testing.T, ns registry.NamespaceStore, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := ns.CreateNamespace(t.Context(), registry.Namespace{Name: n, Kind: registry.KindTeam, CreatedAt: at}); err != nil {
			t.Fatalf("CreateNamespace(%s) error: %v", n, err)
		}
	}
}

// RunStoreContract runs the behaviour every account store must have.
// newBackend must return a fresh, empty backend for each call.
func RunStoreContract(t *testing.T, newBackend func(t *testing.T) Backend) {
	t.Helper()
	t.Run("users", func(t *testing.T) { runUsers(t, newBackend(t)) })
	t.Run("memberships", func(t *testing.T) { runMembers(t, newBackend(t)) })
	t.Run("sessions", func(t *testing.T) { runSessions(t, newBackend(t)) })
	t.Run("challenges", func(t *testing.T) { runChallenges(t, newBackend(t)) })
	t.Run("code failures", func(t *testing.T) { runCodeFailures(t, newBackend(t)) })
	t.Run("delete user cascades", func(t *testing.T) { runDeleteUser(t, newBackend(t)) })
	t.Run("roles over the store", func(t *testing.T) { runRoles(t, newBackend(t)) })
}

func runUsers(t *testing.T, b Backend) {
	t.Helper()
	s := b.Store
	ctx := t.Context()
	alice := User("u1", "alice@example.com")
	mustCreate(t, s, alice)

	got, err := s.UserByEmail(ctx, "alice@example.com")
	if err != nil || got.ID != "u1" || got.PasswordHash != "" || got.GoogleSubject != "" || got.Verified() || !got.CreatedAt.Equal(at) {
		t.Fatalf("UserByEmail() = %+v, %v; want the new unverified user without password", got, err)
	}
	if _, err := s.UserByID(ctx, "ghost"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("UserByID(ghost) error = %v, want ErrNotFound", err)
	}
	if _, err := s.UserByEmail(ctx, "ghost@example.com"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("UserByEmail(ghost) error = %v, want ErrNotFound", err)
	}
	if _, err := s.UserByGoogle(ctx, ""); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("UserByGoogle(empty) error = %v, want ErrNotFound (users without Google must not match)", err)
	}
	if err := s.CreateUser(ctx, User("u2", "alice@example.com")); !errors.Is(err, registry.ErrExists) {
		t.Errorf("CreateUser(same email) error = %v, want ErrExists", err)
	}
	if err := s.CreateUser(ctx, User("u1", "other@example.com")); !errors.Is(err, registry.ErrExists) {
		t.Errorf("CreateUser(same id) error = %v, want ErrExists", err)
	}

	if admins, err := s.Admins(ctx); err != nil || len(admins) != 0 {
		t.Errorf("Admins() = %+v, %v; want none", admins, err)
	}
	if err := s.SetPassword(ctx, "u1", "$argon2id$x"); err != nil {
		t.Fatalf("SetPassword() error: %v", err)
	}
	if err := s.MarkVerified(ctx, "u1", at.Add(time.Minute)); err != nil {
		t.Fatalf("MarkVerified() error: %v", err)
	}
	if err := s.MarkVerified(ctx, "u1", at.Add(time.Hour)); err != nil {
		t.Fatalf("MarkVerified(again) error: %v", err)
	}
	if err := s.LinkGoogle(ctx, "u1", "g-1", "Alice"); err != nil {
		t.Fatalf("LinkGoogle() error: %v", err)
	}
	if err := s.LinkGoogle(ctx, "u1", "g-1", "Other name"); err != nil {
		t.Fatalf("LinkGoogle(same subject again) error: %v", err)
	}
	if err := s.SetAdmin(ctx, "u1", true); err != nil {
		t.Fatalf("SetAdmin() error: %v", err)
	}
	got, err = s.UserByGoogle(ctx, "g-1")
	if err != nil || got.ID != "u1" || got.Email != "alice@example.com" || got.PasswordHash != "$argon2id$x" || got.Name != "Alice" || !got.EmailVerifiedAt.Equal(at.Add(time.Minute)) || !got.IsAdmin {
		t.Errorf("UserByGoogle() after the setters = %+v, %v; want password, first verification time, name Alice, admin", got, err)
	}
	if admins, err := s.Admins(ctx); err != nil || len(admins) != 1 || admins[0].ID != "u1" {
		t.Errorf("Admins() = %+v, %v; want alice", admins, err)
	}
	// Each setter leaves the other columns alone.
	if err := s.SetAdmin(ctx, "u1", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(ctx, "u1", "$argon2id$y"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.UserByID(ctx, "u1"); err != nil || got.IsAdmin || got.PasswordHash != "$argon2id$y" || got.GoogleSubject != "g-1" || !got.Verified() {
		t.Errorf("UserByID() after SetAdmin(false), SetPassword = %+v, %v", got, err)
	}

	bob := User("u2", "bob@example.com")
	mustCreate(t, s, bob)
	if err := s.LinkGoogle(ctx, "u2", "g-1", ""); !errors.Is(err, registry.ErrExists) {
		t.Errorf("LinkGoogle(taken Google subject) error = %v, want ErrExists", err)
	}
	if err := s.LinkGoogle(ctx, "u1", "g-2", ""); !errors.Is(err, registry.ErrExists) {
		t.Errorf("LinkGoogle(user linked to another subject) error = %v, want ErrExists", err)
	}
	if got, err := s.UserByID(ctx, "u2"); err != nil || got.GoogleSubject != "" {
		t.Errorf("bob after a refused link = %+v, %v; want unlinked", got, err)
	}
	for name, err := range map[string]error{
		"SetPassword":  s.SetPassword(ctx, "ghost", "x"),
		"MarkVerified": s.MarkVerified(ctx, "ghost", at),
		"LinkGoogle":   s.LinkGoogle(ctx, "ghost", "g-3", ""),
		"SetAdmin":     s.SetAdmin(ctx, "ghost", true),
	} {
		if !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("%s(ghost) error = %v, want ErrNotFound", name, err)
		}
	}
	if err := s.DeleteUser(ctx, "ghost"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("DeleteUser(ghost) error = %v, want ErrNotFound", err)
	}
}

func runMembers(t *testing.T, b Backend) {
	t.Helper()
	s := b.Store
	ctx := t.Context()
	mustNamespace(t, b.Namespaces, "acme", "lime")
	mustCreate(t, s, User("u1", "zoe@example.com"), User("u2", "adam@example.com"))

	put := func(user, ns string, role account.Role) error {
		return s.PutMember(ctx, account.Membership{UserID: user, Namespace: ns, Role: role, CreatedAt: at})
	}
	for _, m := range []struct {
		user, ns string
		role     account.Role
	}{
		{"u1", "lime", account.RoleMember},
		{"u2", "lime", account.RoleMaintainer},
		{"u1", "acme", account.RoleOwner},
	} {
		if err := put(m.user, m.ns, m.role); err != nil {
			t.Fatalf("PutMember(%s, %s) error: %v", m.user, m.ns, err)
		}
	}
	if err := put("u1", "ghost", account.RoleMember); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("PutMember(missing namespace) error = %v, want ErrNotFound", err)
	}
	if err := put("ghost", "lime", account.RoleMember); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("PutMember(missing user) error = %v, want ErrNotFound", err)
	}

	// PutMembers writes all or none.
	err := s.PutMembers(ctx, []account.Membership{
		{UserID: "u2", Namespace: "acme", Role: account.RoleMember, CreatedAt: at},
		{UserID: "u2", Namespace: "ghost", Role: account.RoleMember, CreatedAt: at},
	})
	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("PutMembers(one missing namespace) error = %v, want ErrNotFound", err)
	}
	if _, err := s.Membership(ctx, "u2", "acme"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Membership(u2, acme) after a failed PutMembers: error = %v, want ErrNotFound (nothing written)", err)
	}
	if err := s.PutMembers(ctx, []account.Membership{
		{UserID: "u2", Namespace: "acme", Role: account.RoleMember, CreatedAt: at},
		{UserID: "ghost", Namespace: "lime", Role: account.RoleMember, CreatedAt: at},
	}); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("PutMembers(one missing user) error = %v, want ErrNotFound", err)
	}
	if _, err := s.Membership(ctx, "u2", "acme"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Membership(u2, acme) after a failed PutMembers: error = %v, want ErrNotFound (nothing written)", err)
	}
	if err := s.PutMembers(ctx, []account.Membership{
		{UserID: "u2", Namespace: "acme", Role: account.RoleMember, CreatedAt: at},
		{UserID: "u2", Namespace: "lime", Role: account.RoleMember, CreatedAt: at},
	}); err != nil {
		t.Fatalf("PutMembers() error: %v", err)
	}
	if m, err := s.Membership(ctx, "u2", "acme"); err != nil || m.Role != account.RoleMember {
		t.Errorf("Membership(u2, acme) = %+v, %v; want member", m, err)
	}
	if err := s.RemoveMember(ctx, "u2", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := put("u2", "lime", account.RoleMaintainer); err != nil {
		t.Fatal(err)
	}

	// Changing a role keeps one membership.
	if err := put("u1", "lime", account.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	m, err := s.Membership(ctx, "u1", "lime")
	if err != nil || m.Role != account.RoleMaintainer || m.Email != "zoe@example.com" {
		t.Errorf("Membership(u1, lime) = %+v, %v; want maintainer for zoe", m, err)
	}
	if _, err := s.Membership(ctx, "u2", "acme"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Membership(u2, acme) error = %v, want ErrNotFound", err)
	}

	members, err := s.Members(ctx, "lime")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].Email != "adam@example.com" || members[1].Email != "zoe@example.com" {
		t.Errorf("Members(lime) = %+v, want adam then zoe", members)
	}
	mine, err := s.UserMemberships(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 2 || mine[0].Namespace != "acme" || mine[1].Namespace != "lime" || mine[0].Email != "zoe@example.com" {
		t.Errorf("UserMemberships(u1) = %+v, want acme then lime", mine)
	}

	if err := s.RemoveMember(ctx, "u1", "lime"); err != nil {
		t.Fatalf("RemoveMember() error: %v", err)
	}
	if err := s.RemoveMember(ctx, "u1", "lime"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("second RemoveMember() error = %v, want ErrNotFound", err)
	}
	if _, err := s.Membership(ctx, "u1", "lime"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Membership() after remove error = %v, want ErrNotFound", err)
	}
}

func runSessions(t *testing.T, b Backend) {
	t.Helper()
	s := b.Store
	ctx := t.Context()
	mustCreate(t, s, User("u1", "alice@example.com"))
	live := account.Session{UserID: "u1", CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
	old := account.Session{UserID: "u1", CreatedAt: at, ExpiresAt: at.Add(-time.Hour)}
	if err := s.CreateSession(ctx, live, hash("live")); err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}
	if err := s.CreateSession(ctx, old, hash("old")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, live, hash("live")); !errors.Is(err, registry.ErrExists) {
		t.Errorf("CreateSession(same hash) error = %v, want ErrExists", err)
	}
	got, err := s.SessionByHash(ctx, hash("live"))
	if err != nil || got.UserID != "u1" || !got.ExpiresAt.Equal(live.ExpiresAt) {
		t.Errorf("SessionByHash() = %+v, %v; want %+v", got, err, live)
	}
	if n, err := s.DeleteExpiredSessions(ctx, at); err != nil || n != 1 {
		t.Errorf("DeleteExpiredSessions() = %d, %v; want 1", n, err)
	}
	if _, err := s.SessionByHash(ctx, hash("old")); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("expired session still there: %v", err)
	}
	if err := s.DeleteSession(ctx, hash("live")); err != nil {
		t.Fatalf("DeleteSession() error: %v", err)
	}
	if err := s.DeleteSession(ctx, hash("live")); err != nil {
		t.Errorf("DeleteSession(missing) error = %v, want nil", err)
	}
	if _, err := s.SessionByHash(ctx, hash("live")); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("SessionByHash() after delete error = %v, want ErrNotFound", err)
	}

	mustCreate(t, s, User("u2", "bob@example.com"))
	for _, h := range []string{"a1", "a2"} {
		if err := s.CreateSession(ctx, live, hash(h)); err != nil {
			t.Fatal(err)
		}
	}
	bobs := account.Session{UserID: "u2", CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
	if err := s.CreateSession(ctx, bobs, hash("b1")); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteUserSessions(ctx, "u1"); err != nil || n != 2 {
		t.Errorf("DeleteUserSessions(u1) = %d, %v; want 2", n, err)
	}
	if _, err := s.SessionByHash(ctx, hash("b1")); err != nil {
		t.Errorf("another user's session was deleted: %v", err)
	}
}

func runChallenges(t *testing.T, b Backend) {
	t.Helper()
	s := b.Store
	ctx := t.Context()
	mustCreate(t, s, User("u1", "alice@example.com"))
	c := account.Challenge{
		ID: "c1", UserID: "u1", Purpose: account.PurposeVerifySignup,
		CodeHash: hash("c1:123456"), LinkHash: hash("link1"), PasswordHash: "$argon2id$pending",
		CreatedAt: at, ExpiresAt: at.Add(15 * time.Minute),
	}
	if err := s.CreateChallenge(ctx, c); err != nil {
		t.Fatalf("CreateChallenge() error: %v", err)
	}
	dup := c
	dup.ID = "c2"
	if err := s.CreateChallenge(ctx, dup); !errors.Is(err, registry.ErrExists) {
		t.Errorf("CreateChallenge(same link) error = %v, want ErrExists", err)
	}

	got, err := s.Challenge(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != "u1" || got.Purpose != c.Purpose || !bytes.Equal(got.CodeHash, c.CodeHash) || got.PasswordHash != c.PasswordHash ||
		got.Attempts != 0 || got.Used || !got.ExpiresAt.Equal(c.ExpiresAt) {
		t.Errorf("Challenge() = %+v, want %+v", got, c)
	}
	byLink, err := s.ChallengeByLink(ctx, hash("link1"))
	if err != nil || byLink.ID != "c1" {
		t.Errorf("ChallengeByLink() = %+v, %v; want c1", byLink, err)
	}
	if _, err := s.ChallengeByLink(ctx, hash("other")); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("ChallengeByLink(other) error = %v, want ErrNotFound", err)
	}
	if _, err := s.AddAttempt(ctx, "ghost"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("AddAttempt(ghost) error = %v, want ErrNotFound", err)
	}

	// Parallel attempts are all counted.
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := s.AddAttempt(ctx, "c1"); err != nil {
				t.Errorf("AddAttempt() error: %v", err)
			}
		})
	}
	wg.Wait()
	if n, err := s.AddAttempt(ctx, "c1"); err != nil || n != 11 {
		t.Errorf("AddAttempt() after 10 parallel ones = %d, %v; want 11", n, err)
	}

	// A challenge is used once, even when two requests race.
	var used sync.WaitGroup
	wins := make(chan bool, 5)
	for range 5 {
		used.Go(func() { wins <- s.UseChallenge(ctx, "c1") == nil })
	}
	used.Wait()
	close(wins)
	n := 0
	for w := range wins {
		if w {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d of 5 concurrent UseChallenge() calls succeeded, want exactly 1", n)
	}
	if got, err := s.Challenge(ctx, "c1"); err != nil || !got.Used {
		t.Errorf("Challenge() after use = %+v, %v; want Used", got, err)
	}

	late := c
	late.ID, late.LinkHash, late.ExpiresAt = "c3", hash("link3"), at.Add(-time.Second)
	if err := s.CreateChallenge(ctx, late); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteExpiredChallenges(ctx, at); err != nil || n != 1 {
		t.Errorf("DeleteExpiredChallenges() = %d, %v; want 1", n, err)
	}
	if _, err := s.Challenge(ctx, "c1"); err != nil {
		t.Errorf("unexpired challenge deleted: %v", err)
	}

	// Cancelling marks open challenges of one purpose used, or of all.
	for _, o := range []account.Challenge{
		{ID: "s1", Purpose: account.PurposeVerifySignup},
		{ID: "s2", Purpose: account.PurposeVerifySignup},
		{ID: "l1", Purpose: account.PurposeLogin},
	} {
		o.UserID, o.CodeHash, o.LinkHash, o.CreatedAt, o.ExpiresAt = "u1", hash(o.ID), hash("link-"+o.ID), at, at.Add(time.Hour)
		if err := s.CreateChallenge(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CancelChallenges(ctx, "u1", account.PurposeVerifySignup); err != nil || n != 2 {
		t.Errorf("CancelChallenges(signup) = %d, %v; want 2 (c1 was used already)", n, err)
	}
	if got, err := s.Challenge(ctx, "l1"); err != nil || got.Used {
		t.Errorf("login challenge after cancelling signups = %+v, %v; want open", got, err)
	}
	if n, err := s.CancelChallenges(ctx, "u1", ""); err != nil || n != 1 {
		t.Errorf("CancelChallenges(all) = %d, %v; want 1", n, err)
	}
	if n, err := s.CancelChallenges(ctx, "ghost", ""); err != nil || n != 0 {
		t.Errorf("CancelChallenges(ghost) = %d, %v; want 0", n, err)
	}
}

func runCodeFailures(t *testing.T, b Backend) {
	t.Helper()
	s := b.Store
	ctx := t.Context()
	mustCreate(t, s, User("u1", "alice@example.com"), User("u2", "bob@example.com"))
	for i := range 3 {
		if err := s.AddCodeFailure(ctx, "u1", account.PurposeLogin, at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("AddCodeFailure() error: %v", err)
		}
	}
	if err := s.AddCodeFailure(ctx, "u1", account.PurposeVerifySignup, at); err != nil {
		t.Fatal(err)
	}
	if err := s.AddCodeFailure(ctx, "u2", account.PurposeLogin, at); err != nil {
		t.Fatal(err)
	}
	if err := s.AddCodeFailure(ctx, "ghost", account.PurposeLogin, at); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("AddCodeFailure(ghost) error = %v, want ErrNotFound", err)
	}
	if n, err := s.CodeFailures(ctx, "u1", account.PurposeLogin, at); err != nil || n != 3 {
		t.Errorf("CodeFailures(u1, login) = %d, %v; want 3", n, err)
	}
	if n, err := s.CodeFailures(ctx, "u1", account.PurposeLogin, at.Add(time.Hour)); err != nil || n != 2 {
		t.Errorf("CodeFailures(u1, login, since +1h) = %d, %v; want 2", n, err)
	}
	if n, err := s.DeleteCodeFailures(ctx, at.Add(time.Hour)); err != nil || n != 3 {
		t.Errorf("DeleteCodeFailures() = %d, %v; want 3", n, err)
	}
	if n, err := s.CodeFailures(ctx, "u1", account.PurposeLogin, at); err != nil || n != 2 {
		t.Errorf("CodeFailures(u1, login) after delete = %d, %v; want 2", n, err)
	}
	if err := s.DeleteUser(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CodeFailures(ctx, "u1", account.PurposeLogin, at); err != nil || n != 0 {
		t.Errorf("CodeFailures() of a deleted user = %d, %v; want 0", n, err)
	}
}

func runDeleteUser(t *testing.T, b Backend) {
	t.Helper()
	s := b.Store
	ctx := t.Context()
	mustNamespace(t, b.Namespaces, "lime")
	mustCreate(t, s, User("u1", "alice@example.com"), User("u2", "bob@example.com"))
	for _, u := range []string{"u1", "u2"} {
		if err := s.PutMember(ctx, account.Membership{UserID: u, Namespace: "lime", Role: account.RoleMember, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateSession(ctx, account.Session{UserID: u, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}, hash("s"+u)); err != nil {
			t.Fatal(err)
		}
		c := account.Challenge{ID: "c" + u, UserID: u, Purpose: account.PurposeLogin, CodeHash: hash("x"), LinkHash: hash("l" + u), CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
		if err := s.CreateChallenge(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteUser(ctx, "u1"); err != nil {
		t.Fatalf("DeleteUser() error: %v", err)
	}
	if _, err := s.UserByID(ctx, "u1"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("deleted user still there: %v", err)
	}
	if _, err := s.Membership(ctx, "u1", "lime"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("membership of the deleted user still there: %v", err)
	}
	if _, err := s.SessionByHash(ctx, hash("su1")); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("session of the deleted user still there: %v", err)
	}
	if _, err := s.Challenge(ctx, "cu1"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("challenge of the deleted user still there: %v", err)
	}
	if _, err := s.Membership(ctx, "u2", "lime"); err != nil {
		t.Errorf("the other user's membership went too: %v", err)
	}
	if _, err := s.SessionByHash(ctx, hash("su2")); err != nil {
		t.Errorf("the other user's session went too: %v", err)
	}
}

// runRoles checks the role rules against a real store: memberships, the
// namespace tree and the admin flag, through account.Roles.
func runRoles(t *testing.T, b Backend) {
	t.Helper()
	ctx := t.Context()
	for _, ns := range []registry.Namespace{
		{Name: "acme", Kind: registry.KindProject, CreatedAt: at},
		{Name: "lime", Parent: "acme", Kind: registry.KindTeam, CreatedAt: at},
		{Name: "other", Kind: registry.KindTeam, CreatedAt: at},
	} {
		if err := b.Namespaces.CreateNamespace(ctx, ns); err != nil {
			t.Fatal(err)
		}
	}
	admin := User("u-admin", "admin@example.com")
	admin.IsAdmin = true
	mustCreate(t, b.Store, admin,
		User("u-owner", "owner@example.com"),
		User("u-maint", "maint@example.com"),
		User("u-member", "member@example.com"),
		User("u-projmember", "projmember@example.com"),
		User("u-stranger", "stranger@example.com"))
	for _, m := range []account.Membership{
		{UserID: "u-owner", Namespace: "acme", Role: account.RoleOwner},
		{UserID: "u-maint", Namespace: "lime", Role: account.RoleMaintainer},
		{UserID: "u-member", Namespace: "lime", Role: account.RoleMember},
		{UserID: "u-projmember", Namespace: "acme", Role: account.RoleMember},
	} {
		m.CreatedAt = at
		if err := b.Store.PutMember(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	roles := account.NewRoles(b.Store, b.Namespaces)
	tests := []struct {
		user, ns string
		want     account.Access
		scope    registry.TokenScope
	}{
		{"u-admin", "lime", account.Access{Role: account.RoleAdmin, Governs: true}, registry.ScopePublish},
		{"u-admin", "other", account.Access{Role: account.RoleAdmin, Governs: true}, registry.ScopePublish},
		{"u-owner", "acme", account.Access{Role: account.RoleOwner, Governs: true}, registry.ScopePublish},
		{"u-owner", "lime", account.Access{Role: account.RoleOwner, Governs: true}, registry.ScopePublish}, // inherited
		{"u-owner", "other", account.Access{}, ""},
		{"u-maint", "lime", account.Access{Role: account.RoleMaintainer}, registry.ScopePublish},
		{"u-maint", "acme", account.Access{Role: account.RoleMember, Below: true}, registry.ScopeRead}, // a team member reads its project, never publishes
		{"u-member", "lime", account.Access{Role: account.RoleMember}, registry.ScopeRead},
		{"u-projmember", "acme", account.Access{Role: account.RoleMember}, registry.ScopeRead},
		{"u-projmember", "lime", account.Access{}, ""}, // a project member is no team member
		{"u-stranger", "lime", account.Access{}, ""},
		{"ghost", "lime", account.Access{}, ""},
		{"u-owner", "ghost", account.Access{}, ""},
	}
	for _, tt := range tests {
		got, err := roles.Access(ctx, tt.user, tt.ns)
		if err != nil || got != tt.want {
			t.Errorf("Access(%s, %s) = %+v, %v; want %+v", tt.user, tt.ns, got, err, tt.want)
		}
		scope, ok, err := roles.MemberScope(ctx, tt.user, tt.ns)
		if err != nil || scope != tt.scope || ok != (tt.scope != "") {
			t.Errorf("MemberScope(%s, %s) = %q, %v, %v; want %q", tt.user, tt.ns, scope, ok, err, tt.scope)
		}
	}
}
