package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
)

// TestInvitePanelByRole checks who gets the Invite someone panel on a
// project's members page, and with which roles and teams.
func TestInvitePanelByRole(t *testing.T) {
	s := newUISite(t)
	ctx, op := t.Context(), account.Operator()
	for _, m := range []struct {
		email string
		role  account.Role
	}{{"olga@example.com", account.RoleOwner}, {"pat@example.com", account.RoleMaintainer}, {"mia@example.com", account.RoleMember}} {
		if _, _, err := s.accounts.AddMember(ctx, op, "acme", m.email, m.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.accounts.SetAdmin(ctx, op, "root@example.com", true); err != nil {
		t.Fatal(err)
	}

	// The owner: member and maintainer in acme and in every team.
	p := s.as("olga@example.com").get("/t/acme/settings")
	wantIn(t, p, "owner", "Invite someone", `action="/t/acme/invite"`, `name="team" value="koala"`, `name="team" value="lime"`,
		`name="team_role.lime"`, `<label for="invite-role-lime" class="vh">Role in lime</label>`, `value="maintainer"`, "loadout mails it to them")
	wantOut(t, p, "owner", `value="owner"`, `action="/t/acme/members"`)

	// An admin may also make owners.
	wantIn(t, s.as("root@example.com").get("/t/acme/settings"), "admin", `action="/t/acme/invite"`, `value="owner"`)

	// A project maintainer adds members to acme only: no teams, and a
	// line saying who adds them to teams.
	p = s.as("pat@example.com").get("/t/acme/settings")
	wantIn(t, p, "project maintainer", `action="/t/acme/invite"`, "To add them to a team as well")
	wantOut(t, p, "project maintainer", `name="team"`, `value="maintainer"`)

	// A project member, and a team maintainer on the team's page, get no
	// invite panel.
	wantOut(t, s.as("mia@example.com").get("/t/acme/settings"), "project member", `action="/t/acme/invite"`, "Invite someone")
	p = s.as("alice@example.com").get("/t/lime/settings")
	wantIn(t, p, "team maintainer", `action="/t/lime/members"`)
	wantOut(t, p, "team maintainer", "/invite", "Invite someone")
}

func TestInvite(t *testing.T) {
	s := newUISite(t)
	ctx, op := t.Context(), account.Operator()
	if _, _, err := s.accounts.AddMember(ctx, op, "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	olga.get("/t/acme/settings")

	p := olga.post("/t/acme/invite", url.Values{
		"email": {"Dana@Example.com"}, "role": {"member"},
		"team": {"lime", "koala"}, "team_role.lime": {"maintainer"}, "team_role.koala": {"member"},
		// A role for a team that is not ticked is ignored.
		"team_role.wombat": {"maintainer"},
	})
	if p.status != http.StatusOK {
		t.Fatalf("invite = %d\n%s", p.status, p.body)
	}
	link := "http://localhost/auth/login?email=dana%40example.com&amp;next=%2Ft%2Facme"
	wantIn(t, p, "result", "dana@example.com is invited", "Sign-in link", link, "loadout mailed it to dana@example.com", "Invite someone else",
		"<td class=\"wide\">lime</td>\n<td>Maintainer</td>")
	wantOut(t, p, "result", `action="/t/acme/invite"`)
	m := s.mails.WaitFor(t, "dana@example.com", 0)
	if !strings.Contains(m.Subject, "acme") || !strings.Contains(m.Text, "olga@example.com added you to acme") {
		t.Errorf("mail = %+v", m)
	}

	// Again: nothing changes, nothing is mailed.
	p = olga.post("/t/acme/invite", url.Values{"email": {"dana@example.com"}, "role": {"member"}, "team": {"lime"}, "team_role.lime": {"member"}})
	wantIn(t, p, "repeat", "Nothing to change for dana@example.com", "Unchanged, already higher", "Nothing changed, so nothing was mailed")
	if n := s.mails.Count("dana@example.com"); n != 1 {
		t.Errorf("mails after a repeat = %d, want 1", n)
	}

	// A bad team names itself, escaped, keeps the form filled in and
	// writes nothing.
	p = olga.post("/t/acme/invite", url.Values{"email": {"erin@example.com"}, "role": {"maintainer"}, "team": {"lime", xss}, "team_role.lime": {"maintainer"}})
	if p.status != http.StatusBadRequest {
		t.Fatalf("invite to a bad team = %d, want 400\n%s", p.status, p.body)
	}
	wantIn(t, p, "bad team", "is not a team in acme", `value="erin@example.com"`, `value="lime" checked`, `<option value="maintainer" selected>`)
	wantOut(t, p, "bad team", xss)
	if _, err := s.store.UserByEmail(ctx, "erin@example.com"); err == nil {
		t.Error("a refused invitation created the user")
	}

	// An address that is not one is refused, escaped.
	p = olga.post("/t/acme/invite", url.Values{"email": {xss}, "role": {"member"}})
	if p.status != http.StatusBadRequest {
		t.Errorf("invite a bad address = %d, want 400", p.status)
	}
	wantOut(t, p, "bad address", xss)

	// The owner cannot make owners.
	if p := olga.post("/t/acme/invite", url.Values{"email": {"x@example.com"}, "role": {"owner"}}); p.status != http.StatusForbidden {
		t.Errorf("owner invites an owner = %d, want 403", p.status)
	}
	// A team member cannot open acme's members page, nor invite there.
	if p := s.as("bob@example.com").post("/t/acme/invite", url.Values{"email": {"x@example.com"}, "role": {"member"}}); p.status != http.StatusNotFound {
		t.Errorf("invite by a team member = %d, want 404", p.status)
	}
}

// TestInviteResultEscapes checks that the name of the person inviting,
// which Google sets, is escaped in the result page and never raw.
func TestInviteResultEscapes(t *testing.T) {
	s := newUISite(t)
	ctx, op := t.Context(), account.Operator()
	m, _, err := s.accounts.AddMember(ctx, op, "acme", "olga@example.com", account.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.LinkGoogle(ctx, m.UserID, "g-olga", "Olga "+xss); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	olga.get("/t/acme/settings")
	p := olga.post("/t/acme/invite", url.Values{"email": {"dana@example.com"}, "role": {"member"}})
	if p.status != http.StatusOK {
		t.Fatalf("invite = %d", p.status)
	}
	wantOut(t, p, "result", xss)
	if html := s.mails.WaitFor(t, "dana@example.com", 0).HTML; strings.Contains(html, xss) || !strings.Contains(html, "Olga &lt;script&gt;") {
		t.Errorf("mail HTML does not escape the inviter's name:\n%s", html)
	}
}

func TestLoginPrefill(t *testing.T) {
	s := newUISite(t)
	b := s.browser()
	p := b.get("/auth/login?email=Dana%40Example.com&next=%2Ft%2Facme")
	wantIn(t, p, "login", `value="dana@example.com"`, "First time here?", `href="/auth/signup?next=%2ft%2facme&amp;email=dana%40example.com"`)
	wantOut(t, p, "login", "New here?")
	p = b.get("/auth/signup?email=dana%40example.com&next=%2Ft%2Facme")
	wantIn(t, p, "signup", `value="dana@example.com"`, `href="/auth/login?next=%2ft%2facme&amp;email=dana%40example.com"`)
	p = b.get("/auth/login?email=" + url.QueryEscape(xss) + "&next=%2Ft%2Facme")
	wantOut(t, p, "login with a bad address", xss, "First time here?")
	wantIn(t, p, "login with a bad address", "New here?")
}

// TestInvitePanelFolds: the panel is a disclosure, closed on arrival, open
// after "Invite someone else" (?invite=1) and after a refused invite, so
// what was typed stays in view.
func TestInvitePanelFolds(t *testing.T) {
	s := newUISite(t)
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	wantIn(t, olga.get("/t/acme/settings"), "closed", `<details class="panel invite-fold" id="invite">`, "<summary>")
	wantIn(t, olga.get("/t/acme/settings?invite=1"), "asked for", `<details class="panel invite-fold" id="invite" open>`)
	p := olga.post("/t/acme/invite", url.Values{"email": {"not an email"}, "role": {"member"}})
	wantIn(t, p, "after an error", `<details class="panel invite-fold" id="invite" open>`, `value="not an email"`)
}
