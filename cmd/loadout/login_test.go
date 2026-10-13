package main

import (
	"bytes"
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/mail/mailtest"
)

const publicURL = "http://localhost"

// serveInBackground runs `loadout serve` for p with env and ext until the
// test ends, and returns the address it listens on and its log.
func serveInBackground(t *testing.T, p *person, env map[string]string, ext externals) (string, *syncBuffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	stderr := &syncBuffer{}
	exited := make(chan struct{})
	var code int
	go func() {
		defer close(exited)
		code = runWith(ctx, []string{"serve", "--addr", "127.0.0.1:0"}, strings.NewReader(""), io.Discard, stderr,
			func(k string) string { return env[k] }, p.wd, ext)
	}()
	t.Cleanup(func() {
		cancel()
		<-exited
	})
	for deadline := time.Now().Add(10 * time.Second); ; {
		if m := listeningRE.FindStringSubmatch(stderr.String()); m != nil {
			return "http://" + m[1], stderr
		}
		select {
		case <-exited:
			t.Fatalf("serve exited with %d before listening\nstderr: %s", code, stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve did not log its address\nstderr: %s", stderr.String())
		}
	}
}

// webBrowser is a person's browser on the registry's pages: it keeps
// cookies and shows redirects instead of following them.
type webBrowser struct {
	t    *testing.T
	base string
	c    *http.Client
	// header is added to every request, as a proxy in front would.
	header http.Header
}

func newWebBrowser(t *testing.T, base string) *webBrowser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &webBrowser{t: t, base: base, c: &http.Client{
		Jar:           jar,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// local turns a URL on the public address into one on the test server.
func (b *webBrowser) local(u string) string {
	return b.base + strings.TrimPrefix(u, publicURL)
}

func (b *webBrowser) send(method, path string, form url.Values) (int, string, string) {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		u, err := url.Parse(b.base)
		if err != nil {
			b.t.Fatal(err)
		}
		for _, c := range b.c.Jar.Cookies(u) {
			if c.Name == "loadout_csrf" {
				form.Set("csrf", c.Value)
			}
		}
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(b.t.Context(), method, b.base+path, body)
	if err != nil {
		b.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	maps.Copy(req.Header, b.header)
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Get("Location"), string(data)
}

// approver is the stubbed browser `loadout login` opens: it shows the
// device page to the signed-in person, who approves the code.
type approver struct {
	t      *testing.T
	web    *webBrowser
	opened []string
}

func (a *approver) Open(_ context.Context, u string) error {
	a.opened = append(a.opened, u)
	path := strings.TrimPrefix(a.web.local(u), a.web.base)
	status, _, page := a.web.send(http.MethodGet, path, nil)
	code := regexp.MustCompile(`name="code" value="([A-Z-]+)"`).FindStringSubmatch(page)
	if status != http.StatusOK || code == nil || !strings.Contains(page, "lime (maintainer)") || !strings.Contains(page, "Every team you belong to") {
		a.t.Errorf("device page = %d, want the code and her teams\n%s", status, page)
		return nil
	}
	status, _, page = a.web.send(http.MethodPost, "/auth/device", url.Values{"code": {code[1]}, "namespace": {"*"}, "action": {"approve"}})
	if status != http.StatusOK || !strings.Contains(page, "Terminal signed in") {
		a.t.Errorf("approve = %d\n%s", status, page)
	}
	return nil
}

func noSleep(context.Context, time.Duration) error { return nil }

func TestLoginEndToEnd(t *testing.T) {
	host := &person{session: newSession(t)}
	alice := &person{session: newSession(t)}
	mails := &mailtest.Recorder{}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL}
	base, serverLog := serveInBackground(t, host, env, externals{mailer: mails})

	// The operator makes project acme with team lime, and Alice a
	// maintainer of lime before she has an account.
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	if out := host.must("", "member", "add", "--namespace", "lime", "--email", "Alice@Example.com", "--role", "maintainer"); !strings.Contains(out, "alice@example.com is a maintainer of lime") {
		t.Errorf("member add = %q", out)
	}
	if _, errOut, code := host.runIn("", "member", "add", "--namespace", "lime", "--email", "x@example.com", "--role", "owner"); code == 0 || !strings.Contains(errOut, "owner is a role on projects") {
		t.Errorf("owner of a team: exit %d, %q; want refused", code, errOut)
	}
	// She is also a plain member of a second team, web; acme publishes a
	// package of its own.
	host.must("", "namespace", "create", "web", "--parent", "acme")
	host.must("", "member", "add", "--namespace", "web", "--email", "alice@example.com")

	// Alice signs up in her browser and confirms her address with the code.
	web := newWebBrowser(t, base)
	web.send(http.MethodGet, "/auth/signup", nil)
	const pw = "tangerine kettle river"
	if status, loc, page := web.send(http.MethodPost, "/auth/signup", url.Values{"email": {"alice@example.com"}, "password": {pw}}); status != http.StatusSeeOther {
		t.Fatalf("signup = %d %s\n%s", status, loc, page)
	}
	m := mails.WaitFor(t, "alice@example.com", 0)
	code := regexp.MustCompile(`\b(\d{6})\b`).FindStringSubmatch(m.Text)
	if code == nil {
		t.Fatalf("no code in mail:\n%s", m.Text)
	}
	if status, loc, _ := web.send(http.MethodPost, "/auth/verify", url.Values{"code": {code[1]}}); status != http.StatusSeeOther || loc != "/account" {
		t.Fatalf("verify = %d %s", status, loc)
	}

	// loadout login: she approves the printed code on her web session. The
	// test server's address is not the public URL, so the CLI prints the
	// registry's own device page and opens nothing.
	browser := &approver{t: t, web: web}
	var out, errOut bytes.Buffer
	codeRE := regexp.MustCompile(`enter the code ([A-Z]{4}-[A-Z]{4})`)
	approveOnce := func(ctx context.Context, _ time.Duration) error {
		if m := codeRE.FindStringSubmatch(out.String()); m != nil && len(browser.opened) == 0 {
			return browser.Open(ctx, publicURL+"/auth/device?code="+m[1])
		}
		return nil
	}
	aliceEnv := map[string]string{"HOME": alice.home}
	exit := runWith(t.Context(), []string{"login", "--remote", base}, strings.NewReader(""), &out, &errOut,
		func(k string) string { return aliceEnv[k] }, alice.wd, externals{sleep: approveOnce})
	if exit != 0 {
		t.Fatalf("login: exit %d\nstdout: %s\nstderr: %s", exit, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "Open "+base+"/auth/device and enter the code") {
		t.Errorf("login output does not point at the registry's device page:\n%s", out.String())
	}
	for _, want := range []string{"enter the code", "signed in to " + base + " as alice@example.com", "teams: lime (maintainer), web (member)", "also reads acme"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("login output is missing %q:\n%s", want, out.String())
		}
	}
	creds := alice.file("credentials.yaml")
	if !tokenRE.MatchString(creds) {
		t.Errorf("credentials.yaml has no token:\n%s", creds)
	}
	if fi, err := os.Stat(filepath.Join(alice.home, ".loadout", "credentials.yaml")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("credentials.yaml mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	if cfg := alice.file("config.yaml"); !strings.Contains(cfg, "namespace: lime") || !strings.Contains(cfg, "namespace: web") || tokenRE.MatchString(cfg) || strings.Count(cfg, "url:") != 1 {
		t.Errorf("config.yaml = %s, want lime and web bound to one remote, and no token", cfg)
	}

	// The token works like any other: publish, list, install.
	alice.must("", "new", "agent", "lime/planner")
	if out := alice.must("", "publish", "planner"); !strings.Contains(out, "published lime/planner@0.1.0") {
		t.Errorf("publish = %q", out)
	}
	if out := alice.must("", "list"); !strings.Contains(out, "lime/planner") {
		t.Errorf("list = %q", out)
	}
	alice.must("", "install", "lime/planner")
	if agent := installedAgent(t, alice, "planner"); !strings.Contains(agent, "lime/planner@0.1.0") {
		t.Errorf("installed agent:\n%s", agent)
	}
	// The same token works in web as a member there: she proposes, but
	// cannot publish.
	alice.must("", "new", "agent", "web/styles")
	if _, errOut, code := alice.runIn("", "publish", "styles"); code == 0 || !strings.Contains(errOut, "maintainer role") {
		t.Errorf("publish to web as a member: exit %d, %q; want forbidden", code, errOut)
	}
	if out := alice.must("", "propose", "styles", "--prompt", "style it"); !strings.Contains(out, "proposed") {
		t.Errorf("propose to web = %q", out)
	}
	// A team member reads its project and may propose there too.
	alice.must("", "new", "agent", "acme/rules")
	if out := alice.must("", "propose", "rules", "--prompt", "rules"); !strings.Contains(out, "proposed") {
		t.Errorf("propose to acme = %q", out)
	}
	// Signing in again keeps one remote and replaces the token.
	oldToken := tokenRE.FindString(creds)
	browser.opened = nil
	out.Reset()
	if exit := runWith(t.Context(), []string{"login", "--remote", base}, strings.NewReader(""), &out, &errOut,
		func(k string) string { return aliceEnv[k] }, alice.wd, externals{sleep: approveOnce}); exit != 0 {
		t.Fatalf("second login: exit %d\nstdout: %s\nstderr: %s", exit, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "using remote") || strings.Count(alice.file("config.yaml"), "url:") != 1 {
		t.Errorf("second login = %s\nconfig: %s; want the same remote", out.String(), alice.file("config.yaml"))
	}
	creds = alice.file("credentials.yaml")
	if strings.Contains(creds, oldToken) {
		t.Error("the first token is still stored after signing in again")
	}

	// As a maintainer she manages lime's members over HTTP, but cannot
	// create teams in acme (that takes an owner).
	remoteName := regexp.MustCompile(`using remote (\S+)\)`).FindStringSubmatch(out.String())
	if remoteName == nil {
		t.Fatalf("no remote name in login output:\n%s", out.String())
	}
	r := remoteName[1]
	if out := alice.must("", "member", "add", "--remote", r, "--namespace", "lime", "--email", "bob@example.com"); !strings.Contains(out, "bob@example.com is a member of lime") {
		t.Errorf("remote member add = %q", out)
	}
	if out := alice.must("", "member", "list", "--remote", r, "--namespace", "lime"); !strings.Contains(out, "bob@example.com") || !strings.Contains(out, "maintainer") {
		t.Errorf("remote member list = %q", out)
	}
	// Her laptop runs no registry, so without --remote the commands use
	// the registry she signed in to.
	if out := alice.must("", "member", "list", "--namespace", "lime"); !strings.Contains(out, "bob@example.com") {
		t.Errorf("member list without --remote = %q", out)
	}
	if out := alice.must("", "namespace", "list"); !strings.Contains(out, "acme") || !strings.Contains(out, "lime") || !strings.Contains(out, "L0") {
		t.Errorf("namespace list without --remote = %q, want her layers", out)
	}
	if _, errOut, code := alice.runIn("", "namespace", "create", "mobile", "--parent", "acme", "--remote", r); code == 0 || !strings.Contains(errOut, "forbidden") {
		t.Errorf("maintainer creating a team: exit %d, %q; want forbidden", code, errOut)
	}

	// With review required, publishing is refused and proposing works.
	host.must("", "namespace", "set", "lime", "--require-review", "on")
	if _, errOut, code := alice.runIn("", "publish", "planner", "--bump", "patch"); code == 0 || !strings.Contains(errOut, "requires review") {
		t.Errorf("publish with review required: exit %d, %q", code, errOut)
	}
	if out := alice.must("", "propose", "planner", "--bump", "patch", "--prompt", "plan TRK-1"); !strings.Contains(out, "proposed") {
		t.Errorf("propose = %q", out)
	}

	// logout revokes the token on the registry and leaves the team.
	token := tokenRE.FindString(creds)
	if out := alice.must("", "logout"); !strings.Contains(out, "revoked the token") || !strings.Contains(out, "left lime") || !strings.Contains(out, "left web") {
		t.Errorf("logout = %q", out)
	}
	if strings.Contains(alice.file("credentials.yaml"), token) {
		t.Error("the token is still stored after logout")
	}
	if status := getWithToken(t, base+"/v1/me", token); status != http.StatusUnauthorized {
		t.Errorf("GET /v1/me with the token after logout = %d, want 401", status)
	}

	// Nothing secret reached the server log.
	logText := serverLog.String()
	for name, secret := range map[string]string{"password": pw, "code": code[1], "token": token} {
		if strings.Contains(logText, secret) {
			t.Errorf("server log contains the %s", name)
		}
	}
	if !strings.Contains(logText, "login is on") {
		t.Errorf("server did not log that login is on:\n%s", logText)
	}
}

func getWithToken(t *testing.T, u, token string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestServeWithoutPublicURLHasNoLogin(t *testing.T) {
	host := &person{session: newSession(t)}
	base, serverLog := serveInBackground(t, host, map[string]string{"HOME": host.home}, externals{})
	for _, path := range []string{"/auth/login", "/account"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 without LOADOUT_PUBLIC_URL", path, resp.StatusCode)
		}
	}
	if !strings.Contains(serverLog.String(), "login is off") {
		t.Errorf("server did not log that login is off:\n%s", serverLog.String())
	}
}
