package account_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
)

func TestPlaces(t *testing.T) {
	f := newFixture(t)
	actors := f.team(t)
	type place struct {
		Name  string
		Level int
		Role  account.Role
		Via   string
	}
	tests := []struct {
		who  string
		want []place
	}{
		{"member", []place{{"acme", 0, "", "web"}, {"web", 1, account.RoleMember, ""}}},
		{"maintainer", []place{{"acme", 0, "", "web"}, {"web", 1, account.RoleMaintainer, ""}}},
		{"owner", []place{{"acme", 0, account.RoleOwner, ""}, {"web", 1, account.RoleOwner, ""}}},
		{"stranger", []place{{"lime", 0, account.RoleMember, ""}}},
		{"admin", []place{{"acme", 0, account.RoleAdmin, ""}, {"lime", 0, account.RoleAdmin, ""}, {"web", 1, account.RoleAdmin, ""}}},
	}
	for _, tc := range tests {
		t.Run(tc.who, func(t *testing.T) {
			u, err := f.store.UserByID(t.Context(), actors[tc.who].UserID)
			if err != nil {
				t.Fatal(err)
			}
			ps, err := f.roles.Places(t.Context(), u)
			if err != nil {
				t.Fatalf("Places() error: %v", err)
			}
			var got []place
			for _, p := range ps {
				got = append(got, place{p.Namespace.Name, p.Level, p.Access.Role, p.Via})
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Places() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWebPrincipal(t *testing.T) {
	f := newFixture(t)
	actors := f.team(t)
	user := func(who string) account.User {
		u, err := f.store.UserByID(t.Context(), actors[who].UserID)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	tests := []struct {
		who, ns   string
		wantTeam  string
		wantScope registry.TokenScope
		wantErr   error
	}{
		{"member", "web", "web", registry.ScopeRead, nil},
		{"member", "acme", "web", registry.ScopeRead, nil},
		{"member", "lime", "", "", registry.ErrNotFound},
		{"member", "ghost", "", "", registry.ErrNotFound},
		{"maintainer", "web", "web", registry.ScopePublish, nil},
		{"owner", "web", "web", registry.ScopePublish, nil},
		{"owner", "acme", "acme", registry.ScopePublish, nil},
		{"stranger", "web", "", "", registry.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.who+" in "+tc.ns, func(t *testing.T) {
			u := user(tc.who)
			pr, _, err := f.roles.Principal(t.Context(), u, tc.ns)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Principal() error = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if pr.Team != tc.wantTeam || pr.Scope != tc.wantScope || pr.UserID != u.ID || !strings.HasPrefix(pr.TokenID, "web:") {
				t.Errorf("Principal() = %+v, want team %s with scope %s for %s", pr, tc.wantTeam, tc.wantScope, u.ID)
			}
			if !pr.CanRead(tc.ns) {
				t.Errorf("Principal() cannot read %s", tc.ns)
			}
		})
	}
}

func TestMemberDetails(t *testing.T) {
	f := newFixture(t)
	actors := f.team(t)
	got, err := f.svc.MemberDetails(t.Context(), actors["maintainer"], "web")
	if err != nil {
		t.Fatalf("MemberDetails() error: %v", err)
	}
	if len(got) != 3 || got[0].Membership.Email != "maintainer@example.com" || got[0].Verified || got[0].Password || got[0].Google {
		t.Errorf("MemberDetails() = %+v, want three members added by email, none signed in yet", got)
	}
	if got, err := f.svc.MemberDetails(t.Context(), actors["member"], "web"); err != nil || len(got) != 3 {
		t.Errorf("MemberDetails() by a member = %d members, %v; want the same three", len(got), err)
	}

	names, err := f.svc.DisplayNames(t.Context(), []string{actors["member"].UserID, "nobody", ""})
	if err != nil || len(names) != 1 || names[actors["member"].UserID] != "member@example.com" {
		t.Errorf("DisplayNames() = %v, %v; want the member's email only", names, err)
	}
}

func TestInheritedMembers(t *testing.T) {
	f := newFixture(t)
	actors := f.team(t)
	ctx, op := t.Context(), account.Operator()
	// Project eu sits under acme, with team berlin under eu.
	if _, err := f.reg.CreateNamespace(ctx, "eu", "acme", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.CreateNamespace(ctx, "berlin", "eu", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		email, ns string
		role      account.Role
	}{
		{"zoe@example.com", "acme", account.RoleOwner},
		{"pm@example.com", "acme", account.RoleMaintainer},
		{"pmember@example.com", "acme", account.RoleMember},
		{"eu@example.com", "eu", account.RoleOwner},
		{"zoe@example.com", "eu", account.RoleOwner},
		{"member@example.com", "berlin", account.RoleMember},
	} {
		if _, _, err := f.svc.AddMember(ctx, op, m.ns, m.email, m.role); err != nil {
			t.Fatal(err)
		}
	}

	type row struct{ Email, From string }
	tests := []struct {
		name, ns string
		actor    account.Actor
		want     []row
		wantErr  error
	}{
		{
			"owners of acme, not its maintainers or members", "web", actors["member"],
			[]row{{"owner@example.com", "acme"}, {"zoe@example.com", "acme"}},
			nil,
		},
		{
			"closest project first, each person once", "berlin", actors["member"],
			[]row{{"eu@example.com", "eu"}, {"zoe@example.com", "eu"}, {"owner@example.com", "acme"}},
			nil,
		},
		{"a project's own owners are not inherited", "acme", actors["owner"], nil, nil},
		{"a root namespace has nobody above", "lime", actors["stranger"], nil, nil},
		{"someone outside the team may not list them", "web", actors["stranger"], nil, registry.ErrForbidden},
		{
			"an admin may", "web", actors["admin"],
			[]row{{"owner@example.com", "acme"}, {"zoe@example.com", "acme"}},
			nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.svc.InheritedMembers(ctx, tc.actor, tc.ns)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("InheritedMembers() error = %v, want %v", err, tc.wantErr)
			}
			var rows []row
			for _, d := range got {
				if d.Membership.Role != account.RoleOwner {
					t.Errorf("InheritedMembers() lists %s as %s, want owners only", d.Membership.Email, d.Membership.Role)
				}
				rows = append(rows, row{d.Membership.Email, d.Membership.Namespace})
			}
			if diff := cmp.Diff(tc.want, rows); diff != "" {
				t.Errorf("InheritedMembers(%s) mismatch (-want +got):\n%s", tc.ns, diff)
			}
		})
	}
}
