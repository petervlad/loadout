package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/petervlad/loadout/internal/api"
)

// These calls manage a registry as the user behind a `loadout login` token;
// the registry checks their role.

// CreateNamespace creates a namespace under parent (empty: a root, for
// admins). kind is "team" or "project".
func (c *Client) CreateNamespace(ctx context.Context, name, parent, kind string) (api.Namespace, error) {
	var out api.Namespace
	err := c.sendJSON(ctx, http.MethodPost, http.StatusCreated, api.NamespaceRequest{Name: name, Parent: parent, Kind: api.LayerKind(kind)}, &out, "v1", "namespaces")
	return out, err
}

// DeleteNamespace deletes an empty namespace (admins only).
func (c *Client) DeleteNamespace(ctx context.Context, name string) error {
	return c.sendJSON(ctx, http.MethodDelete, http.StatusNoContent, nil, nil, "v1", "namespaces", name)
}

// SetRequireReview turns review-only publishing on or off.
func (c *Client) SetRequireReview(ctx context.Context, name string, on bool) (api.Namespace, error) {
	var out api.Namespace
	err := c.sendJSON(ctx, http.MethodPatch, http.StatusOK, api.NamespaceSettings{RequireReview: &on}, &out, "v1", "namespaces", name)
	return out, err
}

// SetWiki turns the namespace's wiki on or off.
func (c *Client) SetWiki(ctx context.Context, name string, on bool) (api.Namespace, error) {
	var out api.Namespace
	err := c.sendJSON(ctx, http.MethodPatch, http.StatusOK, api.NamespaceSettings{Wiki: &on}, &out, "v1", "namespaces", name)
	return out, err
}

// SetCourse sets the knowledge base the namespace's wiki shows as its
// course, as "team/name"; "" clears it. A registry older than courses
// refuses the field by name, which becomes a sentence saying so.
func (c *Client) SetCourse(ctx context.Context, name, course string) (api.Namespace, error) {
	var out api.Namespace
	err := c.sendJSON(ctx, http.MethodPatch, http.StatusOK, api.NamespaceSettings{Course: &course}, &out, "v1", "namespaces", name)
	if refusedField(err, "course") {
		return api.Namespace{}, fmt.Errorf("%s does not know courses yet; it needs a newer loadout on the registry", c.base)
	}
	return out, err
}

// Tiers returns the resolved model table of the client's view: what
// installs through it render with. An older registry answers 404.
func (c *Client) Tiers(ctx context.Context) (api.TiersResponse, error) {
	var out api.TiersResponse
	err := c.sendJSON(ctx, http.MethodGet, http.StatusOK, nil, &out, "v1", "tiers")
	return out, err
}

// NamespaceTiers returns the resolved model table of namespace. A token
// that is not an account token answers for its own namespace only.
func (c *Client) NamespaceTiers(ctx context.Context, namespace string) (api.TiersResponse, error) {
	out, err := c.In(namespace).Tiers(ctx)
	if err != nil {
		return api.TiersResponse{}, err
	}
	if out.Namespace != namespace {
		return api.TiersResponse{}, fmt.Errorf("the token for this registry reads %s, not %s; sign in with loadout login to see other namespaces", out.Namespace, namespace)
	}
	return out, nil
}

// SetTiers replaces namespace's own model table with cells.
func (c *Client) SetTiers(ctx context.Context, namespace string, cells []api.TierCell) (api.TiersResponse, error) {
	var out api.TiersResponse
	err := c.sendJSON(ctx, http.MethodPut, http.StatusOK, api.TiersRequest{Cells: cells}, &out, "v1", "namespaces", namespace, "tiers")
	return out, err
}

// Members lists a namespace's members.
func (c *Client) Members(ctx context.Context, namespace string) ([]api.Member, error) {
	var out api.MembersResponse
	err := c.sendJSON(ctx, http.MethodGet, http.StatusOK, nil, &out, "v1", "namespaces", namespace, "members")
	return out.Members, err
}

// AddMember gives email a role in namespace, or changes it.
func (c *Client) AddMember(ctx context.Context, namespace, email, role string) (api.MemberResponse, error) {
	var out api.MemberResponse
	err := c.sendJSON(ctx, http.MethodPost, http.StatusOK, api.MemberRequest{Email: email, Role: role}, &out, "v1", "namespaces", namespace, "members")
	return out, err
}

// SetMember changes the role of an existing member.
func (c *Client) SetMember(ctx context.Context, namespace, email, role string) (api.MemberResponse, error) {
	var out api.MemberResponse
	err := c.sendJSON(ctx, http.MethodPatch, http.StatusOK, api.MemberRequest{Role: role}, &out, "v1", "namespaces", namespace, "members", email)
	return out, err
}

// Invite gives email a role in project and roles in some of its teams, all
// or nothing.
func (c *Client) Invite(ctx context.Context, project string, req api.InviteRequest) (api.InviteResponse, error) {
	var out api.InviteResponse
	err := c.sendJSON(ctx, http.MethodPost, http.StatusOK, req, &out, "v1", "namespaces", project, "invite")
	return out, err
}

// RemoveMember removes a member and returns how many of their tokens were
// revoked.
func (c *Client) RemoveMember(ctx context.Context, namespace, email string) (int, error) {
	var out api.RevokedResponse
	err := c.sendJSON(ctx, http.MethodDelete, http.StatusOK, nil, &out, "v1", "namespaces", namespace, "members", email)
	return out.Revoked, err
}

// MachineTokens lists a namespace's machine tokens, without secrets.
func (c *Client) MachineTokens(ctx context.Context, namespace string) ([]api.Token, error) {
	var out api.TokensResponse
	err := c.sendJSON(ctx, http.MethodGet, http.StatusOK, nil, &out, "v1", "namespaces", namespace, "tokens")
	return out.Tokens, err
}

// CreateMachineToken creates a machine token; its secret is in the answer
// once.
func (c *Client) CreateMachineToken(ctx context.Context, namespace, scope, label string) (api.TokenCreated, error) {
	var out api.TokenCreated
	err := c.sendJSON(ctx, http.MethodPost, http.StatusCreated, api.TokenRequest{Scope: scope, Label: label}, &out, "v1", "namespaces", namespace, "tokens")
	return out, err
}

// RevokeMachineToken revokes one machine token.
func (c *Client) RevokeMachineToken(ctx context.Context, namespace, id string) error {
	return c.sendJSON(ctx, http.MethodDelete, http.StatusNoContent, nil, nil, "v1", "namespaces", namespace, "tokens", id)
}
