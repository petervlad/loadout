// Package mcpmemory is the MCP server of one team memory: six tools over
// Streamable HTTP, the instructions a client gets when it connects, and the
// check of who may call. It stores nothing and calls no model. The rules are
// memory.Service's, and this package only shapes them for the protocol.
package mcpmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/ratelimit"
)

// Limits of the endpoint.
const (
	// MaxBody is the largest request body, so a loop of huge calls cannot
	// fill the process.
	MaxBody = 64 << 10
	// IndexTTL is how long a namespace's index text is kept before it is read
	// again. A write through this server drops it at once.
	IndexTTL = 30 * time.Second
	// CallBurst and CallEvery are the rate limit of one memory token: 120
	// calls, then one every 500 ms.
	CallBurst = 120
	CallEvery = 500 * time.Millisecond

	// Version is what the server reports as its own version in initialize.
	// It is the version of the tool contract, not of the binary.
	Version = "1.0.0"
)

// Authenticator turns the secret of a memory token into the person behind it.
// memory.Tokens implements it. A secret that is missing, wrong, expired or of
// another kind is memory.ErrUnauthorized.
type Authenticator interface {
	Authenticate(ctx context.Context, secret string) (memory.Caller, error)
}

// Options are optional settings of a Server.
type Options struct {
	Log *slog.Logger // nil discards
	// Limiter rate limits calls per token. Nil gives CallBurst and CallEvery.
	Limiter *ratelimit.Limiter
}

// Server serves the memory of every namespace, one endpoint each. It is safe
// for concurrent use.
type Server struct {
	svc     *memory.Service
	auth    Authenticator
	log     *slog.Logger
	limiter *ratelimit.Limiter
	cache   *indexCache
	mcp     http.Handler
}

// New returns a Server that answers /mcp/t/{ns}. Mount it on a pattern with an
// {ns} wildcard, for example "/mcp/t/{ns}", since it reads the namespace with
// r.PathValue("ns").
func New(svc *memory.Service, auth Authenticator, opts Options) (*Server, error) {
	if svc == nil || auth == nil {
		return nil, errors.New("mcp memory server needs a memory service and an authenticator")
	}
	s := &Server{svc: svc, auth: auth, log: opts.Log, limiter: opts.Limiter, cache: newIndexCache(IndexTTL, svc.Now)}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.limiter == nil {
		s.limiter = ratelimit.New(CallBurst, CallEvery, svc.Now)
	}
	// Stateless and JSON only: no session to keep, no event stream. The
	// localhost protection is off because loadout listens on 127.0.0.1 behind
	// a proxy and the Host is the registry's address. That is safe here: every
	// request needs a bearer token and no cookie is ever read.
	s.mcp = mcp.NewStreamableHTTPHandler(s.serverFor, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        MaxBody,
	})
	return s, nil
}

// InvalidateIndex drops every cached index, for writes made outside this
// server (the web page that retracts or moves a memory).
func (s *Server) InvalidateIndex() { s.cache.drop() }

type reqKey struct{}

// reqInfo is what ServeHTTP learned about a request, for serverFor.
type reqInfo struct {
	caller memory.Caller
	ns     string
	layers []string
}

// serverFor builds the MCP server for one request: its tools know the person
// and the namespace, its instructions hold the namespace's index.
func (s *Server) serverFor(r *http.Request) *mcp.Server {
	info, ok := r.Context().Value(reqKey{}).(reqInfo)
	if !ok {
		return nil // the handler answers 400
	}
	project := ""
	if len(info.layers) > 1 {
		project = info.layers[1]
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "loadout-memory", Version: Version}, &mcp.ServerOptions{
		Instructions: Instructions(info.ns, project, s.index(r.Context(), info)),
	})
	(&call{srv: s, caller: info.caller, ns: info.ns, layers: info.layers}).register(srv)
	return srv
}

// index is the index block of the instructions, from the cache when it is
// fresh.
func (s *Server) index(ctx context.Context, info reqInfo) string {
	if text, ok := s.cache.get(info.ns); ok {
		return text
	}
	gen := s.cache.generation() // before the read, see indexCache.gen
	entries, total, err := s.svc.Index(ctx, info.caller, info.ns)
	if err != nil {
		s.log.ErrorContext(ctx, "memory index failed", "namespace", info.ns, "error", err)
		return "The index could not be read. Use search."
	}
	text := FormatIndex(entries, total, s.svc.Now())
	s.cache.set(info.ns, text, gen)
	return text
}

// The two fixed bodies. Every refusal of a kind sends the same bytes, so
// nothing tells a wrong token from an expired one, or a team that does not
// exist from one the person is not in.
const (
	bodyUnauthorized = `{"error":"A valid memory token is needed. Make one on your account page or with loadout memory token create."}` + "\n"
	bodyNoMemory     = `{"error":"No team memory here."}` + "\n"
	bodyTooMany      = `{"error":"Too many calls. Wait a moment and try again."}` + "\n"
	bodyProblem      = `{"error":"Team memory had a problem. Try again in a moment."}` + "\n"
)

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// ServeHTTP checks, in this order: the bearer token (before the namespace is
// looked at), the rate limit, the person's right to read the namespace with
// its switch on, the method and the size of the body. Then the MCP handler
// runs.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	secret, ok := bearer(r)
	if !ok {
		s.unauthorized(w)
		return
	}
	caller, err := s.auth.Authenticate(r.Context(), secret)
	if errors.Is(err, memory.ErrUnauthorized) {
		s.unauthorized(w)
		return
	}
	if err != nil {
		s.log.ErrorContext(r.Context(), "memory token check failed", "error", err)
		reply(w, http.StatusInternalServerError, bodyProblem)
		return
	}
	if !s.limiter.Allow(tokenKey(secret)) {
		w.Header().Set("Retry-After", "1")
		reply(w, http.StatusTooManyRequests, bodyTooMany)
		return
	}
	ns := r.PathValue("ns")
	layers, err := s.svc.Layers(r.Context(), caller, ns)
	if errors.Is(err, memory.ErrNoMemory) {
		reply(w, http.StatusNotFound, bodyNoMemory)
		return
	}
	if err != nil {
		s.log.ErrorContext(r.Context(), "memory rights failed", "namespace", ns, "error", err)
		reply(w, http.StatusInternalServerError, bodyProblem)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		reply(w, http.StatusMethodNotAllowed, `{"error":"Use POST."}`+"\n")
		return
	}
	if r.ContentLength > MaxBody {
		reply(w, http.StatusRequestEntityTooLarge, `{"error":"The request is larger than 64 KiB."}`+"\n")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	ctx := context.WithValue(r.Context(), reqKey{}, reqInfo{caller: caller, ns: ns, layers: layers})
	s.mcp.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	reply(w, http.StatusUnauthorized, bodyUnauthorized)
}

// bearer returns the secret of an Authorization: Bearer header. A login token
// (ldt_) is refused here, before it reaches any lookup: only memory tokens
// work on this endpoint.
func bearer(r *http.Request) (string, bool) {
	scheme, secret, found := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	secret = strings.TrimSpace(secret)
	if !found || !strings.EqualFold(scheme, "Bearer") || secret == "" || strings.HasPrefix(secret, "ldt_") {
		return "", false
	}
	return secret, true
}

// tokenKey is the rate limit key of a token: a hash, so the limiter never
// holds a secret.
func tokenKey(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}
