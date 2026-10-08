package server_test

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
	"github.com/petervlad/loadout/internal/tier"
)

// syncBuffer is a log sink safe for the server's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// lines decodes the JSON log lines.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

type fixture struct {
	handler http.Handler
	svc     *registry.Service
	logs    *syncBuffer
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	svc := registry.NewService(store, store, store, now, slog.New(slog.DiscardHandler))
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	return fixture{handler: server.New(svc, log, "test"), svc: svc, logs: logs}
}

func (f fixture) token(t *testing.T, team string, scope registry.TokenScope) string {
	t.Helper()
	secret, _, err := f.svc.CreateToken(t.Context(), team, scope, "test")
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func bundleFor(t *testing.T, team, version string) []byte {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + team + "/planner\nkind: agent\nversion: " + version + "\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: d\n---\nprompt\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := bundle.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// oversizedAgent is a bundle of an agent with more files than an agent may
// have. Encode refuses it, so it is built by hand.
func oversizedAgent(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
		pack.ManifestFile: "name: lime/big\nkind: agent\nversion: 1.0.0\ndescription: d\n",
		pack.AgentFile:    "---\nname: big\ndescription: d\n---\nprompt\n",
	}
	for i := range pack.KindAgent.Limits().MaxFiles {
		files[fmt.Sprintf("notes/%d.md", i)] = "x"
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type request struct {
	method, path, token, contentType string
	body                             []byte
}

func serve(t *testing.T, h http.Handler, rq request) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), rq.method, rq.path, bytes.NewReader(rq.body))
	if rq.token != "" {
		req.Header.Set("Authorization", "Bearer "+rq.token)
	}
	if rq.contentType != "" {
		req.Header.Set("Content-Type", rq.contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) api.ErrorBody {
	t.Helper()
	var body api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q: %v", rec.Body.String(), err)
	}
	return body.Error
}

func TestStatusMapping(t *testing.T) {
	f := newFixture(t)
	pub := f.token(t, "lime", registry.ScopePublish)
	otherRead := f.token(t, "other", registry.ScopeRead)
	if rec := serve(t, f.handler, request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: bundleFor(t, "lime", "1.0.0")}); rec.Code != http.StatusCreated {
		t.Fatalf("publish status = %d, want 201: %s", rec.Code, rec.Body)
	}

	tests := []struct {
		name     string
		req      request
		status   int
		code     string
		wwwAuthn bool
	}{
		{name: "no token", req: request{method: http.MethodGet, path: "/v1/packages"}, status: 401, code: api.CodeUnauthorized, wwwAuthn: true},
		{name: "bad token", req: request{method: http.MethodGet, path: "/v1/packages", token: "ldt_nope"}, status: 401, code: api.CodeUnauthorized, wwwAuthn: true},
		{name: "invalid team", req: request{method: http.MethodGet, path: "/v1/packages/Lime/planner", token: pub}, status: 400, code: api.CodeInvalid},
		{name: "invalid name", req: request{method: http.MethodGet, path: "/v1/packages/lime/plan_ner/latest", token: pub}, status: 400, code: api.CodeInvalid},
		{name: "invalid version", req: request{method: http.MethodGet, path: "/v1/packages/lime/planner/newest", token: pub}, status: 400, code: api.CodeInvalid},
		{name: "unknown package", req: request{method: http.MethodGet, path: "/v1/packages/lime/nope", token: pub}, status: 404, code: api.CodeNotFound},
		{name: "unknown version", req: request{method: http.MethodGet, path: "/v1/packages/lime/planner/9.9.9", token: pub}, status: 404, code: api.CodeNotFound},
		{name: "other team", req: request{method: http.MethodGet, path: "/v1/packages/other/planner", token: pub}, status: 403, code: api.CodeForbidden},
		{name: "other team token", req: request{method: http.MethodGet, path: "/v1/packages/lime/planner/latest", token: otherRead}, status: 403, code: api.CodeForbidden},
		{name: "publish to other team", req: request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: bundleFor(t, "other", "1.0.0")}, status: 403, code: api.CodeForbidden},
		{name: "since not a number", req: request{method: http.MethodGet, path: "/v1/changes?since=abc", token: pub}, status: 400, code: api.CodeInvalid},
		{name: "since negative", req: request{method: http.MethodGet, path: "/v1/changes?since=-1", token: pub}, status: 400, code: api.CodeInvalid},
		{name: "garbage upload", req: request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: []byte("nope")}, status: 400, code: api.CodeInvalid},
		{name: "wrong content type", req: request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: "application/json", body: []byte("{}")}, status: 415, code: api.CodeInvalid},
		{name: "oversized upload", req: request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: make([]byte, bundle.MaxBundleSize+1)}, status: 413, code: api.CodeTooLarge},
		{name: "agent over its limits", req: request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: oversizedAgent(t)}, status: 413, code: api.CodeTooLarge},
		{name: "duplicate version", req: request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: bundleFor(t, "lime", "1.0.0")}, status: 409, code: api.CodeVersionConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, f.handler, tt.req)
			if rec.Code != tt.status {
				t.Fatalf("%s %s status = %d, want %d: %s", tt.req.method, tt.req.path, rec.Code, tt.status, rec.Body)
			}
			if got := errorCode(t, rec); got.Code != tt.code || got.Message == "" {
				t.Errorf("error = %+v, want code %s and a message", got, tt.code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); (got == "Bearer") != tt.wwwAuthn {
				t.Errorf("WWW-Authenticate = %q, want present %v", got, tt.wwwAuthn)
			}
			if rec.Header().Get(server.HeaderRequestID) == "" {
				t.Errorf("%s header missing", server.HeaderRequestID)
			}
		})
	}
}

func TestPublishRequiresContentType(t *testing.T) {
	f := newFixture(t)
	pub := f.token(t, "lime", registry.ScopePublish)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/packages", bytes.NewReader([]byte("x")))
	req.Header.Set("Authorization", "Bearer "+pub)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("publish without Content-Type: status = %d, want 415", rec.Code)
	}
}

// TestOversizedStreamed checks the body limit when no Content-Length is
// announced, so only http.MaxBytesReader can stop the upload.
func TestOversizedStreamed(t *testing.T) {
	f := newFixture(t)
	pub := f.token(t, "lime", registry.ScopePublish)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/packages", bytes.NewReader(make([]byte, bundle.MaxBundleSize+10)))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+pub)
	req.Header.Set("Content-Type", bundle.ContentType)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || errorCode(t, rec).Code != api.CodeTooLarge {
		t.Errorf("status = %d %s, want 413 too_large", rec.Code, rec.Body)
	}
}

func TestFetchHeaders(t *testing.T) {
	f := newFixture(t)
	pub := f.token(t, "lime", registry.ScopePublish)
	data := bundleFor(t, "lime", "1.2.0")
	serve(t, f.handler, request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: data})

	rec := serve(t, f.handler, request{method: http.MethodGet, path: "/v1/packages/lime/planner/latest", token: pub})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	p, err := bundle.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	h := rec.Header()
	for name, want := range map[string]string{
		"Content-Type":    bundle.ContentType,
		api.HeaderDigest:  string(p.Digest()),
		api.HeaderVersion: "1.2.0",
		"ETag":            `"` + string(p.Digest()) + `"`,
	} {
		if got := h.Get(name); got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Errorf("body differs from the published bundle")
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/packages/lime/planner/1.2.0", nil)
	req.Header.Set("Authorization", "Bearer "+pub)
	req.Header.Set("If-None-Match", h.Get("ETag"))
	rec = httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("conditional GET = %d with %d bytes, want 304 and no body", rec.Code, rec.Body.Len())
	}
}

func TestAccessLog(t *testing.T) {
	f := newFixture(t)
	pub := f.token(t, "lime", registry.ScopePublish)
	serve(t, f.handler, request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: bundleFor(t, "lime", "1.0.0")})
	serve(t, f.handler, request{method: http.MethodGet, path: "/v1/packages", token: "ldt_wrong"})
	health := serve(t, f.handler, request{method: http.MethodGet, path: "/v1/health"})

	if strings.Contains(f.logs.String(), pub) || strings.Contains(f.logs.String(), "ldt_wrong") {
		t.Fatalf("log contains a token secret:\n%s", f.logs)
	}

	type entry struct {
		route   string
		status  float64
		tokenID bool
	}
	var got []entry
	var ids []string
	for _, m := range f.logs.lines(t) {
		if m["msg"] != "request" {
			continue
		}
		for _, k := range []string{"method", "bytes", "duration_ms", "request_id"} {
			if _, ok := m[k]; !ok {
				t.Errorf("access log line %v lacks %q", m, k)
			}
		}
		route, _ := m["route"].(string)
		status, _ := m["status"].(float64)
		_, hasToken := m["token_id"]
		got = append(got, entry{route: route, status: status, tokenID: hasToken})
		id, _ := m["request_id"].(string)
		ids = append(ids, id)
	}
	want := []entry{
		{route: "POST /v1/packages", status: 201, tokenID: true},
		{route: "GET /v1/packages", status: 401, tokenID: false},
		{route: "GET /v1/health", status: 200, tokenID: false},
	}
	if len(got) != len(want) {
		t.Fatalf("access log entries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if ids[2] != health.Header().Get(server.HeaderRequestID) {
		t.Errorf("logged request_id = %q, want the response header %q", ids[2], health.Header().Get(server.HeaderRequestID))
	}
}

// fakeRegistry authenticates every token and then panics or fails.
type fakeRegistry struct {
	panicValue any
	err        error
}

var _ server.Registry = fakeRegistry{}

func (f fakeRegistry) fail() error {
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	return f.err
}

func (fakeRegistry) Authenticate(context.Context, string) (registry.Principal, error) {
	return registry.Principal{TokenID: "tok1", Team: "lime", Scope: registry.ScopePublish}, nil
}

func (f fakeRegistry) Publish(context.Context, registry.Principal, []byte) (registry.Release, error) {
	return registry.Release{}, f.fail()
}

func (f fakeRegistry) Fetch(context.Context, registry.Principal, pack.ID, string) (registry.Release, []byte, error) {
	return registry.Release{}, nil, f.fail()
}

func (f fakeRegistry) Releases(context.Context, registry.Principal, pack.ID) ([]registry.Release, error) {
	return nil, f.fail()
}

func (f fakeRegistry) Packages(context.Context, registry.Principal) ([]registry.Release, error) {
	return nil, f.fail()
}

func (f fakeRegistry) RevokeToken(context.Context, string) error { return f.fail() }

func (f fakeRegistry) ResolvedTiers(context.Context, registry.Principal) (tier.Stack, error) {
	return tier.Stack{}, f.fail()
}

func (f fakeRegistry) Changes(context.Context, registry.Principal, int64) ([]registry.Release, error) {
	return nil, f.fail()
}

func (f fakeRegistry) Layers(context.Context, registry.Principal) ([]registry.Layer, error) {
	return nil, f.fail()
}

func (f fakeRegistry) In(context.Context, registry.Principal, string) (registry.Principal, error) {
	return registry.Principal{}, f.fail()
}

func TestInternalErrors(t *testing.T) {
	tests := []struct {
		name    string
		reg     fakeRegistry
		wantLog string
	}{
		{name: "panic", reg: fakeRegistry{panicValue: "boom at db.go:12"}, wantLog: "handler panic"},
		{name: "store error", reg: fakeRegistry{err: errors.New("dial postgres://admin@db: refused")}, wantLog: "request failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := &syncBuffer{}
			h := server.New(tt.reg, slog.New(slog.NewJSONHandler(logs, nil)), "test")
			rec := serve(t, h, request{method: http.MethodGet, path: "/v1/packages", token: "ldt_x"})
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			body := errorCode(t, rec)
			if body.Code != api.CodeInternal || strings.Contains(body.Message, "db") {
				t.Errorf("error body = %+v, want a generic internal error", body)
			}
			var sawError, sawAccess bool
			for _, m := range logs.lines(t) {
				if m["msg"] == tt.wantLog && m["level"] == "ERROR" && m["request_id"] == rec.Header().Get(server.HeaderRequestID) {
					sawError = true
				}
				if m["msg"] == "request" && m["status"] == float64(500) {
					sawAccess = true
				}
			}
			if !sawError || !sawAccess {
				t.Errorf("logs lack the error (%v) or the 500 access line (%v):\n%s", sawError, sawAccess, logs)
			}
		})
	}
}

func TestChangesNext(t *testing.T) {
	f := newFixture(t)
	pub := f.token(t, "lime", registry.ScopePublish)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		serve(t, f.handler, request{method: http.MethodPost, path: "/v1/packages", token: pub, contentType: bundle.ContentType, body: bundleFor(t, "lime", v)})
	}
	tests := []struct {
		query    string
		wantLen  int
		wantNext int64
	}{
		{query: "", wantLen: 2, wantNext: 2},
		{query: "?since=0", wantLen: 2, wantNext: 2},
		{query: "?since=1", wantLen: 1, wantNext: 2},
		{query: "?since=2", wantLen: 0, wantNext: 2},
		{query: "?since=50", wantLen: 0, wantNext: 50},
	}
	for _, tt := range tests {
		rec := serve(t, f.handler, request{method: http.MethodGet, path: "/v1/changes" + tt.query, token: pub})
		var got api.ChangesResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("changes%s: %v: %s", tt.query, err, rec.Body)
		}
		if len(got.Changes) != tt.wantLen || got.Next != tt.wantNext || got.Changes == nil {
			t.Errorf("changes%s = %d changes, next %d; want %d, next %d", tt.query, len(got.Changes), got.Next, tt.wantLen, tt.wantNext)
		}
	}
}

func TestLayers(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.CreateNamespace(t.Context(), "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateNamespace(t.Context(), "lime", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	tok := f.token(t, "lime", registry.ScopeRead)

	if rec := serve(t, f.handler, request{method: http.MethodGet, path: "/v1/layers"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/layers without a token = %d, want 401", rec.Code)
	}
	rec := serve(t, f.handler, request{method: http.MethodGet, path: "/v1/layers", token: tok})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/layers = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got api.LayersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []api.Layer{
		{Namespace: "lime", Kind: api.LayerTeam, Depth: 0},
		{Namespace: "acme", Kind: api.LayerProject, Depth: 1},
	}
	if len(got.Layers) != len(want) || got.Layers[0] != want[0] || got.Layers[1] != want[1] {
		t.Errorf("GET /v1/layers = %+v, want %+v", got.Layers, want)
	}
}
