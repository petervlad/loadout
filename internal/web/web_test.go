package web_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/mail/mailtest"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/web"
)

// allowAll is a limiter that never limits.
type allowAll struct{}

func (allowAll) Allow(string) bool { return true }

// allowN allows n attempts in total.
type allowN struct{ n int }

func (a *allowN) Allow(string) bool {
	a.n--
	return a.n >= 0
}

type site struct {
	t        *testing.T
	srv      *httptest.Server
	accounts *account.Service
	reg      *registry.Service
	mails    *mailtest.Recorder
}

type siteOptions struct {
	google *web.Google
	perIP  web.Limiter
	simple bool
}

func newSite(t *testing.T, o siteOptions) *site {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, time.Now, log, registry.WithMemberships(roles))
	if _, err := reg.CreateNamespace(t.Context(), "lime", "", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	mails := &mailtest.Recorder{}
	accounts := account.NewService(store, reg, roles, device.New(time.Now), account.Options{
		PublicURL: "http://localhost",
		Mailer:    mails,
		Now:       time.Now,
		Log:       log,
	})
	perIP := o.perIP
	if perIP == nil {
		perIP = allowAll{}
	}
	h, err := web.New(web.Config{PublicURL: "http://localhost", Google: o.google, Simple: o.simple, PerIP: perIP, PerEmail: allowAll{}, Log: log}, accounts)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &site{t: t, srv: srv, accounts: accounts, reg: reg, mails: mails}
}

// browser keeps cookies and does not follow redirects, so tests see them.
type browser struct {
	t    *testing.T
	base string
	c    *http.Client
}

func (s *site) browser() *browser {
	jar, err := cookiejar.New(nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return &browser{t: s.t, base: s.srv.URL, c: &http.Client{
		Jar:           jar,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type page struct {
	status   int
	location string
	body     string
	header   http.Header
}

func (b *browser) do(req *http.Request) page {
	b.t.Helper()
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return page{status: resp.StatusCode, location: resp.Header.Get("Location"), body: string(body), header: resp.Header}
}

func (b *browser) get(path string) page {
	b.t.Helper()
	req, err := http.NewRequestWithContext(b.t.Context(), http.MethodGet, b.base+path, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	return b.do(req)
}

// cookie returns the value of a cookie the browser holds.
func (b *browser) cookie(name string) string {
	u, err := url.Parse(b.base)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// post submits a form with the browser's CSRF token.
func (b *browser) post(path string, form url.Values) page {
	b.t.Helper()
	if form.Get("csrf") == "" {
		form.Set("csrf", b.cookie("loadout_csrf"))
	}
	req, err := http.NewRequestWithContext(b.t.Context(), http.MethodPost, b.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// What a browser sends for a form on the site itself (PublicURL is
	// http://localhost). The Origin depends on the page's referrer policy,
	// see TestReferrerPolicyKeepsTheOrigin.
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return b.do(req)
}

// With Referrer-Policy no-referrer a browser sends "Origin: null" on every
// form post, and checkCSRF refuses them all: sign-in, the CLI approval, every
// button. The pages must keep a policy that sends the origin to the site.
func TestReferrerPolicyKeepsTheOrigin(t *testing.T) {
	s := newSite(t, siteOptions{})
	b := s.browser()
	for _, path := range []string{"/auth/login", "/auth/device"} {
		p := b.get(path)
		if got := p.header.Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("GET %s Referrer-Policy = %q, want same-origin", path, got)
		}
		if strings.Contains(p.body, `content="no-referrer"`) {
			t.Errorf("GET %s has a no-referrer meta tag", path)
		}
	}
}

var codeRE = regexp.MustCompile(`\b(\d{6})\b`)

const pw = "tangerine kettle river"

// signup creates and verifies Alice's account in b and returns the next
// page.
func (s *site) signup(b *browser) page {
	s.t.Helper()
	const email = "alice@example.com"
	b.get("/auth/signup") // sets the CSRF cookie
	p := b.post("/auth/signup", url.Values{"email": {email}, "password": {pw}})
	if p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/verify") {
		s.t.Fatalf("signup = %d %s\n%s", p.status, p.location, p.body)
	}
	m := s.mails.WaitFor(s.t, email, s.mails.Count(email)-1)
	code := codeRE.FindStringSubmatch(m.Text)
	if code == nil {
		s.t.Fatalf("no code in mail:\n%s", m.Text)
	}
	return b.post("/auth/verify", url.Values{"code": {code[1]}, "next": {"/account"}})
}

func TestSignupVerifyAccountLogout(t *testing.T) {
	s := newSite(t, siteOptions{})
	b := s.browser()

	if p := b.get("/auth/signup"); p.status != 200 || !strings.Contains(p.body, `name="csrf"`) {
		t.Fatalf("GET /auth/signup = %d\n%s", p.status, p.body)
	}
	p := s.signup(b)
	if p.status != http.StatusSeeOther || p.location != "/account" {
		t.Fatalf("verify = %d %s, want a redirect to /account\n%s", p.status, p.location, p.body)
	}
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "lime", "alice@example.com", account.RoleMember); err != nil {
		t.Fatal(err)
	}
	acct := b.get("/account")
	if acct.status != 200 || !strings.Contains(acct.body, "alice@example.com") || !strings.Contains(acct.body, "lime") {
		t.Errorf("GET /account = %d, want the email and the lime membership\n%s", acct.status, acct.body)
	}

	if p := b.post("/auth/logout", url.Values{}); p.status != http.StatusSeeOther || p.location != "/auth/login" {
		t.Errorf("logout = %d %s", p.status, p.location)
	}
	if p := b.get("/account"); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login") {
		t.Errorf("GET /account after logout = %d %s, want a redirect to sign in", p.status, p.location)
	}
}

func TestLoginNeedsTheCode(t *testing.T) {
	s := newSite(t, siteOptions{})
	s.signup(s.browser())

	b := s.browser()
	b.get("/auth/login")
	for _, form := range []url.Values{
		{"email": {"alice@example.com"}, "password": {"wrong password!!"}},
		{"email": {"nobody@example.com"}, "password": {pw}},
		{"email": {"alice@example.com"}, "password": {pw}},
	} {
		p := b.post("/auth/login", form)
		if p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/verify") {
			t.Errorf("login %v = %d %s; want the same redirect for right and wrong details", form, p.status, p.location)
		}
	}
	if p := b.get("/account"); p.status != http.StatusSeeOther {
		t.Error("signed in before entering the code")
	}
	verify := b.get("/auth/verify")
	if !strings.Contains(verify.body, "alice@example.com") {
		t.Errorf("code page does not name the address:\n%s", verify.body)
	}
	if p := b.post("/auth/verify", url.Values{"code": {"000000"}}); p.status != http.StatusBadRequest || !strings.Contains(p.body, "wrong or has expired") {
		t.Errorf("wrong code = %d\n%s", p.status, p.body)
	}
	m := s.mails.WaitFor(t, "alice@example.com", 1)
	code := codeRE.FindStringSubmatch(m.Text)[1]
	if p := b.post("/auth/verify", url.Values{"code": {code}, "next": {"/auth/device?code=X"}}); p.status != http.StatusSeeOther || p.location != "/auth/device?code=X" {
		t.Errorf("right code = %d %s, want the next page", p.status, p.location)
	}
}

func TestMagicLinkNeedsAClick(t *testing.T) {
	s := newSite(t, siteOptions{})
	b := s.browser()
	b.get("/auth/signup")
	b.post("/auth/signup", url.Values{"email": {"alice@example.com"}, "password": {pw}})
	m := s.mails.WaitFor(t, "alice@example.com", 0)
	link := regexp.MustCompile(`http://localhost(/auth/verify\?token=\S+)`).FindStringSubmatch(m.Text)
	if link == nil {
		t.Fatalf("no link in mail:\n%s", m.Text)
	}

	// Another browser (the phone's mail app) opens the link: a GET only
	// shows a button, so a mail scanner cannot use up the link.
	other := s.browser()
	p := other.get(link[1])
	if p.status != 200 || !strings.Contains(p.body, `name="token"`) {
		t.Fatalf("GET link = %d\n%s", p.status, p.body)
	}
	if p := other.get("/account"); p.status != http.StatusSeeOther {
		t.Error("opening the link signed in without a click")
	}
	token := regexp.MustCompile(`name="token" value="([^"]+)"`).FindStringSubmatch(p.body)[1]
	if p := other.post("/auth/verify", url.Values{"token": {token}}); p.status != http.StatusSeeOther {
		t.Fatalf("confirm link = %d\n%s", p.status, p.body)
	}
	if p := other.get("/account"); p.status != 200 {
		t.Errorf("GET /account after the link = %d, want signed in", p.status)
	}
	if p := other.post("/auth/verify", url.Values{"token": {token}}); p.status != http.StatusBadRequest {
		t.Errorf("link used twice = %d, want 400", p.status)
	}
}

func TestCSRF(t *testing.T) {
	s := newSite(t, siteOptions{})
	b := s.browser()
	b.get("/auth/login")
	token := b.cookie("loadout_csrf")
	wrong := []byte(token)
	wrong[0] ^= 1 // differs from the cookie in one byte
	form := url.Values{"email": {"a@example.com"}, "password": {pw}}

	// post sends the form with the given csrf field, or none.
	post := func(csrf *string, origin string) int {
		f := url.Values{"email": form["email"], "password": form["password"]}
		if csrf != nil {
			f.Set("csrf", *csrf)
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, b.base+"/auth/login", strings.NewReader(f.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		return b.do(req).status
	}
	wrongToken := string(wrong)
	if got := post(nil, ""); got != http.StatusForbidden {
		t.Errorf("post without a token = %d, want 403", got)
	}
	if got := post(&wrongToken, ""); got != http.StatusForbidden {
		t.Errorf("post with a wrong token = %d, want 403", got)
	}
	if got := post(&token, "https://evil.example"); got != http.StatusForbidden {
		t.Errorf("cross-origin post with the right token = %d, want 403", got)
	}
	if got := post(&token, "http://localhost"); got != http.StatusSeeOther {
		t.Errorf("same-origin post with the token = %d, want 303", got)
	}
}

func TestPageHeaders(t *testing.T) {
	s := newSite(t, siteOptions{})
	b := s.browser()
	p := b.get("/auth/login")
	want := "default-src 'none'; style-src 'self'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self'; base-uri 'none'"
	if got := p.header.Get("Content-Security-Policy"); got != want {
		t.Errorf("CSP = %q, want %q", got, want)
	}
	if p.header.Get("Cache-Control") != "no-store" || p.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", p.header)
	}
	css := b.get("/auth/static/style.css")
	if css.status != 200 || !strings.Contains(css.header.Get("Content-Type"), "text/css") {
		t.Errorf("GET style.css = %d %s", css.status, css.header.Get("Content-Type"))
	}
	if css.header.Get("Cache-Control") != "no-cache" || css.header.Get("ETag") == "" {
		t.Errorf("unversioned style.css: Cache-Control %q, ETag %q, want no-cache with an ETag", css.header.Get("Cache-Control"), css.header.Get("ETag"))
	}
	link := regexp.MustCompile(`href="(/auth/static/style\.css\?v=[0-9a-f]+)"`).FindStringSubmatch(p.body)
	if link == nil {
		t.Fatalf("the page links no versioned style.css")
	}
	if c := b.get(link[1]).header.Get("Cache-Control"); !strings.Contains(c, "immutable") {
		t.Errorf("versioned style.css: Cache-Control %q, want immutable", c)
	}
}

func TestCookies(t *testing.T) {
	tests := []struct {
		publicURL string
		name      string
		secure    bool
	}{
		{"https://registry.example.com", "__Host-loadout_csrf", true},
		{"http://localhost:7420", "loadout_csrf", false},
		{"http://127.0.0.1:7420", "loadout_csrf", false},
	}
	for _, tt := range tests {
		h, err := web.New(web.Config{PublicURL: tt.publicURL, PerIP: allowAll{}, PerEmail: allowAll{}, Log: slog.New(slog.DiscardHandler)}, newSite(t, siteOptions{}).accounts)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/login", nil))
		cs := rec.Result().Cookies()
		if len(cs) != 1 {
			t.Fatalf("%s: cookies = %v, want the CSRF cookie", tt.publicURL, cs)
		}
		c := cs[0]
		if c.Name != tt.name || c.Secure != tt.secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
			t.Errorf("%s: cookie = %+v, want %s, Secure %v, HttpOnly, Lax, path /", tt.publicURL, c, tt.name, tt.secure)
		}
	}
	for _, bad := range []string{"http://registry.example.com", "ftp://x", "https://x/path", "", "https://user@x"} {
		if _, _, err := web.ParsePublicURL(bad); err == nil {
			t.Errorf("ParsePublicURL(%q) accepted", bad)
		}
	}
}

func TestRateLimit(t *testing.T) {
	s := newSite(t, siteOptions{perIP: &allowN{n: 2}})
	b := s.browser()
	b.get("/auth/login")
	for i, want := range []int{http.StatusSeeOther, http.StatusSeeOther, http.StatusTooManyRequests} {
		if p := b.post("/auth/login", url.Values{"email": {"a@example.com"}, "password": {pw}}); p.status != want {
			t.Errorf("login #%d = %d, want %d", i+1, p.status, want)
		}
	}
}

func TestNextStaysOnSite(t *testing.T) {
	s := newSite(t, siteOptions{})
	b := s.browser()
	s.signup(b)
	for _, next := range []string{"https://evil.example/", "//evil.example/x", "/\\evil.example", "javascript:alert(1)", ""} {
		p := b.get("/auth/login?next=" + url.QueryEscape(next))
		if p.status != http.StatusSeeOther || p.location != "/account" {
			t.Errorf("signed-in GET /auth/login?next=%q = %d %s, want /account", next, p.status, p.location)
		}
	}
	if p := b.get("/auth/login?next=" + url.QueryEscape("/auth/device?code=ABCD")); p.location != "/auth/device?code=ABCD" {
		t.Errorf("local next = %s, want kept", p.location)
	}
}

func TestDeviceApproval(t *testing.T) {
	s := newSite(t, siteOptions{})
	b := s.browser()
	start, err := s.accounts.StartDevice(t.Context(), "lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	devicePath := "/auth/device?code=" + start.UserCode

	// Signed out: sign in first, then come back.
	p := b.get(devicePath)
	if p.status != http.StatusSeeOther || p.location != "/auth/login?next="+url.QueryEscape(devicePath) {
		t.Fatalf("GET %s signed out = %d %s", devicePath, p.status, p.location)
	}
	s.signup(b)

	p = b.get(devicePath)
	if !strings.Contains(p.body, "not a member of lime") {
		t.Errorf("device page for a non-member:\n%s", p.body)
	}
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "lime", "alice@example.com", account.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	p = b.get(devicePath)
	if !strings.Contains(p.body, start.UserCode) || !strings.Contains(p.body, "lime") || !strings.Contains(p.body, "maintainer") {
		t.Errorf("device page does not show the code, team and role:\n%s", p.body)
	}
	if p := b.post("/auth/device", url.Values{"code": {start.UserCode}, "namespace": {"lime"}, "action": {"approve"}}); p.status != 200 || !strings.Contains(p.body, "Terminal signed in") {
		t.Fatalf("approve = %d\n%s", p.status, p.body)
	}
	poll, err := s.accounts.PollDevice(t.Context(), start.DeviceCode)
	if err != nil || poll.Status != device.StatusApproved {
		t.Fatalf("PollDevice() = %+v, %v; want approved", poll, err)
	}
	if _, err := s.reg.Authenticate(t.Context(), poll.Token); err != nil {
		t.Errorf("issued token does not work: %v", err)
	}

	// Deny.
	start, err = s.accounts.StartDevice(t.Context(), "lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if p := b.post("/auth/device", url.Values{"code": {start.UserCode}, "action": {"deny"}}); p.status != 200 {
		t.Errorf("deny = %d", p.status)
	}
	if poll, _ := s.accounts.PollDevice(t.Context(), start.DeviceCode); poll.Status != device.StatusDenied {
		t.Errorf("poll after deny = %s, want denied", poll.Status)
	}
	if p := b.get("/auth/device?code=ZZZZ-ZZZZ"); p.status != http.StatusBadRequest {
		t.Errorf("unknown code = %d, want 400", p.status)
	}
}

// keyedLimit allows n attempts per key.
type keyedLimit struct {
	n    int
	seen map[string]int
}

func (l *keyedLimit) Allow(key string) bool {
	if l.seen == nil {
		l.seen = map[string]int{}
	}
	l.seen[key]++
	return l.seen[key] <= l.n
}

// TestDeviceCodeLookupsAreLimited: a signed-in user cannot try user codes
// on the approval page without limit.
func TestDeviceCodeLookupsAreLimited(t *testing.T) {
	s := newSite(t, siteOptions{perIP: &keyedLimit{n: 5}})
	b := s.browser()
	s.signup(b)
	for i := range 5 {
		if p := b.get("/auth/device?code=ZZZZ-ZZZZ"); p.status != http.StatusBadRequest {
			t.Fatalf("lookup #%d = %d, want 400 (unknown code)", i+1, p.status)
		}
	}
	if p := b.get("/auth/device?code=ZZZZ-ZZZZ"); p.status != http.StatusTooManyRequests {
		t.Errorf("sixth lookup = %d, want 429", p.status)
	}
	if p := b.post("/auth/device", url.Values{"code": {"ZZZZ-ZZZZ"}, "action": {"deny"}}); p.status != http.StatusTooManyRequests {
		t.Errorf("deny after the limit = %d, want 429", p.status)
	}
}

// TestSignOutEverywhere: the account page ends every session and every
// login token of the user, not only this browser's session.
func TestSignOutEverywhere(t *testing.T) {
	s := newSite(t, siteOptions{})
	a, b := s.browser(), s.browser()
	s.signup(a)

	// A second browser signs in with password and code.
	b.get("/auth/login")
	if p := b.post("/auth/login", url.Values{"email": {"alice@example.com"}, "password": {pw}}); p.status != http.StatusSeeOther {
		t.Fatalf("login = %d %s", p.status, p.location)
	}
	m := s.mails.WaitFor(t, "alice@example.com", s.mails.Count("alice@example.com")-1)
	code := codeRE.FindStringSubmatch(m.Text)
	if code == nil {
		t.Fatalf("no code in mail:\n%s", m.Text)
	}
	if p := b.post("/auth/verify", url.Values{"code": {code[1]}}); p.status != http.StatusSeeOther {
		t.Fatalf("verify = %d", p.status)
	}

	// And a terminal signs in with loadout login.
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "lime", "alice@example.com", account.RoleMember); err != nil {
		t.Fatal(err)
	}
	u, err := s.accounts.SessionUser(t.Context(), a.cookie("loadout_session"))
	if err != nil {
		t.Fatal(err)
	}
	start, err := s.accounts.StartDevice(t.Context(), "lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.ApproveDevice(t.Context(), u, start.UserCode, "lime"); err != nil {
		t.Fatal(err)
	}
	poll, err := s.accounts.PollDevice(t.Context(), start.DeviceCode)
	if err != nil || poll.Token == "" {
		t.Fatalf("PollDevice() = %+v, %v", poll, err)
	}

	a.get("/account")
	if p := a.post("/account", url.Values{"action": {"signout-everywhere"}}); p.status != http.StatusOK || !strings.Contains(p.body, "Signed out everywhere") {
		t.Fatalf("POST /account signout-everywhere = %d\n%s", p.status, p.body)
	}
	for name, br := range map[string]*browser{"this browser": a, "the other browser": b} {
		if p := br.get("/account"); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login") {
			t.Errorf("GET /account in %s after signing out everywhere = %d %s, want sent to sign in", name, p.status, p.location)
		}
	}
	if _, err := s.reg.Authenticate(t.Context(), poll.Token); err == nil {
		t.Error("the loadout login token still works after signing out everywhere")
	}
}
