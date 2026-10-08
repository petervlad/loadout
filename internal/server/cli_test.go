package server_test

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/server"
)

// TestCLIAdvice: every answer to a request with a valid token says which
// loadout is current, refusals included; anonymous callers learn nothing.
func TestCLIAdvice(t *testing.T) {
	f := newFixture(t)
	handler := server.New(f.svc, slog.New(slog.DiscardHandler), "v0.5.0", server.WithCLI(semver.New(0, 5, 0), semver.New(0, 3, 0)))
	pub := f.token(t, "lime", registry.ScopePublish)
	read := f.token(t, "lime", registry.ScopeRead)
	if rec := serve(t, handler, request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: bundleFor(t, "lime", "1.0.0")}); rec.Code != http.StatusCreated {
		t.Fatalf("publish = %d, want 201: %s", rec.Code, rec.Body)
	}

	tests := []struct {
		name   string
		req    request
		status int
		advice bool
	}{
		{"listing", request{method: http.MethodGet, path: "/v1/packages", token: read}, 200, true},
		{"bundle", request{method: http.MethodGet, path: "/v1/packages/lime/planner/1.0.0", token: read}, 200, true},
		{"forbidden", request{method: http.MethodPost, path: "/v1/packages", token: read, contentType: bundle.ContentType, body: bundleFor(t, "lime", "1.1.0")}, 403, true},
		{"not found", request{method: http.MethodGet, path: "/v1/packages/lime/nothing", token: read}, 404, true},
		{"health with a token", request{method: http.MethodGet, path: "/v1/health", token: read}, 200, true},
		{"no token", request{method: http.MethodGet, path: "/v1/packages"}, 401, false},
		{"bad token", request{method: http.MethodGet, path: "/v1/packages", token: "ldt_nope"}, 401, false},
		{"health without a token", request{method: http.MethodGet, path: "/v1/health"}, 200, false},
		{"health with a bad token", request{method: http.MethodGet, path: "/v1/health", token: "ldt_nope"}, 200, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(t, handler, tc.req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			latest, minimum := rec.Header().Get(api.HeaderCLILatest), rec.Header().Get(api.HeaderCLIMinimum)
			if tc.advice && (latest != "0.5.0" || minimum != "0.3.0") {
				t.Errorf("headers = %q, %q; want 0.5.0, 0.3.0", latest, minimum)
			}
			if !tc.advice && (latest != "" || minimum != "") {
				t.Errorf("headers = %q, %q; want none", latest, minimum)
			}
		})
	}
}

// TestCLIAdviceZeroSaysNothing: a registry without a release (or without a
// minimum) sends no header for it.
func TestCLIAdviceZeroSaysNothing(t *testing.T) {
	f := newFixture(t)
	read := f.token(t, "lime", registry.ScopeRead)
	for name, h := range map[string]http.Handler{
		"no option": f.handler,
		"zero":      server.New(f.svc, slog.New(slog.DiscardHandler), "dev", server.WithCLI(semver.Version{}, server.MinimumCLI())),
	} {
		rec := serve(t, h, request{method: http.MethodGet, path: "/v1/packages", token: read})
		if got := rec.Header().Values(api.HeaderCLILatest); len(got) != 0 {
			t.Errorf("%s: %s = %q, want absent", name, api.HeaderCLILatest, got)
		}
		if got := rec.Header().Values(api.HeaderCLIMinimum); len(got) != 0 {
			t.Errorf("%s: %s = %q, want absent", name, api.HeaderCLIMinimum, got)
		}
	}
	latestOnly := server.New(f.svc, slog.New(slog.DiscardHandler), "v0.5.0", server.WithCLI(semver.New(0, 5, 0), semver.Version{}))
	rec := serve(t, latestOnly, request{method: http.MethodGet, path: "/v1/packages", token: read})
	if rec.Header().Get(api.HeaderCLILatest) != "0.5.0" || len(rec.Header().Values(api.HeaderCLIMinimum)) != 0 {
		t.Errorf("latest only: headers %v", rec.Header())
	}
}
