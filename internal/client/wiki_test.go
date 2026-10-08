package client_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

// newWikiClient serves the wiki of lime (on) and returns a client with
// the account token of alice, a member of lime, and the last PUT body
// the server received.
func newWikiClient(t *testing.T) (*client.Client, *[]byte) {
	t.Helper()
	ctx, log := t.Context(), slog.New(slog.DiscardHandler)
	now := func() time.Time { return publishedAt }
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, now, log, registry.WithMemberships(roles))
	accounts := account.NewService(store, reg, roles, device.New(now), account.Options{Wiki: registry.NewWikiService(regMem, regMem, now, log), Now: now, Log: log})
	if _, err := reg.CreateNamespace(ctx, "lime", "", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetWiki(ctx, account.Operator(), "lime", true); err != nil {
		t.Fatal(err)
	}
	m, _, err := accounts.AddMember(ctx, account.Operator(), "lime", "alice@example.com", account.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := reg.CreateAccountToken(ctx, "alice", m.UserID)
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(reg, log, "v-test", server.WithWiki(accounts))
	var last []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			last, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(last))
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return newClient(t, ts.URL, secret), &last
}

func TestWikiRoundTrip(t *testing.T) {
	c, last := newWikiClient(t)
	ctx := t.Context()

	page, err := c.Wiki(ctx, "lime", 0)
	if err != nil || page.Revision != 0 || page.Text != "" {
		t.Fatalf("Wiki(empty) = %+v, %v; want revision 0", page, err)
	}
	text := "# Lime\n\n<b>a & b</b>\n"
	page, err = c.SaveWiki(ctx, "lime", api.WikiSave{Base: 0, Format: "md", Text: text, Note: "start"})
	if err != nil || page.Revision != 1 || !page.Saved || page.Text != text || page.Author != "alice@example.com" {
		t.Fatalf("SaveWiki() = %+v, %v; want revision 1", page, err)
	}
	if !bytes.Contains(*last, []byte("<b>a & b</b>")) {
		t.Errorf("PUT body = %s, want <, > and & unescaped", *last)
	}

	_, err = c.SaveWiki(ctx, "lime", api.WikiSave{Base: 0, Format: "md", Text: "other"})
	apiErr, ok := errors.AsType[*client.APIError](err)
	if !errors.Is(err, api.ErrStale) || !ok || apiErr.Newest == nil || apiErr.Newest.Revision != 1 || apiErr.Newest.Author != "alice@example.com" {
		t.Fatalf("SaveWiki(stale) error = %v; want api.ErrStale naming revision 1", err)
	}

	if page, err := c.Wiki(ctx, "lime", 1); err != nil || page.Text != text || page.Note != "start" {
		t.Errorf("Wiki(1) = %+v, %v", page, err)
	}
	revs, err := c.WikiRevisions(ctx, "lime")
	if err != nil || len(revs) != 1 || revs[0].Revision != 1 || !revs[0].CreatedAt.Equal(publishedAt) {
		t.Errorf("WikiRevisions() = %+v, %v; want revision 1", revs, err)
	}
	if _, err := c.Wiki(ctx, "lime", 7); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("Wiki(7) error = %v, want api.ErrNotFound", err)
	}
}
