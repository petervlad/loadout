package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/petervlad/loadout/internal/api"
)

// StartDevice starts a `loadout login` for namespace (empty: chosen in the
// browser). It needs no token.
func (c *Client) StartDevice(ctx context.Context, namespace string) (api.DeviceStartResponse, error) {
	var out api.DeviceStartResponse
	err := c.sendJSON(ctx, http.MethodPost, http.StatusOK, api.DeviceStartRequest{Namespace: namespace}, &out, "v1", "auth", "device")
	return out, err
}

// PollDevice asks whether the sign-in was approved. It needs no token; the
// device code is the secret.
func (c *Client) PollDevice(ctx context.Context, deviceCode string) (api.DeviceTokenResponse, error) {
	var out api.DeviceTokenResponse
	err := c.sendJSON(ctx, http.MethodPost, http.StatusOK, api.DeviceTokenRequest{DeviceCode: deviceCode}, &out, "v1", "auth", "device", "token")
	return out, err
}

// RevokeToken revokes the client's own token, which must come from
// `loadout login`.
func (c *Client) RevokeToken(ctx context.Context) error {
	return c.sendJSON(ctx, http.MethodDelete, http.StatusNoContent, nil, nil, "v1", "auth", "token")
}

// sendJSON sends in (if not nil) as JSON and decodes the answer into out
// (if not nil), which must come with status want.
func (c *Client) sendJSON(ctx context.Context, method string, want int, in, out any, segments ...string) error {
	u := c.url(nil, segments...)
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("%s %s: %w", method, u.Path, err)
		}
	}
	return c.exchange(ctx, method, u, want, body, out)
}

// exchange sends body (if not nil) as JSON to u and decodes the answer
// into out (if not nil), which must come with status want.
func (c *Client) exchange(ctx context.Context, method string, u *url.URL, want int, body []byte, out any) error {
	contentType := ""
	if body != nil {
		contentType = jsonType
	}
	resp, err := c.do(ctx, method, u, body, contentType, jsonType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, want); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := decodeJSON(resp.Body, out); err != nil {
		return fmt.Errorf("%s %s: %w", method, u.Path, err)
	}
	return nil
}

// SignOutEverywhere ends every web session and every `loadout login` token
// of the user the client's token belongs to, and returns how many tokens
// were revoked.
func (c *Client) SignOutEverywhere(ctx context.Context) (int, error) {
	var out api.RevokedResponse
	err := c.sendJSON(ctx, http.MethodDelete, http.StatusOK, nil, &out, "v1", "auth", "tokens")
	return out.Revoked, err
}

// Me returns who the client's token belongs to and every namespace they can
// open. It needs a token from `loadout login`.
func (c *Client) Me(ctx context.Context) (api.MeResponse, error) {
	var out api.MeResponse
	err := c.sendJSON(ctx, http.MethodGet, http.StatusOK, nil, &out, "v1", "me")
	return out, err
}
