package web_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
	"github.com/petervlad/loadout/internal/web"
)

// anyone says every id is a person, for tokens made in a test.
type anyone struct{}

func (anyone) Email(_ context.Context, id string) (string, error) { return id + "@example.com", nil }

// memoryParts are the team memory services a test site's pages use.
type memoryParts struct {
	switches *memorytest.MemSwitchStore
	store    *memorytest.MemStore
	service  *memory.Service
	tokens   *memory.Tokens
	// changed counts the calls of UI.MemoryChanged.
	changed int
}

// withMemory turns team memory on for the pages of a test site.
func withMemory(t *testing.T, parts *memoryParts) func(*web.UI) {
	t.Helper()
	return func(ui *web.UI) {
		parts.switches = memorytest.NewMemSwitchStore()
		roles, ok := ui.Places.(*account.Roles)
		if !ok {
			t.Fatalf("Places is %T, want *account.Roles", ui.Places)
		}
		parts.store = memorytest.NewMemStore()
		svc, err := memory.NewService(parts.store, parts.switches, roles, time.Now, nil)
		if err != nil {
			t.Fatal(err)
		}
		toks, err := memory.NewTokens(memorytest.NewMemTokenStore(), anyone{}, time.Now, nil)
		if err != nil {
			t.Fatal(err)
		}
		parts.service, parts.tokens = svc, toks
		ui.Memory, ui.MemoryTokens = svc, toks
		ui.MemoryChanged = func() { parts.changed++ }
	}
}

func (p *memoryParts) on(t *testing.T, ns string) bool {
	t.Helper()
	sp, err := p.switches.Get(t.Context(), ns)
	if err != nil {
		t.Fatal(err)
	}
	return sp.Enabled
}

// TestMemorySwitch checks the Team memory panel: maintainers and owners turn
// it, members neither see it nor get past the route.
func TestMemorySwitch(t *testing.T) {
	var mem memoryParts
	s := newUISiteWith(t, withMemory(t, &mem))
	alice := s.as("alice@example.com") // maintainer of lime
	bob := s.as("bob@example.com")     // member of lime

	wantOut(t, bob.get("/t/lime/settings"), "member", `id="memory-h"`, `action="/t/lime/settings/memory"`)
	if p := bob.post("/t/lime/settings/memory", url.Values{"memory": {"on"}}); p.status != http.StatusForbidden {
		t.Errorf("member turns memory on = %d, want 403", p.status)
	}
	if mem.on(t, "lime") {
		t.Fatal("a refused post turned memory on")
	}

	p := alice.get("/t/lime/settings")
	wantIn(t, p, "maintainer, off", `<h2 id="memory-h">Team memory</h2>`, `action="/t/lime/settings/memory"`,
		`name="memory" value="on">Team memory`, "Off: lime has no team memory.")
	wantOut(t, p, "maintainer, off", `name="memory" value="on" checked>`)

	p = alice.post("/t/lime/settings/memory", url.Values{"memory": {"on"}})
	if p.status != http.StatusSeeOther || p.location != "/t/lime/settings?done=memory-on" {
		t.Fatalf("maintainer turns memory on = %d %q\n%s", p.status, p.location, p.body)
	}
	if !mem.on(t, "lime") {
		t.Error("memory is off after the post")
	}
	wantIn(t, alice.get(p.location), "maintainer, on", "Team memory is on. Members create a memory token on their Account page",
		`name="memory" value="on" checked>`, "On: members' AI tools read and save short facts for lime")

	// An unticked box sends nothing, which is off.
	p = alice.post("/t/lime/settings/memory", url.Values{})
	if p.status != http.StatusSeeOther || p.location != "/t/lime/settings?done=memory-off" {
		t.Fatalf("maintainer turns memory off = %d %q\n%s", p.status, p.location, p.body)
	}
	wantIn(t, alice.get(p.location), "maintainer, off again", "Team memory is off. What was saved is kept")

	// Another team's maintainer cannot reach lime at all.
	eve := s.as("eve@example.com")
	if p := eve.post("/t/lime/settings/memory", url.Values{"memory": {"on"}}); p.status != http.StatusNotFound {
		t.Errorf("maintainer of another team = %d, want 404", p.status)
	}
}

func TestMemorySwitchOnAProject(t *testing.T) {
	var mem memoryParts
	s := newUISiteWith(t, withMemory(t, &mem))
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	wantIn(t, olga.get("/t/acme/settings"), "owner of the project", `id="memory-h"`)
	if p := olga.post("/t/acme/settings/memory", url.Values{"memory": {"on"}}); p.status != http.StatusSeeOther {
		t.Fatalf("owner turns project memory on = %d\n%s", p.status, p.body)
	}
	if !mem.on(t, "acme") || mem.on(t, "lime") {
		t.Errorf("acme = %v, lime = %v, want only acme on", mem.on(t, "acme"), mem.on(t, "lime"))
	}
}

func TestMemorySwitchIsAbsentWithoutMemory(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	wantOut(t, alice.get("/t/lime/settings"), "no memory service", `id="memory-h"`, "Team memory")
	if p := alice.post("/t/lime/settings/memory", url.Values{"memory": {"on"}}); p.status != http.StatusNotFound {
		t.Errorf("POST without a memory service = %d, want 404", p.status)
	}
	wantOut(t, alice.get("/account"), "no memory service", "Memory tokens")
	if p := alice.post("/account/memory-tokens", url.Values{}); p.status != http.StatusNotFound {
		t.Errorf("POST /account/memory-tokens without a memory service = %d, want 404", p.status)
	}
}

var memorySecretRE = regexp.MustCompile(`ldm_[a-z2-7]{52}`)

// TestMemoryTokensOnAccount: a token is shown once, listed without its
// secret, and revoked.
func TestMemoryTokensOnAccount(t *testing.T) {
	var mem memoryParts
	s := newUISiteWith(t, withMemory(t, &mem))
	bob := s.as("bob@example.com")

	p := bob.get("/account")
	wantIn(t, p, "account", `<h2 id="memtok-h">Memory tokens</h2>`, `action="/account/memory-tokens"`, "No memory tokens yet.")

	p = bob.post("/account/memory-tokens", url.Values{"label": {"work laptop"}})
	if p.status != http.StatusOK || p.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("create = %d, Cache-Control %q, want 200 and no-store\n%s", p.status, p.header.Get("Cache-Control"), p.body)
	}
	secret := memorySecretRE.FindString(p.body)
	if secret == "" {
		t.Fatalf("no secret on the page that answers a create:\n%s", p.body)
	}
	wantIn(t, p, "create", "Memory token work laptop created.", "it is not shown again", `id="memory-secret"`)

	// The secret signs in to team memory as bob.
	c, err := mem.tokens.Authenticate(t.Context(), secret)
	if err != nil || !strings.HasSuffix(c.Label, "@example.com") {
		t.Fatalf("Authenticate(secret) = %+v, %v, want bob", c, err)
	}

	p = bob.get("/account")
	wantIn(t, p, "list", "<code>work laptop</code>", "Created ", "Expires ", `action="/account/memory-tokens/revoke"`)
	wantOut(t, p, "list", secret, "No memory tokens yet.", "it is not shown again")

	// Someone else sees none of it.
	wantOut(t, s.as("dan@example.com").get("/account"), "other person", "work laptop")

	id := hiddenOf(t, p, "id")
	p = bob.post("/account/memory-tokens/revoke", url.Values{"id": {id}})
	if p.status != http.StatusSeeOther || p.location != "/account?done=memory-revoked" {
		t.Fatalf("revoke = %d %q\n%s", p.status, p.location, p.body)
	}
	if _, err := mem.tokens.Authenticate(t.Context(), secret); err == nil {
		t.Error("the revoked token still signs in")
	}
	wantIn(t, bob.get(p.location), "after revoke", "The memory token was revoked.", "No memory tokens yet.")
	// Revoking a token that is gone reads the same.
	if p := bob.post("/account/memory-tokens/revoke", url.Values{"id": {id}}); p.status != http.StatusSeeOther {
		t.Errorf("revoke again = %d, want 303", p.status)
	}
}

func TestMemoryTokenLabelTooLong(t *testing.T) {
	var mem memoryParts
	s := newUISiteWith(t, withMemory(t, &mem))
	bob := s.as("bob@example.com")
	p := bob.post("/account/memory-tokens", url.Values{"label": {strings.Repeat("x", 81)}})
	if p.status != http.StatusBadRequest {
		t.Fatalf("81 character label = %d, want 400\n%s", p.status, p.body)
	}
	wantIn(t, p, "long label", `role="alert"`, `aria-invalid="true"`)
	wantOut(t, p, "long label", "ldm_")
}

func TestMemoryTokenFormsNeedASessionAndCSRF(t *testing.T) {
	var mem memoryParts
	s := newUISiteWith(t, withMemory(t, &mem))
	anon := s.browser()
	anon.get("/auth/login")
	for _, path := range []string{"/account/memory-tokens", "/account/memory-tokens/revoke"} {
		if p := anon.post(path, url.Values{"label": {"x"}}); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login") {
			t.Errorf("POST %s without a session = %d %q, want a redirect to sign in", path, p.status, p.location)
		}
	}
	bob := s.as("bob@example.com")
	if p := bob.post("/account/memory-tokens", url.Values{"csrf": {"wrong"}, "label": {"x"}}); p.status != http.StatusForbidden {
		t.Errorf("POST with a wrong CSRF token = %d, want 403", p.status)
	}
}

// memorySite is a test site with team memory on for lime and its project,
// and the people who act in it: alice maintains lime, bob and dan are
// members, eve belongs to another team.
type memorySite struct {
	*uiSite
	mem                        *memoryParts
	alice, bob, dan, eve, olga *browser
	aliceC, bobC, danC, olgaC  memory.Caller
	ctx                        context.Context
}

func newMemorySite(t *testing.T) *memorySite {
	t.Helper()
	m := &memorySite{mem: &memoryParts{}}
	m.uiSite = newUISiteWith(t, withMemory(t, m.mem))
	m.ctx = t.Context()
	if _, _, err := m.accounts.AddMember(m.ctx, account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	m.alice, m.bob = m.as("alice@example.com"), m.as("bob@example.com")
	m.dan, m.eve, m.olga = m.as("dan@example.com"), m.as("eve@example.com"), m.as("olga@example.com")
	caller := func(email string) memory.Caller { return memory.Caller{UserID: m.userID(email), Label: email} }
	m.aliceC, m.bobC, m.danC, m.olgaC = caller("alice@example.com"), caller("bob@example.com"), caller("dan@example.com"), caller("olga@example.com")
	for _, ns := range []string{"lime", "acme"} {
		if err := m.mem.switches.Set(m.ctx, ns, true, m.aliceC.UserID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// save stores a memory of ns as c.
func (m *memorySite) save(c memory.Caller, ns, name, desc, body string) memory.Entry {
	m.t.Helper()
	d, err := memory.NewDraft(ns, memory.TypeReference, name, desc, body, nil)
	if err != nil {
		m.t.Fatal(err)
	}
	r, err := m.mem.service.Save(m.ctx, c, ns, d, true)
	if err != nil || !r.Done() {
		m.t.Fatalf("Save(%s) = %+v, %v", name, r, err)
	}
	return r.Entry
}

func entryPath(e memory.Entry) string { return "/t/lime/memory/" + strconv.FormatInt(e.ID, 10) }

// TestMemoryTab404WhileOff: with the switch off, every memory route, the
// tab and the retract and move posts answer 404, and so does a person who
// is not in the team.
func TestMemoryTab404WhileOff(t *testing.T) {
	m := newMemorySite(t)
	e := m.save(m.bobC, "lime", "Release label", "Needs the release label", "")
	wantIn(t, m.alice.get("/t/lime"), "on", `<a href="/t/lime/memory">Memory</a>`)
	if err := m.mem.service.SetEnabled(m.ctx, m.aliceC, "lime", false); err != nil {
		t.Fatal(err)
	}
	wantOut(t, m.alice.get("/t/lime"), "off", `href="/t/lime/memory"`)
	for _, path := range []string{"/t/lime/memory", "/t/lime/memory?q=release", "/t/lime/memory/setup", entryPath(e)} {
		if p := m.alice.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s while off = %d, want 404", path, p.status)
		}
	}
	for _, path := range []string{entryPath(e) + "/retract", entryPath(e) + "/move"} {
		if p := m.alice.post(path, url.Values{"reason": {"wrong"}}); p.status != http.StatusNotFound {
			t.Errorf("POST %s while off = %d, want 404", path, p.status)
		}
	}
	if got, _ := m.mem.store.Get(m.ctx, e.ID); !got.Active() {
		t.Error("a post while off retracted the memory")
	}
}

func TestMemoryTabHiddenFromOutsiders(t *testing.T) {
	m := newMemorySite(t)
	e := m.save(m.bobC, "lime", "Release label", "Needs the release label", "")
	for _, path := range []string{"/t/lime/memory", "/t/lime/memory/setup", entryPath(e)} {
		if p := m.eve.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s as another team = %d, want 404", path, p.status)
		}
	}
	if p := m.eve.post(entryPath(e)+"/retract", url.Values{"reason": {"x"}}); p.status != http.StatusNotFound {
		t.Errorf("POST retract as another team = %d, want 404", p.status)
	}
	anon := m.browser()
	if p := anon.get("/t/lime/memory"); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login") {
		t.Errorf("GET without a session = %d %q, want sign in", p.status, p.location)
	}
}

// TestMemorySearch: words find memories with their author and dates, a type
// or a team narrows them, and every text is escaped.
func TestMemorySearch(t *testing.T) {
	m := newMemorySite(t)
	m.save(m.bobC, "lime", "Tracker host "+xss, "OPS tickets live on the support site "+xss, "Read them with the REST API.\nUse a PAT.")
	m.save(m.danC, "lime", "Release label", "A release PR needs the release label", "")
	m.save(m.olgaC, "acme", "House style", "Project wide: write plain sentences", "")

	p := m.bob.get("/t/lime/memory?q=tracker")
	wantIn(t, p, "search", `<h2 id="mem-list-h" class="grow">Results for “tracker”</h2>`, "Tracker host &lt;script&gt;alert(1)&lt;/script&gt;",
		"OPS tickets live on the support site &lt;script&gt;alert(1)&lt;/script&gt;", "bob@example.com", "Saved ", `aria-current="page">Memory</a>`)
	wantOut(t, p, "search", xss, "Release label", "House style", "Unconfirmed")

	// The project's memory is found through the team, tagged with its layer.
	p = m.bob.get("/t/lime/memory?q=sentences")
	wantIn(t, p, "project hit", "House style", `title="This memory belongs to the project above">acme</span>`)
	p = m.bob.get("/t/lime/memory?q=sentences&layer=lime")
	wantIn(t, p, "layer filter", "No memory matches")
	p = m.bob.get("/t/lime/memory?q=sentences&layer=acme")
	wantIn(t, p, "layer filter", "House style")

	// A type filter keeps the form's choice.
	p = m.bob.get("/t/lime/memory?q=release&type=feedback")
	wantIn(t, p, "type filter", "No memory matches “release”", `<option value="feedback" selected>Feedback</option>`)
	p = m.bob.get("/t/lime/memory?q=release&type=reference")
	wantIn(t, p, "type filter", "Release label")

	// A query of punctuation has no words and finds nothing, without an error.
	if p := m.bob.get("/t/lime/memory?q=%21%21"); p.status != http.StatusOK {
		t.Errorf("a query without words = %d, want 200", p.status)
	}
	// An unknown type or layer is ignored.
	if p := m.bob.get("/t/lime/memory?q=release&type=bogus&layer=nowhere"); p.status != http.StatusOK || !strings.Contains(p.body, "Release label") {
		t.Errorf("unknown filters = %d, want the unfiltered results", p.status)
	}
	// Retracted memories are not found.
	r := m.save(m.bobC, "lime", "Old runner trick", "Runner trick", "")
	if _, err := m.mem.service.Retract(m.ctx, m.bobC, r.ID, "gone"); err != nil {
		t.Fatal(err)
	}
	wantOut(t, m.bob.get("/t/lime/memory?q=runner"), "retracted", "Old runner trick")
}

// TestMemoryNewThisWeek: with no query the page lists what was saved or
// changed in the last 7 days, newest first, and marks what nobody confirmed
// for six months.
func TestMemoryNewThisWeek(t *testing.T) {
	m := newMemorySite(t)
	m.save(m.bobC, "lime", "Fresh fact", "Saved today", "")
	old := func(name string, age time.Duration) memory.Entry {
		d, err := memory.NewDraft("lime", memory.TypeProject, name, "An old one", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		e, err := m.mem.store.Insert(m.ctx, d, m.danC, time.Now().Add(-age))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	old("Last month fact", 30*24*time.Hour)
	old("Eight days fact", 8*24*time.Hour)
	old("Six day fact", 6*24*time.Hour)

	p := m.bob.get("/t/lime/memory")
	wantIn(t, p, "new this week", `<h2 id="mem-list-h" class="grow">New this week</h2>`, "Fresh fact", "Six day fact", "2 memories")
	wantOut(t, p, "new this week", "Last month fact", "Eight days fact", "Results for")
	if strings.Index(p.body, "Fresh fact") > strings.Index(p.body, "Six day fact") {
		t.Error("New this week is not newest first")
	}
	wantIn(t, m.bob.get("/t/lime/memory?type=feedback"), "type with no query", "Nothing new this week", "No memory of this type or team")

	// A memory nobody confirmed for more than six months is not new, but
	// search finds it with the marker, and it is never hidden.
	e := old("Ancient fact", 200*24*time.Hour)
	wantOut(t, m.bob.get("/t/lime/memory"), "old", "Ancient fact")
	p = m.bob.get("/t/lime/memory?q=ancient")
	wantIn(t, p, "unconfirmed", "Ancient fact", `<span class="pill pill-warn">Unconfirmed since `)
	wantOut(t, m.bob.get("/t/lime/memory?q=fresh"), "fresh", "Unconfirmed")
	wantIn(t, m.bob.get(entryPath(e)), "unconfirmed entry", `<span class="pill pill-warn">Unconfirmed since `)

	// An empty week says so and points at the search.
	empty := newMemorySite(t)
	wantIn(t, empty.bob.get("/t/lime/memory"), "empty", "Nothing new this week", "Search above to find older ones")
}

// TestMemoryEntryHistory: the entry page shows the text with its line
// breaks, links, and every version with who, when and what changed.
func TestMemoryEntryHistory(t *testing.T) {
	m := newMemorySite(t)
	other := m.save(m.danC, "lime", "Runner image", "Which image the runner uses", "")
	e := m.save(m.bobC, "lime", "Tracker host", "OPS lives on the support site", "Line one\nLine two <b>bold</b>\n\n# Not a heading [x](http://evil.example)")
	name, body, links := "Tracker host (OPS)", "Line one\nLine 2 <b>bold</b>\n\n# Not a heading [x](http://evil.example)", []int64{other.ID}
	if _, err := m.mem.service.Update(m.ctx, m.danC, e.ID, 0, memory.Change{Name: &name, Body: &body, Links: &links, Note: "Fix the second line " + xss}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.mem.service.Update(m.ctx, m.aliceC, e.ID, 0, memory.Change{Confirm: true}); err != nil {
		t.Fatal(err)
	}

	p := m.bob.get(entryPath(e))
	wantIn(t, p, "entry", `<h2 id="mem-h">Tracker host (OPS)</h2>`, `<span class="pill pill-kind">reference</span>`,
		"OPS lives on the support site", "Saved by", "bob@example.com",
		// The text is escaped and keeps its line breaks, with no Markdown.
		`<div class="mem-body">Line one`+"\n"+`Line 2 &lt;b&gt;bold&lt;/b&gt;`+"\n\n"+`# Not a heading [x](http://evil.example)</div>`,
		// Links.
		`<h2 id="mem-links-h" class="grow">Linked memories</h2>`, `href="`+entryPath(other)+`"`, "Runner image",
		// History: newest first, each with who, when and the action.
		`<h2 id="mem-hist-h" class="grow">History</h2>`, "3 versions",
		"<strong>Confirmed</strong> by alice@example.com", "<strong>Updated</strong> by dan@example.com", "<strong>Saved</strong> by bob@example.com",
		"version 3, newest", "version 1", "Note: Fix the second line &lt;script&gt;alert(1)&lt;/script&gt;",
		// The text diff of the update.
		"Changes in version 2", `<div class="del"><span class="mark" aria-hidden="true">−</span><span class="vh">removed: </span>Name: Tracker host</div>`,
		`<span class="vh">added: </span>Name: Tracker host (OPS)</div>`, `<span class="vh">added: </span>Line 2 &lt;b&gt;bold&lt;/b&gt;</div>`,
		`<span class="vh">added: </span>Links: `+strconv.FormatInt(other.ID, 10)+`</div>`)
	wantOut(t, p, "entry", xss, "<b>bold</b>", "<a href=\"http://evil.example\"", "Retracted")
	// A confirm changes no text, so it has no diff of its own, and the first
	// version has nothing before it to differ from.
	if strings.Contains(p.body, "Changes in version 3") || strings.Contains(p.body, "Changes in version 1") {
		t.Error("a version without a text change offers a diff")
	}
	if n := strings.Count(p.body, `<details class="memhist-diff">`); n != 1 {
		t.Errorf("%d diffs, want 1", n)
	}
	// Reading the page counts no use by an AI tool.
	wantIn(t, p, "uses", "0 times")
	// The linked memory's page is reachable from the link.
	wantIn(t, m.bob.get(entryPath(other)), "linked memory", "Runner image")
	// A missing id, and ids written another way, are 404.
	for _, path := range []string{"/t/lime/memory/9999", "/t/lime/memory/0", "/t/lime/memory/-1", "/t/lime/memory/01", "/t/lime/memory/x"} {
		if p := m.bob.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, p.status)
		}
	}
	// A memory of another team is 404 through this one.
	foreign := m.save(m.olgaC, "acme", "Project fact", "Seen through lime", "")
	wantIn(t, m.bob.get("/t/lime/memory/"+strconv.FormatInt(foreign.ID, 10)), "project memory", "Project fact", `title="This memory belongs to the project above"`)
	if p := m.alice.get("/t/plum/memory/" + strconv.FormatInt(e.ID, 10)); p.status != http.StatusNotFound {
		t.Errorf("a memory of lime through plum = %d, want 404", p.status)
	}
}

// TestMemoryShowsLastEditor: a colleague's edit names them on the list and
// the entry page, beside the author. An author's own edit names nobody.
func TestMemoryShowsLastEditor(t *testing.T) {
	m := newMemorySite(t)
	e := m.save(m.bobC, "lime", "Tracker host", "OPS lives on the support site", "")
	body := "newer"
	if _, err := m.mem.service.Update(m.ctx, m.danC, e.ID, 0, memory.Change{Body: &body}); err != nil {
		t.Fatal(err)
	}
	wantIn(t, m.bob.get(entryPath(e)), "entry", "Last edited by", "dan@example.com")
	wantIn(t, m.bob.get("/t/lime/memory?q=tracker"), "list", "Edited by dan@example.com")

	own := "bob again"
	if _, err := m.mem.service.Update(m.ctx, m.bobC, e.ID, 0, memory.Change{Body: &own}); err != nil {
		t.Fatal(err)
	}
	wantOut(t, m.bob.get(entryPath(e)), "entry", "Last edited by")
	wantOut(t, m.bob.get("/t/lime/memory?q=tracker"), "list", "Edited by")
}

// buttons reads which of the two actions a page offers.
func buttons(p page) (retract, move bool) {
	return strings.Contains(p.body, `/retract">`), strings.Contains(p.body, `/move">`)
}

// TestMemoryButtonsPerRole: Retract is for the author and for those who
// moderate, Move to project for those who moderate, and neither for a
// retracted memory or for the project's memory seen through a team.
func TestMemoryButtonsPerRole(t *testing.T) {
	m := newMemorySite(t)
	e := m.save(m.bobC, "lime", "Tracker host", "OPS lives on the support site", "")
	up := m.save(m.olgaC, "acme", "Project fact", "Seen through lime", "")
	upPath := entryPath(up)

	cases := []struct {
		who            string
		b              *browser
		path           string
		retract, moved bool
	}{
		{"author", m.bob, entryPath(e), true, false},
		{"other member", m.dan, entryPath(e), false, false},
		{"maintainer", m.alice, entryPath(e), true, true},
		{"maintainer, project memory", m.alice, upPath, false, false},
		{"owner of the project, on the project", m.olga, "/t/acme/memory/" + strconv.FormatInt(up.ID, 10), true, false},
		{"member, project memory", m.bob, upPath, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.who, func(t *testing.T) {
			r, mv := buttons(tc.b.get(tc.path))
			if r != tc.retract || mv != tc.moved {
				t.Errorf("retract %v, move %v, want %v and %v", r, mv, tc.retract, tc.moved)
			}
		})
	}
	p := m.alice.get(entryPath(e))
	wantIn(t, p, "maintainer", `<summary class="btn btn-danger">Retract…</summary>`, `<summary class="btn">Move to project…</summary>`,
		`<label for="mem-reason">Reason for retracting</label>`, `name="reason" class="field" required`, "Move this memory to acme?")

	// With the project's memory off there is nothing to move to.
	if err := m.mem.service.SetEnabled(m.ctx, m.olgaC, "acme", false); err != nil {
		t.Fatal(err)
	}
	if r, mv := buttons(m.alice.get(entryPath(e))); !r || mv {
		t.Errorf("project off: retract %v, move %v, want retract only", r, mv)
	}

	// A retracted memory offers nothing and says so, with who and why.
	if _, err := m.mem.service.Retract(m.ctx, m.bobC, e.ID, "wrong host "+xss); err != nil {
		t.Fatal(err)
	}
	p = m.alice.get(entryPath(e))
	wantIn(t, p, "retracted", `role="note"`, "<strong>Retracted</strong> by bob@example.com", "It is hidden from search and from AI tools",
		"Reason: wrong host &lt;script&gt;alert(1)&lt;/script&gt;", "<strong>Retracted</strong> by bob@example.com ·")
	wantOut(t, p, "retracted", xss)
	if r, mv := buttons(p); r || mv {
		t.Errorf("retracted: retract %v, move %v, want neither", r, mv)
	}
}

// TestMemoryRetract: the popover form needs a reason, the service decides
// who may, and a retract drops the cached index.
func TestMemoryRetract(t *testing.T) {
	m := newMemorySite(t)
	e := m.save(m.bobC, "lime", "Tracker host", "OPS lives on the support site", "")
	path := entryPath(e) + "/retract"

	// Another member may not, even with a hand-made post.
	if p := m.dan.post(path, url.Values{"reason": {"nope"}}); p.status != http.StatusForbidden {
		t.Errorf("retract by another member = %d, want 403", p.status)
	}
	// A reason is required: the page answers with the form open and the
	// message tied to the field.
	p := m.bob.post(path, url.Values{"reason": {"  "}})
	if p.status != http.StatusBadRequest {
		t.Fatalf("retract without a reason = %d, want 400\n%s", p.status, p.body)
	}
	wantIn(t, p, "no reason", `role="alert"`, `<details class="menu" open>`, `aria-invalid="true"`, `aria-describedby="form-error"`, `<h2 id="mem-h">Tracker host</h2>`)
	// A reason that holds a key is refused and not echoed back.
	key := "AKIA" + "ZZZZZZZZZZZZZZZZ"
	p = m.bob.post(path, url.Values{"reason": {"leaked " + key}})
	if p.status != http.StatusBadRequest {
		t.Fatalf("retract with a key = %d, want 400", p.status)
	}
	if got, _ := m.mem.store.Get(m.ctx, e.ID); !got.Active() {
		t.Fatal("a refused retract hid the memory")
	}
	if m.mem.changed != 0 {
		t.Errorf("MemoryChanged called %d times for refused posts", m.mem.changed)
	}

	// A CSRF token is required.
	if p := m.bob.post(path, url.Values{"csrf": {"wrong"}, "reason": {"x"}}); p.status != http.StatusForbidden {
		t.Errorf("retract with a wrong CSRF token = %d, want 403", p.status)
	}

	p = m.bob.post(path, url.Values{"reason": {"The host moved"}})
	if p.status != http.StatusSeeOther || p.location != entryPath(e)+"?done=retracted" {
		t.Fatalf("retract = %d %q\n%s", p.status, p.location, p.body)
	}
	if m.mem.changed != 1 {
		t.Errorf("MemoryChanged called %d times, want 1", m.mem.changed)
	}
	p = m.bob.get(p.location)
	wantIn(t, p, "after retract", "Retracted. The memory is hidden from search", "Reason: The host moved")
	wantOut(t, m.bob.get("/t/lime/memory?q=tracker"), "search", "Tracker host")
	wantOut(t, m.bob.get("/t/lime/memory"), "new this week", "Tracker host")

	// A maintainer may retract what someone else wrote.
	f := m.save(m.bobC, "lime", "Second fact", "Another one", "")
	if p := m.alice.post(entryPath(f)+"/retract", url.Values{"reason": {"duplicate"}}); p.status != http.StatusSeeOther {
		t.Errorf("retract by a maintainer = %d, want 303", p.status)
	}
	// An id the person cannot read is 404.
	if p := m.eve.post(entryPath(f)+"/retract", url.Values{"reason": {"x"}}); p.status != http.StatusNotFound {
		t.Errorf("retract by another team = %d, want 404", p.status)
	}
}

// TestMemoryMove: a maintainer lifts a team's memory to the project, and it
// is refused while the project's memory is off.
func TestMemoryMove(t *testing.T) {
	m := newMemorySite(t)
	e := m.save(m.bobC, "lime", "Tracker host", "OPS lives on the support site", "")
	path := entryPath(e) + "/move"

	if p := m.bob.post(path, url.Values{}); p.status != http.StatusForbidden {
		t.Errorf("move by the author, a member = %d, want 403", p.status)
	}
	if err := m.mem.service.SetEnabled(m.ctx, m.olgaC, "acme", false); err != nil {
		t.Fatal(err)
	}
	p := m.alice.post(path, url.Values{})
	if p.status != http.StatusBadRequest {
		t.Fatalf("move with the project off = %d, want 400\n%s", p.status, p.body)
	}
	wantIn(t, p, "project off", `role="alert"`, "Ask an owner of acme to turn it on")
	if m.mem.changed != 0 {
		t.Errorf("MemoryChanged called %d times for refused posts", m.mem.changed)
	}
	if err := m.mem.service.SetEnabled(m.ctx, m.olgaC, "acme", true); err != nil {
		t.Fatal(err)
	}
	p = m.alice.post(path, url.Values{})
	if p.status != http.StatusSeeOther || p.location != entryPath(e)+"?done=moved" {
		t.Fatalf("move = %d %q\n%s", p.status, p.location, p.body)
	}
	if m.mem.changed != 1 {
		t.Errorf("MemoryChanged called %d times, want 1", m.mem.changed)
	}
	p = m.bob.get(p.location)
	wantIn(t, p, "after move", "Moved to the project.", `title="This memory belongs to the project above">acme</span>`,
		"<strong>Moved</strong> by alice@example.com", "Changes in version 2", "Team: lime", "Team: acme")
	// The author may still take it back, and nobody can move it again.
	if r, mv := buttons(p); !r || mv {
		t.Errorf("a moved memory offers its author retract %v, move %v, want retract only", r, mv)
	}
	if r, mv := buttons(m.dan.get(entryPath(e))); r || mv {
		t.Errorf("a moved memory offers another member retract %v, move %v, want neither", r, mv)
	}
	// A maintainer of the team has no say in the project's memory.
	if r, mv := buttons(m.alice.get(entryPath(e))); r || mv {
		t.Errorf("a moved memory offers a maintainer of the team retract %v, move %v, want neither", r, mv)
	}
	if r, mv := buttons(m.olga.get(entryPath(e))); !r || mv {
		t.Errorf("a moved memory offers the project's owner retract %v, move %v, want retract only", r, mv)
	}
	// It keeps its number, author and history.
	wantIn(t, p, "after move", "bob@example.com", "2 versions")
}

// TestMemoryHasNoEditing: no page of the tab has a form that changes a
// memory's text, and each form that exists posts to retract or move.
func TestMemoryHasNoEditing(t *testing.T) {
	m := newMemorySite(t)
	e := m.save(m.bobC, "lime", "Tracker host", "OPS lives on the support site", "Body")
	forms := regexp.MustCompile(`<form[^>]*>`)
	for _, path := range []string{"/t/lime/memory", "/t/lime/memory?q=tracker", "/t/lime/memory/setup", entryPath(e)} {
		p := m.alice.get(path)
		wantOut(t, p, path, "<textarea", "contenteditable", `/edit"`, `name="body"`, `name="name"`, `name="description"`)
		for _, f := range forms.FindAllString(p.body, -1) {
			ok := strings.Contains(f, `role="search"`) || strings.HasSuffix(f, `/retract">`) || strings.HasSuffix(f, `/move">`)
			if !ok {
				t.Errorf("%s: a form that is not search, retract or move: %s", path, f)
			}
		}
	}
	// There is no route that edits or saves.
	for _, path := range []string{entryPath(e) + "/edit", entryPath(e) + "/update", "/t/lime/memory/new"} {
		for _, method := range []func(string, url.Values) page{
			func(p string, _ url.Values) page { return m.alice.get(p) },
			m.alice.post,
		} {
			if p := method(path, url.Values{"body": {"x"}}); p.status != http.StatusNotFound && p.status != http.StatusMethodNotAllowed {
				t.Errorf("%s = %d, want 404 or 405", path, p.status)
			}
		}
	}
}

var scriptTag = regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)

// TestMemoryPagesWorkWithoutScripts: take the one script out and every form
// is still a form with a submit button, the popovers are native details, and
// nothing the page needs is added by script, inline style or handler.
func TestMemoryPagesWorkWithoutScripts(t *testing.T) {
	m := newMemorySite(t)
	e := m.save(m.bobC, "lime", "Tracker host", "OPS lives on the support site "+xss, "Body")
	body := "Body, changed"
	if _, err := m.mem.service.Update(m.ctx, m.bobC, e.ID, 0, memory.Change{Body: &body}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/t/lime/memory", "/t/lime/memory?q=tracker", "/t/lime/memory/setup", entryPath(e)} {
		p := m.alice.get(path)
		if p.status != http.StatusOK {
			t.Fatalf("GET %s = %d", path, p.status)
		}
		if n := len(scriptTag.FindAllString(p.body, -1)); n != 1 {
			t.Errorf("%s: %d scripts, want only /auth/static/ui.js", path, n)
		}
		stripped := scriptTag.ReplaceAllString(p.body, "")
		wantOut(t, page{body: stripped}, path, "<script", " onclick=", " onchange=", " onsubmit=", " style=", "javascript:")
		// Copy buttons are the only thing that needs script, and they are
		// hidden without it (js-only), next to text that can be selected.
		for _, b := range regexp.MustCompile(`<button[^>]*data-copy[^>]*>`).FindAllString(stripped, -1) {
			if !strings.Contains(b, "js-only") {
				t.Errorf("%s: a copy button shows without script: %s", path, b)
			}
		}
	}
	p := m.alice.get(entryPath(e))
	wantIn(t, p, "entry", `<details class="menu">`, `<button type="submit" class="btn btn-danger">Retract</button>`, `<button type="submit" class="btn btn-primary">Move to project</button>`,
		`<a class="btn" href="`+entryPath(e)+`">Cancel</a>`, `<details class="memhist-diff">`)
	wantIn(t, m.alice.get("/t/lime/memory"), "search", `<button type="submit" class="btn btn-primary">Search</button>`, `<form class="memsearch" role="search" method="get" action="/t/lime/memory">`)
	// The filter selects need no script to apply: they sit in the search form.
	wantOut(t, m.alice.get("/t/lime/memory"), "search", "data-autosubmit", "js-hide")
}

// TestMemorySetupPage: the manual setup for any other client.
func TestMemorySetupPage(t *testing.T) {
	m := newMemorySite(t)
	p := m.bob.get("/t/lime/memory/setup")
	wantIn(t, p, "setup", "Connect an AI tool to lime",
		"http://localhost/mcp/t/lime", // the endpoint
		`curl http://localhost/mcp/t/lime`, `Authorization: Bearer $LOADOUT_MEMORY_TOKEN`, `&#34;method&#34;:&#34;tools/list&#34;`,
		"loadout mcp --team lime --remote http://localhost",
		`&#34;Authorization&#34;: &#34;Bearer YOUR_MEMORY_TOKEN&#34;`, `&#34;url&#34;: &#34;http://localhost/mcp/t/lime&#34;`,
		`href="/account"`,
		`data-copy="mem-endpoint"`, `<pre class="cmd snippet" id="mem-curl" tabindex="0">`)
	// No token is ever printed here.
	wantOut(t, p, "setup", "ldm_", "ca.crt", "security add-trusted-cert")
	wantIn(t, m.bob.get("/t/lime/memory"), "tab", `href="/t/lime/memory/setup"`)
}

// TestMemoryTabNeedsTheService: without a memory service there is no tab and
// no route.
func TestMemoryTabNeedsTheService(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	wantOut(t, alice.get("/t/lime"), "no service", `href="/t/lime/memory"`)
	if p := alice.get("/t/lime/memory"); p.status != http.StatusNotFound {
		t.Errorf("GET /t/lime/memory without a memory service = %d, want 404", p.status)
	}
}
