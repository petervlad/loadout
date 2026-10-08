package account_test

import (
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
)

// chain is lime (a team) under acme (a project), lime first.
var chain = []registry.Layer{
	{Namespace: "lime", Kind: registry.KindTeam, Depth: 0},
	{Namespace: "acme", Kind: registry.KindProject, Depth: 1},
}

func TestResolve(t *testing.T) {
	admin := account.User{ID: "a", IsAdmin: true}
	user := account.User{ID: "u"}
	tests := []struct {
		name  string
		user  account.User
		roles map[string]account.Role
		want  account.Access
	}{
		{"no role", user, nil, account.Access{}},
		{"member", user, map[string]account.Role{"lime": account.RoleMember}, account.Access{Role: account.RoleMember}},
		{"maintainer", user, map[string]account.Role{"lime": account.RoleMaintainer}, account.Access{Role: account.RoleMaintainer}},
		{"owner above", user, map[string]account.Role{"acme": account.RoleOwner}, account.Access{Role: account.RoleOwner, Governs: true}},
		{"member, owner above", user, map[string]account.Role{"lime": account.RoleMember, "acme": account.RoleOwner}, account.Access{Role: account.RoleOwner, Governs: true}},
		{"member above", user, map[string]account.Role{"acme": account.RoleMember}, account.Access{}},
		{"maintainer above", user, map[string]account.Role{"acme": account.RoleMaintainer}, account.Access{}},
		{"admin", admin, nil, account.Access{Role: account.RoleAdmin, Governs: true}},
	}
	for _, tt := range tests {
		if got := account.Resolve(tt.user, chain, tt.roles); got != tt.want {
			t.Errorf("Resolve(%s) = %+v, want %+v", tt.name, got, tt.want)
		}
	}
	// On the project itself, its owner is owner and governs it.
	got := account.Resolve(user, chain[1:], map[string]account.Role{"acme": account.RoleOwner})
	if got != (account.Access{Role: account.RoleOwner, Governs: true}) {
		t.Errorf("Resolve(owner on the project) = %+v", got)
	}
}

// TestPermissionMatrix is the role table of docs/DESIGN.md, one row per
// role (in a team unless noted), one column per action.
func TestPermissionMatrix(t *testing.T) {
	type row struct {
		read, propose, publish, publishReview, approve, tokens, list bool
		assignMember, assignMaintainer, assignOwner                  bool
		createTeamInProject, createProject, setReview                bool
		editWiki, setWiki                                            bool
	}
	accesses := map[string]account.Access{
		"none":                  {},
		"member":                {Role: account.RoleMember},
		"maintainer":            {Role: account.RoleMaintainer},
		"owner (own project)":   {Role: account.RoleOwner, Governs: true},
		"owner (team below)":    {Role: account.RoleOwner, Governs: true},
		"admin":                 {Role: account.RoleAdmin, Governs: true},
		"project member (team)": {},
		"team member (project)": {Role: account.RoleMember, Below: true},
	}
	want := map[string]row{
		"none":                  {},
		"member":                {read: true, propose: true, list: true, editWiki: true},
		"maintainer":            {read: true, propose: true, publish: true, approve: true, tokens: true, list: true, assignMember: true, editWiki: true},
		"owner (own project)":   {read: true, propose: true, publish: true, approve: true, tokens: true, list: true, assignMember: true, assignMaintainer: true, createTeamInProject: true, setReview: true, editWiki: true, setWiki: true},
		"owner (team below)":    {read: true, propose: true, publish: true, approve: true, tokens: true, list: true, assignMember: true, assignMaintainer: true, createTeamInProject: true, setReview: true, editWiki: true, setWiki: true},
		"admin":                 {read: true, propose: true, publish: true, approve: true, tokens: true, list: true, assignMember: true, assignMaintainer: true, assignOwner: true, createTeamInProject: true, createProject: true, setReview: true, editWiki: true, setWiki: true},
		"project member (team)": {},
		"team member (project)": {read: true, propose: true, list: true},
	}
	for name, a := range accesses {
		got := row{
			read:                a.CanRead(),
			propose:             a.CanPropose(),
			publish:             a.CanPublish(false),
			publishReview:       a.CanPublish(true),
			approve:             a.CanApprove(),
			tokens:              a.CanManageTokens(),
			list:                a.CanListMembers(),
			assignMember:        a.CanAssign(account.RoleMember),
			assignMaintainer:    a.CanAssign(account.RoleMaintainer),
			assignOwner:         a.CanAssign(account.RoleOwner),
			createTeamInProject: a.CanCreateChild(registry.KindProject, registry.KindTeam),
			createProject:       a.CanCreateChild(registry.KindProject, registry.KindProject),
			setReview:           a.CanSetReview(),
			editWiki:            a.CanEditWiki(),
			setWiki:             a.CanSetWiki(),
		}
		if got != want[name] {
			t.Errorf("%s:\n got  %+v\n want %+v", name, got, want[name])
		}
		// Nobody but an admin creates a namespace under a team.
		if got, want := a.CanCreateChild(registry.KindTeam, registry.KindTeam), a.Role == account.RoleAdmin; got != want {
			t.Errorf("%s: CanCreateChild(under a team) = %v, want %v", name, got, want)
		}
	}
}

func TestTokenScope(t *testing.T) {
	for _, tt := range []struct {
		role  account.Role
		scope registry.TokenScope
	}{
		{"", ""},
		{account.RoleMember, registry.ScopeRead},
		{account.RoleMaintainer, registry.ScopePublish},
		{account.RoleOwner, registry.ScopePublish},
		{account.RoleAdmin, registry.ScopePublish},
	} {
		scope, ok := account.Access{Role: tt.role}.TokenScope()
		if scope != tt.scope || ok != (tt.scope != "") {
			t.Errorf("TokenScope(%q) = %q, %v; want %q", tt.role, scope, ok, tt.scope)
		}
	}
	if account.RoleAdmin.Valid() || !account.RoleOwner.Valid() || account.Role("publish").Valid() {
		t.Error("Valid() must accept member, maintainer and owner only: admin is a user flag")
	}
}

// TestOwns checks who owns a namespace for approving their own proposals:
// owners through their memberships, inherited from the project above, and
// never through the admin flag alone.
func TestOwns(t *testing.T) {
	f := newFixture(t)
	actors := f.team(t)
	ctx := t.Context()
	// An owner who is also an admin still owns only where they are owner.
	if _, err := f.svc.SetAdmin(ctx, account.Operator(), "owner@example.com", true); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		who, ns string
		want    bool
	}{
		{"owner", "acme", true},
		{"owner", "web", true},
		{"owner", "lime", false},
		{"owner", "ghost", false},
		{"maintainer", "web", false},
		{"member", "web", false},
		{"admin", "web", false},
		{"admin", "acme", false},
	}
	for _, tc := range tests {
		got, err := f.roles.Owns(ctx, actors[tc.who].UserID, tc.ns)
		if err != nil || got != tc.want {
			t.Errorf("Owns(%s, %s) = %v, %v; want %v", tc.who, tc.ns, got, err, tc.want)
		}
	}
	if got, err := f.roles.Owns(ctx, "nobody", "web"); err != nil || got {
		t.Errorf("Owns(unknown user) = %v, %v; want false", got, err)
	}

	// Places says the same, so the web page needs no extra query.
	for _, who := range []string{"owner", "maintainer", "admin"} {
		u, err := f.store.UserByID(ctx, actors[who].UserID)
		if err != nil {
			t.Fatal(err)
		}
		places, err := f.roles.Places(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range places {
			want, err := f.roles.Owns(ctx, u.ID, p.Namespace.Name)
			if err != nil {
				t.Fatal(err)
			}
			if p.Owns != want {
				t.Errorf("Places(%s): %s Owns = %v, want %v", who, p.Namespace.Name, p.Owns, want)
			}
		}
	}
}
