package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/petervlad/loadout/internal/api"
)

// These calls read and save a namespace's wiki page as the user behind a
// `loadout login` token. A registry older than the wiki answers 404 (an
// *APIError matching api.ErrNotFound), as does one whose wiki is off.

// Wiki returns revision rev of namespace's page, the newest for rev 0.
// An empty page is revision 0 with no text.
func (c *Client) Wiki(ctx context.Context, namespace string, rev int) (api.WikiPage, error) {
	var q url.Values
	if rev > 0 {
		q = url.Values{"revision": {strconv.Itoa(rev)}}
	}
	var out api.WikiPage
	err := c.exchange(ctx, http.MethodGet, c.url(q, "v1", "namespaces", namespace, "wiki"), http.StatusOK, nil, &out)
	return out, err
}

// WikiRevisions lists the kept revisions of namespace's page, newest
// first.
func (c *Client) WikiRevisions(ctx context.Context, namespace string) ([]api.WikiRevision, error) {
	var out api.WikiRevisions
	err := c.sendJSON(ctx, http.MethodGet, http.StatusOK, nil, &out, "v1", "namespaces", namespace, "wiki", "revisions")
	return out.Revisions, err
}

// SaveWiki saves in as the revision after in.Base. The page's Saved is
// false when the text equalled the base. A save on an old base is an
// *APIError matching api.ErrStale whose Newest names the newest revision.
func (c *Client) SaveWiki(ctx context.Context, namespace string, in api.WikiSave) (api.WikiPage, error) {
	u := c.url(nil, "v1", "namespaces", namespace, "wiki")
	// Unescaped <, > and &, so a page full of them stays within the
	// registry's body limit.
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(in); err != nil {
		return api.WikiPage{}, fmt.Errorf("%s %s: %w", http.MethodPut, u.Path, err)
	}
	var out api.WikiPage
	err := c.exchange(ctx, http.MethodPut, u, http.StatusOK, body.Bytes(), &out)
	return out, err
}
