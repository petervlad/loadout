package account

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/petervlad/loadout/internal/pack"
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

// mayDeletePackage is the one check behind PackageFootprint and
// DeletePackage. The operator on the registry host may always; an admin
// signed in on the web may when this registry keeps copies of its database.
// Owners and the rest ask for a deletion instead (registry.DeletionService),
// and a login token never deletes directly. It runs before any lookup so a
// refused person learns nothing about the package.
func (s *Service) mayDeletePackage(ctx context.Context, actor Actor, id pack.ID) error {
	switch {
	case actor.Operator:
		return nil
	case actor.viaToken:
		return forbidden("packages are deleted on the web by an admin, or on the registry host; ask for the deletion with loadout propose delete")
	case s.snapshots == nil:
		return forbidden("deleting packages on the web needs a backup folder (LOADOUT_BACKUP_DIR) on the registry")
	}
	a, err := s.access(ctx, actor, id.Team())
	if err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	if !a.CanDeletePackage() {
		return forbidden("only an admin deletes " + id.String() + " at once; ask for the deletion instead, and a maintainer or owner of " + id.Team() + " approves it")
	}
	return nil
}

// PackageFootprint lists what DeletePackage would remove, for those who may
// delete the package: the counts cross namespaces, so even looking needs
// the right to delete.
func (s *Service) PackageFootprint(ctx context.Context, actor Actor, id pack.ID) (registry.PackageFootprint, error) {
	if err := s.mayDeletePackage(ctx, actor, id); err != nil {
		return registry.PackageFootprint{}, err
	}
	return s.reg.PackageFootprint(ctx, id)
}

// DeletePackage removes a package with every release and proposal, and
// returns what it removed. It cannot be undone, so only the operator on the
// registry host and an admin on the web may (see mayDeletePackage), and the
// delete says why: reason is kept in the registry's record of deletions
// with who deleted it. On the web a copy of the database is taken first,
// and if that fails nothing is deleted. One delete runs at a time, so two
// clicks, or two admins, never race on the same package.
func (s *Service) DeletePackage(ctx context.Context, actor Actor, id pack.ID, reason string) (registry.PackageFootprint, error) {
	if err := s.mayDeletePackage(ctx, actor, id); err != nil {
		return registry.PackageFootprint{}, err
	}
	if actor.Operator {
		if strings.TrimSpace(reason) == "" {
			reason = "Deleted on the registry host."
		}
		return s.deleteAndRecord(ctx, registry.Actor{TokenID: "operator", Label: "operator"}, id, reason)
	}
	if strings.TrimSpace(reason) == "" {
		return registry.PackageFootprint{}, fmt.Errorf("%w: say why %s is deleted", registry.ErrInvalid, id)
	}
	s.deleteMu.Lock()
	defer s.deleteMu.Unlock()
	// A missing package returns here, before any copy is taken.
	if _, err := s.reg.PackageFootprint(ctx, id); err != nil {
		return registry.PackageFootprint{}, err
	}
	backup, err := s.snapshots.Take(ctx, "before-delete-"+id.Team()+"-"+id.Name())
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("back up before deleting %s, nothing was deleted: %w: %w", id, ErrBackupFailed, err)
	}
	u, err := s.store.UserByID(ctx, actor.UserID)
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("delete %s: user %s: %w", id, actor.UserID, err)
	}
	fp, err := s.deleteAndRecord(ctx, registry.Actor{TokenID: webTokenPrefix + u.ID, Label: u.Email + " on the web", UserID: u.ID}, id, reason)
	if err != nil {
		return registry.PackageFootprint{}, err
	}
	s.log.InfoContext(ctx, "package deleted on the web", "package", id.String(), "user_id", actor.UserID, "backup", backup,
		"releases", len(fp.Releases), "proposals", fp.Proposals, "reviews", fp.Reviews, "prompts", fp.Prompts)
	return fp, nil
}

// deleteAndRecord deletes id and leaves the record of by and reason, when
// this registry keeps one.
func (s *Service) deleteAndRecord(ctx context.Context, by registry.Actor, id pack.ID, reason string) (registry.PackageFootprint, error) {
	if s.deletions == nil {
		return s.reg.DeletePackage(ctx, id)
	}
	return s.deletions.RecordDelete(ctx, by, id, reason)
}

// AddUser creates the account for email on the registry host, with a
// password, a verified address and the admin flag if asked. An account that
// exists keeps its password, so this never resets one: use
// SetPasswordAsOperator for that. Only the operator does this.
func (s *Service) AddUser(ctx context.Context, actor Actor, email, pw string, admin bool) error {
	if !actor.Operator {
		return forbidden("users are added on the registry host")
	}
	e, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	if _, err := s.store.UserByEmail(ctx, e); err == nil {
		return nil
	} else if !errors.Is(err, registry.ErrNotFound) {
		return fmt.Errorf("add user: %w", err)
	}
	if _, err := s.SetPasswordAsOperator(ctx, actor, e, pw); err != nil {
		return err
	}
	if admin {
		if _, err := s.SetAdmin(ctx, actor, e, true); err != nil {
			return err
		}
	}
	return nil
}
