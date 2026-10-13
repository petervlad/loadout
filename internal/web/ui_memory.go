package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// Team memory on the web: the Team memory switch on Settings, the memory
// tokens on the Account page, and the Memory tab (ui_memory_tab.go).
//
//	POST /t/{ns}/settings/memory      turn team memory on or off
//	POST /account/memory-tokens       make a token, shown once on the page that answers
//	POST /account/memory-tokens/revoke

// Memory is what the pages need from the team memory service.
// memory.Service is one.
type Memory interface {
	Enabled(ctx context.Context, ns string) (bool, error)
	SetEnabled(ctx context.Context, c memory.Caller, ns string, on bool) error
	// The Memory tab reads and moderates. It never saves or edits.
	Now() time.Time
	Layers(ctx context.Context, c memory.Caller, ns string) ([]string, error)
	Search(ctx context.Context, c memory.Caller, ns string, q memory.Query) ([]memory.Entry, error)
	Recent(ctx context.Context, c memory.Caller, ns string, since time.Time, limit int) ([]memory.Entry, error)
	History(ctx context.Context, c memory.Caller, ns string, id int64) (memory.Record, error)
	Linked(ctx context.Context, c memory.Caller, ns string, ids []int64) ([]memory.Entry, error)
	Retract(ctx context.Context, c memory.Caller, id int64, reason string) (memory.Entry, error)
	Move(ctx context.Context, c memory.Caller, id int64) (memory.Entry, error)
}

// MemoryTokens is what the Account page needs from memory.Tokens.
type MemoryTokens interface {
	Create(ctx context.Context, userID, label string) (string, memory.Token, error)
	List(ctx context.Context, userID string) ([]memory.Token, error)
	Revoke(ctx context.Context, userID, id string) error
}

var (
	_ Memory       = (*memory.Service)(nil)
	_ MemoryTokens = (*memory.Tokens)(nil)
)

func (h *handler) routeMemory(mux *http.ServeMux) {
	if h.ui.Memory != nil {
		mux.HandleFunc("POST /t/{ns}/settings/memory", h.inTeam(h.saveMemorySwitch))
		h.routeMemoryTab(mux)
	}
	if h.ui.MemoryTokens != nil {
		mux.HandleFunc("POST /account/memory-tokens", h.createMemoryToken)
		mux.HandleFunc("POST /account/memory-tokens/revoke", h.revokeMemoryToken)
	}
}

// saveMemorySwitch turns the namespace's team memory on or off. The memory
// service checks the role again, so a hand-made POST by a member is refused.
func (h *handler) saveMemorySwitch(w http.ResponseWriter, r *http.Request, t team) {
	on := r.PostForm.Get("memory") == "on"
	if err := h.ui.Memory.SetEnabled(r.Context(), callerOf(t.user), t.ns(), on); err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	done := "memory-off"
	if on {
		done = "memory-on"
	}
	back(w, r, "/t/"+t.ns()+"/settings", done, nil)
}

// callerOf is the person behind a web session, as team memory knows them.
func callerOf(u account.User) memory.Caller { return memory.Caller{UserID: u.ID, Label: u.Email} }

// memoryTokenRow is one token on the Account page.
type memoryTokenRow struct {
	ID        string
	Label     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// newMemoryToken is a token just made, shown once.
type newMemoryToken struct {
	Secret string
	Label  string
}

// memoryTokensView is the Memory tokens section of the Account page.
type memoryTokensView struct {
	Tokens []memoryTokenRow
	// New is set on the page that answers a create.
	New *newMemoryToken
}

func (h *handler) memoryTokensView(r *http.Request, userID string, created *newMemoryToken) (*memoryTokensView, error) {
	if h.ui.MemoryTokens == nil {
		return nil, nil
	}
	toks, err := h.ui.MemoryTokens.List(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	v := &memoryTokensView{New: created}
	for _, t := range toks {
		v.Tokens = append(v.Tokens, memoryTokenRow{ID: t.ID, Label: t.Label, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt})
	}
	return v, nil
}

// createMemoryToken answers with the Account page showing the secret
// instead of redirecting, so the secret is never in a URL or the history.
func (h *handler) createMemoryToken(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	u, ok := h.user(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {"/account"}}.Encode(), http.StatusSeeOther)
		return
	}
	label := r.PostForm.Get("label")
	secret, tok, err := h.ui.MemoryTokens.Create(r.Context(), u.ID, label)
	if errors.Is(err, registry.ErrInvalid) {
		h.renderAccount(w, r, u, http.StatusBadRequest, "", sentence(err), nil)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.renderAccount(w, r, u, http.StatusOK, "", "", &newMemoryToken{Secret: secret, Label: tok.Label})
}

func (h *handler) revokeMemoryToken(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	u, ok := h.user(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {"/account"}}.Encode(), http.StatusSeeOther)
		return
	}
	err := h.ui.MemoryTokens.Revoke(r.Context(), u.ID, r.PostForm.Get("id"))
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	// A token that is already gone reads the same as one just revoked.
	http.Redirect(w, r, "/account?done=memory-revoked", http.StatusSeeOther)
}
