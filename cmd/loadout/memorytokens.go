package main

import (
	"context"
	"fmt"
	"time"

	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/mcpsetup"
	"github.com/petervlad/loadout/internal/remote"
)

// renewWithin is how close to its end a stored memory token is replaced,
// so a laptop that syncs weekly never meets an expired one.
const renewWithin = 14 * 24 * time.Hour

// memoryTokens makes sure the memory token a team's bridge reads is stored
// and not about to expire. It implements mcpsetup.TokenEnsurer. The token
// is made with the person's login token and goes straight to the store: it
// is never returned, logged or printed.
type memoryTokens struct {
	remotes   *remote.Store
	newClient func(r remote.Remote, loginToken string) (*client.Client, error)
	label     string
	now       func() time.Time
}

var _ mcpsetup.TokenEnsurer = memoryTokens{}

// EnsureToken stores a memory token for the remote that serves s, unless a
// good one is there already or LOADOUT_MEMORY_TOKEN stands in for it.
func (m memoryTokens) EnsureToken(ctx context.Context, s mcpsetup.Space) error {
	cfg, err := m.remotes.Load()
	if err != nil {
		return err
	}
	r, ok := cfg.Get(s.Remote)
	if !ok {
		return fmt.Errorf("no remote called %q; add it with loadout remote add", s.Remote)
	}
	if m.remotes.MemoryTokenFromEnv() {
		return nil
	}
	have, err := m.remotes.StoredMemoryToken(r.Name)
	if err != nil {
		return err
	}
	oldID, err := m.remotes.StoredMemoryTokenID(r.Name)
	if err != nil {
		return err
	}
	expires, err := m.remotes.StoredMemoryTokenExpiry(r.Name)
	if err != nil {
		return err
	}
	if have != "" && expires.After(m.now().Add(renewWithin)) {
		return nil
	}
	login, err := m.remotes.Token(r.Name)
	if err != nil {
		return err
	}
	if login == "" {
		return fmt.Errorf("you are not signed in to %s; run loadout login", r.Name)
	}
	c, err := m.newClient(r, login)
	if err != nil {
		return err
	}
	tok, err := c.CreateMemoryToken(ctx, m.label)
	if err != nil {
		return fmt.Errorf("make a memory token on %s: %w", r.Name, err)
	}
	if err := m.remotes.SetMemoryTokenMade(r.Name, tok.Token, tok.ID, tok.ExpiresAt); err != nil {
		return fmt.Errorf("the registry made memory token %s on %s, but it could not be stored: %w. Revoke it with loadout memory token revoke %s", tok.ID, r.Name, err, tok.ID)
	}
	// The token it replaces would otherwise stay valid until it expires, and
	// every renewal would add one. Best effort: a failure leaves a token that
	// ends by itself, and the registry caps how many there can be.
	if oldID != "" && oldID != tok.ID {
		_ = c.RevokeMemoryToken(ctx, oldID)
	}
	return nil
}
