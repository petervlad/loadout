package account_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/registry"
)

// project sets up project acme with teams web and mobile, lime (a team
// outside it), and one person per role, and returns their actors.
func (f *fixture) project(t *testing.T) map[string]account.Actor {
	t.Helper()
	ctx, op := t.Context(), account.Operator()
	for _, ns := range []struct {
		name, parent string
		kind         registry.NamespaceKind
	}{{"acme", "", registry.KindProject}, {"web", "acme", registry.KindTeam}, {"mobile", "acme", registry.KindTeam}} {
		if _, err := f.reg.CreateNamespace(ctx, ns.name, ns.parent, ns.kind); err != nil {
			t.Fatal(err)
		}
	}
	actors := map[string]account.Actor{}
	for _, m := range []struct {
		name, ns string
		role     account.Role
	}{
		{"owner", "acme", account.RoleOwner},
		{"projmaint", "acme", account.RoleMaintainer},
		{"both", "acme", account.RoleMaintainer},
		{"both", "web", account.RoleMaintainer},
		{"teammaint", "web", account.RoleMaintainer},
		{"member", "web", account.RoleMember},
		{"projmember", "acme", account.RoleMember},
		{"stranger", "lime", account.RoleMaintainer},
	} {
		ms, _, err := f.svc.AddMember(ctx, op, m.ns, m.name+"@example.com", m.role)
		if err != nil {
			t.Fatal(err)
		}
		actors[m.name] = account.UserActor(ms.UserID)
	}
	admin, err := f.svc.SetAdmin(ctx, op, "admin@example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	actors["admin"] = account.UserActor(admin.ID)
	return actors
}

// TestInviteMatrix checks who may invite with which roles: every grant
// follows the rules of AddMember, and a refused grant changes nothing.
func TestInviteMatrix(t *testing.T) {
	type invite struct {
		role  account.Role
		teams map[string]account.Role
	}
	invites := map[string]invite{
		"project member":                 {account.RoleMember, nil},
		"project maintainer":             {account.RoleMaintainer, nil},
		"project owner":                  {account.RoleOwner, nil},
		"project member, web member":     {account.RoleMember, map[string]account.Role{"web": account.RoleMember}},
		"project member, web maintainer": {account.RoleMember, map[string]account.Role{"web": account.RoleMaintainer}},
		"project member, both teams":     {account.RoleMember, map[string]account.Role{"web": account.RoleMember, "mobile": account.RoleMember}},
	}
	allowed := map[string][]string{
		"admin":      {"project member", "project maintainer", "project owner", "project member, web member", "project member, web maintainer", "project member, both teams"},
		"owner":      {"project member", "project maintainer", "project member, web member", "project member, web maintainer", "project member, both teams"},
		"projmaint":  {"project member"},
		"both":       {"project member", "project member, web member"},
		"teammaint":  {},
		"member":     {},
		"projmember": {},
		"stranger":   {},
	}
	for who, ok := range allowed {
		for name, inv := range invites {
			t.Run(who+"/"+name, func(t *testing.T) {
				f := newFixture(t)
				actors := f.project(t)
				got, err := f.svc.InviteToProject(t.Context(), actors[who], "acme", "new@example.com", inv.role, inv.teams)
				want := false
				for _, n := range ok {
					want = want || n == name
				}
				if !want {
					if !errors.Is(err, registry.ErrForbidden) {
						t.Fatalf("error = %v, want ErrForbidden", err)
					}
					if _, err := f.store.UserByEmail(t.Context(), "new@example.com"); !errors.Is(err, registry.ErrNotFound) {
						t.Errorf("a refused invitation created the user (err %v); want nothing written", err)
					}
					if n := f.mails.Count("new@example.com"); n != 0 {
						t.Errorf("a refused invitation sent %d mails", n)
					}
					return
				}
				if err != nil {
					t.Fatalf("error: %v, want allowed", err)
				}
				if len(got.Grants) != 1+len(inv.teams) {
					t.Fatalf("grants = %+v, want the project and %d teams", got.Grants, len(inv.teams))
				}
				for _, g := range got.Grants {
					if g.Result != account.GrantAdded || g.Role != g.Asked {
						t.Errorf("grant %+v, want added with the role asked", g)
					}
					u, err := f.store.UserByEmail(t.Context(), "new@example.com")
					if err != nil {
						t.Fatal(err)
					}
					if m, err := f.store.Membership(t.Context(), u.ID, g.Namespace); err != nil || m.Role != g.Asked {
						t.Errorf("membership in %s = %+v, %v; want %s", g.Namespace, m, err, g.Asked)
					}
				}
				if !got.Mailed {
					t.Error("Mailed = false, want the invitation mailed")
				}
			})
		}
	}
}

func TestInviteRefusesBadTeams(t *testing.T) {
	for _, tc := range []struct {
		name  string
		teams map[string]account.Role
		want  string
	}{
		{"team outside the project", map[string]account.Role{"lime": account.RoleMember}, "lime is not a team in acme"},
		{"missing team", map[string]account.Role{"ghost": account.RoleMember}, "ghost is not a team in acme"},
		{"bad name", map[string]account.Role{"../x": account.RoleMember}, "is not a team in acme"},
		{"the project as a team", map[string]account.Role{"acme": account.RoleMember}, "acme is not a team in acme"},
		{"owner of a team", map[string]account.Role{"web": account.RoleOwner}, "owner is a role on projects"},
		{"bad role", map[string]account.Role{"web": "publish"}, `role "publish" in web`},
		// One good team and one bad: the good one is not written either.
		{"one good, one bad", map[string]account.Role{"web": account.RoleMember, "ghost": account.RoleMember}, "ghost is not a team in acme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			actors := f.project(t)
			_, err := f.svc.InviteToProject(t.Context(), actors["owner"], "acme", "new@example.com", account.RoleMember, tc.teams)
			if !errors.Is(err, registry.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want ErrInvalid saying %q", err, tc.want)
			}
			if _, err := f.store.UserByEmail(t.Context(), "new@example.com"); !errors.Is(err, registry.ErrNotFound) {
				t.Errorf("user after a refused invitation: err %v, want none created", err)
			}
		})
	}

	f := newFixture(t)
	actors := f.project(t)
	if _, err := f.svc.InviteToProject(t.Context(), actors["admin"], "web", "new@example.com", account.RoleMember, nil); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("invite from a team: error = %v, want ErrInvalid", err)
	}
	if _, err := f.svc.InviteToProject(t.Context(), actors["admin"], "acme", "not an address", account.RoleMember, nil); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("invite a bad address: error = %v, want ErrInvalid", err)
	}
	if _, err := f.svc.InviteToProject(t.Context(), actors["admin"], "acme", "new@example.com", "", nil); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("invite without a project role: error = %v, want ErrInvalid", err)
	}
}

// TestInviteExistingMember checks that roles are raised, never lowered,
// and that each namespace says what happened.
func TestInviteExistingMember(t *testing.T) {
	f := newFixture(t)
	actors := f.project(t)
	ctx := t.Context()
	// teammaint maintains web and reads acme through it; member is a
	// member of web only.
	got, err := f.svc.InviteToProject(ctx, actors["owner"], "acme", "teammaint@example.com", account.RoleMember,
		map[string]account.Role{"web": account.RoleMember, "mobile": account.RoleMaintainer})
	if err != nil {
		t.Fatal(err)
	}
	want := []account.Grant{
		{Namespace: "acme", Asked: account.RoleMember, Role: account.RoleMember, Result: account.GrantAdded},
		{Namespace: "mobile", Asked: account.RoleMaintainer, Role: account.RoleMaintainer, Result: account.GrantAdded},
		{Namespace: "web", Asked: account.RoleMember, Role: account.RoleMaintainer, Was: account.RoleMaintainer, Result: account.GrantKept},
	}
	if !equalGrants(got.Grants, want) {
		t.Errorf("grants = %+v\nwant %+v", got.Grants, want)
	}
	u, err := f.store.UserByEmail(ctx, "teammaint@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := f.store.Membership(ctx, u.ID, "web"); m.Role != account.RoleMaintainer {
		t.Errorf("web role after an invitation as member = %s, want maintainer kept", m.Role)
	}

	got, err = f.svc.InviteToProject(ctx, actors["owner"], "acme", "member@example.com", account.RoleMember, map[string]account.Role{"web": account.RoleMaintainer})
	if err != nil {
		t.Fatal(err)
	}
	if g := got.Grants[1]; g.Namespace != "web" || g.Result != account.GrantRaised || g.Was != account.RoleMember || g.Role != account.RoleMaintainer {
		t.Errorf("web grant = %+v, want raised from member to maintainer", g)
	}

	// Nothing to change: no write, no mail.
	n := f.mails.Count("member@example.com")
	got, err = f.svc.InviteToProject(ctx, actors["owner"], "acme", "member@example.com", account.RoleMember, map[string]account.Role{"web": account.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed() || got.Mailed || f.mails.Count("member@example.com") != n {
		t.Errorf("repeat invitation = %+v, want nothing changed and no mail", got)
	}

	// A project maintainer cannot raise a member of acme to maintainer.
	if _, err := f.svc.InviteToProject(ctx, actors["projmaint"], "acme", "projmember@example.com", account.RoleMaintainer, nil); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("project maintainer raises to maintainer: error = %v, want ErrForbidden", err)
	}
}

func equalGrants(a, b []account.Grant) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestInviteMail checks the mail and the link, and that neither tells
// whether the address had an account.
func TestInviteMail(t *testing.T) {
	f := newFixture(t)
	actors := f.project(t)
	ctx := t.Context()
	f.signup(t, "known@example.com")

	teams := map[string]account.Role{"web": account.RoleMember}
	fresh, err := f.svc.InviteToProject(ctx, actors["owner"], "acme", "Fresh@Example.com", account.RoleMember, teams)
	if err != nil {
		t.Fatal(err)
	}
	known, err := f.svc.InviteToProject(ctx, actors["owner"], "acme", "known@example.com", account.RoleMember, teams)
	if err != nil {
		t.Fatal(err)
	}
	wantLink := "https://registry.example.com/auth/login?email=fresh%40example.com&next=%2Ft%2Facme"
	if fresh.Link != wantLink || fresh.Email != "fresh@example.com" {
		t.Errorf("invitation = %+v, want link %s", fresh, wantLink)
	}
	if !equalGrants(fresh.Grants, known.Grants) || fresh.Mailed != known.Mailed {
		t.Errorf("results differ for a new and a known address:\n%+v\n%+v", fresh, known)
	}
	a := f.mails.WaitFor(t, "fresh@example.com", 0)
	b := f.mails.WaitFor(t, "known@example.com", 1) // after the sign-up code
	if a.Subject != "You are invited to acme on loadout" || a.Subject != b.Subject {
		t.Errorf("subjects %q and %q", a.Subject, b.Subject)
	}
	if strings.ReplaceAll(a.Text, "fresh", "X") != strings.ReplaceAll(b.Text, "known", "X") {
		t.Errorf("mail texts differ beyond the address:\n%s\n---\n%s", a.Text, b.Text)
	}
	for _, s := range []string{"owner@example.com added you to acme", "Member of acme", "Member of web", wantLink, "loadout login"} {
		if !strings.Contains(a.Text, s) {
			t.Errorf("mail text lacks %q:\n%s", s, a.Text)
		}
	}
	if !strings.Contains(a.HTML, `href="https://registry.example.com/auth/login?email=fresh%40example.com&amp;next=%2Ft%2Facme"`) {
		t.Errorf("mail HTML lacks the link:\n%s", a.HTML)
	}

	// The inviter's name is theirs to choose; the HTML escapes it.
	owner, err := f.store.UserByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.LinkGoogle(ctx, owner.ID, "g-owner", "Olga <b>Boss</b>"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.InviteToProject(ctx, actors["owner"], "acme", "third@example.com", account.RoleMember, nil); err != nil {
		t.Fatal(err)
	}
	if m := f.mails.WaitFor(t, "third@example.com", 0); strings.Contains(m.HTML, "<b>") || !strings.Contains(m.HTML, "Olga &lt;b&gt;Boss") {
		t.Errorf("inviter name not escaped in the HTML:\n%s", m.HTML)
	}
}

// failingStore fails every PutMembers, like a database that goes away in
// the middle of an invitation.
type failingStore struct{ *accounttest.MemStore }

func (failingStore) PutMembers(context.Context, []account.Membership) error {
	return errors.New("disk I/O error")
}

func TestInviteStoreFailure(t *testing.T) {
	f := newFixture(t)
	actors := f.project(t)
	svc := account.NewService(failingStore{f.store}, f.reg, f.roles, device.New(f.clock.now), account.Options{
		PublicURL: "https://registry.example.com", Mailer: f.mails, Now: f.clock.now, Log: slog.New(slog.DiscardHandler),
	})
	_, err := svc.InviteToProject(t.Context(), actors["owner"], "acme", "member@example.com", account.RoleMember, map[string]account.Role{"web": account.RoleMaintainer})
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("error = %v, want one saying nothing was changed", err)
	}
	u, err := f.store.UserByEmail(t.Context(), "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := f.store.Membership(t.Context(), u.ID, "web"); m.Role != account.RoleMember {
		t.Errorf("web role after a failed invitation = %s, want member unchanged", m.Role)
	}
	if n := f.mails.Count("member@example.com"); n != 0 {
		t.Errorf("a failed invitation sent %d mails", n)
	}
}

func TestInviteWithoutMailOrURL(t *testing.T) {
	f := newFixture(t)
	actors := f.project(t)
	svc := account.NewService(f.store, f.reg, f.roles, device.New(f.clock.now), account.Options{Now: f.clock.now, Log: slog.New(slog.DiscardHandler)})
	got, err := svc.InviteToProject(t.Context(), actors["owner"], "acme", "new@example.com", account.RoleMember, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mailed || got.Link != "" || !got.Changed() {
		t.Errorf("invitation = %+v, want the role given, no mail and no link", got)
	}
}
