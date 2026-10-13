package memorytest

import (
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

func token(id, user string, created time.Time) memory.Token {
	return memory.Token{ID: id, UserID: user, Label: "laptop", CreatedAt: created, ExpiresAt: created.Add(90 * 24 * time.Hour)}
}

// RunTokenStoreContract runs the behaviour every memory.TokenStore must have.
// newStore must return a fresh, empty store for each call.
func RunTokenStoreContract(t *testing.T, newStore func(t *testing.T) memory.TokenStore) {
	t.Helper()
	t.Run("create and find by hash", func(t *testing.T) {
		s := newStore(t)
		tok := token("t1", "u1", at)
		if err := s.Create(t.Context(), tok, []byte("hash-1")); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		got, err := s.ByHash(t.Context(), []byte("hash-1"))
		if err != nil {
			t.Fatalf("ByHash() error: %v", err)
		}
		if got.ID != "t1" || got.UserID != "u1" || got.Label != "laptop" || !got.CreatedAt.Equal(tok.CreatedAt) || !got.ExpiresAt.Equal(tok.ExpiresAt) {
			t.Errorf("ByHash() = %+v, want %+v", got, tok)
		}
		_, err = s.ByHash(t.Context(), []byte("other"))
		wantIs(t, "ByHash(unknown)", err, registry.ErrNotFound)
	})
	t.Run("an expired token is still returned", func(t *testing.T) {
		s := newStore(t)
		old := token("t1", "u1", at.AddDate(-1, 0, 0))
		if err := s.Create(t.Context(), old, []byte("h")); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if got, err := s.ByHash(t.Context(), []byte("h")); err != nil || got.ID != "t1" {
			t.Errorf("ByHash() = %+v, %v, want the token, whose expiry the caller judges", got, err)
		}
	})
	t.Run("repeats are refused", func(t *testing.T) {
		s := newStore(t)
		if err := s.Create(t.Context(), token("t1", "u1", at), []byte("h1")); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		wantIs(t, "Create(same id)", s.Create(t.Context(), token("t1", "u2", at), []byte("h2")), registry.ErrExists)
		wantIs(t, "Create(same hash)", s.Create(t.Context(), token("t2", "u2", at), []byte("h1")), registry.ErrExists)
		if _, err := s.ByHash(t.Context(), []byte("h2")); err == nil {
			t.Error("a refused Create() stored the token")
		}
	})
	t.Run("list is one person newest first", func(t *testing.T) {
		s := newStore(t)
		for i, u := range []string{"u1", "u2", "u1", "u1"} {
			id := string(rune('a' + i))
			if err := s.Create(t.Context(), token(id, u, at.Add(time.Duration(i)*time.Hour)), []byte("h-"+id)); err != nil {
				t.Fatalf("Create() error: %v", err)
			}
		}
		got, err := s.List(t.Context(), "u1")
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		var order []string
		for _, tk := range got {
			order = append(order, tk.ID)
		}
		if len(order) != 3 || order[0] != "d" || order[1] != "c" || order[2] != "a" {
			t.Errorf("List(u1) = %v, want [d c a]", order)
		}
		if got, _ := s.List(t.Context(), "nobody"); len(got) != 0 {
			t.Errorf("List(nobody) = %v, want none", got)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		s := newStore(t)
		if err := s.Create(t.Context(), token("t1", "u1", at), []byte("h1")); err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		wantIs(t, "Revoke(someone else's)", s.Revoke(t.Context(), "u2", "t1"), registry.ErrNotFound)
		if _, err := s.ByHash(t.Context(), []byte("h1")); err != nil {
			t.Errorf("another person's Revoke() removed the token: %v", err)
		}
		if err := s.Revoke(t.Context(), "u1", "t1"); err != nil {
			t.Fatalf("Revoke() error: %v", err)
		}
		_, err := s.ByHash(t.Context(), []byte("h1"))
		wantIs(t, "ByHash(revoked)", err, registry.ErrNotFound)
		wantIs(t, "Revoke(again)", s.Revoke(t.Context(), "u1", "t1"), registry.ErrNotFound)
	})
	t.Run("revoke all", func(t *testing.T) {
		s := newStore(t)
		for i, u := range []string{"u1", "u1", "u2"} {
			id := string(rune('a' + i))
			if err := s.Create(t.Context(), token(id, u, at), []byte("h-"+id)); err != nil {
				t.Fatalf("Create() error: %v", err)
			}
		}
		n, err := s.RevokeAll(t.Context(), "u1")
		if err != nil || n != 2 {
			t.Errorf("RevokeAll(u1) = %d, %v, want 2", n, err)
		}
		if _, err := s.ByHash(t.Context(), []byte("h-a")); err == nil {
			t.Error("a revoked token is still found")
		}
		if _, err := s.ByHash(t.Context(), []byte("h-c")); err != nil {
			t.Errorf("RevokeAll(u1) removed u2's token: %v", err)
		}
		if n, _ := s.RevokeAll(t.Context(), "u1"); n != 0 {
			t.Errorf("RevokeAll(again) = %d, want 0", n)
		}
	})
}

// RunSwitchContract runs the behaviour every memory.SwitchStore must have.
// newStore must return a fresh, empty store for each call.
func RunSwitchContract(t *testing.T, newStore func(t *testing.T) memory.SwitchStore) {
	t.Helper()
	t.Run("unset is off", func(t *testing.T) {
		s := newStore(t)
		sp, err := s.Get(t.Context(), "lime")
		if err != nil || sp.Enabled || sp.Namespace != "lime" {
			t.Errorf("Get() = %+v, %v, want lime off", sp, err)
		}
		if on, _ := s.Enabled(t.Context()); len(on) != 0 {
			t.Errorf("Enabled() = %v, want none", on)
		}
	})
	t.Run("on and off", func(t *testing.T) {
		s := newStore(t)
		if err := s.Set(t.Context(), "lime", true, "u1", at); err != nil {
			t.Fatalf("Set() error: %v", err)
		}
		sp, _ := s.Get(t.Context(), "lime")
		if !sp.Enabled || sp.ChangedBy != "u1" || !sp.ChangedAt.Equal(at) {
			t.Errorf("Get() = %+v, want on, changed by u1 at %v", sp, at)
		}
		if err := s.Set(t.Context(), "lime", false, "u2", at.Add(time.Hour)); err != nil {
			t.Fatalf("Set(off) error: %v", err)
		}
		sp, _ = s.Get(t.Context(), "lime")
		if sp.Enabled || sp.ChangedBy != "u2" || !sp.ChangedAt.Equal(at.Add(time.Hour)) {
			t.Errorf("Get() = %+v, want off, changed by u2", sp)
		}
		if other, _ := s.Get(t.Context(), "acme"); other.Enabled {
			t.Error("turning lime off or on changed acme")
		}
	})
	t.Run("remove forgets the row", func(t *testing.T) {
		s := newStore(t)
		for _, ns := range []string{"lime", "acme"} {
			if err := s.Set(t.Context(), ns, true, "u1", at); err != nil {
				t.Fatalf("Set() error: %v", err)
			}
		}
		if err := s.Remove(t.Context(), "lime"); err != nil {
			t.Fatalf("Remove() error: %v", err)
		}
		sp, _ := s.Get(t.Context(), "lime")
		if sp.Enabled || sp.ChangedBy != "" || !sp.ChangedAt.IsZero() {
			t.Errorf("Get(lime) after Remove = %+v, want a namespace never set", sp)
		}
		if on, _ := s.Enabled(t.Context()); len(on) != 1 || on[0] != "acme" {
			t.Errorf("Enabled() = %v, want [acme]", on)
		}
		if err := s.Remove(t.Context(), "lime"); err != nil {
			t.Errorf("Remove(again) error: %v", err)
		}
	})
	t.Run("enabled is sorted and only on", func(t *testing.T) {
		s := newStore(t)
		for _, c := range []struct {
			ns string
			on bool
		}{{"lime", true}, {"acme", true}, {"pear", true}, {"pear", false}} {
			if err := s.Set(t.Context(), c.ns, c.on, "u1", at); err != nil {
				t.Fatalf("Set() error: %v", err)
			}
		}
		got, err := s.Enabled(t.Context())
		if err != nil || len(got) != 2 || got[0] != "acme" || got[1] != "lime" {
			t.Errorf("Enabled() = %v, %v, want [acme lime]", got, err)
		}
	})
}
