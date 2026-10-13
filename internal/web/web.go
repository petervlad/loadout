// Package web serves the registry's sign-in pages: sign up, sign in with an
// emailed code or Google, approve a `loadout login` from the terminal, and
// a small account page.
//
// Routes (HTML, server-rendered, no JavaScript):
//
//	GET  /auth/signup, POST /auth/signup    email + password, then a code
//	GET  /auth/login,  POST /auth/login     email + password, then a code
//	GET  /auth/verify, POST /auth/verify    enter the code; ?token= confirms a mailed link
//	GET  /auth/google/start                 redirect to Google
//	GET  /auth/google/callback              back from Google
//	POST /auth/logout                       end the session
//	GET  /auth/device, POST /auth/device    approve a CLI sign-in
//	GET  /account                           email and memberships
//	POST /account                           action=signout-everywhere ends all sessions and login tokens
//	GET  /auth/static/style.css, /auth/static/ui.js
//	GET  /auth/static/preview.js, /auth/static/vendor/...   diagram frames only
//
// With Config.UI it also serves the team pages listed in ui.go.
//
// Every POST form carries a CSRF token that must match a cookie
// (double submit). Cookies are HttpOnly, SameSite=Lax, Secure and
// __Host- prefixed, except on an http://localhost URL used for
// development.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/password"
	"github.com/petervlad/loadout/internal/ratelimit"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/wiki"
)

//go:embed templates/*.html templates/ui/*.html templates/frame/*.html static/*.css static/*.js static/vendor
var assets embed.FS

// htmlCSP is the Content-Security-Policy of every page: only the embedded
// stylesheet and script load, forms post only here, nothing frames the
// page.
const htmlCSP = "default-src 'none'; style-src 'self'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self'; base-uri 'none'"

// maxForm caps a form body.
const maxForm = 16 << 10

// maxWikiForm caps the body of the two wiki forms that carry a page:
// percent-encoding grows UTF-8 text up to three times, and the service
// checks wiki.MaxSize on the decoded text.
const maxWikiForm = 3*wiki.MaxSize + 16<<10

// formLimit is the cap of a request's body: maxWikiForm for a post of a
// wiki page (POST /t/{ns}/wiki and /t/{ns}/wiki/edit), maxForm for
// anything else. It runs before the mux, so it reads the path itself.
func formLimit(r *http.Request) int64 {
	if r.Method != http.MethodPost {
		return maxForm
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/t/")
	if !ok {
		return maxForm
	}
	_, tail, _ := strings.Cut(rest, "/")
	if tail == "wiki" || tail == "wiki/edit" {
		return maxWikiForm
	}
	return maxForm
}

// Accounts is what the pages need from the account service.
type Accounts interface {
	EmailEnabled() bool
	Signup(ctx context.Context, email, pw string) (account.Pending, error)
	Login(ctx context.Context, email, pw string) (account.Pending, error)
	VerifyCode(ctx context.Context, challengeID, code string) (account.SessionGrant, error)
	VerifyLink(ctx context.Context, token string) (account.SessionGrant, error)
	SimpleLogin(ctx context.Context, email, pw string) (account.SessionGrant, error)
	GoogleSignIn(ctx context.Context, id account.GoogleIdentity) (account.SessionGrant, error)
	SessionUser(ctx context.Context, secret string) (account.User, error)
	Logout(ctx context.Context, secret string) error
	SignOutEverywhere(ctx context.Context, userID string) (sessions, tokens int, err error)
	Memberships(ctx context.Context, userID string) ([]account.Membership, error)
	DeviceRequest(ctx context.Context, u account.User, userCode string) (account.DeviceRequest, error)
	ApproveDevice(ctx context.Context, u account.User, userCode, namespace string) error
	DenyDevice(ctx context.Context, userCode string) error
}

var _ Accounts = (*account.Service)(nil)

// Limiter says whether one more attempt for key is allowed now.
type Limiter interface {
	Allow(key string) bool
}

// Config configures the pages.
type Config struct {
	// PublicURL is the registry's address in a browser. It must be https,
	// or http on localhost for development (then cookies are not Secure).
	PublicURL string
	// Google enables "Sign in with Google"; nil hides it.
	Google *Google
	// Simple is LOADOUT_LOGIN=simple: the sign-in takes email and password
	// and starts the session at once; sign-up, the code page and Google are
	// not routed (404). Google is ignored.
	Simple bool
	// Demo, when set, puts a banner on every page and lists the demo
	// accounts and their shared password on the sign-in page. It needs
	// Simple: the password is public.
	Demo *Demo
	// PerIP limits attempts per client address, PerEmail per address
	// typed into a form.
	PerIP    Limiter
	PerEmail Limiter
	Log      *slog.Logger
	// UI serves the team pages; nil serves only the sign-in pages.
	UI *UI
	// DocsURL is where the Docs and What's new links go, ending in a slash;
	// empty is DefaultDocsURL.
	DocsURL string
}

// Demo describes a demo registry for its pages. The password is public on
// purpose: the sign-in page prints it.
type Demo struct {
	Password string
	Accounts []DemoAccount
}

// DemoAccount is one account the sign-in page lists, with what it can do.
type DemoAccount struct {
	Email string
	Role  string
}

type handler struct {
	accounts Accounts
	google   *Google
	simple   bool
	demo     *Demo
	perIP    Limiter
	perEmail Limiter
	log      *slog.Logger
	origin   string
	docsURL  string
	cookies  cookies
	pages    map[string]*template.Template
	ui       *uiPages
	// zips holds a slot per zip being built (maxZips).
	zips chan struct{}
}

// New returns the handler for the sign-in pages.
func New(cfg Config, accounts Accounts) (http.Handler, error) {
	u, secure, err := ParsePublicURL(cfg.PublicURL)
	if err != nil {
		return nil, err
	}
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	docsURL := cfg.DocsURL
	if docsURL == "" {
		docsURL = DefaultDocsURL
	}
	h := &handler{
		docsURL:  docsURL,
		accounts: accounts,
		google:   cfg.Google,
		simple:   cfg.Simple,
		demo:     cfg.Demo,
		perIP:    cfg.PerIP,
		perEmail: cfg.PerEmail,
		log:      cfg.Log,
		origin:   u.Scheme + "://" + u.Host,
		cookies:  newCookies(secure),
		pages:    pages,
		zips:     make(chan struct{}, maxZips),
	}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, fmt.Errorf("web assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /auth/static/", staticFiles(static))
	mux.Handle("GET /auth/static/vendor/", vendorFiles(static))
	h.routeSignIn(mux)
	mux.HandleFunc("POST /auth/logout", h.logout)
	mux.HandleFunc("GET /auth/device", h.devicePage)
	mux.HandleFunc("POST /auth/device", h.device)
	mux.HandleFunc("POST /account", h.account)
	if cfg.UI == nil {
		mux.HandleFunc("GET /account", h.accountPage)
		return h.headers(mux), nil
	}
	if h.ui, err = newUIPages(cfg.UI); err != nil {
		return nil, err
	}
	h.routeUI(mux)
	return h.headers(mux), nil
}

// ParsePublicURL checks a public URL and reports whether cookies can be
// Secure: https anywhere, or plain http only on this machine.
func ParsePublicURL(raw string) (*url.URL, bool, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, false, fmt.Errorf("public URL %q: want https://host[:port]", raw)
	}
	switch u.Scheme {
	case "https":
		return u, true, nil
	case "http":
		if isLoopback(u.Hostname()) {
			return u, false, nil
		}
	}
	return nil, false, fmt.Errorf("public URL %q: must be https (plain http only for localhost)", raw)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsLoopback()
}

// headers sets the page security headers and caps request bodies.
func (h *handler) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", htmlCSP)
		hd.Set("X-Content-Type-Options", "nosniff")
		// Only the diagram frame may be framed, by our own pages; it sets
		// SAMEORIGIN itself. Caddy adds DENY only when this is missing.
		hd.Set("X-Frame-Options", "DENY")
		// Not no-referrer: with it browsers send "Origin: null" on form
		// posts, which checkCSRF rightly refuses. same-origin still sends
		// nothing to other sites.
		hd.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/auth/static/") {
			hd.Set("Cache-Control", "no-store")
		}
		r.Body = http.MaxBytesReader(w, r.Body, formLimit(r))
		next.ServeHTTP(w, r)
	})
}

// view is the data every template gets; each page uses what it needs.
type view struct {
	Title string
	CSRF  string
	Error string
	Info  string
	Next  string
	Email string
	// Prefilled is true when the address came in the link (an
	// invitation's sign-in link), not from a form sent back with an error.
	Prefilled bool
	Google    bool
	// Simple is true in LOADOUT_LOGIN=simple: the sign-in form alone.
	Simple bool
	// Demo is set on a demo registry: the banner, and on the sign-in page
	// the accounts.
	Demo *Demo
	// EmailLogin is false when no mailer is configured.
	EmailLogin bool
	// Token is a mailed link's token, confirmed with a button.
	Token string
	// Device approval.
	UserCode  string
	Namespace string
	Choices   []account.Membership
	// Account page.
	User        account.User
	Memberships []account.Membership
	// Theme is the appearance chosen on the account page: "light", "dark",
	// or "" to follow the system.
	Theme string
}

func (h *handler) render(w http.ResponseWriter, r *http.Request, status int, page string, v view) {
	v.CSRF = h.csrfToken(w, r)
	v.Theme = h.theme(r)
	v.Simple = h.simple
	v.Demo = h.demo
	v.Google = h.google != nil && !h.simple
	v.EmailLogin = h.accounts.EmailEnabled() && !h.simple
	t, ok := h.pages[page]
	if !ok {
		h.fail(w, r, fmt.Errorf("no page %s", page))
		return
	}
	var buf strings.Builder
	if err := t.ExecuteTemplate(&buf, "layout", v); err != nil {
		h.fail(w, r, fmt.Errorf("render %s: %w", page, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := fmt.Fprint(w, buf.String()); err != nil {
		h.log.Debug("write page", "err", err)
	}
}

// message shows a page with one line of text.
func (h *handler) message(w http.ResponseWriter, r *http.Request, status int, title, text string) {
	v := view{Title: title}
	if status >= 400 {
		v.Error = text
	} else {
		v.Info = text
	}
	h.render(w, r, status, "message.html", v)
}

// fail logs an unexpected error and shows a generic page, in plain text
// if even that page cannot be drawn.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "web request failed", "route", r.Pattern, "err", err)
	const text = "Something went wrong on our side. Please try again in a moment."
	var buf strings.Builder
	if t, ok := h.pages["message.html"]; ok && t.ExecuteTemplate(&buf, "layout", view{Title: "Something went wrong", Theme: h.theme(r), Demo: h.demo, Error: text}) == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, buf.String())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintln(w, text)
}

// allow checks the rate limits for a form post; email may be empty.
func (h *handler) allow(r *http.Request, email string) bool {
	if !h.perIP.Allow("ip:" + ratelimit.ClientKey(r)) {
		return false
	}
	if email == "" {
		return true
	}
	return h.perEmail.Allow("email:" + strings.ToLower(strings.TrimSpace(email)))
}

// allowCode checks the limits on looking up a CLI sign-in code: per client
// address and per signed-in user, so user codes cannot be guessed quickly.
func (h *handler) allowCode(r *http.Request, u account.User) bool {
	return h.perIP.Allow("code-ip:"+ratelimit.ClientKey(r)) && h.perIP.Allow("code-user:"+u.ID)
}

// busy answers when too many passwords are being checked at once.
func (h *handler) busy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "5")
	h.message(w, r, http.StatusServiceUnavailable, "Busy", "The registry is busy right now. Wait a moment and try again.")
}

func (h *handler) tooMany(w http.ResponseWriter, r *http.Request) {
	h.message(w, r, http.StatusTooManyRequests, "Slow down", "Too many attempts. Wait a few minutes and try again.")
}

// form parses a POST form and checks its CSRF token. It answers the request
// itself and returns false when the form is refused.
func (h *handler) form(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		h.message(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return false
	}
	if !h.checkCSRF(r) {
		h.message(w, r, http.StatusForbidden, "Form expired", "This form expired or came from another site. Go back, reload the page and try again.")
		return false
	}
	return true
}

// user returns the signed-in user, if any.
func (h *handler) user(r *http.Request) (account.User, bool) {
	secret := h.cookies.get(r, cookieSession)
	if secret == "" {
		return account.User{}, false
	}
	u, err := h.accounts.SessionUser(r.Context(), secret)
	if err != nil {
		if !errors.Is(err, account.ErrNoSession) {
			h.log.ErrorContext(r.Context(), "read session", "err", err)
		}
		return account.User{}, false
	}
	return u, true
}

// startSession sets the session cookie and sends the browser on.
func (h *handler) startSession(w http.ResponseWriter, r *http.Request, g account.SessionGrant, next string) {
	h.cookies.set(w, cookieSession, g.Secret, time.Until(g.ExpiresAt))
	h.cookies.clear(w, cookieChallenge)
	http.Redirect(w, r, safeNext(next), http.StatusSeeOther) //nolint:gosec // G710: safeNext keeps it on this site
}

func (h *handler) signupPage(w http.ResponseWriter, r *http.Request) {
	email := prefill(r)
	h.render(w, r, http.StatusOK, "signup.html", view{Title: "Create your account", Next: safeNext(r.FormValue("next")), Email: email, Prefilled: email != ""})
}

// prefill is the address a sign-in link carries in ?email=, for the form.
// It is only ever shown as the field's value (escaped); anything that is
// not a plain address is dropped.
func prefill(r *http.Request) string {
	e, err := account.NormalizeEmail(r.URL.Query().Get("email"))
	if err != nil {
		return ""
	}
	return e
}

func (h *handler) signup(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	email, next := r.PostForm.Get("email"), safeNext(r.PostForm.Get("next"))
	if !h.allow(r, email) {
		h.tooMany(w, r)
		return
	}
	p, err := h.accounts.Signup(r.Context(), email, r.PostForm.Get("password"))
	if errors.Is(err, password.ErrBusy) {
		h.busy(w, r)
		return
	}
	if err != nil {
		v := view{Title: "Create your account", Email: email, Next: next}
		if weak, ok := errors.AsType[*password.WeakError](err); ok {
			v.Error = "Your " + weak.Error() + "."
		} else if msg, ok := formError(err); ok {
			v.Error = msg
		} else {
			h.fail(w, r, err)
			return
		}
		h.render(w, r, http.StatusBadRequest, "signup.html", v)
		return
	}
	h.startChallenge(w, r, p, email, next)
}

// routeSignIn routes the pages that start a session: sign-up, sign-in,
// the code page and Google, or in simple mode only the sign-in.
func (h *handler) routeSignIn(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", h.loginPage)
	if h.simple {
		mux.HandleFunc("POST /auth/login", h.simpleLogin)
		return
	}
	mux.HandleFunc("POST /auth/login", h.login)
	mux.HandleFunc("GET /auth/signup", h.signupPage)
	mux.HandleFunc("POST /auth/signup", h.signup)
	mux.HandleFunc("GET /auth/verify", h.verifyPage)
	mux.HandleFunc("POST /auth/verify", h.verify)
	mux.HandleFunc("GET /auth/google/start", h.googleStart)
	mux.HandleFunc("GET /auth/google/callback", h.googleCallback)
}

// simpleLogin checks email and password and starts the session at once.
// A wrong password and an unknown email give the same page.
func (h *handler) simpleLogin(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	email, next := r.PostForm.Get("email"), safeNext(r.PostForm.Get("next"))
	if !h.allow(r, email) {
		h.tooMany(w, r)
		return
	}
	g, err := h.accounts.SimpleLogin(r.Context(), email, r.PostForm.Get("password"))
	if errors.Is(err, password.ErrBusy) {
		h.busy(w, r)
		return
	}
	if err != nil {
		msg, ok := formError(err)
		if !ok {
			h.fail(w, r, err)
			return
		}
		h.render(w, r, http.StatusBadRequest, "login.html", view{Title: "Sign in", Email: email, Next: next, Error: msg})
		return
	}
	h.startSession(w, r, g, next)
}

func (h *handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(r); ok {
		http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther) //nolint:gosec // G710: safeNext keeps it on this site
		return
	}
	email := prefill(r)
	h.render(w, r, http.StatusOK, "login.html", view{Title: "Sign in", Next: safeNext(r.FormValue("next")), Email: email, Prefilled: email != ""})
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	email, next := r.PostForm.Get("email"), safeNext(r.PostForm.Get("next"))
	if !h.allow(r, email) {
		h.tooMany(w, r)
		return
	}
	p, err := h.accounts.Login(r.Context(), email, r.PostForm.Get("password"))
	if errors.Is(err, password.ErrBusy) {
		h.busy(w, r)
		return
	}
	if err != nil {
		msg, ok := formError(err)
		if !ok {
			h.fail(w, r, err)
			return
		}
		h.render(w, r, http.StatusBadRequest, "login.html", view{Title: "Sign in", Email: email, Next: next, Error: msg})
		return
	}
	h.startChallenge(w, r, p, email, next)
}

// formError turns an expected sign-up or sign-in error into a message for
// the form.
func formError(err error) (string, bool) {
	switch {
	case errors.Is(err, account.ErrInvalidEmail):
		return "Enter a valid email address.", true
	case errors.Is(err, account.ErrBadLogin):
		return "Wrong email or password.", true
	case errors.Is(err, account.ErrEmailDisabled):
		return "Email sign-in is not available on this registry.", true
	}
	return "", false
}

// startChallenge remembers which code the browser is waiting for and sends
// it to the code page. The answer is the same whether or not a mail went
// out.
func (h *handler) startChallenge(w http.ResponseWriter, r *http.Request, p account.Pending, email, next string) {
	h.cookies.set(w, cookieChallenge, p.ChallengeID+"."+base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(email))), account.ChallengeTTL)
	http.Redirect(w, r, "/auth/verify?"+url.Values{"next": {next}}.Encode(), http.StatusSeeOther)
}

// challenge reads the challenge cookie: the challenge id and the email the
// person typed.
func (h *handler) challenge(r *http.Request) (id, email string) {
	id, enc, _ := strings.Cut(h.cookies.get(r, cookieChallenge), ".")
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return id, ""
	}
	return id, string(raw)
}

func (h *handler) verifyPage(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	if token := r.FormValue("token"); token != "" {
		// A mail scanner may open the link; only the button completes it.
		h.render(w, r, http.StatusOK, "verify_link.html", view{Title: "Finish signing in", Token: token, Next: next})
		return
	}
	id, email := h.challenge(r)
	if id == "" {
		h.message(w, r, http.StatusBadRequest, "Start again", "There is no code waiting for this browser. Sign in or sign up again.")
		return
	}
	h.render(w, r, http.StatusOK, "verify.html", view{Title: "Check your email", Email: email, Next: next})
}

func (h *handler) verify(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	if !h.allow(r, "") {
		h.tooMany(w, r)
		return
	}
	next := safeNext(r.PostForm.Get("next"))
	var (
		g   account.SessionGrant
		err error
	)
	token := r.PostForm.Get("token")
	if token != "" {
		g, err = h.accounts.VerifyLink(r.Context(), token)
	} else {
		id, _ := h.challenge(r)
		g, err = h.accounts.VerifyCode(r.Context(), id, r.PostForm.Get("code"))
	}
	if errors.Is(err, account.ErrCodeLocked) {
		_, email := h.challenge(r)
		h.render(w, r, http.StatusTooManyRequests, "verify.html", view{
			Title: "Check your email", Email: email, Next: next,
			Error: "Too many wrong codes were entered for this account. Wait up to 24 hours before trying a code again, or use the link in the email.",
		})
		return
	}
	if errors.Is(err, account.ErrInvalidCode) {
		if token != "" {
			h.message(w, r, http.StatusBadRequest, "Link expired", "This link is invalid, expired or used. Sign in again to get a new one.")
			return
		}
		_, email := h.challenge(r)
		h.render(w, r, http.StatusBadRequest, "verify.html", view{
			Title: "Check your email", Email: email, Next: next,
			Error: "That code is wrong or has expired. After 5 wrong codes, sign in again for a new one.",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.startSession(w, r, g, next)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	if err := h.accounts.Logout(r.Context(), h.cookies.get(r, cookieSession)); err != nil {
		h.fail(w, r, err)
		return
	}
	h.cookies.clear(w, cookieSession)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (h *handler) accountPage(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {"/account"}}.Encode(), http.StatusSeeOther)
		return
	}
	ms, err := h.accounts.Memberships(r.Context(), u.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, "account.html", view{Title: "Your account", User: u, Memberships: ms})
}

// account handles the forms of the account page: signing out everywhere.
func (h *handler) account(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	u, ok := h.user(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {"/account"}}.Encode(), http.StatusSeeOther)
		return
	}
	if r.PostForm.Get("action") != "signout-everywhere" {
		h.message(w, r, http.StatusBadRequest, "Bad request", "Unknown action.")
		return
	}
	if _, _, err := h.accounts.SignOutEverywhere(r.Context(), u.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	h.cookies.clear(w, cookieSession)
	h.message(w, r, http.StatusOK, "Signed out everywhere", "Every browser and every loadout login token of your account is signed out. Run loadout login again on the machines you still use.")
}

func (h *handler) devicePage(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	u, ok := h.user(r)
	if !ok {
		self := "/auth/device"
		if code != "" {
			self += "?" + url.Values{"code": {code}}.Encode()
		}
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {self}}.Encode(), http.StatusSeeOther)
		return
	}
	v := view{Title: "Sign in on your terminal", UserCode: code, User: u}
	if code == "" {
		h.render(w, r, http.StatusOK, "device.html", v)
		return
	}
	if !h.allowCode(r, u) {
		h.tooMany(w, r)
		return
	}
	req, err := h.accounts.DeviceRequest(r.Context(), u, code)
	if errors.Is(err, account.ErrInvalidCode) {
		v.UserCode, v.Error = "", "That code is unknown or has expired. Check the code in your terminal, or run loadout login again."
		h.render(w, r, http.StatusBadRequest, "device.html", v)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.UserCode, v.Namespace, v.Choices = req.UserCode, req.Namespace, req.Choices
	if len(req.Choices) == 0 {
		v.Error = "You are not in any team yet. Ask a maintainer of your team to invite " + u.Email + "."
		if req.Namespace != "" && req.Namespace != registry.AccountNamespace {
			v.Error = "You are not a member of " + req.Namespace + ". Ask a maintainer of " + req.Namespace + " to invite " + u.Email + "."
		}
	}
	h.render(w, r, http.StatusOK, "device.html", v)
}

func (h *handler) device(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	u, ok := h.user(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {"/auth/device"}}.Encode(), http.StatusSeeOther)
		return
	}
	if !h.allowCode(r, u) {
		h.tooMany(w, r)
		return
	}
	code := r.PostForm.Get("code")
	if r.PostForm.Get("action") == "deny" {
		if err := h.accounts.DenyDevice(r.Context(), code); err != nil && !errors.Is(err, account.ErrInvalidCode) {
			h.fail(w, r, err)
			return
		}
		h.message(w, r, http.StatusOK, "Sign-in denied", "The terminal was not signed in. You can close this page.")
		return
	}
	err := h.accounts.ApproveDevice(r.Context(), u, code, r.PostForm.Get("namespace"))
	switch {
	case err == nil:
		h.message(w, r, http.StatusOK, "Terminal signed in", "Done. Go back to your terminal; you can close this page.")
	case errors.Is(err, account.ErrInvalidCode):
		h.message(w, r, http.StatusBadRequest, "Code expired", "That code is unknown or has expired. Run loadout login again.")
	case errors.Is(err, account.ErrNotMember), errors.Is(err, registry.ErrInvalid):
		h.message(w, r, http.StatusForbidden, "Not a member", "You are not a member of that team. Ask one of its maintainers to invite you.")
	default:
		h.fail(w, r, err)
	}
}

// safeNext returns next if it is a path on this site, else /account. It
// keeps the pages from redirecting anywhere else.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/account"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/account"
	}
	return next
}

func parsePages() (map[string]*template.Template, error) {
	names, err := fs.Glob(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web templates: %w", err)
	}
	pages := map[string]*template.Template{}
	for _, n := range names {
		base := strings.TrimPrefix(n, "templates/")
		if base == "layout.html" {
			continue
		}
		t, err := template.New(base).Funcs(assetFuncs()).ParseFS(assets, "templates/layout.html", n)
		if err != nil {
			return nil, fmt.Errorf("web template %s: %w", base, err)
		}
		pages[base] = t
	}
	return pages, nil
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
