package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"github.com/petervlad/loadout/internal/api"
)

// deletionID matches the ids the registry hands out for requests to delete
// a package; checking it keeps an id such as "../packages" from turning
// into another path.
var deletionID = regexp.MustCompile(`^del-[a-z0-9]{1,60}$`)

func checkDeletionID(id string) error {
	if !deletionID.MatchString(id) {
		return fmt.Errorf("invalid deletion request id %q", id)
	}
	return nil
}

// AskDelete asks to delete a package. Nothing is deleted until a maintainer
// or owner of its namespace who is not the caller approves.
func (c *Client) AskDelete(ctx context.Context, req api.DeletionRequest) (api.Deletion, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return api.Deletion{}, fmt.Errorf("ask to delete %s: %w", req.Package, err)
	}
	var out api.DeletionResponse
	if err := c.post(ctx, c.url(nil, "v1", "deletions"), body, jsonType, http.StatusCreated, &out); err != nil {
		return api.Deletion{}, err
	}
	return out.Deletion, nil
}

// Deletions lists requests to delete packages of the token's team (or of
// team, when set), newest first. status is api.StatusOpen, another status,
// or api.StatusAny.
func (c *Client) Deletions(ctx context.Context, team, status string) ([]api.Deletion, error) {
	q := url.Values{}
	if team != "" {
		q.Set("team", team)
	}
	if status != "" {
		q.Set("status", status)
	}
	var out api.DeletionsResponse
	if err := c.getJSON(ctx, c.url(q, "v1", "deletions"), &out); err != nil {
		return nil, err
	}
	return out.Deletions, nil
}

// Deletion returns one request, with where it stands against its package
// now when it is open.
func (c *Client) Deletion(ctx context.Context, id string) (api.Deletion, error) {
	if err := checkDeletionID(id); err != nil {
		return api.Deletion{}, err
	}
	var out api.DeletionResponse
	if err := c.getJSON(ctx, c.url(nil, "v1", "deletions", id), &out); err != nil {
		return api.Deletion{}, err
	}
	return out.Deletion, nil
}

// ApproveDelete deletes the package of an open request. req.AsOwner
// approves the caller's own request as an owner of its namespace; req.Over
// does not apply and is ignored.
func (c *Client) ApproveDelete(ctx context.Context, id string, req api.DecisionRequest) (api.Deletion, error) {
	return c.decideDeletion(ctx, id, "approve", req)
}

// RejectDelete closes a request without deleting.
func (c *Client) RejectDelete(ctx context.Context, id, note string) (api.Deletion, error) {
	return c.decideDeletion(ctx, id, "reject", api.DecisionRequest{Note: note})
}

// WithdrawDelete closes the caller's own request.
func (c *Client) WithdrawDelete(ctx context.Context, id, note string) (api.Deletion, error) {
	return c.decideDeletion(ctx, id, "withdraw", api.DecisionRequest{Note: note})
}

func (c *Client) decideDeletion(ctx context.Context, id, verb string, req api.DecisionRequest) (api.Deletion, error) {
	if err := checkDeletionID(id); err != nil {
		return api.Deletion{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return api.Deletion{}, fmt.Errorf("%s %s: %w", verb, id, err)
	}
	var out api.DeletionResponse
	if err := c.post(ctx, c.url(nil, "v1", "deletions", id, verb), body, jsonType, http.StatusOK, &out); err != nil {
		return api.Deletion{}, err
	}
	return out.Deletion, nil
}
