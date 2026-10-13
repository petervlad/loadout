package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// Deletion routes (all need a bearer token):
//
//	POST /v1/deletions                          DeletionRequest in, DeletionResponse out (201)
//	GET  /v1/deletions?team=T&status=S          DeletionsResponse (default status: open; "any" for all)
//	GET  /v1/deletions/{id}                     DeletionResponse, with where it stands now
//	POST /v1/deletions/{id}/approve             DecisionRequest in, DeletionResponse out
//	POST /v1/deletions/{id}/reject              DecisionRequest in, DeletionResponse out
//	POST /v1/deletions/{id}/withdraw            DecisionRequest in, DeletionResponse out

// Deletions is what the HTTP layer needs from the deletion service.
type Deletions interface {
	Propose(ctx context.Context, pr registry.Principal, id pack.ID, reason string) (registry.Deletion, error)
	Deletions(ctx context.Context, pr registry.Principal, team string, status registry.DeletionStatus) ([]registry.Deletion, error)
	Deletion(ctx context.Context, pr registry.Principal, id string) (registry.Deletion, error)
	Standing(ctx context.Context, d registry.Deletion) (registry.DeletionStanding, error)
	Approve(ctx context.Context, pr registry.Principal, id string, in registry.ApproveInput) (registry.Deletion, error)
	Reject(ctx context.Context, pr registry.Principal, id, note string) (registry.Deletion, error)
	Withdraw(ctx context.Context, pr registry.Principal, id, note string) (registry.Deletion, error)
}

var _ Deletions = (*registry.DeletionService)(nil)

// WithDeletions serves the deletion routes from d. Without it they do not
// exist.
func WithDeletions(d Deletions) Option { return func(s *server) { s.dels = d } }

// deletionID accepts the ids the registry hands out. A proposal's id has no
// hyphen, so the two never mix, and an older CLI asked to approve one gets a
// clean 400 from the proposal routes.
var deletionID = regexp.MustCompile(`^del-[a-z0-9]{1,60}$`)

const maxDeletionBody = 64 << 10

func (s *server) routeDeletions(mux *http.ServeMux) {
	mux.Handle("POST /v1/deletions", s.authed(s.askDeletion))
	mux.Handle("GET /v1/deletions", s.authed(s.listDeletions))
	mux.Handle("GET /v1/deletions/{id}", s.authed(s.withDeletionID(s.getDeletion)))
	mux.Handle("POST /v1/deletions/{id}/approve", s.authed(s.withDeletionID(s.approveDeletion)))
	mux.Handle("POST /v1/deletions/{id}/reject", s.authed(s.withDeletionID(s.decideDeletion(s.dels.Reject))))
	mux.Handle("POST /v1/deletions/{id}/withdraw", s.authed(s.withDeletionID(s.decideDeletion(s.dels.Withdraw))))
}

// withDeletionID validates the {id} path value.
func (s *server) withDeletionID(h idHandler) func(http.ResponseWriter, *http.Request, registry.Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
		id := r.PathValue("id")
		if !deletionID.MatchString(id) {
			s.writeError(w, r, fmt.Errorf("%w: deletion id %q", registry.ErrInvalid, id))
			return
		}
		h(w, r, pr, id)
	}
}

func (s *server) askDeletion(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDeletionBody))
	if err != nil {
		s.writeError(w, r, fmt.Errorf("read body: %w", err))
		return
	}
	var req api.DeletionRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, fmt.Errorf("%w: body: %w", registry.ErrInvalid, err))
		return
	}
	id, err := pack.ParseID(req.Package)
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %w", registry.ErrInvalid, err))
		return
	}
	d, err := s.dels.Propose(r.Context(), pr, id, req.Reason)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/deletions/"+d.ID)
	s.writeJSON(w, r, http.StatusCreated, api.DeletionResponse{Deletion: toAPIDeletion(d, pr)})
}

func (s *server) listDeletions(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if !s.listingView(w, r, pr) {
		return
	}
	q := r.URL.Query()
	status := registry.DeletionOpen
	switch raw := q.Get("status"); raw {
	case "":
	case api.StatusAny:
		status = ""
	default:
		status = registry.DeletionStatus(raw)
	}
	ds, err := s.dels.Deletions(r.Context(), pr, q.Get("team"), status)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]api.Deletion, 0, len(ds))
	for _, d := range ds {
		out = append(out, toAPIDeletion(d, pr))
	}
	s.writeJSON(w, r, http.StatusOK, api.DeletionsResponse{Deletions: out})
}

func (s *server) getDeletion(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	d, err := s.dels.Deletion(r.Context(), pr, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := toAPIDeletion(d, pr)
	if d.Status == registry.DeletionOpen {
		st, err := s.dels.Standing(r.Context(), d)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		out.Latest, out.Stale = st.Newest.String(), st.Stale
	}
	s.writeJSON(w, r, http.StatusOK, api.DeletionResponse{Deletion: out})
}

func (s *server) approveDeletion(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	req, err := decisionRequest(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	d, err := s.dels.Approve(r.Context(), pr, id, registry.ApproveInput{Note: req.Note, AsOwner: req.AsOwner})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.DeletionResponse{Deletion: toAPIDeletion(d, pr)})
}

type deleteDecideFunc func(ctx context.Context, pr registry.Principal, id, note string) (registry.Deletion, error)

func (s *server) decideDeletion(fn deleteDecideFunc) idHandler {
	return func(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
		req, err := decisionRequest(w, r)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		d, err := fn(r.Context(), pr, id, req.Note)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		s.writeJSON(w, r, http.StatusOK, api.DeletionResponse{Deletion: toAPIDeletion(d, pr)})
	}
}
