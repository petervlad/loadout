package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/petervlad/loadout/internal/account"
)

// GoogleIssuer is Google's OpenID Connect issuer.
const GoogleIssuer = "https://accounts.google.com"

// oauthTTL is how long a browser may take at Google.
const oauthTTL = 10 * time.Minute

// GoogleConfig configures sign-in with Google.
type GoogleConfig struct {
	ClientID string
	// ClientSecret is a secret; it is never logged.
	ClientSecret string
	// RedirectURL is {public URL}/auth/google/callback.
	RedirectURL string
	// Issuer defaults to GoogleIssuer; tests point it at a fake provider.
	Issuer string
	// HTTPClient defaults to one with a 30 second timeout.
	HTTPClient *http.Client
}

// Google signs people in with Google: the authorization code flow with
// PKCE, state and nonce, and an ID token verified for issuer, audience,
// expiry, signature and nonce.
type Google struct {
	cfg    GoogleConfig
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

// NewGoogle returns a Google sign-in. It does not contact Google; the
// discovery document is fetched on the first sign-in.
func NewGoogle(cfg GoogleConfig) (*Google, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, errors.New("google sign-in needs a client id, a client secret and a redirect URL")
	}
	if cfg.Issuer == "" {
		cfg.Issuer = GoogleIssuer
	}
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	return &Google{cfg: cfg, client: c}, nil
}

// discover returns the provider, fetching its discovery document once.
func (g *Google) discover(ctx context.Context) (*oidc.Provider, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider != nil {
		return g.provider, nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, g.client), g.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("google discovery: %w", err)
	}
	g.provider = p
	return p, nil
}

func (g *Google) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     g.cfg.ClientID,
		ClientSecret: g.cfg.ClientSecret,
		RedirectURL:  g.cfg.RedirectURL,
		Endpoint:     p.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
}

// authURL is where the browser goes to sign in at Google.
func (g *Google) authURL(ctx context.Context, s oauthState) (string, error) {
	p, err := g.discover(ctx)
	if err != nil {
		return "", err
	}
	return g.oauth(p).AuthCodeURL(s.State,
		oauth2.S256ChallengeOption(s.Verifier),
		oidc.Nonce(s.Nonce),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	), nil
}

// identity exchanges the code for tokens and verifies the ID token.
func (g *Google) identity(ctx context.Context, code string, s oauthState) (account.GoogleIdentity, error) {
	p, err := g.discover(ctx)
	if err != nil {
		return account.GoogleIdentity{}, err
	}
	ctx = oidc.ClientContext(ctx, g.client)
	tok, err := g.oauth(p).Exchange(ctx, code, oauth2.VerifierOption(s.Verifier))
	if err != nil {
		return account.GoogleIdentity{}, fmt.Errorf("google token exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return account.GoogleIdentity{}, errors.New("google sent no id token")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: g.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return account.GoogleIdentity{}, fmt.Errorf("google id token: %w", err)
	}
	if !equal(idt.Nonce, s.Nonce) {
		return account.GoogleIdentity{}, errors.New("google id token: nonce does not match")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil {
		return account.GoogleIdentity{}, fmt.Errorf("google id token claims: %w", err)
	}
	return account.GoogleIdentity{Subject: idt.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified, Name: claims.Name}, nil
}

// oauthState is what the browser carries to Google and back, in a cookie
// only this site can read.
type oauthState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x"`
}

func newOAuthState(next string) (oauthState, error) {
	var s oauthState
	for _, p := range []*string{&s.State, &s.Nonce} {
		v, err := randomToken()
		if err != nil {
			return oauthState{}, err
		}
		*p = v
	}
	s.Verifier = oauth2.GenerateVerifier()
	s.Next = safeNext(next)
	return s, nil
}

func (s oauthState) encode() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeOAuthState(v string) (oauthState, bool) {
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return oauthState{}, false
	}
	var s oauthState
	if json.Unmarshal(b, &s) != nil || s.State == "" || s.Nonce == "" || s.Verifier == "" {
		return oauthState{}, false
	}
	return s, true
}

func (h *handler) googleStart(w http.ResponseWriter, r *http.Request) {
	if h.google == nil {
		http.NotFound(w, r)
		return
	}
	if !h.allow(r, "") {
		h.tooMany(w, r)
		return
	}
	s, err := newOAuthState(r.FormValue("next"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	target, err := h.google.authURL(r.Context(), s)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	enc, err := s.encode()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.cookies.set(w, cookieOAuth, enc, oauthTTL)
	http.Redirect(w, r, target, http.StatusSeeOther) //nolint:gosec // G710: the provider's own authorization endpoint
}

func (h *handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	if h.google == nil {
		http.NotFound(w, r)
		return
	}
	s, ok := decodeOAuthState(h.cookies.get(r, cookieOAuth))
	h.cookies.clear(w, cookieOAuth) // one attempt per state
	q := r.URL.Query()
	if !ok || !equal(q.Get("state"), s.State) {
		h.message(w, r, http.StatusBadRequest, "Sign-in expired", "This Google sign-in expired or was started elsewhere. Try again.")
		return
	}
	if q.Get("error") != "" {
		h.message(w, r, http.StatusBadRequest, "Google sign-in cancelled", "Google did not sign you in. Try again.")
		return
	}
	id, err := h.google.identity(r.Context(), q.Get("code"), s)
	if err != nil {
		h.log.WarnContext(r.Context(), "google sign-in failed", "err", err)
		h.message(w, r, http.StatusBadGateway, "Google sign-in failed", "Google sign-in did not work. Try again.")
		return
	}
	g, err := h.accounts.GoogleSignIn(r.Context(), id)
	switch {
	case errors.Is(err, account.ErrGoogleEmailUnverified):
		h.message(w, r, http.StatusForbidden, "Email not verified", "Google has not verified the email address of this account.")
		return
	case errors.Is(err, account.ErrGoogleConflict):
		h.message(w, r, http.StatusConflict, "Different Google account", "This email address is linked to another Google account. Sign in with that one, or with your password.")
		return
	case errors.Is(err, account.ErrInvalidEmail):
		h.message(w, r, http.StatusBadRequest, "Unusable email", "Google sent an email address loadout cannot use.")
		return
	case err != nil:
		h.fail(w, r, err)
		return
	}
	h.startSession(w, r, g, s.Next)
}
