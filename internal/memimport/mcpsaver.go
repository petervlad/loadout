package memimport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petervlad/loadout/internal/client"
)

// MCPSaver saves through the `save` tool of a team's MCP endpoint with the
// person's memory token, so an imported entry gets the same checks as one an
// agent saves. It is the one place in this package that uses the network.
type MCPSaver struct {
	session *mcp.ClientSession
}

// NewMCPSaver connects to registry's endpoint for team. caFile, if not empty,
// adds PEM certificates to the roots. Close it when done.
func NewMCPSaver(ctx context.Context, registry, team, token, caFile string) (*MCPSaver, error) {
	if token == "" {
		return nil, errors.New("saving to team memory needs a memory token")
	}
	if err := client.CheckMemoryURL(registry); err != nil {
		return nil, err
	}
	hc, err := client.HTTPClient(caFile)
	if err != nil {
		return nil, err
	}
	hc.Transport = bearer{token: token, next: hc.Transport}
	endpoint := strings.TrimRight(registry, "/") + "/mcp/t/" + url.PathEscape(team)
	c := mcp.NewClient(&mcp.Implementation{Name: "loadout-memory-import", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to team memory of %s: %w. Check the team name, your memory token and your connection", team, err)
	}
	return &MCPSaver{session: cs}, nil
}

// Close ends the session.
func (s *MCPSaver) Close() error { return s.session.Close() }

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// Save implements Saver.
func (s *MCPSaver) Save(ctx context.Context, e Entry, allowDup bool) (Result, error) {
	res, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: "save", Arguments: map[string]any{
		"type": string(e.Kind), "name": e.Name, "description": e.Description, "body": e.Body, "new": allowDup,
	}})
	if err != nil {
		return Result{}, fmt.Errorf("save: %w", err)
	}
	if res.IsError {
		return Result{}, errors.New(firstText(res))
	}
	out, _ := res.StructuredContent.(map[string]any)
	if saved, ok := out["saved"].(map[string]any); ok {
		id, _ := saved["id"].(float64)
		return Result{ID: int64(id)}, nil
	}
	if dups, ok := out["duplicates"].([]any); ok && len(dups) > 0 {
		return Result{Duplicate: true, Detail: describe(dups)}, nil
	}
	return Result{}, errors.New("team memory answered with something this loadout does not understand. Update loadout and try again")
}

func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return "team memory refused the entry"
}

// describe names the likely duplicates as "[id] name".
func describe(dups []any) string {
	var parts []string
	for _, d := range dups {
		m, _ := d.(map[string]any)
		id, _ := m["id"].(float64)
		name, _ := m["name"].(string)
		parts = append(parts, fmt.Sprintf("[%d] %s", int64(id), name))
	}
	return strings.Join(parts, ", ")
}
