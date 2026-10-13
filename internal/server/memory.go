package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// Team memory routes. The token routes are for a person signed in with
// `loadout login` (a machine token acts for nobody, and a login token for
// one team could not hand out a token that works in all of them, so only an
// account token qualifies). A memory token itself works on /mcp/t/{ns} and
// nowhere else: registry.Service.Authenticate refuses every secret that does
// not start with "ldt_", so each route above answers it 401.
//
//	POST   /v1/memory/tokens        MemoryTokenRequest in, MemoryToken out (the secret, once)
//	GET    /v1/memory/tokens        MemoryTokensResponse
//	DELETE /v1/memory/tokens/{id}   revoke one of the caller's own
//	GET    /v1/memory/spaces        MemorySpacesResponse (namespaces with memory on)
//	*      /mcp/t/{ns}              the handler given to WithMemory, if any
//
// PATCH /v1/namespaces/{ns} also takes "memory" (manage.go).

// MemoryTokens is what the token routes need from memory.Tokens.
type MemoryTokens interface {
	Create(ctx context.Context, userID, label string) (string, memory.Token, error)
	List(ctx context.Context, userID string) ([]memory.Token, error)
	Revoke(ctx context.Context, userID, id string) error
}

// MemorySwitches is what the spaces route and the namespace setting need
// from memory.Service.
type MemorySwitches interface {
	Spaces(ctx context.Context, c memory.Caller) ([]memory.Place, error)
	Enabled(ctx context.Context, ns string) (bool, error)
	SetEnabled(ctx context.Context, c memory.Caller, ns string, on bool) error
}

var (
	_ MemoryTokens   = (*memory.Tokens)(nil)
	_ MemorySwitches = (*memory.Service)(nil)
)

// Memory is the team memory parts of the registry. PublicURL is the address
// people reach the registry at, which the endpoints in /v1/memory/spaces
// start with.
type Memory struct {
	Tokens    MemoryTokens
	Switches  MemorySwitches
	PublicURL string
}

// mcpPath is where a namespace's MCP server lives.
const mcpPath = "/mcp/t/"

// WithMemory serves the memory token and space routes, and the namespace
// setting "memory". mcp, if not nil, is mounted at /mcp/t/{ns} and reads the
// namespace with r.PathValue("ns"). It does its own authentication: the
// bearer there is a memory token, which the other routes refuse. Without
// WithMemory, none of this exists.
func WithMemory(m Memory, mcp http.Handler) Option {
	return func(s *server) {
		s.memory = m
		s.mcp = mcp
	}
}

func (s *server) hasMemory() bool { return s.memory.Tokens != nil && s.memory.Switches != nil }

func (s *server) routeMemory(mux *http.ServeMux) {
	mux.Handle("POST /v1/memory/tokens", s.asAccount(s.createMemoryToken))
	mux.Handle("GET /v1/memory/tokens", s.asAccount(s.memoryTokens))
	mux.Handle("DELETE /v1/memory/tokens/{id}", s.asAccount(s.revokeMemoryToken))
	mux.Handle("GET /v1/memory/spaces", s.asAccount(s.memorySpaces))
	if s.mcp != nil {
		mux.Handle(mcpPath+"{ns}", s.mcp)
	}
}

// asAccount wraps a handler that acts for a person through their account
// token from `loadout login`. A machine token acts for nobody, and a token
// limited to one team must not make one that reaches every team.
func (s *server) asAccount(h func(http.ResponseWriter, *http.Request, memory.Caller)) http.Handler {
	return s.authed(func(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
		c, err := accountCaller(pr)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		h(w, r, c)
	})
}

func accountCaller(pr registry.Principal) (memory.Caller, error) {
	if pr.UserID == "" {
		return memory.Caller{}, fmt.Errorf("%w: team memory needs a token from loadout login, not a machine token", registry.ErrForbidden)
	}
	if !pr.Account {
		return memory.Caller{}, fmt.Errorf("%w: this token is limited to one team, so it cannot manage team memory; run loadout login without a team", registry.ErrForbidden)
	}
	return memory.Caller{UserID: pr.UserID}, nil
}

func (s *server) createMemoryToken(w http.ResponseWriter, r *http.Request, c memory.Caller) {
	var req api.MemoryTokenRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	secret, tok, err := s.memory.Tokens.Create(r.Context(), c.UserID, req.Label)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := toAPIMemoryToken(tok)
	out.Token = secret
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, http.StatusCreated, out)
}

func (s *server) memoryTokens(w http.ResponseWriter, r *http.Request, c memory.Caller) {
	toks, err := s.memory.Tokens.List(r.Context(), c.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := api.MemoryTokensResponse{Tokens: make([]api.MemoryToken, 0, len(toks))}
	for _, t := range toks {
		out.Tokens = append(out.Tokens, toAPIMemoryToken(t))
	}
	s.writeJSON(w, r, http.StatusOK, out)
}

func (s *server) revokeMemoryToken(w http.ResponseWriter, r *http.Request, c memory.Caller) {
	if err := s.memory.Tokens.Revoke(r.Context(), c.UserID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) memorySpaces(w http.ResponseWriter, r *http.Request, c memory.Caller) {
	places, err := s.memory.Switches.Spaces(r.Context(), c)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	base := strings.TrimRight(s.memory.PublicURL, "/")
	out := api.MemorySpacesResponse{Spaces: make([]api.MemorySpace, 0, len(places))}
	for _, p := range places {
		out.Spaces = append(out.Spaces, api.MemorySpace{Namespace: p.Namespace, Kind: api.LayerKind(p.Kind), Endpoint: base + mcpPath + p.Namespace})
	}
	s.writeJSON(w, r, http.StatusOK, out)
}

// setMemory turns a namespace's team memory on or off for the user behind
// the request. The memory service checks the role.
func (s *server) setMemory(r *http.Request, ns string, on bool) error {
	if !s.hasMemory() {
		return fmt.Errorf("%w: this registry has no team memory", registry.ErrInvalid)
	}
	pr, _ := principalFrom(r.Context())
	c, err := accountCaller(pr)
	if err != nil {
		return err
	}
	return s.memory.Switches.SetEnabled(r.Context(), c, ns, on)
}

func toAPIMemoryToken(t memory.Token) api.MemoryToken {
	return api.MemoryToken{ID: t.ID, Label: t.Label, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt}
}
