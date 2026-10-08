package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/web"
)

// seedSimple gives Alice a password the way `loadout user password` does.
func seedSimple(t *testing.T, s *site) {
	t.Helper()
	_, err := s.accounts.SetPasswordAsOperator(t.Context(), account.Operator(), "alice@example.com", "a long enough password")
	if err != nil {
		t.Fatal(err)
	}
}

func TestSimpleLoginOffersNoGoogleOrSignup(t *testing.T) {
	g, err := web.NewGoogle(web.GoogleConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/auth/google/callback"})
	if err != nil {
		t.Fatal(err)
	}
	s := newSite(t, siteOptions{simple: true, google: g})
	b := s.browser()
	p := b.get("/auth/login")
	for _, bad := range []string{"Google", "/auth/signup", "code by email"} {
		if strings.Contains(p.body, bad) {
			t.Errorf("simple sign-in page mentions %q", bad)
		}
	}
	for _, path := range []string{"/auth/signup", "/auth/verify", "/auth/google/start", "/auth/google/callback"} {
		if p := b.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, p.status)
		}
	}
	if p := b.post("/auth/signup", url.Values{"email": {"x@example.com"}, "password": {"password"}}); p.status != http.StatusNotFound && p.status != http.StatusMethodNotAllowed {
		t.Errorf("POST /auth/signup = %d, want 404", p.status)
	}
}

func TestSimpleLogin(t *testing.T) {
	tests := []struct {
		name, email, password string
		wantStatus            int
		wantSession           bool
	}{
		{"right password", "alice@example.com", "a long enough password", http.StatusSeeOther, true},
		{"wrong password", "alice@example.com", "nope", http.StatusBadRequest, false},
		{"unknown email", "bob@example.com", "password", http.StatusBadRequest, false},
		{"bad address", "not an email", "password", http.StatusBadRequest, false},
	}
	var wrongBody string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSite(t, siteOptions{simple: true})
			seedSimple(t, s)
			b := s.browser()
			b.get("/auth/login")
			p := b.post("/auth/login", url.Values{"email": {tt.email}, "password": {tt.password}, "next": {"/account"}})
			if p.status != tt.wantStatus {
				t.Fatalf("status = %d, want %d\n%s", p.status, tt.wantStatus, p.body)
			}
			if got := b.get("/account").status == http.StatusOK; got != tt.wantSession {
				t.Errorf("signed in = %v, want %v", got, tt.wantSession)
			}
			if tt.wantSession {
				if p.location != "/account" {
					t.Errorf("redirect = %q, want /account", p.location)
				}
				return
			}
			if tt.name == "wrong password" || tt.name == "unknown email" {
				// Same answer for both, so the page does not reveal which.
				body := strings.ReplaceAll(p.body, tt.email, "")
				if wrongBody == "" {
					wrongBody = stripTokens(body)
				} else if stripTokens(body) != wrongBody {
					t.Error("wrong password and unknown email answer differently")
				}
			}
		})
	}
}

// stripTokens drops the per-request CSRF value so two pages compare equal.
func stripTokens(s string) string {
	const key = `name="csrf" value="`
	i := strings.Index(s, key)
	if i < 0 {
		return s
	}
	j := strings.Index(s[i+len(key):], `"`)
	return s[:i+len(key)] + s[i+len(key)+j:]
}
