package account_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/device"
)

// revokeAdminOnRead is a store where the operator revokes a user's admin
// flag right after the first read of that user by email, as a concurrent
// `loadout admin revoke` would.
type revokeAdminOnRead struct {
	account.Store
	revoke func()
	once   sync.Once
}

func (s *revokeAdminOnRead) UserByEmail(ctx context.Context, email string) (account.User, error) {
	u, err := s.Store.UserByEmail(ctx, email)
	s.once.Do(s.revoke)
	return u, err
}

// TestUserChangesDoNotUndoEachOther: linking Google while an admin revoke
// happens must not write the stale admin flag back.
func TestUserChangesDoNotUndoEachOther(t *testing.T) {
	f := newFixture(t)
	ctx, op := t.Context(), account.Operator()
	if _, err := f.svc.SetAdmin(ctx, op, "alice@example.com", true); err != nil {
		t.Fatal(err)
	}
	racy := &revokeAdminOnRead{Store: f.store, revoke: func() {
		if _, err := f.svc.SetAdmin(ctx, op, "alice@example.com", false); err != nil {
			t.Errorf("SetAdmin(false) error: %v", err)
		}
	}}
	svc := account.NewService(racy, f.reg, f.roles, device.New(f.clock.now), account.Options{
		PublicURL: "https://registry.example.com",
		Mailer:    f.mails,
		Now:       f.clock.now,
		Log:       slog.New(slog.DiscardHandler),
	})
	if _, err := svc.GoogleSignIn(ctx, account.GoogleIdentity{Subject: "g-alice", Email: "alice@example.com", EmailVerified: true}); err != nil {
		t.Fatal(err)
	}
	u, err := f.store.UserByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAdmin {
		t.Errorf("alice is admin again after linking Google; the revoke was undone")
	}
	if u.GoogleSubject != "g-alice" || !u.Verified() {
		t.Errorf("alice = %+v, want linked to Google and verified", u)
	}
}
