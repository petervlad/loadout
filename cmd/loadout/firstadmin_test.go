package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var firstAdminPasswordRE = regexp.MustCompile(`password: (\S+)`)

// A fresh simple-login registry gets one admin with a generated password
// shown once; signing in over the web works; a second start creates
// nothing and says nothing; the password is never in the log file.
func TestFirstAdminEndToEnd(t *testing.T) {
	host := &person{session: newSession(t)}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_LOGIN": "simple"}
	host.env = env

	out, errOut, code := host.run("serve", "--check")
	if code != 0 || !strings.Contains(out, "first admin: will be created (empty database)") {
		t.Fatalf("check before the first start: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(host.home, ".loadout", "server", "registry.db")); err == nil {
		t.Error("serve --check created the database")
	}

	var generated string
	t.Run("first start", func(t *testing.T) {
		base, stderr := serveInBackground(t, host, env, externals{})
		log := stderr.String()
		m := firstAdminPasswordRE.FindAllStringSubmatch(log, -1)
		if len(m) != 1 || len(m[0][1]) != 24 {
			t.Fatalf("want one 24 character password in stderr, got %v\n%s", m, log)
		}
		generated = m[0][1]
		if strings.Count(log, generated) != 1 {
			t.Errorf("the password appears %d times in stderr, want 1", strings.Count(log, generated))
		}

		web := newWebBrowser(t, base)
		web.send(http.MethodGet, "/auth/login", nil)
		if status, _, _ := web.send(http.MethodPost, "/auth/login", url.Values{"email": {"admin@loadout.local"}, "password": {generated + "x"}}); status != http.StatusBadRequest {
			t.Errorf("wrong password = %d, want 400", status)
		}
		if status, loc, page := web.send(http.MethodPost, "/auth/login", url.Values{"email": {"admin@loadout.local"}, "password": {generated}}); status != http.StatusSeeOther || loc != "/account" {
			t.Fatalf("sign in = %d %s\n%s", status, loc, page)
		}
		status, _, page := web.send(http.MethodGet, "/account", nil)
		if status != http.StatusOK || !strings.Contains(page, "First-run admin") || !strings.Contains(page, "Yes, of this whole registry") {
			t.Errorf("account page = %d, want the admin and the reminder\n%s", status, page)
		}
	})

	if b, err := os.ReadFile(filepath.Join(host.home, ".loadout", "loadout.log")); err == nil && strings.Contains(string(b), generated) {
		t.Error("the password is in the log file")
	}
	out, _, code = host.run("serve", "--check")
	if code != 0 || !strings.Contains(out, "first admin: exists") || strings.Contains(out, generated) {
		t.Errorf("check after the first start: exit %d\n%s", code, out)
	}

	t.Run("second start", func(t *testing.T) {
		_, stderr := serveInBackground(t, host, env, externals{})
		if strings.Contains(stderr.String(), generated) || strings.Contains(stderr.String(), "first admin created") {
			t.Errorf("a second start created or showed an admin:\n%s", stderr.String())
		}
	})
}

// The configured password is used, never printed, and a weak one stops
// serve before it listens.
func TestFirstAdminConfiguredPassword(t *testing.T) {
	const chosen = "my own long password"
	host := &person{session: newSession(t)}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_LOGIN": "simple", "LOADOUT_ADMIN_PASSWORD": "short"}
	host.env = env
	if out, errOut, code := host.run("serve", "--addr", "127.0.0.1:0"); code == 0 || !strings.Contains(errOut, "LOADOUT_ADMIN_PASSWORD is refused") || strings.Contains(out+errOut, "short\n") {
		t.Fatalf("weak password: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}

	env["LOADOUT_ADMIN_PASSWORD"] = chosen
	base, stderr := serveInBackground(t, host, env, externals{})
	if strings.Contains(stderr.String(), chosen) || strings.Contains(stderr.String(), "password:") {
		t.Errorf("stderr shows a password:\n%s", stderr.String())
	}
	web := newWebBrowser(t, base)
	web.send(http.MethodGet, "/auth/login", nil)
	if status, loc, page := web.send(http.MethodPost, "/auth/login", url.Values{"email": {"admin@loadout.local"}, "password": {chosen}}); status != http.StatusSeeOther || loc != "/account" {
		t.Fatalf("sign in = %d %s\n%s", status, loc, page)
	}
}

// Email login cannot sign the admin in, so nothing is created.
func TestFirstAdminOnlyInSimpleLogin(t *testing.T) {
	host := &person{session: newSession(t)}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL}
	host.env = env
	out, _, code := host.run("serve", "--check")
	if code != 0 || !strings.Contains(out, "first admin: off") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	_, stderr := serveInBackground(t, host, env, externals{})
	if strings.Contains(stderr.String(), "first admin") {
		t.Errorf("email login created an admin:\n%s", stderr.String())
	}
}
