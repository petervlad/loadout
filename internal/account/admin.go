package account

import (
	"context"
	"fmt"

	"github.com/petervlad/loadout/internal/registry"
)

// SetAdmin grants or revokes the admin flag. Only the operator on the
// registry host does this (`loadout admin grant|revoke`), so the first admin
// needs no admin. Granting creates the user if the address has no account
// yet. It returns the user.
func (s *Service) SetAdmin(ctx context.Context, actor Actor, email string, on bool) (User, error) {
	if !actor.Operator {
		return User{}, forbidden("admins are managed on the registry host")
	}
	e, err := NormalizeEmail(email)
	if err != nil {
		return User{}, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}
	var u User
	if on {
		u, err = s.userForEmail(ctx, e)
	} else {
		u, err = s.userByEmail(ctx, e)
	}
	if err != nil {
		return User{}, fmt.Errorf("set admin: %w", err)
	}
	u.IsAdmin = on
	if err := s.store.SetAdmin(ctx, u.ID, on); err != nil {
		return User{}, fmt.Errorf("set admin %s: %w", u.ID, err)
	}
	s.log.InfoContext(ctx, "admin flag set", "user_id", u.ID, "admin", on)
	return u, nil
}

// Admins lists the admins, for the operator.
func (s *Service) Admins(ctx context.Context, actor Actor) ([]User, error) {
	if !actor.Operator {
		return nil, forbidden("admins are listed on the registry host")
	}
	us, err := s.store.Admins(ctx)
	if err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	return us, nil
}

// DeleteUser removes the user with email: their memberships, sessions,
// pending codes and every token `loadout login` issued them. Only the
// operator does this. It returns how many tokens were revoked.
func (s *Service) DeleteUser(ctx context.Context, actor Actor, email string) (int, error) {
	if !actor.Operator {
		return 0, forbidden("users are deleted on the registry host")
	}
	u, err := s.userByEmail(ctx, email)
	if err != nil {
		return 0, fmt.Errorf("delete user: %w", err)
	}
	if err := s.store.DeleteUser(ctx, u.ID); err != nil {
		return 0, fmt.Errorf("delete user %s: %w", u.ID, err)
	}
	s.log.InfoContext(ctx, "user deleted", "user_id", u.ID)
	n, err := s.reg.RevokeUserTokens(ctx, u.ID, "")
	if err != nil {
		return 0, fmt.Errorf("user deleted, but revoking their tokens failed: %w", err)
	}
	return n, nil
}
