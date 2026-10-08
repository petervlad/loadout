package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
)

// TestSettingsTab checks that the page once called Members is Settings in
// the tab bar and the title, with its people under a Members heading.
func TestSettingsTab(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	tab := `<a href="/t/lime/settings" aria-current="page">Settings</a>`
	p := alice.get("/t/lime/settings")
	if p.status != http.StatusOK {
		t.Fatalf("GET /t/lime/settings = %d", p.status)
	}
	wantIn(t, p, "settings", tab, "<title>Settings · lime", `<h2 id="members-h">Members</h2>`, `aria-labelledby="members-h"`)
	wantOut(t, p, "settings", ">Members</a>", `href="/t/lime/members`)
	// The other pages link the tab, and the models page sits under it.
	wantIn(t, alice.get("/t/lime"), "packages", `<a href="/t/lime/settings">Settings</a>`)
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	wantIn(t, s.as("olga@example.com").get("/t/lime/tiers"), "tiers", tab, `<a href="/t/lime/settings">`)

	// A member, who changes nothing here, still finds the heading.
	wantIn(t, s.as("bob@example.com").get("/t/lime/settings"), "member", tab, `<h2 id="members-h">Members</h2>`)
}

// TestMembersMovedToSettings checks the old address: a permanent redirect
// with its query, which answers the same for any namespace and leaves the
// checks to the settings page.
func TestMembersMovedToSettings(t *testing.T) {
	s := newUISite(t)
	browsers := map[string]*browser{"": s.browser()}
	for _, who := range []string{"alice@example.com", "olga@example.com", "eve@example.com"} {
		browsers[who] = s.as(who)
	}
	tests := []struct {
		who, path, want string
	}{
		{"alice@example.com", "/t/lime/members", "/t/lime/settings"},
		{"olga@example.com", "/t/acme/members?invite=1", "/t/acme/settings?invite=1"},
		{"alice@example.com", "/t/lime/members?done=added&email=a%40b.example", "/t/lime/settings?done=added&email=a%40b.example"},
		{"eve@example.com", "/t/ghost/members", "/t/ghost/settings"},
		{"", "/t/lime/members", "/t/lime/settings"},
	}
	for _, tc := range tests {
		p := browsers[tc.who].get(tc.path)
		if p.status != http.StatusMovedPermanently || p.location != tc.want {
			t.Errorf("%s GET %s = %d %q, want 301 to %s", tc.who, tc.path, p.status, p.location, tc.want)
		}
	}
	// Where it lands, a stranger still finds nothing.
	if p := browsers["eve@example.com"].get("/t/lime/settings"); p.status != http.StatusNotFound {
		t.Errorf("eve GET /t/lime/settings = %d, want 404", p.status)
	}
}

// TestSettingsPostsComeBack checks that every form on the page lands on
// /settings, though the member and token forms keep their old addresses.
func TestSettingsPostsComeBack(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	p := alice.get("/t/lime/settings")
	wantIn(t, p, "forms", `action="/t/lime/members"`, `action="/t/lime/members/role"`, `action="/t/lime/members/remove"`,
		`action="/t/lime/tokens"`, `action="/t/lime/tokens/revoke"`)
	for _, step := range []struct {
		path string
		form url.Values
		want string
	}{
		{"/t/lime/members", url.Values{"email": {"new@example.com"}, "role": {"member"}}, "/t/lime/settings?done=added&email=new%40example.com"},
		{"/t/lime/members/role", url.Values{"email": {"new@example.com"}, "role": {"member"}}, "/t/lime/settings?done=role&email=new%40example.com"},
		{"/t/lime/members/remove", url.Values{"email": {"new@example.com"}}, "/t/lime/settings?done=removed&email=new%40example.com"},
	} {
		if p := alice.post(step.path, step.form); p.status != http.StatusSeeOther || p.location != step.want {
			t.Errorf("POST %s = %d %q, want 303 to %s", step.path, p.status, p.location, step.want)
		}
	}
	if p := alice.post("/t/lime/tokens", url.Values{"label": {"ci"}, "scope": {"read"}}); p.status != http.StatusOK {
		t.Errorf("new token = %d, want the settings page with the secret", p.status)
	} else {
		wantIn(t, p, "new token", "<title>Settings · lime", `<a class="btn" href="/t/lime/settings">Cancel</a>`)
	}
	toks, err := s.accounts.MachineTokens(t.Context(), account.Operator(), "lime")
	if err != nil || len(toks) == 0 {
		t.Fatalf("tokens = %v, %v", toks, err)
	}
	if p := alice.post("/t/lime/tokens/revoke", url.Values{"id": {toks[0].ID}}); p.status != http.StatusSeeOther || p.location != "/t/lime/settings?done=revoked" {
		t.Errorf("revoke = %d %q", p.status, p.location)
	}

	// A refusal leads back to Settings.
	if p := s.as("bob@example.com").post("/t/lime/members", url.Values{"email": {"x@example.com"}, "role": {"member"}}); p.status != http.StatusForbidden || !strings.Contains(p.body, `<a href="/t/lime/settings">Go back</a>`) {
		t.Errorf("refused add = %d, want 403 with a way back to Settings\n%s", p.status, p.body)
	}

	// The review switch posts to /settings/review; the old address still
	// saves, for a page open from before the rename.
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	wantIn(t, olga.get("/t/lime/settings"), "owner", `action="/t/lime/settings/review"`)
	wantIn(t, olga.get("/t/lime/reviews"), "reviews", `<a href="/t/lime/settings#review-h">Change in Settings</a>`)
	for _, step := range []struct {
		path, on, want string
	}{
		{"/t/lime/settings/review", "on", "/t/lime/settings?done=review-on"},
		{"/t/lime/settings", "", "/t/lime/settings?done=review-off"},
	} {
		if p := olga.post(step.path, url.Values{"require_review": {step.on}}); p.status != http.StatusSeeOther || p.location != step.want {
			t.Errorf("POST %s = %d %q, want 303 to %s", step.path, p.status, p.location, step.want)
		}
	}
	if p := alice.post("/t/lime/settings/review", url.Values{"require_review": {"on"}}); p.status != http.StatusForbidden {
		t.Errorf("maintainer turns review on = %d, want 403", p.status)
	}

	// A new team opens on its Settings.
	p = olga.post("/new", url.Values{"kind": {"team"}, "name": {"web"}, "parent": {"acme"}})
	if p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/t/web/settings?") {
		t.Errorf("create a team = %d %q, want 303 to /t/web/settings", p.status, p.location)
	}
}

// TestWikiSwitch checks the Wiki panel: owners turn it on and off, a
// maintainer neither sees it nor gets past the route.
func TestWikiSwitch(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	wantOut(t, alice.get("/t/lime/settings"), "maintainer", `id="wiki-h"`, `action="/t/lime/settings/wiki"`)
	if p := alice.post("/t/lime/settings/wiki", url.Values{"wiki": {"on"}}); p.status != http.StatusForbidden {
		t.Errorf("maintainer turns the wiki on = %d, want 403 (owners do)", p.status)
	}
	if ns, err := s.reg.Namespace(t.Context(), "lime"); err != nil || ns.Wiki {
		t.Errorf("lime after the maintainer's post = %+v, %v; want the wiki off", ns, err)
	}

	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	p := olga.get("/t/lime/settings")
	wantIn(t, p, "owner, off", `<h2 id="wiki-h">Wiki</h2>`, `action="/t/lime/settings/wiki"`, `name="wiki" value="on">Show a wiki`,
		"Off: lime has no Wiki tab.")
	wantOut(t, p, "owner, off", `href="/t/lime/wiki"`)

	p = olga.post("/t/lime/settings/wiki", url.Values{"wiki": {"on"}})
	if p.status != http.StatusSeeOther || p.location != "/t/lime/settings?done=wiki-on" {
		t.Fatalf("owner turns the wiki on = %d %q\n%s", p.status, p.location, p.body)
	}
	if ns, err := s.reg.Namespace(t.Context(), "lime"); err != nil || !ns.Wiki {
		t.Errorf("lime = %+v, %v; want the wiki on", ns, err)
	}
	wantIn(t, olga.get(p.location), "owner, on", "The wiki is on. Open the Wiki tab", `name="wiki" value="on" checked>`,
		"lime has a Wiki tab: one page its members write and everyone in lime reads.", `<a href="/t/lime/wiki">Open the wiki</a>`)

	// An unticked box sends nothing, which is off.
	p = olga.post("/t/lime/settings/wiki", url.Values{})
	if p.status != http.StatusSeeOther || p.location != "/t/lime/settings?done=wiki-off" {
		t.Fatalf("owner turns the wiki off = %d %q\n%s", p.status, p.location, p.body)
	}
	wantIn(t, olga.get(p.location), "owner, off again", "The wiki is off. Its history is kept")
	if ns, err := s.reg.Namespace(t.Context(), "lime"); err != nil || ns.Wiki {
		t.Errorf("lime = %+v, %v; want the wiki off", ns, err)
	}
}
