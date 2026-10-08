package accounttest

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/account"
)

// CountStore is an account store that also counts.
type CountStore interface {
	account.Store
	account.CountStore
}

// RunCountStoreContract runs the behaviour every account.CountStore must
// have. newBackend must return a fresh, empty backend for each call, whose
// Store is a CountStore.
func RunCountStoreContract(t *testing.T, newBackend func(t *testing.T) Backend) {
	t.Helper()
	t.Run("counts accounts", func(t *testing.T) {
		b := newBackend(t)
		s, ok := b.Store.(CountStore)
		if !ok {
			t.Fatalf("%T is not an account.CountStore", b.Store)
		}
		ctx := t.Context()
		if got, err := s.AccountCounts(ctx, at); err != nil || got.Users != 0 || len(got.Members) != 0 {
			t.Errorf("AccountCounts() of an empty store = %+v, %v; want zeros", got, err)
		}
		mustNamespace(t, b.Namespaces, "lime", "koala")
		mustCreate(t, s, User("u1", "alice@example.com"), User("u2", "bob@example.com"), User("u3", "carla@example.com"))
		if err := s.MarkVerified(ctx, "u1", at); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkVerified(ctx, "u2", at); err != nil {
			t.Fatal(err)
		}
		if err := s.SetAdmin(ctx, "u1", true); err != nil {
			t.Fatal(err)
		}
		for _, m := range []account.Membership{
			{UserID: "u1", Namespace: "lime", Role: account.RoleMaintainer, CreatedAt: at},
			{UserID: "u2", Namespace: "lime", Role: account.RoleMember, CreatedAt: at},
			{UserID: "u2", Namespace: "koala", Role: account.RoleMember, CreatedAt: at},
		} {
			if err := s.PutMember(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CreateSession(ctx, account.Session{UserID: "u1", CreatedAt: at, ExpiresAt: at.Add(time.Hour)}, hash("live")); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateSession(ctx, account.Session{UserID: "u2", CreatedAt: at, ExpiresAt: at.Add(-time.Hour)}, hash("old")); err != nil {
			t.Fatal(err)
		}
		got, err := s.AccountCounts(ctx, at)
		if err != nil {
			t.Fatalf("AccountCounts() error: %v", err)
		}
		want := account.Counts{
			Users: 3, Verified: 2, Admins: 1,
			Members:  map[account.Role]int{account.RoleMaintainer: 1, account.RoleMember: 2},
			Sessions: 1,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("AccountCounts() mismatch (-want +got):\n%s", diff)
		}
	})
}
