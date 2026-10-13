package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/tier"
)

// Management routes, for users signed in with `loadout login` (their token
// names the user; the account service checks their role). A login token
// manages only its own namespace and those below it, only with publish
// scope, and never with admin rights: admin actions (root namespaces,
// owners, deleting namespaces) need the web or the registry host.
//
//	POST   /v1/namespaces                         NamespaceRequest in, Namespace out
//	PATCH  /v1/namespaces/{ns}                    NamespaceSettings in, Namespace out (course, require_review, wiki, memory)
//	PUT    /v1/namespaces/{ns}/tiers              TiersRequest in, TiersResponse out
//	DELETE /v1/namespaces/{ns}                    admins only
//	GET    /v1/namespaces/{ns}/members            MembersResponse
//	POST   /v1/namespaces/{ns}/members            MemberRequest in, MemberResponse out
//	PATCH  /v1/namespaces/{ns}/members/{email}    MemberRequest in, MemberResponse out
//	DELETE /v1/namespaces/{ns}/members/{email}    RevokedResponse
//	POST   /v1/namespaces/{project}/invite        InviteRequest in, InviteResponse out
//	GET    /v1/namespaces/{ns}/tokens             TokensResponse (machine tokens)
//	POST   /v1/namespaces/{ns}/tokens             TokenRequest in, TokenCreated out
//	DELETE /v1/namespaces/{ns}/tokens/{id}
//	GET    /v1/namespaces/{ns}/wiki               WikiPage (wiki.go, with WithWiki)
//	PUT    /v1/namespaces/{ns}/wiki               WikiSave in, WikiPage out
//	GET    /v1/namespaces/{ns}/wiki/revisions     WikiRevisions

// Manage is what the management routes need from the account service.
type Manage interface {
	CreateNamespace(ctx context.Context, actor account.Actor, name, parent string, kind registry.NamespaceKind) (registry.Namespace, error)
	DeleteNamespace(ctx context.Context, actor account.Actor, name string) error
	SetRequireReview(ctx context.Context, actor account.Actor, name string, on bool) (registry.Namespace, error)
	SetWiki(ctx context.Context, actor account.Actor, name string, on bool) (registry.Namespace, error)
	SetCourse(ctx context.Context, actor account.Actor, namespace string, course pack.ID) (registry.Namespace, error)
	CourseAbove(ctx context.Context, actor account.Actor, namespace string) (registry.Course, bool, error)
	SetModelTiers(ctx context.Context, actor account.Actor, namespace string, t tier.Table) (tier.Stack, error)
	Members(ctx context.Context, actor account.Actor, namespace string) ([]account.Membership, error)
	AddMember(ctx context.Context, actor account.Actor, namespace, email string, role account.Role) (account.Membership, bool, error)
	SetMember(ctx context.Context, actor account.Actor, namespace, email string, role account.Role) (account.Membership, error)
	RemoveMember(ctx context.Context, actor account.Actor, namespace, email string) (int, error)
	InviteToProject(ctx context.Context, actor account.Actor, project, email string, projectRole account.Role, teams map[string]account.Role) (account.Invitation, error)
	CreateMachineToken(ctx context.Context, actor account.Actor, namespace string, scope registry.TokenScope, label string) (string, registry.Token, error)
	MachineTokens(ctx context.Context, actor account.Actor, namespace string) ([]registry.Token, error)
	RevokeMachineToken(ctx context.Context, actor account.Actor, namespace, id string) error
}

var _ Manage = (*account.Service)(nil)

// WithManagement serves the management routes.
func WithManagement(m Manage) Option { return func(s *server) { s.manage = m } }

func (s *server) routeManage(mux *http.ServeMux) {
	mux.Handle("POST /v1/namespaces", s.asUser(s.createNamespace))
	mux.Handle("PATCH /v1/namespaces/{ns}", s.asUser(s.updateNamespace))
	mux.Handle("DELETE /v1/namespaces/{ns}", s.asUser(s.deleteNamespace))
	mux.Handle("PUT /v1/namespaces/{ns}/tiers", s.asUser(s.setTiers))
	mux.Handle("GET /v1/namespaces/{ns}/members", s.asUser(s.members))
	mux.Handle("POST /v1/namespaces/{ns}/members", s.asUser(s.addMember))
	mux.Handle("PATCH /v1/namespaces/{ns}/members/{email}", s.asUser(s.setMember))
	mux.Handle("DELETE /v1/namespaces/{ns}/members/{email}", s.asUser(s.removeMember))
	mux.Handle("POST /v1/namespaces/{ns}/invite", s.asUser(s.invite))
	mux.Handle("GET /v1/namespaces/{ns}/tokens", s.asUser(s.tokens))
	mux.Handle("POST /v1/namespaces/{ns}/tokens", s.asUser(s.createToken))
	mux.Handle("DELETE /v1/namespaces/{ns}/tokens/{id}", s.asUser(s.revokeToken))
}

// asUser wraps a handler that acts for the user behind a login token,
// bounded by the token's namespace and scope. A machine token acts for
// nobody, so it is refused.
func (s *server) asUser(h func(http.ResponseWriter, *http.Request, account.Actor)) http.Handler {
	return s.authed(func(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
		if pr.UserID == "" {
			s.writeError(w, r, fmt.Errorf("%w: managing the registry needs a token from loadout login, not a machine token", registry.ErrForbidden))
			return
		}
		if pr.Account {
			h(w, r, account.WholeAccountActor(pr.UserID))
			return
		}
		h(w, r, account.TokenActor(pr.UserID, pr.Team, pr.Scope))
	})
}

func (s *server) createNamespace(w http.ResponseWriter, r *http.Request, a account.Actor) {
	var req api.NamespaceRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	kind := registry.NamespaceKind(req.Kind)
	if kind == "" {
		kind = registry.KindTeam
	}
	ns, err := s.manage.CreateNamespace(r.Context(), a, req.Name, req.Parent, kind)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusCreated, toAPINamespace(ns))
}

func (s *server) updateNamespace(w http.ResponseWriter, r *http.Request, a account.Actor) {
	var req api.NamespaceSettings
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if req.RequireReview == nil && req.Wiki == nil && req.Course == nil && req.Memory == nil {
		s.writeError(w, r, fmt.Errorf("%w: nothing to change", registry.ErrInvalid))
		return
	}
	// Every setting needs the same role, and the course, the only one that
	// can be refused for its value, goes first: a refusal comes before any
	// change, so the settings never half apply for that reason.
	var ns registry.Namespace
	var err error
	if req.Course != nil {
		course, perr := parseCourse(*req.Course)
		if perr != nil {
			s.writeError(w, r, perr)
			return
		}
		if ns, err = s.manage.SetCourse(r.Context(), a, r.PathValue("ns"), course); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	if req.RequireReview != nil {
		if ns, err = s.manage.SetRequireReview(r.Context(), a, r.PathValue("ns"), *req.RequireReview); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	if req.Wiki != nil {
		if ns, err = s.manage.SetWiki(r.Context(), a, r.PathValue("ns"), *req.Wiki); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	// The memory switch needs a lower role than the settings above, so it
	// goes last: a refusal of those comes before any change.
	if req.Memory != nil {
		if err := s.setMemory(r, r.PathValue("ns"), *req.Memory); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	out := toAPINamespace(ns)
	if out.Name == "" {
		// Only the memory switch changed, which does not touch the registry's
		// record of the namespace.
		out.Name = r.PathValue("ns")
	}
	if s.hasMemory() {
		on, err := s.memory.Switches.Enabled(r.Context(), out.Name)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		out.Memory = on
	}
	if req.Course != nil && ns.Course.IsZero() {
		// Cleared: say what the wiki shows instead, if anything.
		above, ok, err := s.manage.CourseAbove(r.Context(), a, ns.Name)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		if ok {
			out.CourseAbove, out.CourseFrom = above.Package.String(), above.From
		}
	}
	s.writeJSON(w, r, http.StatusOK, out)
}

// parseCourse reads a course setting: "team/name", or "" for none.
func parseCourse(raw string) (pack.ID, error) {
	if raw == "" {
		return pack.ID{}, nil
	}
	id, err := pack.ParseID(raw)
	if err != nil {
		return pack.ID{}, fmt.Errorf("%w: course %q: name a knowledge base as team/name, or none", registry.ErrInvalid, raw)
	}
	return id, nil
}

func (s *server) deleteNamespace(w http.ResponseWriter, r *http.Request, a account.Actor) {
	if err := s.manage.DeleteNamespace(r.Context(), a, r.PathValue("ns")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) members(w http.ResponseWriter, r *http.Request, a account.Actor) {
	ms, err := s.manage.Members(r.Context(), a, r.PathValue("ns"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]api.Member, 0, len(ms))
	for _, m := range ms {
		out = append(out, toAPIMember(m))
	}
	s.writeJSON(w, r, http.StatusOK, api.MembersResponse{Members: out})
}

func (s *server) addMember(w http.ResponseWriter, r *http.Request, a account.Actor) {
	var req api.MemberRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	m, created, err := s.manage.AddMember(r.Context(), a, r.PathValue("ns"), req.Email, account.Role(req.Role))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.MemberResponse{Member: toAPIMember(m), Created: created})
}

func (s *server) setMember(w http.ResponseWriter, r *http.Request, a account.Actor) {
	var req api.MemberRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	m, err := s.manage.SetMember(r.Context(), a, r.PathValue("ns"), r.PathValue("email"), account.Role(req.Role))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.MemberResponse{Member: toAPIMember(m)})
}

func (s *server) removeMember(w http.ResponseWriter, r *http.Request, a account.Actor) {
	n, err := s.manage.RemoveMember(r.Context(), a, r.PathValue("ns"), r.PathValue("email"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.RevokedResponse{Revoked: n})
}

func (s *server) invite(w http.ResponseWriter, r *http.Request, a account.Actor) {
	var req api.InviteRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	teams := make(map[string]account.Role, len(req.Teams))
	for name, role := range req.Teams {
		teams[name] = account.Role(role)
	}
	inv, err := s.manage.InviteToProject(r.Context(), a, r.PathValue("ns"), req.Email, account.Role(req.Role), teams)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, toAPIInvitation(inv))
}

// toAPIInvitation is an invitation as the API sends it.
func toAPIInvitation(inv account.Invitation) api.InviteResponse {
	out := api.InviteResponse{Email: inv.Email, Project: inv.Project, Link: inv.Link, Mailed: inv.Mailed, Grants: []api.Grant{}}
	for _, g := range inv.Grants {
		out.Grants = append(out.Grants, api.Grant{Namespace: g.Namespace, Asked: string(g.Asked), Role: string(g.Role), Was: string(g.Was), Result: string(g.Result)})
	}
	return out
}

func (s *server) tokens(w http.ResponseWriter, r *http.Request, a account.Actor) {
	toks, err := s.manage.MachineTokens(r.Context(), a, r.PathValue("ns"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]api.Token, 0, len(toks))
	for _, t := range toks {
		out = append(out, toAPIToken(t))
	}
	s.writeJSON(w, r, http.StatusOK, api.TokensResponse{Tokens: out})
}

func (s *server) createToken(w http.ResponseWriter, r *http.Request, a account.Actor) {
	var req api.TokenRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	secret, tok, err := s.manage.CreateMachineToken(r.Context(), a, r.PathValue("ns"), registry.TokenScope(req.Scope), req.Label)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, http.StatusCreated, api.TokenCreated{Token: toAPIToken(tok), Secret: secret})
}

func (s *server) revokeToken(w http.ResponseWriter, r *http.Request, a account.Actor) {
	if err := s.manage.RevokeMachineToken(r.Context(), a, r.PathValue("ns"), r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toAPINamespace(ns registry.Namespace) api.Namespace {
	out := api.Namespace{Name: ns.Name, Parent: ns.Parent, Kind: api.LayerKind(ns.Kind), RequireReview: ns.RequireReview, Wiki: ns.Wiki, CreatedAt: ns.CreatedAt}
	if !ns.Course.IsZero() {
		out.Course = ns.Course.String()
	}
	return out
}

func toAPIMember(m account.Membership) api.Member {
	return api.Member{Email: m.Email, Role: string(m.Role), CreatedAt: m.CreatedAt}
}

func toAPIToken(t registry.Token) api.Token {
	return api.Token{ID: t.ID, Namespace: t.Team, Scope: string(t.Scope), Label: t.Label, CreatedAt: t.CreatedAt}
}
