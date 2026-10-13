// Package server exposes a registry over HTTP.
//
// Routes (all JSON unless noted, all but health need a bearer token; an
// account token names its namespace in the X-Loadout-Namespace header):
//
//	GET  /v1/health                               HealthResponse
//	GET  /v1/packages                             PackagesResponse
//	GET  /v1/packages/{team}/{name}               ReleasesResponse
//	GET  /v1/packages/{team}/{name}/{version}     bundle bytes ("latest" allowed)
//	POST /v1/packages                             bundle bytes in, PublishResponse out
//	GET  /v1/changes?since=N                      ChangesResponse
//	GET  /v1/layers                               LayersResponse
//	GET  /v1/tiers                                TiersResponse (the view's model table)
//
// With WithProposals it also serves the proposal routes listed in
// proposals.go, and with WithDeletions the routes for requests to delete a
// package listed in deletions.go; auth.go lists the routes of `loadout login` and `logout`,
// manage.go those for managing namespaces, members and machine tokens,
// wiki.go those of a namespace's wiki page and memory.go those of team
// memory.
//
// Every response to a request with a valid token (health included) may
// carry X-Loadout-CLI-Latest and X-Loadout-CLI-Minimum; see WithCLI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/tier"
)

// Registry is what the HTTP layer needs from the registry service.
type Registry interface {
	Authenticate(ctx context.Context, secret string) (registry.Principal, error)
	Publish(ctx context.Context, pr registry.Principal, data []byte) (registry.Release, error)
	Fetch(ctx context.Context, pr registry.Principal, id pack.ID, version string) (registry.Release, []byte, error)
	Releases(ctx context.Context, pr registry.Principal, id pack.ID) ([]registry.Release, error)
	Packages(ctx context.Context, pr registry.Principal) ([]registry.Release, error)
	Changes(ctx context.Context, pr registry.Principal, since int64) ([]registry.Release, error)
	Layers(ctx context.Context, pr registry.Principal) ([]registry.Layer, error)
	RevokeToken(ctx context.Context, id string) error
	In(ctx context.Context, pr registry.Principal, ns string) (registry.Principal, error)
	ResolvedTiers(ctx context.Context, pr registry.Principal) (tier.Stack, error)
}

var _ Registry = (*registry.Service)(nil)

// latest is the version path segment that selects the newest release.
const latest = "latest"

type server struct {
	reg         Registry
	props       Proposals
	dels        Deletions
	device      Device
	deviceLimit Limiter
	pages       http.Handler
	manage      Manage
	wikis       Wikis
	memory      Memory
	mcp         http.Handler
	log         *slog.Logger
	version     string
	// cliLatest and cliMinimum are the loadout releases this registry
	// advertises (see WithCLI); zero says nothing.
	cliLatest, cliMinimum semver.Version
}

// New returns the HTTP handler for reg. version is the server build version
// reported by the health endpoint.
func New(reg Registry, log *slog.Logger, version string, opts ...Option) http.Handler {
	s := &server{reg: reg, log: log, version: version}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.Handle("GET /v1/packages", s.authed(s.packages))
	mux.Handle("GET /v1/packages/{team}/{name}", s.authed(s.releases))
	mux.Handle("GET /v1/packages/{team}/{name}/{version}", s.authed(s.fetch))
	mux.Handle("POST /v1/packages", s.authed(s.publish))
	mux.Handle("GET /v1/changes", s.authed(s.changes))
	mux.Handle("GET /v1/layers", s.authed(s.layers))
	mux.Handle("GET /v1/tiers", s.authed(s.tiers))
	if s.props != nil {
		s.routeProposals(mux)
	}
	if s.dels != nil {
		s.routeDeletions(mux)
	}
	s.routeAuth(mux)
	if s.manage != nil {
		s.routeManage(mux)
	}
	if s.wikis != nil {
		s.routeWiki(mux)
	}
	if s.hasMemory() {
		s.routeMemory(mux)
	}

	// Outermost first: every request gets an id, then is logged, and a
	// panic below the logger is turned into a 500 the logger still sees.
	return s.requestID(s.accessLog(s.recoverPanic(apiHeaders(mux))))
}

// apiCSP is the Content-Security-Policy of every API response: nothing may
// load, and nothing may frame it. The web pages replace it with their own.
const apiCSP = "default-src 'none'; frame-ancestors 'none'"

func apiHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", apiCSP)
		next.ServeHTTP(w, r)
	})
}

// health needs no token. The build version is only shown to callers with a
// valid token, so anonymous scanners cannot pin the exact code running.
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	resp := api.HealthResponse{Status: "ok"}
	if token, ok := bearer(r); ok {
		if _, err := s.reg.Authenticate(r.Context(), token); err == nil {
			resp.Version = s.version
			s.adviseCLI(w)
		}
	}
	s.writeJSON(w, r, http.StatusOK, resp)
}

func (s *server) packages(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if !s.listingView(w, r, pr) {
		return
	}
	rels, err := s.reg.Packages(r.Context(), pr)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.PackagesResponse{Packages: toAPIAll(rels)})
}

func (s *server) releases(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	rels, err := s.reg.Releases(r.Context(), pr, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, api.ReleasesResponse{Releases: toAPIAll(rels)})
}

func (s *server) fetch(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	version := r.PathValue("version")
	if version != latest {
		if _, err := semver.Parse(version); err != nil {
			s.writeError(w, r, fmt.Errorf("%w: %w", registry.ErrInvalid, err))
			return
		}
	}
	s.allowTransfer(w, r)
	rel, data, err := s.reg.Fetch(r.Context(), pr, id, version)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	etag := strconv.Quote(string(rel.Digest))
	h := w.Header()
	h.Set("ETag", etag)
	h.Set(api.HeaderDigest, string(rel.Digest))
	h.Set(api.HeaderVersion, rel.Version.String())
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", bundle.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	// The bytes are an opaque gzip bundle served with its own media type and
	// nosniff, so a browser never renders them as HTML.
	if _, err := w.Write(data); err != nil { //nolint:gosec // G705: not HTML, see above
		s.log.Debug("write bundle", "request_id", requestIDFrom(r.Context()), "err", err)
	}
}

func (s *server) publish(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	ct := r.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != bundle.ContentType {
		s.writeError(w, r, fmt.Errorf("%w: content type %q, want %s", errUnsupportedMedia, ct, bundle.ContentType))
		return
	}
	if r.ContentLength > bundle.MaxBundleSize {
		s.writeError(w, r, fmt.Errorf("%w: %d bytes, limit %d", bundle.ErrTooLarge, r.ContentLength, bundle.MaxBundleSize))
		return
	}
	s.allowTransfer(w, r)
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, bundle.MaxBundleSize))
	if err != nil {
		s.writeError(w, r, fmt.Errorf("read upload: %w", err))
		return
	}
	rel, err := s.reg.Publish(r.Context(), pr, data)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/packages/"+rel.ID.String()+"/"+rel.Version.String())
	s.writeJSON(w, r, http.StatusCreated, api.PublishResponse{Release: toAPI(rel)})
}

// allowTransfer gives a request that uploads or downloads a bundle
// transferTimeout to read its body and write its response, in place of the
// server's short timeouts.
func (s *server) allowTransfer(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(transferTimeout)
	if err := errors.Join(rc.SetReadDeadline(deadline), rc.SetWriteDeadline(deadline)); err != nil {
		s.log.Debug("extend deadlines", "request_id", requestIDFrom(r.Context()), "err", err)
	}
}

func (s *server) changes(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if !s.listingView(w, r, pr) {
		return
	}
	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			s.writeError(w, r, fmt.Errorf("%w: since %q: want a non-negative integer", registry.ErrInvalid, raw))
			return
		}
		since = n
	}
	rels, err := s.reg.Changes(r.Context(), pr, since)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	next := since
	for _, rel := range rels {
		next = max(next, rel.Seq)
	}
	s.writeJSON(w, r, http.StatusOK, api.ChangesResponse{Changes: toAPIAll(rels), Next: next})
}

func (s *server) layers(w http.ResponseWriter, r *http.Request, pr registry.Principal) {
	if !s.listingView(w, r, pr) {
		return
	}
	layers, err := s.reg.Layers(r.Context(), pr)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]api.Layer, 0, len(layers))
	for _, l := range layers {
		out = append(out, api.Layer{Namespace: l.Namespace, Kind: api.LayerKind(l.Kind), Depth: l.Depth})
	}
	s.writeJSON(w, r, http.StatusOK, api.LayersResponse{Layers: out})
}

// pathID reads {team} and {name}; a bad value is ErrInvalid.
func pathID(r *http.Request) (pack.ID, error) {
	id, err := pack.NewID(r.PathValue("team"), r.PathValue("name"))
	if err != nil {
		return pack.ID{}, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}
	return id, nil
}

func (s *server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		s.writeError(w, r, fmt.Errorf("encode response: %w", err))
		return
	}
	body = append(body, '\n')
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.log.Debug("write response", "request_id", requestIDFrom(r.Context()), "err", err)
	}
}
