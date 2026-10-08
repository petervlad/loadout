package main

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Simple login: an admin with a host-set password signs in with email and password (no
// code), approves `loadout login`, and the log says the mode is on without
// the password.
func TestSimpleLoginEndToEnd(t *testing.T) {
	host := &person{session: newSession(t)}
	cli := &person{session: newSession(t)}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_LOGIN": "simple"}
	base, serverLog := serveInBackground(t, host, env, externals{})
	if !strings.Contains(serverLog.String(), "simple login is on") || strings.Contains(serverLog.String(), "a long enough password") {
		t.Errorf("log = %s, want the simple login line and no password", serverLog.String())
	}

	// The admin is a maintainer of a team, so the token has a role.
	host.must("a long enough password\n", "user", "password", "--email", "ada@example.com")
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	host.must("", "member", "add", "--namespace", "lime", "--email", "ada@example.com", "--role", "maintainer")

	web := newWebBrowser(t, base)
	web.send(http.MethodGet, "/auth/login", nil)
	for _, path := range []string{"/auth/signup", "/auth/google/start"} {
		if status, _, _ := web.send(http.MethodGet, path, nil); status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, status)
		}
	}
	if status, _, _ := web.send(http.MethodPost, "/auth/login", url.Values{"email": {"ada@example.com"}, "password": {"wrong"}}); status != http.StatusBadRequest {
		t.Errorf("wrong password = %d, want 400", status)
	}
	if status, loc, page := web.send(http.MethodPost, "/auth/login", url.Values{"email": {"ada@example.com"}, "password": {"a long enough password"}}); status != http.StatusSeeOther || loc != "/account" {
		t.Fatalf("sign in = %d %s\n%s", status, loc, page)
	}

	var out, errOut bytes.Buffer
	codeRE := regexp.MustCompile(`enter the code ([A-Z]{4}-[A-Z]{4})`)
	approved := false
	approve := func(context.Context, time.Duration) error {
		m := codeRE.FindStringSubmatch(out.String())
		if m == nil || approved {
			return nil
		}
		approved = true
		status, _, page := web.send(http.MethodPost, "/auth/device", url.Values{"code": {m[1]}, "namespace": {"*"}, "action": {"approve"}})
		if status != http.StatusOK || !strings.Contains(page, "Terminal signed in") {
			t.Errorf("approve = %d\n%s", status, page)
		}
		return nil
	}
	cliEnv := map[string]string{"HOME": cli.home}
	if exit := runWith(t.Context(), []string{"login", "--remote", base}, strings.NewReader(""), &out, &errOut,
		func(k string) string { return cliEnv[k] }, cli.wd, externals{sleep: approve}); exit != 0 {
		t.Fatalf("login: exit %d\nstdout: %s\nstderr: %s", exit, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "signed in to "+base+" as ada@example.com") {
		t.Errorf("login output:\n%s", out.String())
	}
}
