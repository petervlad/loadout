package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// Proposal routes (all need a bearer token):
//
//	POST /v1/proposals                          multipart (metadata + bundle) in, ProposalResponse out
//	GET  /v1/proposals?package=T/N&status=S     ProposalsResponse (default status: open; "any" for all)
//	GET  /v1/proposals/{id}                     ProposalResponse
//	GET  /v1/proposals/{id}/bundle              candidate bundle bytes
//	POST /v1/proposals/{id}/approve             DecisionRequest in, ApproveResponse out
//	POST /v1/proposals/{id}/reject              DecisionRequest in, ProposalResponse out
//	POST /v1/proposals/{id}/withdraw            DecisionRequest in, ProposalResponse out
//	POST /v1/proposals/{id}/reviews             Markdown, or multipart (Markdown + JSON report), in; ReviewResponse out
//	GET  /v1/proposals/{id}/reviews/{seq}       Markdown
//	POST   /v1/proposals/{id}/prompts           PromptRequest in, PromptResponse out (201)
//	PUT    /v1/proposals/{id}/prompts/{seq}     PromptRequest in, PromptResponse out
//	DELETE /v1/proposals/{id}/prompts/{seq}     204

// Proposals is what the HTTP layer needs from the proposal service.
type Proposals interface {
	Propose(ctx context.Context, pr registry.Principal, data []byte, in registry.ProposalInput) (registry.Proposal, error)
	Proposals(ctx context.Context, pr registry.Principal, pkg pack.ID, status registry.ProposalStatus) ([]registry.Proposal, error)
	Proposal(ctx context.Context, pr registry.Principal, id string) (registry.Proposal, error)
	ProposalBundle(ctx context.Context, pr registry.Principal, id string) (registry.Proposal, []byte, error)
	Approve(ctx context.Context, pr registry.Principal, id string, in registry.ApproveInput) (registry.Proposal, registry.Release, error)
	Reject(ctx context.Context, pr registry.Principal, id, note string) (registry.Proposal, error)
	Withdraw(ctx context.Context, pr registry.Principal, id, note string) (registry.Proposal, error)
	AddReview(ctx context.Context, pr registry.Principal, id string, text, data []byte) (registry.Review, error)
	ReviewText(ctx context.Context, pr registry.Principal, id string, seq int) (registry.Review, []byte, error)
	AddPrompt(ctx context.Context, pr registry.Principal, id, text string) (registry.Prompt, error)
	EditPrompt(ctx context.Context, pr registry.Principal, id string, seq int, text string) (registry.Prompt, error)
	RemovePrompt(ctx context.Context, pr registry.Principal, id string, seq int) error
	Standings(ctx context.Context, pr registry.Principal, props []registry.Proposal) ([]registry.Standing, error)
}

var _ Proposals = (*registry.ProposalService)(nil)

// Body limits of the proposal routes. JSON escaping can grow text up to
// six times, so the metadata limit leaves room for that.
const (
	maxProposalMetadata = 1 << 20
	maxMultipartSlack   = 64 << 10
	maxDecisionBody     = 64 << 10
	maxPromptBody       = 64 << 10
)

// Option configures the handler New returns.
type Option func(*server)

// WithProposals serves the proposal routes from p. Without it they do not
// exist.
func WithProposals(p Proposals) Option { return func(s *server) { s.props = p } }

// idPattern accepts proposal ids; anything else is rejected before a lookup.
var idPattern = regexp.MustCompile(`^[a-z0-9]{1,64}$`)

func (s *server) routeProposals(mux *http.ServeMux) {
	mux.Handle("POST /v1/proposals", s.authed(s.propose))
	mux.Handle("GET /v1/proposals", s.authed(s.listProposals))
	mux.Handle("GET /v1/proposals/{id}", s.authed(s.withID(s.getProposal)))
	mux.Handle("GET /v1/proposals/{id}/bundle", s.authed(s.withID(s.proposalBundle)))
	mux.Handle("POST /v1/proposals/{id}/approve", s.authed(s.withID(s.approve)))
	mux.Handle("POST /v1/proposals/{id}/reject", s.authed(s.withID(s.decide(s.props.Reject))))
	mux.Handle("POST /v1/proposals/{id}/withdraw", s.authed(s.withID(s.decide(s.props.Withdraw))))
	mux.Handle("POST /v1/proposals/{id}/reviews", s.authed(s.withID(s.addReview)))
	mux.Handle("GET /v1/proposals/{id}/reviews/{seq}", s.authed(s.withID(s.reviewText)))
	mux.Handle("POST /v1/proposals/{id}/prompts", s.authed(s.withID(s.addPrompt)))
	mux.Handle("PUT /v1/proposals/{id}/prompts/{seq}", s.authed(s.withID(s.editPrompt)))
	mux.Handle("DELETE /v1/proposals/{id}/prompts/{seq}", s.authed(s.withID(s.removePrompt)))
}

type idHandler func(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string)

// withID validates the {id} path value.
func (s *server) withID(h idHandler) func(http.ResponseWriter, *http.Request, registry.Principal) {
	return func(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
		id := r.PathValue("id")
		if !idPattern.MatchString(id) {
			s.writeError(w, r, fmt.Errorf("%w: proposal id %q", registry.ErrInvalid, id))
			return
		}
		h(w, r, pr, id)
	}
}

func (s *server) propose(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	ct := r.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "multipart/form-data" {
		s.writeError(w, r, fmt.Errorf("%w: content type %q, want multipart/form-data", errUnsupportedMedia, ct))
		return
	}
	s.allowTransfer(w, r)
	r.Body = http.MaxBytesReader(w, r.Body, bundle.MaxBundleSize+maxProposalMetadata+maxMultipartSlack)
	meta, data, err := readProposalParts(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req api.ProposalRequest
	dec := json.NewDecoder(bytes.NewReader(meta))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %s part: %w", registry.ErrInvalid, api.ProposalMetadataPart, err))
		return
	}
	p, err := s.props.Propose(r.Context(), pr, data, registry.ProposalInput{Note: req.Note, Prompts: req.Prompts, Base: req.Base, Replaces: req.Replaces})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/proposals/"+p.ID)
	s.writeJSON(w, r, http.StatusCreated, api.ProposalResponse{Proposal: toAPIProposal(p, pr)})
}

// readProposalParts reads exactly one metadata part and one bundle part,
// each within its own limit.
func readProposalParts(r *http.Request) (meta, data []byte, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: read upload: %w", registry.ErrInvalid, err)
		}
		name := part.FormName()
		var dst *[]byte
		var limit int64
		switch name {
		case api.ProposalMetadataPart:
			dst, limit = &meta, maxProposalMetadata
		case api.ProposalBundlePart:
			dst, limit = &data, bundle.MaxBundleSize
		default:
			return nil, nil, fmt.Errorf("%w: unexpected part %q", registry.ErrInvalid, name)
		}
		if *dst != nil {
			return nil, nil, fmt.Errorf("%w: part %q appears twice", registry.ErrInvalid, name)
		}
		b, err := io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil {
			return nil, nil, fmt.Errorf("read %s part: %w", name, err)
		}
		if int64(len(b)) > limit {
			return nil, nil, fmt.Errorf("%w: %s part is over %d bytes", bundle.ErrTooLarge, name, limit)
		}
		*dst = b
	}
	if meta == nil || data == nil {
		return nil, nil, fmt.Errorf("%w: want a %q and a %q part", registry.ErrInvalid, api.ProposalMetadataPart, api.ProposalBundlePart)
	}
	return meta, data, nil
}

func (s *server) listProposals(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if !s.listingView(w, r, pr) {
		return
	}
	q := r.URL.Query()
	var pkg pack.ID
	if raw := q.Get("package"); raw != "" {
		id, err := pack.ParseID(raw)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: %w", registry.ErrInvalid, err))
			return
		}
		pkg = id
	}
	status := registry.ProposalOpen
	switch raw := q.Get("status"); raw {
	case "":
	case api.StatusAny:
		status = ""
	default:
		status = registry.ProposalStatus(raw)
	}
	props, err := s.props.Proposals(r.Context(), pr, pkg, status)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	// One more query for the whole list, not one per row.
	standings, err := s.props.Standings(r.Context(), pr, props)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.ProposalsResponse{Proposals: toAPIProposals(props, standings, pr)})
}

func (s *server) getProposal(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	p, err := s.props.Proposal(r.Context(), pr, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	standings, err := s.props.Standings(r.Context(), pr, []registry.Proposal{p})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.ProposalResponse{Proposal: toAPIProposals([]registry.Proposal{p}, standings, pr)[0]})
}

func (s *server) proposalBundle(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	s.allowTransfer(w, r)
	p, data, err := s.props.ProposalBundle(r.Context(), pr, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	h := w.Header()
	h.Set(api.HeaderDigest, string(p.Digest))
	h.Set(api.HeaderVersion, p.Version.String())
	h.Set("Content-Type", bundle.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	// An opaque gzip bundle with its own media type and nosniff, as in fetch.
	if _, err := w.Write(data); err != nil { //nolint:gosec // G705: not HTML
		s.log.Debug("write bundle", "request_id", requestIDFrom(r.Context()), "err", err)
	}
}

// decisionRequest reads the optional DecisionRequest body.
func decisionRequest(w http.ResponseWriter, r *http.Request) (api.DecisionRequest, error) {
	var req api.DecisionRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDecisionBody))
	if err != nil {
		return req, fmt.Errorf("read body: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return req, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("%w: body: %w", registry.ErrInvalid, err)
	}
	return req, nil
}

func (s *server) approve(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	req, err := decisionRequest(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	p, rel, err := s.props.Approve(r.Context(), pr, id, registry.ApproveInput{Note: req.Note, AsOwner: req.AsOwner, Over: req.Over})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.ApproveResponse{Proposal: toAPIProposal(p, pr), Release: toAPI(rel)})
}

type decideFunc func(ctx context.Context, pr registry.Principal, id, note string) (registry.Proposal, error)

func (s *server) decide(fn decideFunc) idHandler {
	return func(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
		req, err := decisionRequest(w, r)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		p, err := fn(r.Context(), pr, id, req.Note)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		s.writeJSON(w, r, http.StatusOK, api.ProposalResponse{Proposal: toAPIProposal(p, pr)})
	}
}

func (s *server) addReview(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	ct := r.Header.Get("Content-Type")
	mt, _, err := mime.ParseMediaType(ct)
	var text, data []byte
	switch {
	case err == nil && (mt == "text/markdown" || mt == "text/plain"):
		text, err = io.ReadAll(http.MaxBytesReader(w, r.Body, registry.MaxReviewSize))
		if err != nil {
			err = fmt.Errorf("read review: %w", err)
		}
	case err == nil && mt == "multipart/form-data":
		r.Body = http.MaxBytesReader(w, r.Body, 2*registry.MaxReviewSize+maxMultipartSlack)
		text, data, err = readReviewParts(r)
	default:
		err = fmt.Errorf("%w: content type %q, want text/markdown or multipart/form-data", errUnsupportedMedia, ct)
	}
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	rev, err := s.props.AddReview(r.Context(), pr, id, text, data)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/proposals/"+id+"/reviews/"+strconv.Itoa(rev.Seq))
	s.writeJSON(w, r, http.StatusCreated, api.ReviewResponse{Review: toAPIReview(rev)})
}

// readReviewParts reads the Markdown part and the optional JSON report part
// of a review upload, each within the review size limit.
func readReviewParts(r *http.Request) (text, data []byte, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: read upload: %w", registry.ErrInvalid, err)
		}
		name := part.FormName()
		var dst *[]byte
		switch name {
		case api.ReviewMarkdownPart:
			dst = &text
		case api.ReviewReportPart:
			dst = &data
		default:
			return nil, nil, fmt.Errorf("%w: unexpected part %q", registry.ErrInvalid, name)
		}
		if *dst != nil {
			return nil, nil, fmt.Errorf("%w: part %q appears twice", registry.ErrInvalid, name)
		}
		b, err := io.ReadAll(io.LimitReader(part, registry.MaxReviewSize+1))
		if err != nil {
			return nil, nil, fmt.Errorf("read %s part: %w", name, err)
		}
		if len(b) > registry.MaxReviewSize {
			return nil, nil, fmt.Errorf("%w: %s part is over %d bytes", bundle.ErrTooLarge, name, registry.MaxReviewSize)
		}
		*dst = b
	}
	if text == nil {
		return nil, nil, fmt.Errorf("%w: want a %q part", registry.ErrInvalid, api.ReviewMarkdownPart)
	}
	return text, data, nil
}

// pathSeq reads the {seq} path value, a positive number of what.
func pathSeq(r *http.Request, what string) (int, error) {
	raw := r.PathValue("seq")
	seq, err := strconv.Atoi(raw)
	if err != nil || seq < 1 || strings.HasPrefix(raw, "+") {
		return 0, fmt.Errorf("%w: %s %q: want a positive number", registry.ErrInvalid, what, raw)
	}
	return seq, nil
}

func (s *server) reviewText(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	seq, err := pathSeq(r, "review")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	_, text, err := s.props.ReviewText(r.Context(), pr, id, seq)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", api.ReviewContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Content-Length", strconv.Itoa(len(text)))
	w.WriteHeader(http.StatusOK)
	// Markdown served as text with nosniff and a sandbox CSP, never as HTML.
	if _, err := w.Write(text); err != nil { //nolint:gosec // G705: not HTML
		s.log.Debug("write review", "request_id", requestIDFrom(r.Context()), "err", err)
	}
}

// promptRequest reads the PromptRequest body: strict JSON within
// maxPromptBody. The service checks the text itself.
func promptRequest(w http.ResponseWriter, r *http.Request) (api.PromptRequest, error) {
	var req api.PromptRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPromptBody))
	if err != nil {
		return req, fmt.Errorf("read body: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("%w: body: want {\"text\": \"...\"}: %w", registry.ErrInvalid, err)
	}
	if dec.More() {
		return req, fmt.Errorf("%w: body: more than one JSON value", registry.ErrInvalid)
	}
	return req, nil
}

func (s *server) addPrompt(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	req, err := promptRequest(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	p, err := s.props.AddPrompt(r.Context(), pr, id, req.Text)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/proposals/"+id+"/prompts/"+strconv.Itoa(p.Seq))
	s.writeJSON(w, r, http.StatusCreated, api.PromptResponse{Prompt: toAPIPrompt(p)})
}

func (s *server) editPrompt(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	seq, err := pathSeq(r, "prompt")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	req, err := promptRequest(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	p, err := s.props.EditPrompt(r.Context(), pr, id, seq, req.Text)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.PromptResponse{Prompt: toAPIPrompt(p)})
}

func (s *server) removePrompt(w http.ResponseWriter, r *http.Request, pr registry.Principal, id string) {
	seq, err := pathSeq(r, "prompt")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.props.RemovePrompt(r.Context(), pr, id, seq); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
