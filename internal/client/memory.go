package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/petervlad/loadout/internal/api"
)

// These calls manage the caller's memory tokens and find the namespaces whose
// team memory they can use. They need a `loadout login` token. A registry
// older than team memory answers 404 (an *APIError matching api.ErrNotFound).

// CreateMemoryToken makes a memory token. The secret is in the answer's Token
// and cannot be read again.
func (c *Client) CreateMemoryToken(ctx context.Context, label string) (api.MemoryToken, error) {
	var out api.MemoryToken
	err := c.sendJSON(ctx, http.MethodPost, http.StatusCreated, api.MemoryTokenRequest{Label: label}, &out, "v1", "memory", "tokens")
	return out, err
}

// MemoryTokens lists the caller's memory tokens, newest first, without
// secrets.
func (c *Client) MemoryTokens(ctx context.Context) ([]api.MemoryToken, error) {
	var out api.MemoryTokensResponse
	err := c.sendJSON(ctx, http.MethodGet, http.StatusOK, nil, &out, "v1", "memory", "tokens")
	return out.Tokens, err
}

// RevokeMemoryToken removes one of the caller's own memory tokens.
func (c *Client) RevokeMemoryToken(ctx context.Context, id string) error {
	return c.sendJSON(ctx, http.MethodDelete, http.StatusNoContent, nil, nil, "v1", "memory", "tokens", id)
}

// MemorySpaces lists the namespaces where the caller has memory rights and
// team memory is on, with their MCP endpoints.
func (c *Client) MemorySpaces(ctx context.Context) ([]api.MemorySpace, error) {
	var out api.MemorySpacesResponse
	err := c.sendJSON(ctx, http.MethodGet, http.StatusOK, nil, &out, "v1", "memory", "spaces")
	return out.Spaces, err
}

// SetMemory turns the namespace's team memory on or off. A registry older
// than team memory refuses the field by name, which becomes a sentence saying
// so.
func (c *Client) SetMemory(ctx context.Context, name string, on bool) (api.Namespace, error) {
	var out api.Namespace
	err := c.sendJSON(ctx, http.MethodPatch, http.StatusOK, api.NamespaceSettings{Memory: &on}, &out, "v1", "namespaces", name)
	if refusedField(err, "memory") {
		return api.Namespace{}, fmt.Errorf("%s does not know team memory yet; it needs a newer loadout on the registry", c.base)
	}
	return out, err
}
