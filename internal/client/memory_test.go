package client_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
)

// memoryRegistry is a stand-in for the routes of team memory, which the
// server grows in a later step. It records the last request.
type memoryRegistry struct {
	method, path, auth, body string
}

func (m *memoryRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.method, m.path, m.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
	body, _ := io.ReadAll(r.Body)
	m.body = strings.TrimSpace(string(body))
	w.Header().Set("Content-Type", "application/json")
	exp := time.Date(2027, 1, 7, 0, 0, 0, 0, time.UTC)
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/memory/tokens":
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.MemoryToken{ID: "ab12", Label: "laptop", ExpiresAt: exp, Token: "ldm_secret"})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/memory/tokens":
		_ = json.NewEncoder(w).Encode(api.MemoryTokensResponse{Tokens: []api.MemoryToken{{ID: "ab12", ExpiresAt: exp}}})
	case r.Method == http.MethodDelete && r.URL.Path == "/v1/memory/tokens/ab12":
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/v1/memory/spaces":
		_ = json.NewEncoder(w).Encode(api.MemorySpacesResponse{Spaces: []api.MemorySpace{{Namespace: "lime", Kind: api.LayerTeam, Endpoint: "https://r/mcp/t/lime"}}})
	case r.Method == http.MethodPatch && r.URL.Path == "/v1/namespaces/old":
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(api.ErrorResponse{Error: api.ErrorBody{Code: api.CodeInvalid, Message: `json: unknown field "memory"`}})
	case r.Method == http.MethodPatch:
		_ = json.NewEncoder(w).Encode(api.Namespace{Name: "lime", Memory: true})
	default:
		http.NotFound(w, r)
	}
}

func TestMemoryClientCalls(t *testing.T) {
	m := &memoryRegistry{}
	ts := httptest.NewServer(m)
	t.Cleanup(ts.Close)
	c := newClient(t, ts.URL, "ldt_login")

	tok, err := c.CreateMemoryToken(t.Context(), "laptop")
	if err != nil || tok.Token != "ldm_secret" || tok.ID != "ab12" {
		t.Fatalf("CreateMemoryToken() = %+v, %v", tok, err)
	}
	if m.auth != "Bearer ldt_login" || !strings.Contains(m.body, `"label":"laptop"`) {
		t.Errorf("request auth %q body %q, want the login token and the label", m.auth, m.body)
	}
	if toks, err := c.MemoryTokens(t.Context()); err != nil || len(toks) != 1 || toks[0].Token != "" {
		t.Errorf("MemoryTokens() = %+v, %v, want one without a secret", toks, err)
	}
	if err := c.RevokeMemoryToken(t.Context(), "ab12"); err != nil {
		t.Errorf("RevokeMemoryToken() error: %v", err)
	}
	if sp, err := c.MemorySpaces(t.Context()); err != nil || len(sp) != 1 || sp[0].Endpoint != "https://r/mcp/t/lime" {
		t.Errorf("MemorySpaces() = %+v, %v", sp, err)
	}
	ns, err := c.SetMemory(t.Context(), "lime", true)
	if err != nil || !ns.Memory || m.body != `{"memory":true}` {
		t.Errorf("SetMemory() = %+v, %v, body %q", ns, err, m.body)
	}
}

func TestSetMemoryOnOlderRegistry(t *testing.T) {
	ts := httptest.NewServer(&memoryRegistry{})
	t.Cleanup(ts.Close)
	_, err := newClient(t, ts.URL, "ldt_login").SetMemory(t.Context(), "old", true)
	if err == nil || !strings.Contains(err.Error(), "needs a newer loadout on the registry") {
		t.Errorf("SetMemory() error = %v, want the newer registry sentence", err)
	}
}
