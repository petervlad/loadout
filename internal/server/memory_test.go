package server_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/mcpmemory"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
	"github.com/petervlad/loadout/internal/ratelimit"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

// memFixture is a registry with every optional route on, team memory
// included: project acme with team web, an owner of acme, a maintainer and a
// member of web, each with an account token, and a stub where the MCP
// handler goes.
type memFixture struct {
	h        http.Handler
	reg      *registry.Service
	tokens   map[string]string // account token per person
	team     string            // a token limited to team web, of the maintainer
	machine  string
	switches *memorytest.MemSwitchStore
	mcpSeen  []string
}

func newMemFixture(t *testing.T, withMemory bool) *memFixture {
	t.Helper()
	return buildMemFixture(t, withMemory, false)
}

// buildMemFixture is newMemFixture with the choice of the real MCP server
// (internal/mcpmemory) over the stub that records what it saw.
func buildMemFixture(t *testing.T, withMemory, realMCP bool) *memFixture {
	t.Helper()
	ctx, log := t.Context(), slog.New(slog.DiscardHandler)
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, time.Now, log, registry.WithMemberships(roles))
	accounts := account.NewService(store, reg, roles, device.New(time.Now), account.Options{Wiki: registry.NewWikiService(regMem, regMem, time.Now, log), Now: time.Now, Log: log})
	for _, ns := range []struct {
		name, parent string
		kind         registry.NamespaceKind
	}{{"acme", "", registry.KindProject}, {"web", "acme", registry.KindTeam}, {"zeta", "", registry.KindProject}, {"ops", "zeta", registry.KindTeam}} {
		if _, err := reg.CreateNamespace(ctx, ns.name, ns.parent, ns.kind); err != nil {
			t.Fatal(err)
		}
	}
	f := &memFixture{reg: reg, tokens: map[string]string{}, switches: memorytest.NewMemSwitchStore()}
	for _, p := range []struct {
		name, ns string
		role     account.Role
	}{{"owner", "acme", account.RoleOwner}, {"maintainer", "web", account.RoleMaintainer}, {"member", "web", account.RoleMember}, {"stranger", "ops", account.RoleMember}} {
		m, _, err := accounts.AddMember(ctx, account.Operator(), p.ns, p.name+"@example.com", p.role)
		if err != nil {
			t.Fatal(err)
		}
		secret, _, err := reg.CreateAccountToken(ctx, p.name, m.UserID)
		if err != nil {
			t.Fatal(err)
		}
		f.tokens[p.name] = secret
		if p.name == "maintainer" {
			if f.team, _, err = reg.CreateUserToken(ctx, "web", registry.ScopePublish, "team only", m.UserID); err != nil {
				t.Fatal(err)
			}
		}
	}
	var err error
	if f.machine, _, err = reg.CreateToken(ctx, "web", registry.ScopePublish, "ci"); err != nil {
		t.Fatal(err)
	}
	opts := []server.Option{
		server.WithManagement(accounts),
		server.WithWiki(accounts),
		server.WithDeviceFlow(accounts, ratelimit.New(1000, time.Second, time.Now)),
		server.WithProposals(registry.NewProposalService(regMem, regMem, time.Now, log)),
		server.WithDeletions(registry.NewDeletionService(regMem, regMem, time.Now, log)),
	}
	if withMemory {
		svc, err := memory.NewService(memorytest.NewMemStore(), f.switches, roles, time.Now, log)
		if err != nil {
			t.Fatal(err)
		}
		toks, err := memory.NewTokens(memorytest.NewMemTokenStore(), account.NewMemoryUsers(store), time.Now, log)
		if err != nil {
			t.Fatal(err)
		}
		var mcp http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.mcpSeen = append(f.mcpSeen, r.Method+" "+r.PathValue("ns"))
			w.WriteHeader(http.StatusNoContent)
		})
		if realMCP {
			if mcp, err = mcpmemory.New(svc, toks, mcpmemory.Options{Log: log}); err != nil {
				t.Fatal(err)
			}
		}
		opts = append(opts, server.WithMemory(server.Memory{Tokens: toks, Switches: svc, PublicURL: "https://reg.example/"}, mcp))
	}
	f.h = server.New(reg, log, "test", opts...)
	return f
}

func (f *memFixture) call(t *testing.T, token, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// makeToken makes a memory token for a person and returns the secret.
func (f *memFixture) makeToken(t *testing.T, who string) api.MemoryToken {
	t.Helper()
	code, body := f.call(t, f.tokens[who], http.MethodPost, "/v1/memory/tokens", `{"label":"laptop"}`)
	if code != http.StatusCreated {
		t.Fatalf("create memory token as %s = %d %s, want 201", who, code, body)
	}
	var tok api.MemoryToken
	if err := json.Unmarshal([]byte(body), &tok); err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestMemoryTokenLifecycle(t *testing.T) {
	f := newMemFixture(t, true)
	tok := f.makeToken(t, "member")
	if !strings.HasPrefix(tok.Token, memory.TokenPrefix) || tok.Label != "laptop" || !tok.ExpiresAt.After(tok.CreatedAt) {
		t.Fatalf("created = %+v, want an ldm_ secret, the label and an expiry", tok)
	}

	code, body := f.call(t, f.tokens["member"], http.MethodGet, "/v1/memory/tokens", "")
	if code != http.StatusOK || strings.Contains(body, tok.Token) {
		t.Fatalf("list = %d %s, want 200 and no secret", code, body)
	}
	var list api.MemoryTokensResponse
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list.Tokens) != 1 || list.Tokens[0].ID != tok.ID {
		t.Fatalf("list = %+v, %v, want the one token", list, err)
	}
	// Someone else sees none of it and cannot revoke it.
	if _, body := f.call(t, f.tokens["owner"], http.MethodGet, "/v1/memory/tokens", ""); strings.Contains(body, tok.ID) {
		t.Errorf("owner lists the member's token: %s", body)
	}
	if code, _ := f.call(t, f.tokens["owner"], http.MethodDelete, "/v1/memory/tokens/"+tok.ID, ""); code != http.StatusNotFound {
		t.Errorf("revoke someone else's token = %d, want 404", code)
	}

	if code, _ := f.call(t, f.tokens["member"], http.MethodDelete, "/v1/memory/tokens/"+tok.ID, ""); code != http.StatusNoContent {
		t.Errorf("revoke = %d, want 204", code)
	}
	if _, body := f.call(t, f.tokens["member"], http.MethodGet, "/v1/memory/tokens", ""); strings.Contains(body, tok.ID) {
		t.Errorf("revoked token still listed: %s", body)
	}
}

func TestMemoryTokenCreateIsNotCached(t *testing.T) {
	f := newMemFixture(t, true)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/memory/tokens", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+f.tokens["member"])
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestMemoryRoutesNeedALoginToken(t *testing.T) {
	f := newMemFixture(t, true)
	for _, tt := range []struct {
		name, token string
		want        int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"machine token", f.machine, http.StatusForbidden},
		{"login token of one team", f.team, http.StatusForbidden},
		{"account token", f.tokens["member"], http.StatusCreated},
	} {
		if code, body := f.call(t, tt.token, http.MethodPost, "/v1/memory/tokens", `{}`); code != tt.want {
			t.Errorf("%s: POST /v1/memory/tokens = %d %s, want %d", tt.name, code, body, tt.want)
		}
		wantList := tt.want
		if wantList == http.StatusCreated {
			wantList = http.StatusOK
		}
		if code, _ := f.call(t, tt.token, http.MethodGet, "/v1/memory/spaces", ""); code != wantList {
			t.Errorf("%s: GET /v1/memory/spaces = %d, want %d", tt.name, code, wantList)
		}
	}
}

func TestMemoryTokenLabelIsChecked(t *testing.T) {
	f := newMemFixture(t, true)
	body := `{"label":"` + strings.Repeat("x", 81) + `"}`
	if code, _ := f.call(t, f.tokens["member"], http.MethodPost, "/v1/memory/tokens", body); code != http.StatusBadRequest {
		t.Errorf("81 character label = %d, want 400", code)
	}
}

func TestMemorySpacesFollowTheSwitchAndRights(t *testing.T) {
	f := newMemFixture(t, true)
	spaces := func(who string) []api.MemorySpace {
		t.Helper()
		code, body := f.call(t, f.tokens[who], http.MethodGet, "/v1/memory/spaces", "")
		if code != http.StatusOK {
			t.Fatalf("GET spaces as %s = %d %s", who, code, body)
		}
		var out api.MemorySpacesResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out.Spaces
	}
	if got := spaces("member"); len(got) != 0 {
		t.Fatalf("spaces while off = %+v, want none", got)
	}
	if code, body := f.call(t, f.tokens["maintainer"], http.MethodPatch, "/v1/namespaces/web", `{"memory":true}`); code != http.StatusOK {
		t.Fatalf("switch on = %d %s", code, body)
	}
	want := api.MemorySpace{Namespace: "web", Kind: api.LayerKind(registry.KindTeam), Endpoint: "https://reg.example/mcp/t/web"}
	for _, who := range []string{"member", "maintainer", "owner"} {
		if got := spaces(who); !slices.Equal(got, []api.MemorySpace{want}) {
			t.Errorf("spaces for %s = %+v, want %+v", who, got, want)
		}
	}
}

func TestPatchNamespaceMemory(t *testing.T) {
	f := newMemFixture(t, true)
	patch := func(token, ns, body string) (int, string) {
		t.Helper()
		return f.call(t, token, http.MethodPatch, "/v1/namespaces/"+ns, body)
	}

	code, body := patch(f.tokens["maintainer"], "web", `{"memory":true}`)
	var ns api.Namespace
	if err := json.Unmarshal([]byte(body), &ns); code != http.StatusOK || err != nil || ns.Name != "web" || !ns.Memory {
		t.Fatalf("maintainer turns memory on = %d %s, want 200 with name web and memory true", code, body)
	}
	if code, body := patch(f.tokens["member"], "web", `{"memory":false}`); code != http.StatusForbidden {
		t.Errorf("member turns memory off = %d %s, want 403", code, body)
	}
	if on, _ := f.switches.Get(t.Context(), "web"); !on.Enabled {
		t.Error("a refused change turned memory off")
	}
	if code, _ := patch(f.machine, "web", `{"memory":false}`); code != http.StatusForbidden {
		t.Errorf("machine token = %d, want 403", code)
	}
	if code, _ := patch(f.team, "web", `{"memory":false}`); code != http.StatusForbidden {
		t.Errorf("token of one team = %d, want 403", code)
	}
	if code, _ := patch(f.tokens["owner"], "ghost", `{"memory":true}`); code != http.StatusNotFound {
		t.Errorf("unknown namespace = %d, want 404", code)
	}
	// Along with another setting, the answer still says where memory stands.
	code, body = patch(f.tokens["owner"], "web", `{"require_review":true,"memory":false}`)
	ns = api.Namespace{}
	if err := json.Unmarshal([]byte(body), &ns); code != http.StatusOK || err != nil || ns.Memory || !ns.RequireReview || ns.Name != "web" {
		t.Errorf("owner changes two settings = %d %s, want 200, review on, memory off", code, body)
	}
}

func TestPatchMemoryWithoutMemory(t *testing.T) {
	f := newMemFixture(t, false)
	if code, body := f.call(t, f.tokens["maintainer"], http.MethodPatch, "/v1/namespaces/web", `{"memory":true}`); code != http.StatusBadRequest {
		t.Errorf("memory on a registry without it = %d %s, want 400", code, body)
	}
	for _, path := range []string{"/v1/memory/tokens", "/v1/memory/spaces"} {
		if code, _ := f.call(t, f.tokens["member"], http.MethodGet, path, ""); code != http.StatusNotFound {
			t.Errorf("GET %s without WithMemory = %d, want 404", path, code)
		}
	}
	if code, _ := f.call(t, "", http.MethodPost, "/mcp/t/web", "{}"); code != http.StatusNotFound {
		t.Errorf("POST /mcp/t/web without WithMemory = %d, want 404", code)
	}
}

// TestMCPHandlerIsMounted: the handler given to WithMemory gets every method
// on /mcp/t/{ns} with the namespace as a path value, and nothing else.
func TestMCPHandlerIsMounted(t *testing.T) {
	f := newMemFixture(t, true)
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		if code, _ := f.call(t, "", method, "/mcp/t/web", ""); code != http.StatusNoContent {
			t.Errorf("%s /mcp/t/web = %d, want the handler's 204", method, code)
		}
	}
	if want := []string{"POST web", "GET web", "DELETE web"}; !slices.Equal(f.mcpSeen, want) {
		t.Errorf("handler saw %v, want %v", f.mcpSeen, want)
	}
	if code, _ := f.call(t, "", http.MethodPost, "/mcp/t/web/extra", ""); code != http.StatusNotFound {
		t.Errorf("POST /mcp/t/web/extra = %d, want 404", code)
	}
}

// publicRoutes answer without a token on purpose: health tells nothing but
// "ok" to a caller without a valid token, and the device flow is how a
// person gets a token.
var publicRoutes = []string{"GET /v1/health", "POST /v1/auth/device", "POST /v1/auth/device/token"}

var pathParam = regexp.MustCompile(`\{[^}]*\}`)

// v1Routes lists every pattern with a /v1 path the server package registers,
// read from its source so a new route cannot be forgotten here.
func v1Routes(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var routes []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if pattern, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(pattern, " /v1/") {
				routes = append(routes, pattern)
			}
			return true
		})
	}
	slices.Sort(routes)
	return slices.Compact(routes)
}

// TestMemoryTokenRefusedEverywhereElse sends an ldm_ token to every /v1 route
// and expects 401: a leaked MCP config must never carry publish rights.
func TestMemoryTokenRefusedEverywhereElse(t *testing.T) {
	f := newMemFixture(t, true)
	secret := f.makeToken(t, "maintainer").Token
	routes := v1Routes(t)
	if len(routes) < 40 {
		t.Fatalf("found only %d /v1 routes in the source, the walk is broken: %v", len(routes), routes)
	}
	walked := 0
	for _, route := range routes {
		if slices.Contains(publicRoutes, route) {
			continue
		}
		method, pattern, _ := strings.Cut(route, " ")
		path := pathParam.ReplaceAllString(pattern, "x")
		code, body := f.call(t, secret, method, path, "{}")
		if code != http.StatusUnauthorized {
			t.Errorf("%s with a memory token = %d %s, want 401", route, code, body)
		}
		walked++
	}
	t.Logf("%d routes refused the memory token", walked)

	// The same token is no better on the public routes: health shows no version.
	if _, body := f.call(t, secret, http.MethodGet, "/v1/health", ""); strings.Contains(body, "version") {
		t.Errorf("health with a memory token shows the version: %s", body)
	}
}
