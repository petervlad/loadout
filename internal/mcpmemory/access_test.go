package mcpmemory_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/mcpmemory"
	"github.com/petervlad/loadout/internal/ratelimit"
)

func TestAccessMatrix(t *testing.T) {
	r := newRig(t)
	init := rpc(1, "initialize", initParams)
	for _, tc := range []struct {
		name   string
		secret string
		ns     string
		want   int
	}{
		{"member", secDana, "lime", 200},
		{"member, project endpoint", secDana, "acme", 404},
		{"project member on the project", secPM, "acme", 200},
		{"project member on the team", secPM, "lime", 404},
		{"admin on the team", secAdmin, "lime", 200},
		{"admin on an unknown team", secAdmin, "nope", 404},
		{"valid token, not a member", secOutside, "lime", 404},
		{"member, unknown team", secDana, "nope", 404},
		{"no token", "", "lime", 401},
		{"no token, unknown team", "", "nope", 401},
		{"wrong token", "ldm_wrong", "lime", 401},
		{"expired token", "ldm_expired", "lime", 401},
		{"login token of a member", secLogin, "lime", 401},
		{"login token, unknown team", secLogin, "nope", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, hdr, body := r.post(t, tc.secret, tc.ns, init)
			if status != tc.want {
				t.Fatalf("status = %d, want %d: %s", status, tc.want, body)
			}
			if status == 401 && hdr.Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("WWW-Authenticate = %q", hdr.Get("WWW-Authenticate"))
			}
		})
	}

	t.Run("every 401 has one body", func(t *testing.T) {
		bodies := map[string]bool{}
		for _, secret := range []string{"", "ldm_wrong", "ldm_expired", secLogin} {
			_, _, body := r.post(t, secret, "lime", init)
			bodies[body] = true
		}
		if len(bodies) != 1 {
			t.Errorf("%d different 401 bodies: %v", len(bodies), bodies)
		}
	})

	t.Run("every 404 has one body", func(t *testing.T) {
		if err := r.switches.Set(t.Context(), "off", false, "boss", r.clock.now()); err != nil {
			t.Fatal(err)
		}
		bodies := map[string]bool{}
		for _, c := range []struct{ secret, ns string }{
			{secOutside, "lime"}, {secDana, "nope"}, {secPM, "lime"}, {secDana, "off"}, {secAdmin, "nope"},
		} {
			status, _, body := r.post(t, c.secret, c.ns, init)
			if status != 404 {
				t.Fatalf("%s on %s: status %d", c.secret, c.ns, status)
			}
			bodies[body] = true
		}
		if len(bodies) != 1 {
			t.Errorf("%d different 404 bodies: %v", len(bodies), bodies)
		}
	})

	t.Run("switch off", func(t *testing.T) {
		if err := r.switches.Set(t.Context(), "lime", false, "boss", r.clock.now()); err != nil {
			t.Fatal(err)
		}
		if status, _, _ := r.post(t, secDana, "lime", init); status != 404 {
			t.Errorf("status with the switch off = %d, want 404", status)
		}
	})
}

func TestProjectOffHidesProject(t *testing.T) {
	r := newRig(t)
	r.seed(t, "acme", "Project fact", "from above", "", dana)
	if err := r.switches.Set(t.Context(), "acme", false, "boss", r.clock.now()); err != nil {
		t.Fatal(err)
	}
	s := r.connect(t, secDana, "lime")
	if in := s.InitializeResult().Instructions; strings.Contains(in, "Project fact") || strings.Contains(in, "acme") {
		t.Errorf("project shows while its switch is off:\n%s", in)
	}
	if got := text(t, s.call(t, "search", map[string]any{"query": "project"})); strings.Contains(got, "<memory") {
		t.Errorf("search reads a project with memory off:\n%s", got)
	}
}

func TestMethodsOtherThanPost(t *testing.T) {
	r := newRig(t)
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		req, err := http.NewRequestWithContext(t.Context(), m, r.url("lime"), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+secDana)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "POST" {
			t.Errorf("%s: status %d, Allow %q", m, resp.StatusCode, resp.Header.Get("Allow"))
		}
		req.Header.Del("Authorization")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a token: status %d, want 401", m, resp.StatusCode)
		}
	}
}

func TestBodyCap(t *testing.T) {
	r := newRig(t)
	big := rpc(1, "tools/call", `{"name":"save","arguments":{"body":"`+strings.Repeat("a", mcpmemory.MaxBody)+`"}}`)
	if status, _, _ := r.post(t, secDana, "lime", big); status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", status)
	}
	// Without a Content-Length the cap still holds.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.url("lime"), io.NopCloser(bytes.NewReader([]byte(big))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secDana)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("a chunked body over the cap got status %d", resp.StatusCode)
	}
}

func TestRateLimitPerToken(t *testing.T) {
	r := newRig(t)
	ping := rpc(1, "ping", "")
	for i := range mcpmemory.CallBurst {
		if status, _, body := r.post(t, secDana, "lime", ping); status != 200 {
			t.Fatalf("call %d: status %d: %s", i+1, status, body)
		}
	}
	status, hdr, _ := r.post(t, secDana, "lime", ping)
	if status != http.StatusTooManyRequests || hdr.Get("Retry-After") == "" {
		t.Errorf("call 121: status %d, Retry-After %q", status, hdr.Get("Retry-After"))
	}
	if status, _, _ := r.post(t, secLee, "lime", ping); status != 200 {
		t.Errorf("another token is limited too: status %d", status)
	}
	r.clock.advance(time.Second)
	for i := range 2 {
		if status, _, _ := r.post(t, secDana, "lime", ping); status != 200 {
			t.Errorf("after a second, call %d: status %d", i+1, status)
		}
	}
	if status, _, _ := r.post(t, secDana, "lime", ping); status != http.StatusTooManyRequests {
		t.Errorf("a third call after a second: status %d, want 429", status)
	}
}

func TestOwnLimiter(t *testing.T) {
	r := newRig(t, func(o *mcpmemory.Options) {
		o.Limiter = ratelimit.New(1, time.Hour, time.Now)
	})
	ping := rpc(1, "ping", "")
	if status, _, _ := r.post(t, secDana, "lime", ping); status != 200 {
		t.Fatalf("first call: %d", status)
	}
	if status, _, _ := r.post(t, secDana, "lime", ping); status != http.StatusTooManyRequests {
		t.Errorf("second call: %d, want 429", status)
	}
}

func TestNewNeedsParts(t *testing.T) {
	r := newRig(t)
	if _, err := mcpmemory.New(nil, fakeAuth{}, mcpmemory.Options{}); err == nil {
		t.Error("New without a service succeeded")
	}
	if _, err := mcpmemory.New(r.svc, nil, mcpmemory.Options{}); err == nil {
		t.Error("New without an authenticator succeeded")
	}
}
