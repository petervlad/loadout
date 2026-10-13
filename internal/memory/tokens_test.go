package memory_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
	"github.com/petervlad/loadout/internal/registry"
)

type fakeUsers map[string]string

func (f fakeUsers) Email(_ context.Context, id string) (string, error) {
	if e, ok := f[id]; ok {
		return e, nil
	}
	return "", registry.ErrNotFound
}

type tokenFixture struct {
	tokens *memory.Tokens
	now    time.Time
}

var tokenStart = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func newTokenFixture(t *testing.T) *tokenFixture {
	t.Helper()
	f := &tokenFixture{now: tokenStart}
	users := fakeUsers{"u1": "dana@example.com", "u2": "lee@example.com"}
	tk, err := memory.NewTokens(memorytest.NewMemTokenStore(), users, func() time.Time { return f.now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.tokens = tk
	return f
}

var secretRE = regexp.MustCompile(`^ldm_[a-z2-7]{52}$`)

func TestNewTokensNeedsParts(t *testing.T) {
	if _, err := memory.NewTokens(nil, fakeUsers{}, nil, nil); err == nil {
		t.Error("NewTokens(nil store) = nil error")
	}
	if _, err := memory.NewTokens(memorytest.NewMemTokenStore(), nil, nil, nil); err == nil {
		t.Error("NewTokens(nil users) = nil error")
	}
}

func TestTokenCreateAndAuthenticate(t *testing.T) {
	f := newTokenFixture(t)
	secret, tok, err := f.tokens.Create(t.Context(), "u1", " laptop ")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if !secretRE.MatchString(secret) {
		t.Errorf("secret = %q, want ldm_ and 52 base32 characters", secret)
	}
	if tok.Label != "laptop" || tok.UserID != "u1" || !tok.CreatedAt.Equal(f.now) || !tok.ExpiresAt.Equal(f.now.Add(90*24*time.Hour)) {
		t.Errorf("token = %+v, want laptop for u1 valid 90 days", tok)
	}
	if tok.ID == "" || strings.Contains(secret, tok.ID) {
		t.Errorf("token id = %q", tok.ID)
	}
	c, err := f.tokens.Authenticate(t.Context(), secret)
	if err != nil || c.UserID != "u1" || c.Label != "dana@example.com" {
		t.Errorf("Authenticate() = %+v, %v, want u1 dana", c, err)
	}
	other, _, _ := f.tokens.Create(t.Context(), "u1", "")
	if other == secret {
		t.Error("two tokens share a secret")
	}
}

func TestTokenCreateRefusals(t *testing.T) {
	f := newTokenFixture(t)
	tests := map[string]struct{ user, label string }{
		"no person":        {"", "x"},
		"long label":       {"u1", strings.Repeat("a", 81)},
		"control in label": {"u1", "a\nb"},
	}
	for name, c := range tests {
		if _, _, err := f.tokens.Create(t.Context(), c.user, c.label); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("%s: Create() error = %v, want ErrInvalid", name, err)
		}
	}
}

// TestTokenCap: a person holds at most 20 valid tokens, the refusal names the
// limit, another person is not affected, and expired tokens do not count.
func TestTokenCap(t *testing.T) {
	f := newTokenFixture(t)
	var first memory.Token
	for i := range memory.MaxTokensPerUser {
		_, tok, err := f.tokens.Create(t.Context(), "u1", "t")
		if err != nil {
			t.Fatalf("Create(%d) error: %v", i+1, err)
		}
		if i == 0 {
			first = tok
		}
	}
	_, _, err := f.tokens.Create(t.Context(), "u1", "one too many")
	if !errors.Is(err, registry.ErrInvalid) || !strings.Contains(err.Error(), "20 memory tokens") {
		t.Fatalf("Create(21st) error = %v, want ErrInvalid naming 20", err)
	}
	if _, _, err := f.tokens.Create(t.Context(), "u2", "t"); err != nil {
		t.Errorf("u1's tokens limited u2: %v", err)
	}
	if err := f.tokens.Revoke(t.Context(), "u1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.tokens.Create(t.Context(), "u1", "t"); err != nil {
		t.Errorf("Create() after a revoke error: %v", err)
	}
	f.now = f.now.Add(memory.TokenLifetime + time.Hour)
	if _, _, err := f.tokens.Create(t.Context(), "u1", "t"); err != nil {
		t.Errorf("Create() after every token expired error: %v", err)
	}
}

func TestTokenAuthenticateRefusals(t *testing.T) {
	f := newTokenFixture(t)
	secret, _, _ := f.tokens.Create(t.Context(), "u1", "x")
	ghost, _, _ := f.tokens.Create(t.Context(), "ghost", "x")
	tests := []struct {
		name   string
		secret string
		move   time.Duration
	}{
		{"empty", "", 0},
		{"login token prefix", "ldt_" + secret[4:], 0},
		{"unknown", "ldm_" + strings.Repeat("a", 52), 0},
		{"expired", secret, 90 * 24 * time.Hour},
		{"user is gone", ghost, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.now = tokenStart.Add(tt.move)
			if _, err := f.tokens.Authenticate(t.Context(), tt.secret); !errors.Is(err, memory.ErrUnauthorized) {
				t.Errorf("Authenticate() error = %v, want ErrUnauthorized", err)
			}
		})
	}
	f.now = tokenStart.Add(90*24*time.Hour - time.Second)
	if _, err := f.tokens.Authenticate(t.Context(), secret); err != nil {
		t.Errorf("Authenticate() one second before expiry error: %v", err)
	}
}

func TestTokenListRevoke(t *testing.T) {
	f := newTokenFixture(t)
	s1, t1, _ := f.tokens.Create(t.Context(), "u1", "a")
	f.now = f.now.Add(time.Hour)
	_, t2, _ := f.tokens.Create(t.Context(), "u1", "b")
	_, t3, _ := f.tokens.Create(t.Context(), "u2", "c")

	got, err := f.tokens.List(t.Context(), "u1")
	if err != nil || len(got) != 2 || got[0].ID != t2.ID || got[1].ID != t1.ID {
		t.Errorf("List(u1) = %+v, %v, want newest first", got, err)
	}
	if err := f.tokens.Revoke(t.Context(), "u1", t3.ID); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Revoke(someone else's) error = %v, want ErrNotFound", err)
	}
	if err := f.tokens.Revoke(t.Context(), "u1", t1.ID); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}
	if _, err := f.tokens.Authenticate(t.Context(), s1); !errors.Is(err, memory.ErrUnauthorized) {
		t.Errorf("Authenticate(revoked) error = %v, want ErrUnauthorized", err)
	}
	if n, err := f.tokens.RevokeAll(t.Context(), "u1"); err != nil || n != 1 {
		t.Errorf("RevokeAll(u1) = %d, %v, want 1", n, err)
	}
	if left, _ := f.tokens.List(t.Context(), "u2"); len(left) != 1 {
		t.Errorf("RevokeAll(u1) left %d of u2's tokens, want 1", len(left))
	}
}
