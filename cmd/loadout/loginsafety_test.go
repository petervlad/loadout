package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeLoginRegistry is a registry that approves every `loadout login` at
// once, with a token for namespace, and pages at the addresses it is told.
type fakeLoginRegistry struct {
	// namespace is the team the account belongs to; tokenNS the namespace
	// of the token it issues (default: an account token).
	namespace, tokenNS string
	// page and pageComplete are the verification URIs it answers; empty
	// means its own /auth/device.
	page, pageComplete string

	mu        sync.Mutex
	revoked   []string
	signedOut int
}

const fakeToken = "ldt_faketokenfaketokenfaketokenfaketoken" //nolint:gosec // G101: a made-up token for a fake registry

func (f *fakeLoginRegistry) serve(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Error(err)
		}
	}
	mux.HandleFunc("POST /v1/auth/device", func(w http.ResponseWriter, _ *http.Request) {
		page, complete := f.page, f.pageComplete
		if page == "" {
			page = base + "/auth/device"
		}
		if complete == "" {
			complete = page + "?code=BCDF-GHJK"
		}
		reply(w, map[string]any{
			"device_code": "dev", "user_code": "BCDF-GHJK",
			"verification_uri": page, "verification_uri_complete": complete,
			"expires_in": 900, "interval": 1,
		})
	})
	mux.HandleFunc("POST /v1/auth/device/token", func(w http.ResponseWriter, _ *http.Request) {
		ns := f.tokenNS
		if ns == "" {
			ns = "*"
		}
		reply(w, map[string]any{"status": "approved", "token": fakeToken, "namespace": ns})
	})
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"email": "pat@example.com", "namespaces": []any{map[string]any{"name": f.namespace, "kind": "team", "role": "maintainer"}}})
	})
	mux.HandleFunc("DELETE /v1/auth/token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.revoked = append(f.revoked, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /v1/auth/tokens", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if r.Header.Get("Authorization") == "Bearer "+fakeToken {
			f.signedOut++
		}
		f.mu.Unlock()
		reply(w, map[string]any{"revoked": 3})
	})
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) { reply(w, map[string]any{"status": "ok"}) })
	mux.HandleFunc("GET /v1/packages", func(w http.ResponseWriter, _ *http.Request) { reply(w, map[string]any{"packages": []any{}}) })
	mux.HandleFunc("GET /v1/layers", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"layers": []any{map[string]any{"namespace": f.namespace, "kind": "team", "depth": 0}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL
	return base
}

func (f *fakeLoginRegistry) wasRevoked() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.revoked) == 1 && f.revoked[0] == fakeToken
}

// recordingBrowser only notes what it was asked to open.
type recordingBrowser struct{ opened []string }

func (b *recordingBrowser) Open(_ context.Context, u string) error {
	b.opened = append(b.opened, u)
	return nil
}

func loginWith(t *testing.T, p *person, b *recordingBrowser, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := map[string]string{"HOME": p.home}
	code := runWith(t.Context(), append([]string{"login"}, args...), strings.NewReader(""), &out, &errOut,
		func(k string) string { return env[k] }, p.wd, externals{browser: b, sleep: noSleep})
	return out.String(), errOut.String(), code
}

// TestLoginRefusesANamespaceToken: loadout login asks for an account
// token; a token for one namespace is revoked and never stored.
func TestLoginRefusesANamespaceToken(t *testing.T) {
	f := &fakeLoginRegistry{namespace: "lime", tokenNS: "evil"}
	base := f.serve(t)
	p := &person{session: newSession(t)}
	out, errOut, code := loginWith(t, p, &recordingBrowser{}, "--remote", base)
	if code == 0 || !strings.Contains(errOut, "not for your account") {
		t.Errorf("login given a token for evil: exit %d\nstdout: %s\nstderr: %s; want refused", code, out, errOut)
	}
	if !f.wasRevoked() {
		t.Errorf("the unwanted token was not revoked (revoked %v)", f.revoked)
	}
	if strings.Contains(out, "signed in") {
		t.Errorf("signed in with a token it did not ask for:\n%s", out)
	}
}

// TestLoginBindsTheAccountsTeams: without questions, the account's teams
// are bound to one remote.
func TestLoginBindsTheAccountsTeams(t *testing.T) {
	f := &fakeLoginRegistry{namespace: "lime"}
	base := f.serve(t)
	p := &person{session: newSession(t)}
	out, errOut, code := loginWith(t, p, &recordingBrowser{}, "--remote", base, "--yes")
	if code != 0 || strings.Contains(out, "[y/N]") || !strings.Contains(out, "signed in to "+base+" as pat@example.com") || !strings.Contains(out, "teams: lime (maintainer)") {
		t.Errorf("login: exit %d\nstdout: %s\nstderr: %s; want lime bound without a question", code, out, errOut)
	}
}

// TestLoginOpensOnlyTheRegistrysOwnPage: a device page the registry names
// on another site is neither opened nor shown.
func TestLoginOpensOnlyTheRegistrysOwnPage(t *testing.T) {
	f := &fakeLoginRegistry{namespace: "lime", page: "https://evil.example/auth/device", pageComplete: "https://evil.example/phish?code=BCDF-GHJK"}
	base := f.serve(t)
	b := &recordingBrowser{}
	p := &person{session: newSession(t)}
	out, errOut, code := loginWith(t, p, b, "--remote", base)
	if code != 0 {
		t.Fatalf("login: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if len(b.opened) != 0 {
		t.Errorf("opened %v, want nothing opened for a page on another site", b.opened)
	}
	if strings.Contains(out, "evil.example") || !strings.Contains(out, "Open "+base+"/auth/device and enter the code BCDF-GHJK") {
		t.Errorf("login output = %s, want the registry's own device page", out)
	}

	// Its own pages are opened as before.
	own := &fakeLoginRegistry{namespace: "lime"}
	ownBase := own.serve(t)
	b = &recordingBrowser{}
	if _, errOut, code := loginWith(t, &person{session: newSession(t)}, b, "--team", "lime", "--remote", ownBase); code != 0 {
		t.Fatalf("login: exit %d\nstderr: %s", code, errOut)
	}
	if len(b.opened) != 1 || b.opened[0] != ownBase+"/auth/device?code=BCDF-GHJK" {
		t.Errorf("opened %v, want the registry's device page", b.opened)
	}
}

// TestLogoutAll: logout --all asks the registry to sign the user out
// everywhere, then leaves the team.
func TestLogoutAll(t *testing.T) {
	f := &fakeLoginRegistry{namespace: "lime"}
	base := f.serve(t)
	p := &person{session: newSession(t)}
	if out, errOut, code := loginWith(t, p, &recordingBrowser{}, "--remote", base); code != 0 {
		t.Fatalf("login: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	out := p.must("", "logout", "--all")
	if !strings.Contains(out, "signed out everywhere") || !strings.Contains(out, "left lime") {
		t.Errorf("logout --all = %q", out)
	}
	if f.signedOut != 1 {
		t.Errorf("registry sign-outs = %d, want 1 with the stored token", f.signedOut)
	}
}
