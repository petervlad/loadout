package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/registry"
)

// Role is what a user may do in a namespace. Member, maintainer and owner
// are stored on memberships; admin comes from the user's flag and applies
// everywhere.
type Role string

// Roles, lowest first.
const (
	// RoleMember installs, syncs and proposes (a team or a project).
	RoleMember Role = "member"
	// RoleMaintainer also publishes, approves and rejects proposals, and
	// manages the team's members and machine tokens.
	RoleMaintainer Role = "maintainer"
	// RoleOwner is given on a project: it creates teams below it, assigns
	// their maintainers, and is an owner of every team below.
	RoleOwner Role = "owner"
	// RoleAdmin is the registry-wide flag: everything, everywhere.
	RoleAdmin Role = "admin"
)

// Valid reports whether r can be stored on a membership.
func (r Role) Valid() bool { return r == RoleMember || r == RoleMaintainer || r == RoleOwner }

func (r Role) rank() int {
	switch r {
	case RoleMember:
		return 1
	case RoleMaintainer:
		return 2
	case RoleOwner:
		return 3
	case RoleAdmin:
		return 4
	}
	return 0
}

// AtLeast reports whether r is o or above. The empty role is below all.
func (r Role) AtLeast(o Role) bool { return r.rank() >= o.rank() && r.rank() > 0 }

// Access is what one user may do in one namespace: their effective role,
// and whether they govern it.
type Access struct {
	// Role is the effective role: the membership's role, raised to owner
	// by ownership of an ancestor project, or to member by a membership in
	// a team below; or admin. Empty means no access.
	Role Role
	// Governs is true for an admin and for an owner of the namespace or
	// of one of its ancestors. Governing means assigning maintainers,
	// creating teams below and turning review-only publishing on or off.
	Governs bool
	// Below is true when Role comes only from a membership in a team
	// below: the user reads the namespace and may propose to it.
	Below bool
}

// Resolve computes a user's access to the first namespace of chain (the
// namespace, then its ancestors, closest first) from their memberships by
// namespace. Inherited rights are computed here, never stored. Membership
// in a team below raises no access here; Roles adds it, since it needs the
// namespace tree.
func Resolve(u User, chain []registry.Layer, memberships map[string]Role) Access {
	if u.IsAdmin {
		return Access{Role: RoleAdmin, Governs: true}
	}
	if len(chain) == 0 {
		return Access{}
	}
	a := Access{Role: memberships[chain[0].Namespace]}
	if a.Role == RoleOwner {
		a.Governs = true
	}
	for _, l := range chain[1:] {
		if memberships[l.Namespace] == RoleOwner {
			a.Governs = true
			a.Role = RoleOwner
		}
	}
	return a
}

// CanRead reports whether the user may install, sync and list the
// namespace (and its ancestors).
func (a Access) CanRead() bool { return a.Role.AtLeast(RoleMember) }

// CanPropose reports whether the user may propose a version for review.
func (a Access) CanPropose() bool { return a.Role.AtLeast(RoleMember) }

// CanPublish reports whether the user may publish directly: maintainers
// and above, unless the namespace requires review.
func (a Access) CanPublish(requireReview bool) bool {
	return a.Role.AtLeast(RoleMaintainer) && !requireReview
}

// CanApprove reports whether the user may approve or reject someone
// else's proposal.
func (a Access) CanApprove() bool { return a.Role.AtLeast(RoleMaintainer) }

// CanManageTokens reports whether the user may create, list and revoke the
// namespace's machine tokens.
func (a Access) CanManageTokens() bool { return a.Role.AtLeast(RoleMaintainer) }

// CanListMembers reports whether the user may see who belongs to the
// namespace.
func (a Access) CanListMembers() bool { return a.Role.AtLeast(RoleMember) }

// CanAssign reports whether the user may give (or take away) role r in the
// namespace: members by maintainers, maintainers by those who govern it,
// owners only by admins.
func (a Access) CanAssign(r Role) bool {
	switch r {
	case RoleMember:
		return a.Role.AtLeast(RoleMaintainer)
	case RoleMaintainer:
		return a.Governs
	case RoleOwner:
		return a.Role == RoleAdmin
	}
	return false
}

// CanCreateChild reports whether the user may create a namespace of kind
// under this one (of parentKind): admins anything, and those who govern a
// project may create teams in it.
func (a Access) CanCreateChild(parentKind, kind registry.NamespaceKind) bool {
	if a.Role == RoleAdmin {
		return true
	}
	return a.Governs && parentKind == registry.KindProject && kind == registry.KindTeam
}

// CanSetReview reports whether the user may turn review-only publishing on
// or off.
func (a Access) CanSetReview() bool { return a.Governs }

// TokenScope is the scope of a `loadout login` token for this access:
// publish for maintainers and above, read for members, none otherwise.
func (a Access) TokenScope() (registry.TokenScope, bool) {
	switch {
	case a.Role.AtLeast(RoleMaintainer):
		return registry.ScopePublish, true
	case a.Role.AtLeast(RoleMember):
		return registry.ScopeRead, true
	}
	return "", false
}

// Roles resolves users' access from their memberships, the namespace tree
// and the admin flag. It is what the registry asks about every request
// made with a `loadout login` token.
type Roles struct {
	store      Store
	namespaces registry.NamespaceStore
}

var (
	_ registry.Memberships = (*Roles)(nil)
	_ registry.Owners      = (*Roles)(nil)
)

// NewRoles returns a resolver over the account store and the namespaces.
func NewRoles(store Store, namespaces registry.NamespaceStore) *Roles {
	return &Roles{store: store, namespaces: namespaces}
}

// Access returns what user userID may do in namespace. An unknown user has
// no access; an unknown namespace gives access only to admins.
func (r *Roles) Access(ctx context.Context, userID, namespace string) (Access, error) {
	u, err := r.store.UserByID(ctx, userID)
	if errors.Is(err, registry.ErrNotFound) {
		return Access{}, nil
	}
	if err != nil {
		return Access{}, fmt.Errorf("access of %s: %w", userID, err)
	}
	return r.accessOf(ctx, u, namespace)
}

func (r *Roles) accessOf(ctx context.Context, u User, namespace string) (Access, error) {
	if u.IsAdmin {
		return Resolve(u, nil, nil), nil
	}
	chain, err := registry.Chain(ctx, r.namespaces, namespace)
	if errors.Is(err, registry.ErrNotFound) {
		return Access{}, nil
	}
	if err != nil {
		return Access{}, fmt.Errorf("access to %s: %w", namespace, err)
	}
	ms, err := r.store.UserMemberships(ctx, u.ID)
	if err != nil {
		return Access{}, fmt.Errorf("access of %s: %w", u.ID, err)
	}
	byNS := make(map[string]Role, len(ms))
	for _, m := range ms {
		byNS[m.Namespace] = m.Role
	}
	a := Resolve(u, chain, byNS)
	if a.Role == "" {
		// A member of a team reads its project and may propose to it; a
		// maintainer there is still only a member here.
		for _, m := range ms {
			below, err := r.within(ctx, m.Namespace, namespace)
			if err != nil {
				return Access{}, err
			}
			if below {
				a.Role, a.Below = RoleMember, true
				break
			}
		}
	}
	return a, nil
}

// accessAsMember is Access without the admin flag: what the user's
// memberships alone allow.
func (r *Roles) accessAsMember(ctx context.Context, userID, namespace string) (Access, error) {
	u, err := r.store.UserByID(ctx, userID)
	if errors.Is(err, registry.ErrNotFound) {
		return Access{}, nil
	}
	if err != nil {
		return Access{}, fmt.Errorf("access of %s: %w", userID, err)
	}
	u.IsAdmin = false
	return r.accessOf(ctx, u, namespace)
}

// Owns implements registry.Owners: whether the user's memberships make them
// an owner of namespace, directly or through a project above it. The admin
// flag does not count, so a stolen admin token cannot skip review
// everywhere. An unknown user or namespace owns nothing.
func (r *Roles) Owns(ctx context.Context, userID, namespace string) (bool, error) {
	a, err := r.accessAsMember(ctx, userID, namespace)
	if err != nil {
		return false, err
	}
	return a.Governs, nil
}

// within reports whether namespace is root or lies below it. An unknown
// namespace is not within anything.
func (r *Roles) within(ctx context.Context, namespace, root string) (bool, error) {
	chain, err := registry.Chain(ctx, r.namespaces, namespace)
	if errors.Is(err, registry.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("namespace %s: %w", namespace, err)
	}
	for _, l := range chain {
		if l.Namespace == root {
			return true, nil
		}
	}
	return false, nil
}

// MemberScope implements registry.Memberships: the scope a login token of
// the user has in namespace right now.
func (r *Roles) MemberScope(ctx context.Context, userID, namespace string) (registry.TokenScope, bool, error) {
	a, err := r.Access(ctx, userID, namespace)
	if err != nil {
		return "", false, err
	}
	scope, ok := a.TokenScope()
	return scope, ok, nil
}
