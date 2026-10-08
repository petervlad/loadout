package account

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/petervlad/loadout/internal/password"
)

// FirstAdminEmail is the sign-in of the account serve creates in an empty
// registry. Addresses need a dot in the domain, so "admin@localhost" is not
// one; ".local" can never receive mail, which is why only simple login
// (no mailed code) can use it.
const FirstAdminEmail = "admin@loadout.local"

// generatedLength and generatedAlphabet make the password serve invents:
// 24 letters and digits, about 143 bits, easy to copy from a terminal.
const (
	generatedLength   = 24
	generatedAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
)

// FirstAdmin says what EnsureFirstAdmin did.
type FirstAdmin struct {
	// Created is false when the registry already had accounts.
	Created bool
	// Email is the new admin's sign-in.
	Email string
	// Password is set only when it was generated; the caller shows it once
	// and keeps it nowhere. A password the operator chose is never echoed.
	Password string
}

// EnsureFirstAdmin creates the first admin when the registry has no
// accounts at all, so a fresh deployment can be signed in to at once. With
// any account present it does nothing, which also means deleting the admin
// later does not bring it back unless the table is empty again. configured
// is the operator's own password (LOADOUT_ADMIN_PASSWORD); empty makes one
// up. A configured password that password.Check refuses is an error, and
// nothing is created.
func (s *Service) EnsureFirstAdmin(ctx context.Context, counts CountStore, configured string) (FirstAdmin, error) {
	c, err := counts.AccountCounts(ctx, s.now())
	if err != nil {
		return FirstAdmin{}, fmt.Errorf("first admin: %w", err)
	}
	if c.Users > 0 {
		return FirstAdmin{}, nil
	}
	pw, shown := configured, ""
	if pw == "" {
		if pw, err = GeneratePassword(); err != nil {
			return FirstAdmin{}, fmt.Errorf("first admin: %w", err)
		}
		shown = pw
	}
	if err := password.Check(pw, FirstAdminEmail); err != nil {
		return FirstAdmin{}, fmt.Errorf("first admin: LOADOUT_ADMIN_PASSWORD is refused (%w); choose a longer one or unset it to get a generated password", err)
	}
	op := Operator()
	if _, err := s.SetAdmin(ctx, op, FirstAdminEmail, true); err != nil {
		return FirstAdmin{}, fmt.Errorf("first admin: %w", err)
	}
	if _, err := s.SetPasswordAsOperator(ctx, op, FirstAdminEmail, pw); err != nil {
		return FirstAdmin{}, fmt.Errorf("first admin: %w", err)
	}
	return FirstAdmin{Created: true, Email: FirstAdminEmail, Password: shown}, nil
}

// GeneratePassword returns generatedLength random characters that
// password.Check accepts. It draws from crypto/rand without modulo bias.
func GeneratePassword() (string, error) {
	size := big.NewInt(int64(len(generatedAlphabet)))
	for range 10 {
		b := make([]byte, generatedLength)
		for i := range b {
			n, err := rand.Int(rand.Reader, size)
			if err != nil {
				return "", fmt.Errorf("generate password: %w", err)
			}
			b[i] = generatedAlphabet[n.Int64()]
		}
		if password.Check(string(b), FirstAdminEmail) == nil {
			return string(b), nil
		}
	}
	return "", errors.New("generate password: no acceptable password after several tries")
}
