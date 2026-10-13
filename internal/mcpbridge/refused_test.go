package mcpbridge_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petervlad/loadout/internal/mcpbridge"
)

// TestRefusedIsNotTheVPN: a registry that answers 401 or 404 is reachable, so
// the call must not tell the person to check the connection.
func TestRefusedIsNotTheVPN(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"token refused", http.StatusUnauthorized, "memory token was refused"},
		{"memory off or not a member", http.StatusNotFound, "off for this team"},
	} {
		t.Run(tc.name+" at start", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "no", tc.status)
			}))
			defer srv.Close()
			b := start(t, mcpbridge.Config{URL: srv.URL, Team: "lime", Token: secret})
			res, err := b.CallTool(t.Context(), &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "x"}})
			if err != nil {
				t.Fatal(err)
			}
			if got := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(got, tc.want) || strings.Contains(got, "VPN") {
				t.Errorf("answer = %q, want one naming %q", got, tc.want)
			}
		})
	}

	t.Run("token revoked mid-session", func(t *testing.T) {
		r := newRemote(t)
		var revoked atomic.Bool
		inner := r.srv.Config.Handler
		r.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if revoked.Load() {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			inner.ServeHTTP(w, req)
		})
		b := start(t, r.config())
		revoked.Store(true)
		res, err := b.CallTool(t.Context(), &mcp.CallToolParams{Name: "recent", Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(got, "memory token was refused") {
			t.Errorf("answer = %q", got)
		}
	})
}
