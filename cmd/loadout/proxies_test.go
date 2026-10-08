package main

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// signInAttempts posts wrong passwords, each for another email so only the
// per-address limit can stop them, and reports the status of the last one.
func signInAttempts(t *testing.T, w *webBrowser, n int) int {
	t.Helper()
	w.send(http.MethodGet, "/auth/login", nil) // the CSRF cookie
	status := 0
	for i := range n {
		status, _, _ = w.send(http.MethodPost, "/auth/login", url.Values{
			"email": {fmt.Sprintf("user%d@example.com", i)}, "password": {"wrong"},
		})
	}
	return status
}

// Behind a proxy that is trusted, each client has its own sign-in limit; with
// no proxy configured (or one that is not the peer) everyone shares the
// proxy's address, which is the problem the settings solve.
func TestTrustedProxiesGiveEachClientItsOwnLimit(t *testing.T) {
	const limit = webIPBurst // wrong attempts one address may make in a row
	tests := []struct {
		name string
		env  map[string]string
		// sharedBucket: a second client is limited by the first one's attempts.
		sharedBucket bool
	}{
		{"trusted proxy", map[string]string{"LOADOUT_TRUSTED_PROXIES": "127.0.0.1", "LOADOUT_CLIENT_IP_HEADER": "Fly-Client-IP"}, false},
		{"trusted range", map[string]string{"LOADOUT_TRUSTED_PROXIES": "127.0.0.0/8,2001:db8::/32", "LOADOUT_CLIENT_IP_HEADER": "CF-Connecting-IP"}, false},
		{"peer is not trusted", map[string]string{"LOADOUT_TRUSTED_PROXIES": "203.0.113.0/24", "LOADOUT_CLIENT_IP_HEADER": "Fly-Client-IP"}, true},
		{"nothing configured", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &person{session: newSession(t)}
			env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_LOGIN": "simple"}
			maps.Copy(env, tt.env)
			base, _ := serveInBackground(t, host, env, externals{})
			header := "Fly-Client-IP"
			if strings.Contains(tt.env["LOADOUT_CLIENT_IP_HEADER"], "CF") {
				header = "CF-Connecting-IP"
			}
			client := func(addr string) *webBrowser {
				w := newWebBrowser(t, base)
				w.header = http.Header{header: {addr}}
				return w
			}
			first, second := client("198.51.100.1"), client("198.51.100.2")
			if got := signInAttempts(t, first, limit+1); got != http.StatusTooManyRequests {
				t.Fatalf("attempt %d from one client = %d, want 429", limit+1, got)
			}
			got := signInAttempts(t, second, 1)
			if tt.sharedBucket && got != http.StatusTooManyRequests {
				t.Errorf("second client = %d, want 429: without a trusted proxy everyone has the proxy's address", got)
			}
			if !tt.sharedBucket && got != http.StatusBadRequest {
				t.Errorf("second client = %d, want 400 (its own limit, a wrong password)", got)
			}
		})
	}
}

func TestBadProxySettingsStopServeAndCheck(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"everyone", "LOADOUT_TRUSTED_PROXIES=0.0.0.0/0\nLOADOUT_CLIENT_IP_HEADER=Fly-Client-IP\n", "every peer"},
		{"x-forwarded-for", "LOADOUT_TRUSTED_PROXIES=203.0.113.0/24\nLOADOUT_CLIENT_IP_HEADER=X-Forwarded-For\n", "holds a list"},
		{"no header", "LOADOUT_TRUSTED_PROXIES=203.0.113.0/24\n", "LOADOUT_CLIENT_IP_HEADER"},
		{"not a range", "LOADOUT_TRUSTED_PROXIES=fly\nLOADOUT_CLIENT_IP_HEADER=Fly-Client-IP\n", `"fly"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &person{session: newSession(t)}
			out, errOut, code := p.run("serve", "--check", "--env-file", p.envFile(tt.env))
			if code != 1 || !strings.Contains(errOut, tt.want) {
				t.Errorf("exit %d, want 1 naming %q\nstdout: %s\nstderr: %s", code, tt.want, out, errOut)
			}
		})
	}

	p := &person{session: newSession(t)}
	good := "LOADOUT_PUBLIC_URL=https://registry.example.com\nLOADOUT_TRUSTED_PROXIES=203.0.113.0/24, 2001:db8::/32\nLOADOUT_CLIENT_IP_HEADER=Fly-Client-IP\n"
	out := p.must("", "serve", "--check", "--env-file", p.envFile(good))
	if !strings.Contains(out, "proxies:    header Fly-Client-IP from 203.0.113.0/24, 2001:db8::/32") {
		t.Errorf("check output lacks the proxies line:\n%s", out)
	}
	out = p.must("", "serve", "--check", "--env-file", p.envFile("LOADOUT_PUBLIC_URL=https://registry.example.com\n"))
	if !strings.Contains(out, "proxies:    a proxy on this machine through X-Real-IP") {
		t.Errorf("check output lacks the default proxies line:\n%s", out)
	}
}
