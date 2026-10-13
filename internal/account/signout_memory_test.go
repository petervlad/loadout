package account_test

import (
	"context"
	"errors"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
)

type emailUsers struct{ f *fixture }

func (e emailUsers) Email(ctx context.Context, id string) (string, error) {
	u, err := e.f.store.UserByID(ctx, id)
	if err != nil {
		return "", err
	}
	return u.Email, nil
}

// TestSignOutEverywhereRevokesMemoryTokens: the memory tokens of the person
// end with their sessions and login tokens, and another person's stay.
func TestSignOutEverywhereRevokesMemoryTokens(t *testing.T) {
	var tokens *memory.Tokens
	f := newFixtureWith(t, func(o *account.Options) {
		// The fixture's store does not exist yet, so the lookup is bound below.
		o.MemoryTokens = revokerFunc(func(ctx context.Context, id string) (int, error) { return tokens.RevokeAll(ctx, id) })
	})
	var err error
	tokens, err = memory.NewTokens(memorytest.NewMemTokenStore(), emailUsers{f}, f.clock.now, nil)
	if err != nil {
		t.Fatal(err)
	}
	alice := f.signup(t, "alice@example.com").User
	bob := f.signup(t, "bob@example.com").User
	aliceSecret, _, err := tokens.Create(t.Context(), alice.ID, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	bobSecret, _, _ := tokens.Create(t.Context(), bob.ID, "laptop")
	if _, err := tokens.Authenticate(t.Context(), aliceSecret); err != nil {
		t.Fatalf("Authenticate() before sign out: %v", err)
	}

	sessions, n, err := f.svc.SignOutEverywhere(t.Context(), alice.ID)
	if err != nil || sessions != 1 || n != 1 {
		t.Errorf("SignOutEverywhere() = %d sessions, %d tokens, %v, want 1 and 1 (the memory token)", sessions, n, err)
	}
	if _, err := tokens.Authenticate(t.Context(), aliceSecret); !errors.Is(err, memory.ErrUnauthorized) {
		t.Errorf("Authenticate(alice) after sign out = %v, want ErrUnauthorized", err)
	}
	if _, err := tokens.Authenticate(t.Context(), bobSecret); err != nil {
		t.Errorf("Authenticate(bob) after alice signed out: %v", err)
	}
}

// TestSignOutEverywhereWithoutMemory: a registry with no team memory signs
// out as before.
func TestSignOutEverywhereWithoutMemory(t *testing.T) {
	f := newFixture(t)
	alice := f.signup(t, "alice@example.com").User
	if sessions, n, err := f.svc.SignOutEverywhere(t.Context(), alice.ID); err != nil || sessions != 1 || n != 0 {
		t.Errorf("SignOutEverywhere() = %d, %d, %v, want 1 session and no tokens", sessions, n, err)
	}
}

// TestSignOutEverywhereMemoryFailure: a failing revoke is reported.
func TestSignOutEverywhereMemoryFailure(t *testing.T) {
	boom := errors.New("disk full")
	f := newFixtureWith(t, func(o *account.Options) {
		o.MemoryTokens = revokerFunc(func(context.Context, string) (int, error) { return 0, boom })
	})
	alice := f.signup(t, "alice@example.com").User
	if _, _, err := f.svc.SignOutEverywhere(t.Context(), alice.ID); !errors.Is(err, boom) {
		t.Errorf("SignOutEverywhere() error = %v, want the revoke failure", err)
	}
}

type revokerFunc func(ctx context.Context, userID string) (int, error)

func (r revokerFunc) RevokeAll(ctx context.Context, userID string) (int, error) {
	return r(ctx, userID)
}
