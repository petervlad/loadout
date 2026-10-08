package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/password"
	"github.com/petervlad/loadout/internal/registry"
)

// ErrBadLogin is the one answer of SimpleLogin for a wrong password, an
// unknown email and a user without a password.
var ErrBadLogin = errors.New("wrong email or password")

// SimpleLogin is the sign-in of LOADOUT_LOGIN=simple: email and password,
// and a right one starts a session at once, with no mailed code. Every way
// to fail costs the same argon2id work and gives ErrBadLogin.
func (s *Service) SimpleLogin(ctx context.Context, email, pw string) (SessionGrant, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return SessionGrant{}, err
	}
	u, err := s.store.UserByEmail(ctx, email)
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		return SessionGrant{}, fmt.Errorf("sign in: %w", err)
	}
	if err != nil || u.PasswordHash == "" {
		if err := s.hasher.VerifyDummy(ctx, pw); err != nil {
			return SessionGrant{}, err
		}
		return SessionGrant{}, ErrBadLogin
	}
	ok, err := s.hasher.Verify(ctx, pw, u.PasswordHash)
	if errors.Is(err, password.ErrBusy) {
		return SessionGrant{}, err
	}
	if err != nil {
		return SessionGrant{}, fmt.Errorf("sign in: user %s: %w", u.ID, err)
	}
	if !ok {
		s.log.InfoContext(ctx, "sign-in with a wrong password", "user_id", u.ID)
		return SessionGrant{}, ErrBadLogin
	}
	return s.newSession(ctx, u)
}
