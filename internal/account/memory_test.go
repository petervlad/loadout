package account_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// TestMemoryAccess is the role table for the four memory permissions.
func TestMemoryAccess(t *testing.T) {
	type row struct{ read, writeTeam, writeProject, moderate, set bool }
	tests := []struct {
		name string
		a    account.Access
		want row
	}{
		{"none", account.Access{}, row{}},
		{"member", account.Access{Role: account.RoleMember}, row{read: true, writeTeam: true}},
		{"maintainer", account.Access{Role: account.RoleMaintainer}, row{true, true, true, true, true}},
		{"owner", account.Access{Role: account.RoleOwner, Governs: true}, row{true, true, true, true, true}},
		{"admin", account.Access{Role: account.RoleAdmin, Governs: true}, row{true, true, true, true, true}},
		{"team member reading the project", account.Access{Role: account.RoleMember, Below: true}, row{read: true}},
	}
	for _, tt := range tests {
		got := row{
			read:         tt.a.CanReadMemory(),
			writeTeam:    tt.a.CanWriteMemory(registry.KindTeam),
			writeProject: tt.a.CanWriteMemory(registry.KindProject),
			moderate:     tt.a.CanModerateMemory(),
			set:          tt.a.CanSetMemory(),
		}
		if got != tt.want {
			t.Errorf("%s:\n got  %+v\n want %+v", tt.name, got, tt.want)
		}
	}
}

func TestMemoryUsersEmail(t *testing.T) {
	f := newFixture(t)
	actors := f.team(t)
	users := account.NewMemoryUsers(f.store)
	got, err := users.Email(t.Context(), actors["member"].UserID)
	if err != nil || got != "member@example.com" {
		t.Errorf("Email(member) = %q, %v, want member@example.com", got, err)
	}
	if _, err := users.Email(t.Context(), "u-nobody"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Email(unknown) error = %v, want ErrNotFound", err)
	}
}

// TestMemoryRights checks MemoryRights against real memberships: team web
// under project acme, and team lime on its own.
func TestMemoryRights(t *testing.T) {
	f := newFixture(t)
	actors := f.team(t)
	all := func(layers ...string) memory.Rights {
		return memory.Rights{Read: true, Write: true, Moderate: true, Switch: true, Layers: layers}
	}
	reader := func(layers ...string) memory.Rights { return memory.Rights{Read: true, Layers: layers} }
	member := func(layers ...string) memory.Rights { return memory.Rights{Read: true, Write: true, Layers: layers} }
	none := memory.Rights{}
	tests := []struct {
		who, ns string
		want    memory.Rights
	}{
		{"owner", "web", all("web", "acme")},
		{"owner", "acme", all("acme")},
		{"owner", "lime", none},
		{"maintainer", "web", all("web", "acme")},
		{"maintainer", "acme", reader("acme")},
		{"member", "web", member("web", "acme")},
		{"member", "acme", reader("acme")},
		{"stranger", "web", none},
		{"stranger", "acme", none},
		{"stranger", "lime", member("lime")},
		{"admin", "web", all("web", "acme")},
		{"admin", "acme", all("acme")},
		{"admin", "lime", all("lime")},
		// Nobody, an admin included, reads a namespace that does not exist.
		{"admin", "nosuch", none},
		{"owner", "nosuch", none},
	}
	for _, tt := range tests {
		got, err := f.roles.MemoryRights(t.Context(), actors[tt.who].UserID, tt.ns)
		if err != nil {
			t.Fatalf("%s in %s: %v", tt.who, tt.ns, err)
		}
		if got.Read != tt.want.Read || got.Write != tt.want.Write || got.Moderate != tt.want.Moderate ||
			got.Switch != tt.want.Switch || !slices.Equal(got.Layers, tt.want.Layers) {
			t.Errorf("%s in %s:\n got  %+v\n want %+v", tt.who, tt.ns, got, tt.want)
		}
		wantKind := registry.KindTeam
		if tt.ns == "acme" {
			wantKind = registry.KindProject
		}
		if tt.want.Read && got.Kind != wantKind {
			t.Errorf("%s in %s: Kind = %q, want %q", tt.who, tt.ns, got.Kind, wantKind)
		}
	}
	if got, err := f.roles.MemoryRights(t.Context(), "u-nobody", "web"); err != nil || got.Read {
		t.Errorf("unknown user: %+v, %v", got, err)
	}
}
