package account_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/password"
)

// TestHashingIsCapped: when no hashing slot is free, sign-up and sign-in
// (the decoy work for unknown addresses included) answer ErrBusy instead
// of piling up argon2 work.
func TestHashingIsCapped(t *testing.T) {
	f := newFixture(t)
	f.signup(t, "alice@example.com")
	busy := account.NewService(f.store, f.reg, f.roles, device.New(f.clock.now), account.Options{
		PublicURL: "https://registry.example.com",
		Mailer:    f.mails,
		Hasher:    password.NewHasher(0, time.Millisecond),
		Now:       f.clock.now,
		Log:       slog.New(slog.DiscardHandler),
	})
	if _, err := busy.Signup(t.Context(), "bob@example.com", pw); !errors.Is(err, password.ErrBusy) {
		t.Errorf("Signup() while busy error = %v, want ErrBusy", err)
	}
	if _, err := busy.Login(t.Context(), "alice@example.com", pw); !errors.Is(err, password.ErrBusy) {
		t.Errorf("Login() while busy error = %v, want ErrBusy", err)
	}
	if _, err := busy.Login(t.Context(), "nobody@example.com", pw); !errors.Is(err, password.ErrBusy) {
		t.Errorf("Login(unknown address) while busy error = %v, want ErrBusy (the decoy hash is capped too)", err)
	}
}
