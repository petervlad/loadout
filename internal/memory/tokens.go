package memory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

const (
	// TokenPrefix starts every memory token secret. registry.Service refuses
	// anything that does not start with "ldt_", so a memory token never works
	// on the other API routes.
	TokenPrefix = "ldm_"
	// TokenLifetime is how long a new token is valid.
	TokenLifetime = 90 * 24 * time.Hour

	secretBytes = 32 // 256 bits, 52 base32 characters
	maxLabelLen = 80
	// MaxTokensPerUser is how many valid memory tokens a person can hold. A
	// loop that makes tokens, or a laptop that never revokes, stops here.
	MaxTokensPerUser = 20
)

// Users answers whether a person still exists, and under which email. It
// returns registry.ErrNotFound for one that does not.
type Users interface {
	Email(ctx context.Context, userID string) (string, error)
}

// Tokens makes and checks memory tokens. It is safe for concurrent use.
type Tokens struct {
	store TokenStore
	users Users
	now   func() time.Time
	log   *slog.Logger
}

// NewTokens returns a token service. A nil clock reads time.Now and a nil
// logger discards. The store and the users are required.
func NewTokens(store TokenStore, users Users, now func() time.Time, log *slog.Logger) (*Tokens, error) {
	if store == nil || users == nil {
		return nil, errors.New("memory tokens need a store and a user lookup")
	}
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Tokens{store: store, users: users, now: now, log: log}, nil
}

func hashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Create makes a token for a person and returns its secret, which is shown
// once and never stored. The label says where the token is used.
func (t *Tokens) Create(ctx context.Context, userID, label string) (string, Token, error) {
	label = strings.TrimSpace(label)
	if userID == "" {
		return "", Token{}, fmt.Errorf("%w: a memory token needs a person", registry.ErrInvalid)
	}
	if len(label) > maxLabelLen || strings.ContainsFunc(label, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return "", Token{}, fmt.Errorf("%w: a token label is at most %d characters, without control characters", registry.ErrInvalid, maxLabelLen)
	}
	if err := t.underTokenCap(ctx, userID); err != nil {
		return "", Token{}, err
	}
	raw := make([]byte, secretBytes)
	idRaw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", Token{}, fmt.Errorf("create memory token: %w", err)
	}
	if _, err := rand.Read(idRaw); err != nil {
		return "", Token{}, fmt.Errorf("create memory token: %w", err)
	}
	secret := TokenPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	at := t.now().UTC()
	tok := Token{ID: fmt.Sprintf("%x", idRaw), UserID: userID, Label: label, CreatedAt: at, ExpiresAt: at.Add(TokenLifetime)}
	if err := t.store.Create(ctx, tok, hashSecret(secret)); err != nil {
		return "", Token{}, fmt.Errorf("create memory token: %w", err)
	}
	t.log.InfoContext(ctx, "memory token created", "token_id", tok.ID, "user_id", userID, "label", label)
	return secret, tok, nil
}

// underTokenCap refuses a new token for a person who holds the most allowed.
// Expired tokens do not count: they no longer work.
func (t *Tokens) underTokenCap(ctx context.Context, userID string) error {
	have, err := t.store.List(ctx, userID)
	if err != nil {
		return fmt.Errorf("create memory token: %w", err)
	}
	valid := 0
	for _, tok := range have {
		if t.now().Before(tok.ExpiresAt) {
			valid++
		}
	}
	if valid >= MaxTokensPerUser {
		return fmt.Errorf("%w: you already have %d memory tokens, the most allowed. Revoke the ones you no longer use with loadout memory token revoke, or on your account page", registry.ErrInvalid, MaxTokensPerUser)
	}
	return nil
}

// Authenticate resolves a secret to the person it acts for. Every failure is
// ErrUnauthorized, so a caller learns nothing about why. Only a store or user
// lookup that broke is another error.
func (t *Tokens) Authenticate(ctx context.Context, secret string) (Caller, error) {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return Caller{}, ErrUnauthorized
	}
	tok, err := t.store.ByHash(ctx, hashSecret(secret))
	if errors.Is(err, registry.ErrNotFound) {
		return Caller{}, ErrUnauthorized
	}
	if err != nil {
		return Caller{}, fmt.Errorf("memory token: %w", err)
	}
	if !t.now().Before(tok.ExpiresAt) {
		return Caller{}, ErrUnauthorized
	}
	email, err := t.users.Email(ctx, tok.UserID)
	if errors.Is(err, registry.ErrNotFound) {
		return Caller{}, ErrUnauthorized
	}
	if err != nil {
		return Caller{}, fmt.Errorf("memory token user: %w", err)
	}
	return Caller{UserID: tok.UserID, Label: email}, nil
}

// List returns a person's tokens, newest first, without secrets.
func (t *Tokens) List(ctx context.Context, userID string) ([]Token, error) {
	toks, err := t.store.List(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list memory tokens: %w", err)
	}
	return toks, nil
}

// Revoke removes one of a person's own tokens, or is registry.ErrNotFound.
func (t *Tokens) Revoke(ctx context.Context, userID, id string) error {
	if err := t.store.Revoke(ctx, userID, id); err != nil {
		return fmt.Errorf("revoke memory token: %w", err)
	}
	t.log.InfoContext(ctx, "memory token revoked", "token_id", id, "user_id", userID)
	return nil
}

// RevokeAll removes every token of a person and says how many.
func (t *Tokens) RevokeAll(ctx context.Context, userID string) (int, error) {
	n, err := t.store.RevokeAll(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke memory tokens: %w", err)
	}
	t.log.InfoContext(ctx, "memory tokens revoked", "user_id", userID, "count", n)
	return n, nil
}
