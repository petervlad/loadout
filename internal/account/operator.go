package account

import (
	"context"
	"fmt"

	"github.com/petervlad/loadout/internal/password"
)

// SetPasswordAsOperator gives the account with this email a new password,
// creating the account when it does not exist. It is how the first admin
// and every simple-login user get a password. The operator vouches for the
// address, so it is marked verified, and the user's other sessions end
// because the password they were started with is gone.
func (s *Service) SetPasswordAsOperator(ctx context.Context, actor Actor, email, pw string) (User, error) {
	if !actor.Operator {
		return User{}, forbidden("passwords are set on the registry host")
	}
	e, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	if err := password.Check(pw, e); err != nil {
		return User{}, err
	}
	hash, err := s.hasher.Hash(ctx, pw)
	if err != nil {
		return User{}, fmt.Errorf("set password: %w", err)
	}
	u, err := s.userForEmail(ctx, e)
	if err != nil {
		return User{}, fmt.Errorf("set password: %w", err)
	}
	if err := s.store.SetPassword(ctx, u.ID, hash); err != nil {
		return User{}, fmt.Errorf("set password %s: %w", u.ID, err)
	}
	if err := s.store.MarkVerified(ctx, u.ID, s.now().UTC()); err != nil {
		return User{}, fmt.Errorf("set password %s: %w", u.ID, err)
	}
	if _, err := s.store.DeleteUserSessions(ctx, u.ID); err != nil {
		return User{}, fmt.Errorf("set password %s: %w", u.ID, err)
	}
	s.log.InfoContext(ctx, "password set by the operator", "user_id", u.ID)
	return s.store.UserByID(ctx, u.ID)
}
