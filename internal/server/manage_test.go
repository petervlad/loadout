package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

type manageFixture struct {
	h        http.Handler
	reg      *registry.Service
	accounts *account.Service
	mem      *registrytest.MemStore
	tokens   map[string]string // login token per person
	machine  string
}

func newManageFixture(t *testing.T) manageFixture {
	t.Helper()
	ctx, log := t.Context(), slog.New(slog.DiscardHandler)
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, time.Now, log, registry.WithMemberships(roles))
	accounts := account.NewService(store, reg, roles, device.New(time.Now), account.Options{Wiki: registry.NewWikiService(regMem, regMem, time.Now, log), Now: time.Now, Log: log})
	if _, err := reg.CreateNamespace(ctx, "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CreateNamespace(ctx, "web", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	f := manageFixture{reg: reg, accounts: accounts, mem: regMem, tokens: map[string]string{}}
	for _, p := range []struct {
		name, ns string
		role     account.Role
	}{
		{"owner", "acme", account.RoleOwner},
		{"maintainer", "web", account.RoleMaintainer},
		{"member", "web", account.RoleMember},
	} {
		m, _, err := accounts.AddMember(ctx, account.Operator(), p.ns, p.name+"@example.com", p.role)
		if err != nil {
			t.Fatal(err)
		}
		// The scope `loadout login` gives: publish for maintainers and up.
		scope := registry.ScopePublish
		if p.role == account.RoleMember {
			scope = registry.ScopeRead
		}
		secret, _, err := reg.CreateUserToken(ctx, p.ns, scope, p.name, m.UserID)
		if err != nil {
			t.Fatal(err)
		}
		f.tokens[p.name] = secret
	}
	machine, _, err := reg.CreateToken(ctx, "web", registry.ScopePublish, "ci")
	if err != nil {
		t.Fatal(err)
	}
	f.machine = machine
	f.h = server.New(reg, log, "test", server.WithManagement(accounts))
	return f
}

func (f manageFixture) call(t *testing.T, token, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestManagementRoutes(t *testing.T) {
	f := newManageFixture(t)
	tests := []struct {
		who, method, path, body string
		want                    int
	}{
		{"machine", "POST", "/v1/namespaces", `{"name":"mobile","parent":"acme"}`, http.StatusForbidden},
		{"member", "POST", "/v1/namespaces", `{"name":"mobile","parent":"acme"}`, http.StatusForbidden},
		{"owner", "POST", "/v1/namespaces", `{"name":"mobile","parent":"acme"}`, http.StatusCreated},
		{"owner", "POST", "/v1/namespaces", `{"name":"globex","kind":"project"}`, http.StatusForbidden},
		{"maintainer", "POST", "/v1/namespaces/web/members", `{"email":"new@example.com","role":"member"}`, http.StatusOK},
		{"maintainer", "POST", "/v1/namespaces/web/members", `{"email":"new2@example.com","role":"maintainer"}`, http.StatusForbidden},
		{"owner", "PATCH", "/v1/namespaces/web/members/new@example.com", `{"role":"maintainer"}`, http.StatusOK},
		{"member", "GET", "/v1/namespaces/web/members", ``, http.StatusForbidden},
		{"maintainer", "GET", "/v1/namespaces/web/members", ``, http.StatusOK},
		{"maintainer", "DELETE", "/v1/namespaces/web/members/new@example.com", ``, http.StatusForbidden},
		{"owner", "DELETE", "/v1/namespaces/web/members/new@example.com", ``, http.StatusOK},
		{"maintainer", "PATCH", "/v1/namespaces/web", `{"require_review":true}`, http.StatusForbidden},
		{"owner", "PATCH", "/v1/namespaces/web", `{"require_review":true}`, http.StatusOK},
		{"member", "POST", "/v1/namespaces/web/tokens", `{"scope":"read","label":"ci"}`, http.StatusForbidden},
		{"maintainer", "POST", "/v1/namespaces/web/tokens", `{"scope":"read","label":"ci"}`, http.StatusCreated},
		{"maintainer", "GET", "/v1/namespaces/web/tokens", ``, http.StatusOK},
		{"owner", "DELETE", "/v1/namespaces/mobile", ``, http.StatusForbidden},
	}
	for _, tt := range tests {
		tok := f.tokens[tt.who]
		if tt.who == "machine" {
			tok = f.machine
		}
		if got, body := f.call(t, tok, tt.method, tt.path, tt.body); got != tt.want {
			t.Errorf("%s %s %s as %s = %d, want %d\n%s", tt.method, tt.path, tt.body, tt.who, got, tt.want, body)
		}
	}

	// The listing shows members with their roles; tokens show no secrets.
	_, body := f.call(t, f.tokens["maintainer"], "GET", "/v1/namespaces/web/members", "")
	var members api.MembersResponse
	if err := json.Unmarshal([]byte(body), &members); err != nil || len(members.Members) != 2 {
		t.Errorf("members = %s, want maintainer and member", body)
	}
	_, body = f.call(t, f.tokens["maintainer"], "GET", "/v1/namespaces/web/tokens", "")
	if strings.Contains(body, "ldt_") {
		t.Errorf("token list contains a secret: %s", body)
	}
	if ns, err := f.reg.Namespace(t.Context(), "web"); err != nil || !ns.RequireReview {
		t.Errorf("web after PATCH = %+v, %v; want review required", ns, err)
	}
}

// TestPatchNamespaceSettings: PATCH changes the settings it is given and
// leaves the others as they are; the wiki switch needs an owner.
func TestPatchNamespaceSettings(t *testing.T) {
	f := newManageFixture(t)
	tests := []struct {
		who, body            string
		want                 int
		wantReview, wantWiki bool
	}{
		{"maintainer", `{"wiki":true}`, http.StatusForbidden, false, false},
		{"owner", `{}`, http.StatusBadRequest, false, false},
		{"owner", `{"wiki":true}`, http.StatusOK, false, true},
		{"owner", `{"require_review":true}`, http.StatusOK, true, true},
		{"owner", `{"require_review":false,"wiki":false}`, http.StatusOK, false, false},
	}
	for _, tt := range tests {
		got, body := f.call(t, f.tokens[tt.who], "PATCH", "/v1/namespaces/web", tt.body)
		if got != tt.want {
			t.Errorf("PATCH %s as %s = %d, want %d\n%s", tt.body, tt.who, got, tt.want, body)
			continue
		}
		if got == http.StatusOK {
			var ns api.Namespace
			if err := json.Unmarshal([]byte(body), &ns); err != nil || ns.RequireReview != tt.wantReview || ns.Wiki != tt.wantWiki {
				t.Errorf("PATCH %s answered %s, want review %v and wiki %v", tt.body, body, tt.wantReview, tt.wantWiki)
			}
		}
		if ns, err := f.reg.Namespace(t.Context(), "web"); err != nil || ns.RequireReview != tt.wantReview || ns.Wiki != tt.wantWiki {
			t.Errorf("web after PATCH %s as %s = %+v, %v; want review %v and wiki %v", tt.body, tt.who, ns, err, tt.wantReview, tt.wantWiki)
		}
	}
}

// TestPatchCourse: the course goes first, so a course refused for its
// value leaves the other settings unchanged too.
func TestPatchCourse(t *testing.T) {
	f := newManageFixture(t)
	for _, p := range []struct {
		name string
		kind pack.Kind
	}{{"onboarding", pack.KindKB}, {"planner", pack.KindAgent}} {
		rel := registrytest.Release(t, "acme", p.name, "1.0.0")
		rel.Kind = p.kind
		if _, err := f.mem.PutRelease(t.Context(), rel, []byte("b")); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		who, body  string
		want       int
		wantCourse string
		wantWiki   bool
	}{
		{"maintainer", `{"course":"acme/onboarding"}`, http.StatusForbidden, "", false},
		{"owner", `{"course":"acme/planner","wiki":true}`, http.StatusBadRequest, "", false},
		{"owner", `{"course":"not an id","wiki":true}`, http.StatusBadRequest, "", false},
		{"owner", `{"course":"acme/onboarding"}`, http.StatusOK, "acme/onboarding", false},
		{"owner", `{"course":"","require_review":true,"wiki":true}`, http.StatusOK, "", true},
		{"owner", `{"course":"acme/onboarding"}`, http.StatusOK, "acme/onboarding", true},
	}
	for _, tt := range tests {
		got, body := f.call(t, f.tokens[tt.who], "PATCH", "/v1/namespaces/web", tt.body)
		if got != tt.want {
			t.Errorf("PATCH %s as %s = %d, want %d\n%s", tt.body, tt.who, got, tt.want, body)
			continue
		}
		if got == http.StatusOK {
			var ns api.Namespace
			if err := json.Unmarshal([]byte(body), &ns); err != nil || ns.Course != tt.wantCourse {
				t.Errorf("PATCH %s answered %s, want course %q", tt.body, body, tt.wantCourse)
			}
		}
		ns, err := f.reg.Namespace(t.Context(), "web")
		course := ""
		if !ns.Course.IsZero() {
			course = ns.Course.String()
		}
		if err != nil || course != tt.wantCourse || ns.Wiki != tt.wantWiki {
			t.Errorf("web after PATCH %s as %s = %+v, %v; want course %q and wiki %v", tt.body, tt.who, ns, err, tt.wantCourse, tt.wantWiki)
		}
	}
}

// TestPatchCourseAbove: clearing a team's course answers with the
// project's, which its wiki shows instead.
func TestPatchCourseAbove(t *testing.T) {
	f := newManageFixture(t)
	rel := registrytest.Release(t, "acme", "onboarding", "1.0.0")
	rel.Kind = pack.KindKB
	if _, err := f.mem.PutRelease(t.Context(), rel, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if got, body := f.call(t, f.tokens["owner"], "PATCH", "/v1/namespaces/acme", `{"course":"acme/onboarding"}`); got != http.StatusOK {
		t.Fatalf("PATCH acme = %d\n%s", got, body)
	}
	got, body := f.call(t, f.tokens["owner"], "PATCH", "/v1/namespaces/web", `{"course":""}`)
	var ns api.Namespace
	if err := json.Unmarshal([]byte(body), &ns); got != http.StatusOK || err != nil || ns.Course != "" || ns.CourseAbove != "acme/onboarding" || ns.CourseFrom != "acme" {
		t.Errorf("PATCH web = %d %s; want the course above", got, body)
	}
}

// loginAs adds email with role in ns and returns a login token of tokenNS
// with scope, as `loadout login` would issue it.
func (f manageFixture) loginAs(t *testing.T, email, ns string, role account.Role, tokenNS string, scope registry.TokenScope) string {
	t.Helper()
	m, _, err := f.accounts.AddMember(t.Context(), account.Operator(), ns, email, role)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := f.reg.CreateUserToken(t.Context(), tokenNS, scope, email, m.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

// TestLoginTokenManagesOnlyItsNamespace: a login token carries the user's
// management rights only with publish scope, only in its own namespace and
// below, and never admin rights.
func TestLoginTokenManagesOnlyItsNamespace(t *testing.T) {
	f := newManageFixture(t)
	ctx := t.Context()
	if _, err := f.reg.CreateNamespace(ctx, "lime", "", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.CreateNamespace(ctx, "old", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	// A maintainer of web holding a read token of web (as a member would).
	readOnly := f.loginAs(t, "reader@example.com", "web", account.RoleMaintainer, "web", registry.ScopeRead)
	// An owner of acme who is also a member of lime, holding a lime token.
	otherTeam := f.loginAs(t, "owner2@example.com", "acme", account.RoleOwner, "lime", registry.ScopePublish)
	if _, _, err := f.accounts.AddMember(ctx, account.Operator(), "lime", "owner2@example.com", account.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	// An admin with a publish token of acme.
	admin, err := f.accounts.SetAdmin(ctx, account.Operator(), "admin@example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	adminTok, _, err := f.reg.CreateUserToken(ctx, "acme", registry.ScopePublish, "admin", admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, token, method, path, body string
		want                            int
	}{
		{"read token adds a member", readOnly, "POST", "/v1/namespaces/web/members", `{"email":"x@example.com","role":"member"}`, http.StatusForbidden},
		{"read token makes a machine token", readOnly, "POST", "/v1/namespaces/web/tokens", `{"scope":"publish","label":"ci"}`, http.StatusForbidden},
		{"token of another team manages acme", otherTeam, "POST", "/v1/namespaces/web/members", `{"email":"y@example.com","role":"maintainer"}`, http.StatusForbidden},
		{"token of another team creates a team", otherTeam, "POST", "/v1/namespaces", `{"name":"mobile","parent":"acme"}`, http.StatusForbidden},
		{"token of lime manages lime", otherTeam, "GET", "/v1/namespaces/lime/members", ``, http.StatusOK},
		{"owner token manages a team below", f.tokens["owner"], "GET", "/v1/namespaces/web/members", ``, http.StatusOK},
		{"admin token creates a root namespace", adminTok, "POST", "/v1/namespaces", `{"name":"globex","kind":"project"}`, http.StatusForbidden},
		{"admin token grants owner", adminTok, "POST", "/v1/namespaces/acme/members", `{"email":"z@example.com","role":"owner"}`, http.StatusForbidden},
		{"admin token deletes a namespace", adminTok, "DELETE", "/v1/namespaces/old", ``, http.StatusForbidden},
		{"admin token manages outside its namespace", adminTok, "GET", "/v1/namespaces/lime/members", ``, http.StatusForbidden},
	}
	for _, tt := range tests {
		if got, body := f.call(t, tt.token, tt.method, tt.path, tt.body); got != tt.want {
			t.Errorf("%s: %s %s = %d, want %d\n%s", tt.name, tt.method, tt.path, got, tt.want, body)
		}
	}
}
