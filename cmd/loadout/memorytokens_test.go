package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/mcpsetup"
	"github.com/petervlad/loadout/internal/remote"
)

// TestRenewalRevokesThePreviousToken: when sync replaces a token that is about
// to expire, the one it replaces is revoked, so renewals do not pile up.
func TestRenewalRevokesThePreviousToken(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var made int
	var revoked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/memory/tokens":
			made++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.MemoryToken{ID: "new" + strconv.Itoa(made), Token: "ldm_new" + strconv.Itoa(made), ExpiresAt: now.Add(90 * 24 * time.Hour)})
		case r.Method == http.MethodDelete:
			revoked = append(revoked, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := remote.NewStore(t.TempDir(), func(string) string { return "" })
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Add(remote.Remote{Name: "office", URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.SetToken("office", "ldt_login"); err != nil {
		t.Fatal(err)
	}
	m := memoryTokens{
		remotes:   store,
		newClient: func(r remote.Remote, login string) (*client.Client, error) { return client.New(r.URL, login) },
		label:     "laptop",
		now:       func() time.Time { return now },
	}
	space := mcpsetup.Space{Namespace: "lime", Remote: "office"}

	// No token yet: one is made, and there is nothing to revoke.
	if err := m.EnsureToken(t.Context(), space); err != nil {
		t.Fatal(err)
	}
	if made != 1 || len(revoked) != 0 {
		t.Fatalf("first run made %d tokens and revoked %v, want 1 and none", made, revoked)
	}
	if id, _ := store.StoredMemoryTokenID("office"); id != "new1" {
		t.Errorf("stored token id = %q, want new1", id)
	}

	// Still good for more than 14 days: nothing happens.
	if err := m.EnsureToken(t.Context(), space); err != nil || made != 1 {
		t.Fatalf("second run made %d tokens, %v, want it left alone", made, err)
	}

	// Under 14 days left: a new one replaces it and the old one is revoked.
	m.now = func() time.Time { return now.Add(80 * 24 * time.Hour) }
	if err := m.EnsureToken(t.Context(), space); err != nil {
		t.Fatal(err)
	}
	if made != 2 || len(revoked) != 1 || revoked[0] != "/v1/memory/tokens/new1" {
		t.Errorf("renewal made %d tokens and revoked %v, want 2 and the first", made, revoked)
	}
	if tok, _ := store.StoredMemoryToken("office"); tok != "ldm_new2" {
		t.Errorf("stored token = %q, want the new one", tok)
	}
}
