package server_test

import (
	"context"
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
	"github.com/petervlad/loadout/internal/ratelimit"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

func TestAPISetsItsOwnCSP(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/v1/health", "/v1/packages", "/v1/nope"} {
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'none'; frame-ancestors 'none'" {
			t.Errorf("GET %s CSP = %q, want the strict API policy", path, got)
		}
	}
}

// member is a Memberships that says every user is a reader.
type member struct{}

func (member) MemberScope(context.Context, string, string) (registry.TokenScope, bool, error) {
	return registry.ScopeRead, true, nil
}

func TestRevokeOwnToken(t *testing.T) {
	store := registrytest.NewMemStore()
	svc := registry.NewService(store, store, store, time.Now, slog.New(slog.DiscardHandler), registry.WithMemberships(member{}))
	h := server.New(svc, slog.New(slog.DiscardHandler), "test")
	tok, _, err := svc.CreateUserToken(t.Context(), "lime", registry.ScopeRead, "a via loadout login", "u1")
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := svc.CreateToken(t.Context(), "lime", registry.ScopeRead, "ci")
	if err != nil {
		t.Fatal(err)
	}
	del := func(tok string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/v1/auth/token", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := del(plain); code != http.StatusForbidden {
		t.Errorf("DELETE /v1/auth/token with an admin-made token = %d, want 403", code)
	}
	if code := del(tok); code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/auth/token = %d, want 204", code)
	}
	if code := del(tok); code != http.StatusUnauthorized {
		t.Errorf("DELETE /v1/auth/token again = %d, want 401: the token is gone", code)
	}
}

func TestPagesKeepTheirCSP(t *testing.T) {
	pages := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "style-src 'self'")
		w.WriteHeader(http.StatusOK)
	})
	h := server.New(newFixture(t).svc, slog.New(slog.DiscardHandler), "test", server.WithPages(pages))
	for path, want := range map[string]string{
		"/auth/login": "style-src 'self'",
		"/account":    "style-src 'self'",
		"/account/x":  "style-src 'self'",
		"/t/lime":     "style-src 'self'",
		"/new":        "style-src 'self'",
		"/skills":     "style-src 'self'",
		"/skills/x":   "style-src 'self'",
		"/":           "style-src 'self'",
		"/v1/health":  "default-src 'none'; frame-ancestors 'none'",
		"/v1/no-such": "default-src 'none'; frame-ancestors 'none'",
		"/other/page": "default-src 'none'; frame-ancestors 'none'",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if got := rec.Header().Get("Content-Security-Policy"); got != want {
			t.Errorf("GET %s CSP = %q, want %q", path, got, want)
		}
	}
}

// fakeDevice answers polls from a fixed list.
type fakeDevice struct {
	polls []account.DevicePoll
	asked []string
}

func (d *fakeDevice) StartDevice(_ context.Context, ns, _ string) (account.DeviceStart, error) {
	d.asked = append(d.asked, ns)
	if ns == "Bad" {
		return account.DeviceStart{}, registry.ErrInvalid
	}
	return account.DeviceStart{
		DeviceCode: "dev-code", UserCode: "BCDF-GHJK",
		VerificationURI: "https://r.example/auth/device", VerificationURIComplete: "https://r.example/auth/device?code=BCDF-GHJK",
		ExpiresIn: 15 * time.Minute, Interval: 5 * time.Second,
	}, nil
}

func (d *fakeDevice) SignOutEverywhere(context.Context, string) (int, int, error) { return 0, 0, nil }

func (d *fakeDevice) Me(context.Context, string) (account.User, []account.Place, error) {
	return account.User{}, nil, nil
}

func (d *fakeDevice) PollDevice(_ context.Context, code string) (account.DevicePoll, error) {
	if code != "dev-code" || len(d.polls) == 0 {
		return account.DevicePoll{Status: device.StatusExpired}, nil
	}
	p := d.polls[0]
	d.polls = d.polls[1:]
	return p, nil
}

// allowN allows n requests in total.
type allowN struct{ n int }

func (a *allowN) Allow(string) bool {
	a.n--
	return a.n >= 0
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDeviceRoutes(t *testing.T) {
	store := newFixture(t).svc
	d := &fakeDevice{polls: []account.DevicePoll{
		{Status: device.StatusPending},
		{Status: device.StatusSlowDown},
		{Status: device.StatusApproved, Token: "ldt_secret", Namespace: "lime", Scope: registry.ScopePublish},
	}}
	h := server.New(store, slog.New(slog.DiscardHandler), "test", server.WithDeviceFlow(d, &allowN{n: 100}))

	rec := postJSON(t, h, "/v1/auth/device", `{"namespace":"lime"}`)
	var start api.DeviceStartResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &start) != nil {
		t.Fatalf("POST /v1/auth/device = %d %s", rec.Code, rec.Body)
	}
	want := api.DeviceStartResponse{
		DeviceCode: "dev-code", UserCode: "BCDF-GHJK",
		VerificationURI: "https://r.example/auth/device", VerificationURIComplete: "https://r.example/auth/device?code=BCDF-GHJK",
		ExpiresIn: 900, Interval: 5,
	}
	if start != want || d.asked[0] != "lime" {
		t.Errorf("start = %+v, want %+v for lime", start, want)
	}
	if rec := postJSON(t, h, "/v1/auth/device", `{"namespace":"Bad"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("start with a bad namespace = %d, want 400", rec.Code)
	}
	if rec := postJSON(t, h, "/v1/auth/device", `{"namespace":"lime","extra":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("start with an unknown field = %d, want 400", rec.Code)
	}

	for _, want := range []api.DeviceTokenResponse{
		{Status: api.DevicePending},
		{Status: api.DeviceSlowDown},
		{Status: api.DeviceApproved, Token: "ldt_secret", Namespace: "lime", Role: "publish"},
		{Status: api.DeviceExpired},
	} {
		rec := postJSON(t, h, "/v1/auth/device/token", `{"device_code":"dev-code"}`)
		var got api.DeviceTokenResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("poll = %d %s", rec.Code, rec.Body)
		}
		if got != want {
			t.Errorf("poll = %+v, want %+v", got, want)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Error("poll response may be cached")
		}
	}
}

func TestDeviceRoutesAreRateLimited(t *testing.T) {
	store := newFixture(t).svc
	h := server.New(store, slog.New(slog.DiscardHandler), "test", server.WithDeviceFlow(&fakeDevice{}, &allowN{n: 1}))
	if rec := postJSON(t, h, "/v1/auth/device", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("first start = %d, want 200", rec.Code)
	}
	rec := postJSON(t, h, "/v1/auth/device", `{}`)
	var body api.ErrorResponse
	if rec.Code != http.StatusTooManyRequests || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error.Code != api.CodeTooManyRequests {
		t.Errorf("second start = %d %s, want 429 too_many_requests", rec.Code, rec.Body)
	}
}

func TestNoDeviceRoutesWithoutLogin(t *testing.T) {
	h := newFixture(t).handler
	if rec := postJSON(t, h, "/v1/auth/device", `{}`); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/auth/device without login = %d, want 404", rec.Code)
	}
}

// TestDeviceStartIsLimitedPerIPv6Prefix: addresses of one IPv6 /64 share a
// limit, so rotating through them does not help.
func TestDeviceStartIsLimitedPerIPv6Prefix(t *testing.T) {
	store := newFixture(t).svc
	h := server.New(store, slog.New(slog.DiscardHandler), "test", server.WithDeviceFlow(&fakeDevice{}, ratelimit.New(1, time.Hour, time.Now)))
	start := func(remote string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/device", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := start("[2001:db8:1:2::1]:4000"); got != http.StatusOK {
		t.Fatalf("first start = %d, want 200", got)
	}
	if got := start("[2001:db8:1:2::abcd]:4000"); got != http.StatusTooManyRequests {
		t.Errorf("start from another address of the same /64 = %d, want 429", got)
	}
	if got := start("[2001:db8:1:3::1]:4000"); got != http.StatusOK {
		t.Errorf("start from another /64 = %d, want 200", got)
	}
}

// TestDeviceStartIsCappedPerClient: one address may have only a few
// sign-ins pending, so it cannot fill the table for everyone.
func TestDeviceStartIsCappedPerClient(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, time.Now, log, registry.WithMemberships(roles))
	accounts := account.NewService(store, reg, roles, device.New(time.Now), account.Options{PublicURL: "https://r.example", Now: time.Now, Log: log})
	h := server.New(reg, log, "test", server.WithDeviceFlow(accounts, &allowN{n: 100}))
	start := func(remote string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/device", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range 5 {
		if got := start("203.0.113.7:4000"); got != http.StatusOK {
			t.Fatalf("start #%d = %d, want 200", i+1, got)
		}
	}
	if got := start("203.0.113.7:4001"); got != http.StatusTooManyRequests {
		t.Errorf("sixth pending start from one address = %d, want 429", got)
	}
	if got := start("198.51.100.1:4000"); got != http.StatusOK {
		t.Errorf("start from another address = %d, want 200", got)
	}
}
