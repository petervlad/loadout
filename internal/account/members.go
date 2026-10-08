package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/registry"
)

// AddMember gives the user with email a role in namespace, or changes it.
// The user is created (without a password) if the address has no account
// yet; they take it over by signing up or signing in with Google. Owner is
// only given on a project. Maintainers add members, those who govern the
// namespace add maintainers, admins add owners. It reports whether the
// user was created.
func (s *Service) AddMember(ctx context.Context, actor Actor, namespace, email string, role Role) (Membership, bool, error) {
	return s.putMember(ctx, actor, namespace, email, role, false)
}

// SetMember changes the role of an existing member, with the rules of
// AddMember.
func (s *Service) SetMember(ctx context.Context, actor Actor, namespace, email string, role Role) (Membership, error) {
	m, _, err := s.putMember(ctx, actor, namespace, email, role, true)
	return m, err
}

func (s *Service) putMember(ctx context.Context, actor Actor, namespace, email string, role Role, existing bool) (Membership, bool, error) {
	if !role.Valid() {
		return Membership{}, false, fmt.Errorf("%w: role %q: want member, maintainer or owner", registry.ErrInvalid, role)
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return Membership{}, false, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}
	ns, err := s.namespace(ctx, namespace)
	if err != nil {
		return Membership{}, false, fmt.Errorf("member of %s: %w", namespace, err)
	}
	if role == RoleOwner && ns.Kind != registry.KindProject {
		return Membership{}, false, fmt.Errorf("%w: owner is a role on projects; %s is a %s", registry.ErrInvalid, namespace, ns.Kind)
	}
	a, err := s.access(ctx, actor, namespace)
	if err != nil {
		return Membership{}, false, err
	}
	if !a.CanAssign(role) {
		return Membership{}, false, forbidden(fmt.Sprintf("you cannot give the %s role in %s", role, namespace))
	}

	u, err := s.store.UserByEmail(ctx, email)
	created := errors.Is(err, registry.ErrNotFound)
	if err != nil && !created {
		return Membership{}, false, fmt.Errorf("member of %s: %w", namespace, err)
	}
	var old Membership
	if !created {
		old, err = s.store.Membership(ctx, u.ID, namespace)
		if err != nil && !errors.Is(err, registry.ErrNotFound) {
			return Membership{}, false, fmt.Errorf("member of %s: %w", namespace, err)
		}
	}
	if existing && old.Role == "" {
		return Membership{}, false, fmt.Errorf("%s is not a member of %s: %w", email, namespace, registry.ErrNotFound)
	}
	if old.Role != "" && !a.CanAssign(old.Role) {
		return Membership{}, false, forbidden(fmt.Sprintf("%s is a %s of %s, which you cannot change", email, old.Role, namespace))
	}
	if created {
		if u, err = s.userForEmail(ctx, email); err != nil {
			return Membership{}, false, fmt.Errorf("member of %s: %w", namespace, err)
		}
	}
	m := Membership{UserID: u.ID, Email: u.Email, Namespace: namespace, Role: role, CreatedAt: s.now().UTC()}
	if err := s.store.PutMember(ctx, m); err != nil {
		return Membership{}, false, fmt.Errorf("member %s of %s: %w", u.ID, namespace, err)
	}
	s.log.InfoContext(ctx, "member role set", "user_id", u.ID, "namespace", namespace, "role", role, "by", actorLog(actor))
	return m, created, nil
}

// RemoveMember takes the user with email out of namespace and revokes the
// tokens `loadout login` issued them there. Removing needs the right to
// give the member's role. It returns how many tokens were revoked.
func (s *Service) RemoveMember(ctx context.Context, actor Actor, namespace, email string) (int, error) {
	u, err := s.userByEmail(ctx, email)
	if err != nil {
		return 0, fmt.Errorf("remove member: %w", err)
	}
	m, err := s.store.Membership(ctx, u.ID, namespace)
	if err != nil {
		return 0, fmt.Errorf("remove member %s from %s: %w", u.Email, namespace, err)
	}
	a, err := s.access(ctx, actor, namespace)
	if err != nil {
		return 0, err
	}
	if !a.CanAssign(m.Role) {
		return 0, forbidden(fmt.Sprintf("you cannot remove a %s of %s", m.Role, namespace))
	}
	if err := s.store.RemoveMember(ctx, u.ID, namespace); err != nil {
		return 0, fmt.Errorf("remove member %s from %s: %w", u.ID, namespace, err)
	}
	s.log.InfoContext(ctx, "member removed", "user_id", u.ID, "namespace", namespace, "by", actorLog(actor))
	n, err := s.reg.RevokeUserTokens(ctx, u.ID, namespace)
	if err != nil {
		// The registry refuses the tokens anyway: the role is gone.
		return 0, fmt.Errorf("member removed, but revoking their tokens failed: %w", err)
	}
	return n, nil
}

// Members lists the members of a namespace, sorted by email. Maintainers
// and above may list them.
func (s *Service) Members(ctx context.Context, actor Actor, namespace string) ([]Membership, error) {
	a, err := s.access(ctx, actor, namespace)
	if err != nil {
		return nil, err
	}
	if !a.CanListMembers() {
		return nil, forbidden("listing members of " + namespace + " needs the maintainer role")
	}
	ms, err := s.store.Members(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("members of %s: %w", namespace, err)
	}
	return ms, nil
}

func (s *Service) userByEmail(ctx context.Context, email string) (User, error) {
	e, err := NormalizeEmail(email)
	if err != nil {
		return User{}, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}
	u, err := s.store.UserByEmail(ctx, e)
	if errors.Is(err, registry.ErrNotFound) {
		return User{}, fmt.Errorf("no user with email %s: %w", e, registry.ErrNotFound)
	}
	return u, err
}

// actorLog names an actor in logs.
func actorLog(a Actor) string {
	if a.Operator {
		return "operator"
	}
	return a.UserID
}
