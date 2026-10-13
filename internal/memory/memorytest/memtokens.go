package memorytest

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// MemTokenStore is a memory.TokenStore held in memory.
type MemTokenStore struct {
	mu     sync.Mutex
	tokens []stored
}

type stored struct {
	tok  memory.Token
	hash []byte
}

// NewMemTokenStore returns an empty store.
func NewMemTokenStore() *MemTokenStore { return &MemTokenStore{} }

var _ memory.TokenStore = (*MemTokenStore)(nil)

// Create implements memory.TokenStore.
func (s *MemTokenStore) Create(ctx context.Context, t memory.Token, hash []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.tokens {
		if o.tok.ID == t.ID || bytes.Equal(o.hash, hash) {
			return fmt.Errorf("memory token: %w", registry.ErrExists)
		}
	}
	s.tokens = append(s.tokens, stored{t, bytes.Clone(hash)})
	return nil
}

// ByHash implements memory.TokenStore.
func (s *MemTokenStore) ByHash(ctx context.Context, hash []byte) (memory.Token, error) {
	if err := ctx.Err(); err != nil {
		return memory.Token{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.tokens {
		if bytes.Equal(o.hash, hash) {
			return o.tok, nil
		}
	}
	return memory.Token{}, fmt.Errorf("memory token: %w", registry.ErrNotFound)
}

// List implements memory.TokenStore.
func (s *MemTokenStore) List(ctx context.Context, userID string) ([]memory.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []memory.Token
	for _, o := range s.tokens {
		if o.tok.UserID == userID {
			out = append(out, o.tok)
		}
	}
	slices.SortStableFunc(out, func(a, b memory.Token) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// Revoke implements memory.TokenStore.
func (s *MemTokenStore) Revoke(ctx context.Context, userID, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.tokens, func(o stored) bool { return o.tok.ID == id && o.tok.UserID == userID })
	if i < 0 {
		return fmt.Errorf("memory token: %w", registry.ErrNotFound)
	}
	s.tokens = slices.Delete(s.tokens, i, i+1)
	return nil
}

// RevokeAll implements memory.TokenStore.
func (s *MemTokenStore) RevokeAll(ctx context.Context, userID string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.tokens)
	s.tokens = slices.DeleteFunc(s.tokens, func(o stored) bool { return o.tok.UserID == userID })
	return n - len(s.tokens), nil
}

// MemSwitchStore is a memory.SwitchStore held in memory.
type MemSwitchStore struct {
	mu     sync.Mutex
	spaces map[string]memory.Space
}

// NewMemSwitchStore returns a store with every namespace off.
func NewMemSwitchStore() *MemSwitchStore {
	return &MemSwitchStore{spaces: map[string]memory.Space{}}
}

var _ memory.SwitchStore = (*MemSwitchStore)(nil)

// Get implements memory.SwitchStore.
func (s *MemSwitchStore) Get(ctx context.Context, namespace string) (memory.Space, error) {
	if err := ctx.Err(); err != nil {
		return memory.Space{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sp, ok := s.spaces[namespace]; ok {
		return sp, nil
	}
	return memory.Space{Namespace: namespace}, nil
}

// Set implements memory.SwitchStore.
func (s *MemSwitchStore) Set(ctx context.Context, namespace string, on bool, by string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spaces[namespace] = memory.Space{Namespace: namespace, Enabled: on, ChangedBy: by, ChangedAt: at.UTC()}
	return nil
}

// Remove implements memory.SwitchStore.
func (s *MemSwitchStore) Remove(ctx context.Context, namespace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.spaces, namespace)
	return nil
}

// Enabled implements memory.SwitchStore.
func (s *MemSwitchStore) Enabled(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for ns, sp := range s.spaces {
		if sp.Enabled {
			out = append(out, ns)
		}
	}
	slices.Sort(out)
	return out, nil
}
