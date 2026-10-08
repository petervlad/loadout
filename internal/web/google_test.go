package web_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/web"
)

const clientID = "test-client.apps.example"

// fakeOIDC is an OpenID provider: discovery, JWKS and a token endpoint that
// answers one code with an ID token signed by a test RSA key.
type fakeOIDC struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu sync.Mutex
	// Set from the authorization request, checked at the token endpoint.
	challenge string
	nonce     string
	// claims overrides the ID token claims of the next exchange.
	claims map[string]any
	// gotVerifier is the code_verifier the token request carried.
	gotVerifier string
}

func newFakeOIDC(t *testing.T) *fakeOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOIDC{t: t, key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/auth",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		panic(err)
	}
}

func (f *fakeOIDC) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("code") != "good-code" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotVerifier = r.PostForm.Get("code_verifier")
	sum := sha256.Sum256([]byte(f.gotVerifier))
	if b64(sum[:]) != f.challenge {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant", "error_description": "PKCE"})
		return
	}
	now := time.Now()
	claims := map[string]any{
		"iss": f.srv.URL, "aud": clientID, "sub": "google-123",
		"email": "Alice@Example.com", "email_verified": true, "name": "Alice",
		"nonce": f.nonce, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	maps.Copy(claims, f.claims)
	writeJSON(w, map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": f.sign(claims)})
}

func (f *fakeOIDC) sign(claims map[string]any) string {
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	if err != nil {
		f.t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		f.t.Fatal(err)
	}
	input := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return input + "." + b64(sig)
}

// authorize plays the provider's sign-in page: it reads the authorization
// request the site redirected to and remembers its PKCE challenge and
// nonce. It returns the state to send back.
func (f *fakeOIDC) authorize(t *testing.T, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(location, f.srv.URL+"/auth?") {
		t.Fatalf("redirect to %s, want the provider", location)
	}
	q := u.Query()
	for k, want := range map[string]string{"client_id": clientID, "response_type": "code", "code_challenge_method": "S256", "redirect_uri": "http://localhost/auth/google/callback"} {
		if q.Get(k) != want {
			t.Errorf("authorization request %s = %q, want %q", k, q.Get(k), want)
		}
	}
	if !strings.Contains(q.Get("scope"), "openid") || !strings.Contains(q.Get("scope"), "email") {
		t.Errorf("scope = %q, want openid and email", q.Get("scope"))
	}
	if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		t.Fatalf("authorization request lacks state, nonce or PKCE: %s", location)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.challenge, f.nonce = q.Get("code_challenge"), q.Get("nonce")
	return q.Get("state")
}

func newGoogleSite(t *testing.T) (*site, *fakeOIDC) {
	t.Helper()
	f := newFakeOIDC(t)
	g, err := web.NewGoogle(web.GoogleConfig{
		ClientID:     clientID,
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost/auth/google/callback",
		Issuer:       f.srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return newSite(t, siteOptions{google: g}), f
}

func TestGoogleSignIn(t *testing.T) {
	s, f := newGoogleSite(t)
	b := s.browser()
	if p := b.get("/auth/login"); !strings.Contains(p.body, "Continue with Google") {
		t.Error("the sign-in page has no Google button")
	}
	p := b.get("/auth/google/start?next=" + url.QueryEscape("/auth/device?code=BCDF-GHJK"))
	if p.status != http.StatusSeeOther {
		t.Fatalf("start = %d\n%s", p.status, p.body)
	}
	state := f.authorize(t, p.location)

	p = b.get("/auth/google/callback?" + url.Values{"code": {"good-code"}, "state": {state}}.Encode())
	if p.status != http.StatusSeeOther || p.location != "/auth/device?code=BCDF-GHJK" {
		t.Fatalf("callback = %d %s, want the next page\n%s", p.status, p.location, p.body)
	}
	if f.gotVerifier == "" {
		t.Error("the token request carried no PKCE verifier")
	}
	acct := b.get("/account")
	if acct.status != 200 || !strings.Contains(acct.body, "alice@example.com") {
		t.Errorf("account after Google = %d\n%s", acct.status, acct.body)
	}
	if len(s.mails.Sent()) != 0 {
		t.Error("Google sign-in sent a mail")
	}

	// The state cookie is single use.
	if p := b.get("/auth/google/callback?" + url.Values{"code": {"good-code"}, "state": {state}}.Encode()); p.status != http.StatusBadRequest {
		t.Errorf("replayed callback = %d, want 400", p.status)
	}
}

func TestGoogleCallbackRejects(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
		state  func(string) string
		code   string
		want   int
	}{
		{name: "wrong state", state: func(string) string { return "forged" }, want: http.StatusBadRequest},
		{name: "wrong nonce", claims: map[string]any{"nonce": "other"}, want: http.StatusBadGateway},
		{name: "wrong audience", claims: map[string]any{"aud": "someone-else"}, want: http.StatusBadGateway},
		{name: "wrong issuer", claims: map[string]any{"iss": "https://evil.example"}, want: http.StatusBadGateway},
		{name: "expired", claims: map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}, want: http.StatusBadGateway},
		{name: "unverified email", claims: map[string]any{"email_verified": false}, want: http.StatusForbidden},
		{name: "bad code", code: "bad-code", want: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, f := newGoogleSite(t)
			f.claims = tt.claims
			b := s.browser()
			p := b.get("/auth/google/start")
			state := f.authorize(t, p.location)
			if tt.state != nil {
				state = tt.state(state)
			}
			code := tt.code
			if code == "" {
				code = "good-code"
			}
			p = b.get("/auth/google/callback?" + url.Values{"code": {code}, "state": {state}}.Encode())
			if p.status != tt.want {
				t.Errorf("callback = %d, want %d\n%s", p.status, tt.want, p.body)
			}
			if p := b.get("/account"); p.status != http.StatusSeeOther {
				t.Error("signed in after a rejected callback")
			}
		})
	}
}

func TestNoGoogleWithoutConfig(t *testing.T) {
	s := newSite(t, siteOptions{})
	b := s.browser()
	if p := b.get("/auth/login"); strings.Contains(p.body, "Google") {
		t.Error("the Google button shows without Google settings")
	}
	if p := b.get("/auth/google/start"); p.status != http.StatusNotFound {
		t.Errorf("GET /auth/google/start = %d, want 404", p.status)
	}
	if _, err := web.NewGoogle(web.GoogleConfig{ClientID: "x"}); err == nil {
		t.Error("NewGoogle() without a secret succeeded")
	}
}
