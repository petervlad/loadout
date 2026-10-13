package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// CanReadMemory reports whether the user may read the namespace's team
// memory. Members of a team below read the project's memory too.
func (a Access) CanReadMemory() bool { return a.CanRead() }

// CanWriteMemory reports whether the user may save and update memory in a
// namespace of this kind: members and above in a team, maintainers and above
// in a project. A member of a team below never writes the project's memory.
func (a Access) CanWriteMemory(kind registry.NamespaceKind) bool {
	if a.Below {
		return false
	}
	if kind == registry.KindProject {
		return a.Role.AtLeast(RoleMaintainer)
	}
	return a.Role.AtLeast(RoleMember)
}

// CanModerateMemory reports whether the user may retract anyone's memory and
// move one up to the project. Owners of the project above are owners here.
func (a Access) CanModerateMemory() bool { return !a.Below && a.Role.AtLeast(RoleMaintainer) }

// CanSetMemory reports whether the user may turn team memory on or off:
// maintainers and above, unlike the wiki switch, which owners hold.
func (a Access) CanSetMemory() bool { return !a.Below && a.Role.AtLeast(RoleMaintainer) }

var _ memory.Roles = (*Roles)(nil)

// MemoryRights implements memory.Roles. An unknown namespace gives no rights
// at all, not even to an admin, so it looks like a team the person is not in.
func (r *Roles) MemoryRights(ctx context.Context, userID, namespace string) (memory.Rights, error) {
	chain, err := registry.Chain(ctx, r.namespaces, namespace)
	if errors.Is(err, registry.ErrNotFound) {
		return memory.Rights{}, nil
	}
	if err != nil {
		return memory.Rights{}, fmt.Errorf("memory rights to %s: %w", namespace, err)
	}
	a, err := r.Access(ctx, userID, namespace)
	if err != nil {
		return memory.Rights{}, err
	}
	if !a.CanReadMemory() {
		return memory.Rights{}, nil
	}
	layers := make([]string, len(chain))
	for i, l := range chain {
		layers[i] = l.Namespace
	}
	return memory.Rights{
		Read:     true,
		Write:    a.CanWriteMemory(chain[0].Kind),
		Moderate: a.CanModerateMemory(),
		Switch:   a.CanSetMemory(),
		Layers:   layers,
		Kind:     chain[0].Kind,
	}, nil
}

// MemoryUsers answers memory.Users from the account store, so a memory token
// stops working when its person is gone.
type MemoryUsers struct{ users userByID }

type userByID interface {
	UserByID(ctx context.Context, id string) (User, error)
}

var _ memory.Users = MemoryUsers{}

// NewMemoryUsers returns the user lookup for memory.NewTokens.
func NewMemoryUsers(users userByID) MemoryUsers { return MemoryUsers{users: users} }

// Email implements memory.Users. A missing user is registry.ErrNotFound.
func (m MemoryUsers) Email(ctx context.Context, userID string) (string, error) {
	u, err := m.users.UserByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("memory user %s: %w", userID, err)
	}
	return u.Email, nil
}
