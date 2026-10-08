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

	"github.com/petervlad/loadout/internal/mail/mailtest"
)

var loginCodeRE = regexp.MustCompile(`enter the code ([A-Z]{4}-[A-Z]{4})`)

// loginAs runs `loadout login` for p with args; the person behind web
// approves the code for their whole account.
func loginAs(t *testing.T, p *person, web *webBrowser, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	approved := false
	approve := func(context.Context, time.Duration) error {
		if m := loginCodeRE.FindStringSubmatch(out.String()); m != nil && !approved {
			approved = true
			if status, _, page := web.send(http.MethodPost, "/auth/device", url.Values{"code": {m[1]}, "namespace": {"*"}, "action": {"approve"}}); status != http.StatusOK {
				t.Errorf("approve = %d\n%s", status, page)
			}
		}
		return nil
	}
	env := map[string]string{"HOME": p.home}
	code := runWith(t.Context(), append([]string{"login"}, args...), strings.NewReader(""), &out, &errOut,
		func(k string) string { return env[k] }, p.wd, externals{sleep: approve})
	return out.String(), errOut.String(), code
}

// accountRegistry serves a registry with login on: project acme with teams
// lime and web; alice maintains lime and is a member of web, bob maintains
// web (without an account). lime/planner, web/styles and acme/rules are published.
func accountRegistry(t *testing.T) (host *person, base string, alice *webBrowser) {
	t.Helper()
	host, base, alice, _ = ownedRegistry(t)
	return host, base, alice
}

// ownedRegistry is accountRegistry, also returning the web session of
// olga, who owns acme.
func ownedRegistry(t *testing.T) (host *person, base string, alice, olga *webBrowser) {
	t.Helper()
	host = &person{session: newSession(t)}
	mails := &mailtest.Recorder{}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL}
	base, _ = serveInBackground(t, host, env, externals{mailer: mails})
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	host.must("", "namespace", "create", "web", "--parent", "acme")
	host.must("", "member", "add", "--namespace", "lime", "--email", "alice@example.com", "--role", "maintainer")
	host.must("", "member", "add", "--namespace", "web", "--email", "alice@example.com")
	host.must("", "member", "add", "--namespace", "web", "--email", "bob@example.com", "--role", "maintainer")
	host.must("", "member", "add", "--namespace", "acme", "--email", "olga@example.com", "--role", "owner")
	alice = signUp(t, base, mails, "alice@example.com")
	olga = signUp(t, base, mails, "olga@example.com")

	// The packages are published by their maintainers.
	publisher := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, publisher, olga, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, id := range []string{"lime/planner", "web/styles", "acme/rules"} {
		publisher.must("", "new", "agent", id)
		publisher.must("", "publish", id[strings.Index(id, "/")+1:])
	}
	return host, base, alice, olga
}

// TestLoginFoldsOlderSignIns: remotes of the same registry from before
// account tokens become one; a remote of another registry, and the default
// remote, are kept.
func TestLoginFoldsOlderSignIns(t *testing.T) {
	host, base, aliceWeb := accountRegistry(t)
	// Another registry, joined first, so it is the default remote.
	nora := &person{session: newSession(t)}
	other := startServer(t, nora)
	alice := &person{session: newSession(t)}
	alice.must(createInvite(t, nora, "north", "read", other)+"\n", "team", "join")
	// Two older sign-ins of the registry: a team joined with an invite,
	// and a remote added by hand with a trailing slash.
	alice.must(createInvite(t, host, "lime", "read", base)+"\n", "team", "join")
	machine := tokenRE.FindString(host.must("", "token", "create", "--team", "web", "--read", "--label", "hand"))
	alice.must(machine+"\n", "remote", "add", "byhand", base+"/", "--token-stdin")
	before := alice.must("", "remote", "list")
	defaultRE := regexp.MustCompile(`(?m)^\*?\s*(\S+).*` + regexp.QuoteMeta(other))
	if defaultRE.FindString(before) == "" {
		t.Fatalf("remote list = %s", before)
	}

	out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base)
	if code != 0 {
		t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "replaced older sign-ins of this registry") {
		t.Errorf("login = %s, want the older remotes folded", out)
	}
	cfg := alice.file("config.yaml")
	if n := strings.Count(cfg, "url: "+base); n != 1 {
		t.Errorf("config.yaml has %d remotes of the registry, want 1:\n%s", n, cfg)
	}
	if !strings.Contains(cfg, "url: "+other) || !strings.Contains(cfg, "namespace: north") {
		t.Errorf("config.yaml lost the other registry:\n%s", cfg)
	}
	if !regexp.MustCompile(`(?m)^default: 127-0-0-1\s*$`).MatchString(cfg) {
		t.Errorf("the default remote changed:\n%s", cfg)
	}
	if strings.Contains(cfg, "byhand") {
		t.Errorf("the hand-added remote was kept:\n%s", cfg)
	}
	if creds := alice.file("credentials.yaml"); strings.Contains(creds, machine) || strings.Count(creds, "ldt_") != 2 {
		t.Errorf("credentials.yaml = %s, want the other registry's token and the account token only", creds)
	}
	// The other registry still works, and so do all three layers here.
	list := alice.must("", "list")
	for _, want := range []string{"lime/planner", "web/styles", "acme/rules"} {
		if !strings.Contains(list, want) {
			t.Errorf("list = %s, want %s", list, want)
		}
	}
	if strings.Count(list, "acme/rules") != 1 {
		t.Errorf("list shows acme/rules %d times, want once:\n%s", strings.Count(list, "acme/rules"), list)
	}

	// A new name for the remote folds the old one, and it becomes the
	// default when the old one was.
	carolHome := &person{session: newSession(t)}
	carolHome.must(createInvite(t, host, "lime", "read", base)+"\n", "team", "join")
	out, errOut, code = loginAs(t, carolHome, aliceWeb, "--remote", base, "--name", "work")
	if code != 0 {
		t.Fatalf("login --name work: exit %d\n%s\n%s", code, out, errOut)
	}
	cfg = carolHome.file("config.yaml")
	if !strings.Contains(cfg, "default: work") || strings.Count(cfg, "url: ") != 1 {
		t.Errorf("config.yaml after login --name work:\n%s", cfg)
	}
	// A name taken by another registry is refused, and nothing changes.
	carolHome.must(createInvite(t, nora, "north", "read", other)+"\n", "team", "join")
	before = carolHome.file("config.yaml")
	remoteOfOther := regexp.MustCompile(`name: (\S+)\n\s+url: ` + regexp.QuoteMeta(other)).FindStringSubmatch(before)
	if remoteOfOther == nil {
		t.Fatalf("config.yaml:\n%s", before)
	}
	if _, errOut, code := loginAs(t, carolHome, aliceWeb, "--remote", base, "--name", remoteOfOther[1]); code == 0 || !strings.Contains(errOut, "another URL") {
		t.Errorf("login --name of another registry: exit %d, %s; want refused", code, errOut)
	}
	if after := carolHome.file("config.yaml"); after != before {
		t.Errorf("a refused login changed config.yaml:\n%s\nwas:\n%s", after, before)
	}
}

// TestAccountListAndInstallAll: one account over two teams lists and
// installs each package once, including the project both teams read.
func TestAccountListAndInstallAll(t *testing.T) {
	_, base, aliceWeb := accountRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
	}
	out := alice.must("", "install", "--all")
	for _, name := range []string{"planner", "styles", "rules"} {
		if !strings.Contains(installedAgent(t, alice, name), "/"+name+"@0.1.0") {
			t.Errorf("install --all did not install %s:\n%s", name, out)
		}
	}
	if n := strings.Count(out, "acme/rules"); n != 1 {
		t.Errorf("install --all mentions acme/rules %d times, want once:\n%s", n, out)
	}
	layers := alice.must("", "layers")
	if strings.Count(layers, "LAYER") != 2 || !strings.Contains(layers, "lime") || !strings.Contains(layers, "web") {
		t.Errorf("layers = %s, want one table per team", layers)
	}
	if out := alice.must("", "namespace", "list"); !strings.Contains(out, "acme") || !strings.Contains(out, "web") || !strings.Contains(out, "alice@example.com") {
		t.Errorf("namespace list = %s", out)
	}
	if out := alice.must("", "sync"); strings.Contains(out, "error") {
		t.Errorf("sync = %s", out)
	}
}

// TestAccountLogout covers the logout variants with an account token.
func TestAccountLogout(t *testing.T) {
	_, base, aliceWeb := accountRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
	}
	token := tokenRE.FindString(alice.file("credentials.yaml"))

	// --team leaves one team here; the token stays for the other.
	out := alice.must("", "logout", "--team", "lime")
	if !strings.Contains(out, "left lime") || strings.Contains(out, "revoked") {
		t.Errorf("logout --team lime = %s, want lime left and the token kept", out)
	}
	if cfg := alice.file("config.yaml"); strings.Contains(cfg, "namespace: lime") || !strings.Contains(cfg, "namespace: web") || !strings.Contains(cfg, "namespace: acme") {
		t.Errorf("config.yaml after logout --team lime:\n%s", cfg)
	}
	if status := getWithToken(t, base+"/v1/me", token); status != http.StatusOK {
		t.Errorf("GET /v1/me after logout --team lime = %d, want the token to still work", status)
	}
	if list := alice.must("", "list"); !strings.Contains(list, "web/styles") || !strings.Contains(list, "acme/rules") || strings.Contains(list, "lime/planner") {
		t.Errorf("list after leaving lime = %s", list)
	}
	if _, errOut, code := alice.runIn("", "logout", "--team", "lime"); code == 0 || !strings.Contains(errOut, "have not joined lime") {
		t.Errorf("logout --team lime twice: exit %d, %s", code, errOut)
	}

	// --team of the last team is a whole logout: the token is revoked.
	out = alice.must("", "logout", "--team", "web")
	if !strings.Contains(out, "revoked the token") || !strings.Contains(out, "left web") {
		t.Errorf("logout --team web = %s, want a whole logout", out)
	}
	if status := getWithToken(t, base+"/v1/me", token); status != http.StatusUnauthorized {
		t.Errorf("GET /v1/me after the last logout = %d, want 401", status)
	}
	if creds := alice.file("credentials.yaml"); strings.Contains(creds, token) {
		t.Errorf("the token is still stored:\n%s", creds)
	}
	if cfg := alice.file("config.yaml"); strings.Contains(cfg, base) {
		t.Errorf("the remote is still there:\n%s", cfg)
	}

	// --team with --all names the registry even with two of them.
	nora := &person{session: newSession(t)}
	other := startServer(t, nora)
	alice.must(createInvite(t, nora, "north", "read", other)+"\n", "team", "join")
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("login again: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, errOut, code := alice.runIn("", "logout"); code == 0 || !strings.Contains(errOut, "pass --remote") {
		t.Errorf("logout with two registries: exit %d, %s; want asked for --remote", code, errOut)
	}
	out, errOut, code := alice.runIn("", "logout", "--team", "lime", "--all")
	if code != 0 || !strings.Contains(out, "signed out everywhere") || !strings.Contains(out, "left lime") || !strings.Contains(out, "left web") {
		t.Errorf("logout --team lime --all: exit %d\n%s\n%s; want a sign-out of lime's registry", code, out, errOut)
	}
	if cfg := alice.file("config.yaml"); !strings.Contains(cfg, "namespace: north") || strings.Contains(cfg, "namespace: lime") {
		t.Errorf("config.yaml after logout --team lime --all:\n%s", cfg)
	}
}

// TestAccountAfterLosingATeam: when a person is removed from one of their
// teams, the others keep working on this machine without a new login.
func TestAccountAfterLosingATeam(t *testing.T) {
	host, base, aliceWeb := accountRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
	}
	alice.must("", "install", "lime/planner")
	alice.must("", "install", "acme/rules")
	host.must("", "member", "remove", "--namespace", "web", "--email", "alice@example.com")

	if out, errOut, code := alice.runIn("", "list"); code != 0 || !strings.Contains(out, "lime/planner") || !strings.Contains(out, "acme/rules") || !strings.Contains(out, "no longer a member of web") {
		t.Errorf("list after losing web: exit %d\n%s\n%s; want lime's packages and a note", code, out, errOut)
	}
	if out, errOut, code := alice.runIn("", "sync"); code != 0 {
		t.Errorf("sync after losing web: exit %d\n%s\n%s", code, out, errOut)
	}
}

// TestAccountAfterLosingTheFirstTeam: the project came in through lime;
// once alice leaves lime she still reads it through web, so sync keeps
// updating its packages.
func TestAccountAfterLosingTheFirstTeam(t *testing.T) {
	host, base, aliceWeb := accountRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("login: exit %d\n%s\n%s", code, out, errOut)
	}
	alice.must("", "install", "acme/rules")
	host.must("", "member", "remove", "--namespace", "lime", "--email", "alice@example.com")
	if out, errOut, code := alice.runIn("", "sync"); code != 0 || strings.Contains(out, "not a member") {
		t.Errorf("sync after losing lime: exit %d\n%s\n%s; want acme/rules checked through web", code, out, errOut)
	}
}
