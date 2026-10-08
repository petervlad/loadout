package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
	"github.com/petervlad/loadout/internal/wiki"
)

type wikiFixture struct {
	manageFixture
	member string // an account token of a member of web
}

// newWikiFixture serves acme (a project, wiki off) and web below it
// (wiki on) with the wiki routes.
func newWikiFixture(t *testing.T) wikiFixture {
	t.Helper()
	ctx, log := t.Context(), slog.New(slog.DiscardHandler)
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, time.Now, log, registry.WithMemberships(roles))
	wikis := registry.NewWikiService(regMem, regMem, time.Now, log)
	accounts := account.NewService(store, reg, roles, device.New(time.Now), account.Options{Wiki: wikis, Now: time.Now, Log: log})
	if _, err := reg.CreateNamespace(ctx, "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CreateNamespace(ctx, "web", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetWiki(ctx, account.Operator(), "web", true); err != nil {
		t.Fatal(err)
	}
	f := wikiFixture{reg: reg, accounts: accounts, tokens: map[string]string{}}
	f.tokens["owner"] = f.loginAs(t, "owner@example.com", "acme", account.RoleOwner, "acme", registry.ScopePublish)
	f.tokens["maintainer"] = f.loginAs(t, "maintainer@example.com", "web", account.RoleMaintainer, "web", registry.ScopePublish)
	m, _, err := accounts.AddMember(ctx, account.Operator(), "web", "member@example.com", account.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if f.member, _, err = reg.CreateAccountToken(ctx, "member", m.UserID); err != nil {
		t.Fatal(err)
	}
	if f.machine, _, err = reg.CreateToken(ctx, "web", registry.ScopePublish, "ci"); err != nil {
		t.Fatal(err)
	}
	f.h = server.New(reg, log, "test", server.WithManagement(accounts), server.WithWiki(accounts))
	return f
}

func saveBody(t *testing.T, base int, text string) string {
	t.Helper()
	b, err := json.Marshal(api.WikiSave{Base: base, Format: "md", Text: text, Note: "first"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func TestWikiSaveAndStale(t *testing.T) {
	f := newWikiFixture(t)
	maintainer := f.tokens["maintainer"]

	code, body := f.call(t, f.member, "GET", "/v1/namespaces/web/wiki", "")
	if page := decode[api.WikiPage](t, body); code != http.StatusOK || page.Revision != 0 || page.Format != "md" || page.Text != "" || page.Namespace != "web" {
		t.Fatalf("GET empty wiki = %d %s; want revision 0", code, body)
	}

	code, body = f.call(t, maintainer, "PUT", "/v1/namespaces/web/wiki", saveBody(t, 0, "# Web\r\n"))
	page := decode[api.WikiPage](t, body)
	if code != http.StatusOK || page.Revision != 1 || !page.Saved || page.Text != "# Web\n" || page.Author != "maintainer@example.com" || page.Note != "first" {
		t.Fatalf("PUT on base 0 = %d %s; want revision 1, saved", code, body)
	}

	code, body = f.call(t, f.member, "PUT", "/v1/namespaces/web/wiki", saveBody(t, 0, "# Other\n"))
	e := decode[api.ErrorResponse](t, body).Error
	if code != http.StatusConflict || e.Code != api.CodeStale || e.Newest == nil || e.Newest.Revision != 1 || e.Newest.Author != "maintainer@example.com" {
		t.Fatalf("PUT on base 0 again = %d %s; want 409 stale naming revision 1", code, body)
	}

	code, body = f.call(t, f.member, "PUT", "/v1/namespaces/web/wiki", saveBody(t, 1, "# Web\n"))
	if page := decode[api.WikiPage](t, body); code != http.StatusOK || page.Saved || page.Revision != 1 {
		t.Errorf("PUT of the same text = %d %s; want revision 1, not saved", code, body)
	}

	code, body = f.call(t, f.member, "GET", "/v1/namespaces/web/wiki?revision=1", "")
	if page := decode[api.WikiPage](t, body); code != http.StatusOK || page.Revision != 1 || page.Text != "# Web\n" || page.CreatedAt.IsZero() {
		t.Errorf("GET revision 1 = %d %s", code, body)
	}

	code, body = f.call(t, f.member, "GET", "/v1/namespaces/web/wiki/revisions", "")
	revs := decode[api.WikiRevisions](t, body)
	if code != http.StatusOK || revs.Namespace != "web" || len(revs.Revisions) != 1 || revs.Revisions[0].Size != len("# Web\n") || revs.Revisions[0].Author != "maintainer@example.com" {
		t.Errorf("GET revisions = %d %s; want revision 1", code, body)
	}
}

func TestWikiRoutes(t *testing.T) {
	f := newWikiFixture(t)
	tooLong := saveBody(t, 0, strings.Repeat("<", 2*wiki.MaxSize)) // six bytes each once escaped
	tests := []struct {
		name, token, method, path, body string
		want                            int
		code                            string
	}{
		{"machine token reads", f.machine, "GET", "/v1/namespaces/web/wiki", "", http.StatusForbidden, api.CodeForbidden},
		{"machine token saves", f.machine, "PUT", "/v1/namespaces/web/wiki", saveBody(t, 0, "x"), http.StatusForbidden, api.CodeForbidden},
		{"body over the cap", f.tokens["maintainer"], "PUT", "/v1/namespaces/web/wiki", tooLong, http.StatusRequestEntityTooLarge, api.CodeTooLarge},
		{"300 KB of text", f.tokens["maintainer"], "PUT", "/v1/namespaces/web/wiki", saveBody(t, 0, strings.Repeat("a", 300<<10)), http.StatusBadRequest, api.CodeInvalid},
		{"unknown format", f.tokens["maintainer"], "PUT", "/v1/namespaces/web/wiki", `{"base":0,"format":"html","text":"x"}`, http.StatusBadRequest, api.CodeInvalid},
		{"unknown field", f.tokens["maintainer"], "PUT", "/v1/namespaces/web/wiki", `{"base":0,"format":"md","text":"x","x":1}`, http.StatusBadRequest, api.CodeInvalid},
		{"revision not a number", f.member, "GET", "/v1/namespaces/web/wiki?revision=one", "", http.StatusBadRequest, api.CodeInvalid},
		{"missing revision", f.member, "GET", "/v1/namespaces/web/wiki?revision=9", "", http.StatusNotFound, api.CodeNotFound},
		{"wiki off", f.tokens["owner"], "GET", "/v1/namespaces/acme/wiki", "", http.StatusNotFound, api.CodeNotFound},
		{"missing namespace", f.member, "GET", "/v1/namespaces/nope/wiki/revisions", "", http.StatusNotFound, api.CodeNotFound},
	}
	for _, tt := range tests {
		got, body := f.call(t, tt.token, tt.method, tt.path, tt.body)
		var e api.ErrorResponse
		_ = json.Unmarshal([]byte(body), &e)
		if got != tt.want || e.Error.Code != tt.code {
			t.Errorf("%s: %s %s = %d %s, want %d %s", tt.name, tt.method, tt.path, got, e.Error.Code, tt.want, tt.code)
		}
	}
}

func TestWikiRoutesNeedTheOption(t *testing.T) {
	f := newManageFixture(t)
	if got, body := f.call(t, f.tokens["maintainer"], "GET", "/v1/namespaces/web/wiki", ""); got != http.StatusNotFound {
		t.Errorf("GET wiki without WithWiki = %d %s, want 404", got, body)
	}
}

// TestWikiBodyCap: a page at the size limit made of characters Go's JSON
// encoder escapes still fits the body cap.
func TestWikiBodyCap(t *testing.T) {
	f := newWikiFixture(t)
	body := saveBody(t, 0, strings.Repeat("&", wiki.MaxSize))
	if got, resp := f.call(t, f.tokens["maintainer"], "PUT", "/v1/namespaces/web/wiki", body); got != http.StatusOK {
		t.Errorf("PUT of %d escaped bytes = %d %.200s, want 200", len(body), got, resp)
	}
}
