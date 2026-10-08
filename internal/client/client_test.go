package client_test

import (
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

var publishedAt = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

type env struct {
	url string
	svc *registry.Service
}

// newEnv runs a real server over a real registry service and memory store.
func newEnv(t *testing.T) env {
	t.Helper()
	store := registrytest.NewMemStore()
	svc := registry.NewService(store, store, store, func() time.Time { return publishedAt }, slog.New(slog.DiscardHandler))
	ts := httptest.NewServer(server.New(svc, slog.New(slog.DiscardHandler), "v-test"))
	t.Cleanup(ts.Close)
	return env{url: ts.URL, svc: svc}
}

func (e env) client(t *testing.T, team string, scope registry.TokenScope) *client.Client {
	t.Helper()
	token := ""
	if team != "" {
		secret, _, err := e.svc.CreateToken(t.Context(), team, scope, "test")
		if err != nil {
			t.Fatal(err)
		}
		token = secret
	}
	return newClient(t, e.url+"/", token)
}

func newClient(t *testing.T, url, token string, opts ...client.Option) *client.Client {
	t.Helper()
	c, err := client.New(url, token, opts...)
	if err != nil {
		t.Fatalf("New(%q) error: %v", url, err)
	}
	return c
}

func newPack(t *testing.T, team, name, version string) *pack.Pack {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + team + "/" + name + "\nkind: agent\nversion: " + version + "\ndescription: " + name + " " + version + "\n"),
		pack.AgentFile:    []byte("---\nname: " + name + "\ndescription: d\n---\nprompt " + version + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustID(t *testing.T, s string) pack.ID {
	t.Helper()
	id, err := pack.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// wantAPIError checks that err is an *client.APIError with status and code.
func wantAPIError(t *testing.T, err error, status int, code string) {
	t.Helper()
	apiErr, ok := errors.AsType[*client.APIError](err)
	if !ok {
		t.Fatalf("error = %v, want *client.APIError %d %s", err, status, code)
	}
	if apiErr.Status != status || apiErr.Code != code {
		t.Errorf("APIError = %d %s (%s), want %d %s", apiErr.Status, apiErr.Code, apiErr.Message, status, code)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{url: "http://localhost:8080"},
		{url: "https://registry.example.com/"},
		{url: "https://registry.example.com/base/"},
		{url: "ftp://registry.example.com", wantErr: true},
		{url: "registry.example.com", wantErr: true},
		{url: "https://", wantErr: true},
		{url: "https://someone@registry.example.com", wantErr: true},
		{url: "https://registry.example.com/?x=1", wantErr: true},
		{url: "://bad", wantErr: true},
	}
	for _, tt := range tests {
		_, err := client.New(tt.url, "ldt_x")
		if (err != nil) != tt.wantErr {
			t.Errorf("New(%q) error = %v, want error %v", tt.url, err, tt.wantErr)
		}
	}
	if _, err := client.New("https://r.example.com", "", client.WithCAFile(filepath.Join(t.TempDir(), "missing.pem"))); err == nil {
		t.Error("New(WithCAFile(missing)) = nil error, want one")
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	e := newEnv(t)
	got, err := e.client(t, "", "").Health(t.Context())
	if err != nil {
		t.Fatalf("Health() error: %v", err)
	}
	if want := (api.HealthResponse{Status: "ok"}); got != want {
		t.Errorf("anonymous Health() = %+v, want %+v (no version for anonymous callers)", got, want)
	}
	authed, err := e.client(t, "lime", registry.ScopeRead).Health(t.Context())
	if err != nil {
		t.Fatalf("Health() with token error: %v", err)
	}
	if authed.Version != "v-test" {
		t.Errorf("Health() with token version = %q, want v-test", authed.Version)
	}
}

func TestUnauthorized(t *testing.T) {
	e := newEnv(t)
	for name, c := range map[string]*client.Client{
		"no token":  e.client(t, "", ""),
		"bad token": newClient(t, e.url, "ldt_not-a-real-token"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.Packages(t.Context())
			wantAPIError(t, err, http.StatusUnauthorized, api.CodeUnauthorized)
			if strings.Contains(err.Error(), "ldt_") {
				t.Errorf("error %q leaks the token", err)
			}
		})
	}
}

func TestPublishFetchRoundTrip(t *testing.T) {
	e := newEnv(t)
	c := e.client(t, "lime", registry.ScopePublish)
	v1, v2 := newPack(t, "lime", "planner", "1.0.0"), newPack(t, "lime", "planner", "1.1.0")

	for i, p := range []*pack.Pack{v1, v2} {
		rel, err := c.Publish(t.Context(), p)
		if err != nil {
			t.Fatalf("Publish(%s) error: %v", p.Version(), err)
		}
		if rel.PublishedBy == nil || rel.PublishedBy.TokenID == "" {
			t.Fatalf("Publish(%s) PublishedBy = %+v, want the token that published", p.Version(), rel.PublishedBy)
		}
		want := api.Release{
			ID: "lime/planner", Version: p.Version().String(), Digest: string(p.Digest()),
			Description: p.Manifest().Description, Kind: "agent", PublishedAt: publishedAt, Seq: int64(i + 1),
			PublishedBy: &api.Actor{TokenID: rel.PublishedBy.TokenID, Label: "test"},
		}
		if diff := cmp.Diff(want, rel, cmpIgnoreSize); diff != "" {
			t.Errorf("Publish(%s) mismatch (-want +got):\n%s", p.Version(), diff)
		}
	}

	id := mustID(t, "lime/planner")
	tests := []struct {
		version string
		want    *pack.Pack
	}{
		{version: "latest", want: v2},
		{version: "1.0.0", want: v1},
		{version: "1.1.0", want: v2},
	}
	for _, tt := range tests {
		got, err := c.Fetch(t.Context(), id, tt.version)
		if err != nil {
			t.Fatalf("Fetch(%s) error: %v", tt.version, err)
		}
		if got.Digest() != tt.want.Digest() || got.Version() != tt.want.Version() {
			t.Errorf("Fetch(%s) = %s %s, want %s %s", tt.version, got.Version(), got.Digest(), tt.want.Version(), tt.want.Digest())
		}
	}

	rels, err := c.Releases(t.Context(), id)
	if err != nil || len(rels) != 2 || rels[0].Version != "1.0.0" || rels[1].Version != "1.1.0" {
		t.Errorf("Releases() = %+v, %v; want 1.0.0 then 1.1.0", rels, err)
	}
	pkgs, err := c.Packages(t.Context())
	if err != nil || len(pkgs) != 1 || pkgs[0].Version != "1.1.0" {
		t.Errorf("Packages() = %+v, %v; want the latest planner", pkgs, err)
	}
}

var cmpIgnoreSize = cmp.FilterPath(func(p cmp.Path) bool { return p.String() == "Size" }, cmp.Ignore())

func TestErrors(t *testing.T) {
	e := newEnv(t)
	pub := e.client(t, "lime", registry.ScopePublish)
	read := e.client(t, "lime", registry.ScopeRead)
	if _, err := pub.Publish(t.Context(), newPack(t, "lime", "planner", "1.0.0")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		call   func() error
		status int
		code   string
	}{
		{
			name:   "same version again",
			call:   func() error { _, err := pub.Publish(t.Context(), newPack(t, "lime", "planner", "1.0.0")); return err },
			status: http.StatusConflict, code: api.CodeVersionConflict,
		},
		{
			name:   "lower version",
			call:   func() error { _, err := pub.Publish(t.Context(), newPack(t, "lime", "planner", "0.9.0")); return err },
			status: http.StatusConflict, code: api.CodeVersionConflict,
		},
		{
			name:   "read token publishing",
			call:   func() error { _, err := read.Publish(t.Context(), newPack(t, "lime", "planner", "2.0.0")); return err },
			status: http.StatusForbidden, code: api.CodeForbidden,
		},
		{
			name:   "publishing to another team",
			call:   func() error { _, err := pub.Publish(t.Context(), newPack(t, "other", "planner", "1.0.0")); return err },
			status: http.StatusForbidden, code: api.CodeForbidden,
		},
		{
			name:   "fetching another team's package",
			call:   func() error { _, err := read.Fetch(t.Context(), mustID(t, "other/planner"), "latest"); return err },
			status: http.StatusForbidden, code: api.CodeForbidden,
		},
		{
			name:   "listing another team's package",
			call:   func() error { _, err := read.Releases(t.Context(), mustID(t, "other/planner")); return err },
			status: http.StatusForbidden, code: api.CodeForbidden,
		},
		{
			name:   "unknown package",
			call:   func() error { _, err := read.Fetch(t.Context(), mustID(t, "lime/nope"), "latest"); return err },
			status: http.StatusNotFound, code: api.CodeNotFound,
		},
		{
			name:   "unknown version",
			call:   func() error { _, err := read.Fetch(t.Context(), mustID(t, "lime/planner"), "3.0.0"); return err },
			status: http.StatusNotFound, code: api.CodeNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			wantAPIError(t, err, tt.status, tt.code)
			if got, want := errors.Is(err, api.ErrNotFound), tt.code == api.CodeNotFound; got != want {
				t.Errorf("errors.Is(%v, api.ErrNotFound) = %t, want %t", err, got, want)
			}
		})
	}

	if _, err := read.Fetch(t.Context(), mustID(t, "lime/planner"), "../../x"); err == nil {
		t.Error("Fetch(bad version) = nil error, want one")
	}
}

func TestChanges(t *testing.T) {
	e := newEnv(t)
	c := e.client(t, "lime", registry.ScopePublish)
	other := e.client(t, "other", registry.ScopePublish)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		if _, err := c.Publish(t.Context(), newPack(t, "lime", "planner", v)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := other.Publish(t.Context(), newPack(t, "other", "reviewer", "1.0.0")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		since        int64
		wantVersions []string
		wantNext     int64
	}{
		{since: 0, wantVersions: []string{"1.0.0", "1.1.0"}, wantNext: 2},
		{since: 1, wantVersions: []string{"1.1.0"}, wantNext: 2},
		{since: 2, wantVersions: []string{}, wantNext: 2},
		{since: 3, wantVersions: []string{}, wantNext: 3},
	}
	for _, tt := range tests {
		got, err := c.Changes(t.Context(), tt.since)
		if err != nil {
			t.Fatalf("Changes(%d) error: %v", tt.since, err)
		}
		versions := []string{}
		for _, r := range got.Changes {
			versions = append(versions, r.Version)
		}
		if !cmp.Equal(versions, tt.wantVersions) || got.Next != tt.wantNext {
			t.Errorf("Changes(%d) = %v next %d, want %v next %d", tt.since, versions, got.Next, tt.wantVersions, tt.wantNext)
		}
	}
}

// TestOversizedUpload bypasses Publish, which refuses to encode an oversized
// bundle, and checks the server's own limit.
func TestOversizedUpload(t *testing.T) {
	e := newEnv(t)
	secret, _, err := e.svc.CreateToken(t.Context(), "lime", registry.ScopePublish, "test")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.url+"/v1/packages", strings.NewReader(strings.Repeat("x", bundle.MaxBundleSize+1)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", bundle.ContentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload status = %d, want 413", resp.StatusCode)
	}
}

// TestFetchVerifies serves a real bundle with lying headers.
func TestFetchVerifies(t *testing.T) {
	p := newPack(t, "lime", "planner", "1.0.0")
	data, err := bundle.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		digest  string
		version string
		id      string
		wantErr string
	}{
		{name: "honest", digest: string(p.Digest()), version: "1.0.0", id: "lime/planner"},
		{name: "tampered digest", digest: "sha256:0000", version: "1.0.0", id: "lime/planner", wantErr: "digest mismatch"},
		{name: "missing digest", digest: "", version: "1.0.0", id: "lime/planner", wantErr: api.HeaderDigest},
		{name: "wrong version header", digest: string(p.Digest()), version: "2.0.0", id: "lime/planner", wantErr: "announced version"},
		{name: "different package", digest: string(p.Digest()), version: "1.0.0", id: "lime/other", wantErr: "sent package"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.digest != "" {
					w.Header().Set(api.HeaderDigest, tt.digest)
				}
				w.Header().Set(api.HeaderVersion, tt.version)
				w.Header().Set("Content-Type", bundle.ContentType)
				_, _ = w.Write(data)
			}))
			defer ts.Close()

			_, err := newClient(t, ts.URL, "ldt_x").Fetch(t.Context(), mustID(t, tt.id), "latest")
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("Fetch() error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("Fetch() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestNonAPIErrorResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "<html>bad gateway</html>", http.StatusBadGateway)
	}))
	defer ts.Close()
	_, err := newClient(t, ts.URL, "ldt_x").Packages(t.Context())
	wantAPIError(t, err, http.StatusBadGateway, "")
}

func TestNetworkErrorHidesToken(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close()
	_, err := newClient(t, url, "ldt_supersecret").Packages(t.Context())
	if err == nil {
		t.Fatal("Packages() on a closed server = nil error, want one")
	}
	if strings.Contains(err.Error(), "supersecret") || !strings.Contains(err.Error(), strings.TrimPrefix(url, "http://")) {
		t.Errorf("error = %q, want the host and no token", err)
	}
}

func TestWithCAFile(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"tls"}`))
	}))
	defer ts.Close()

	if _, err := newClient(t, ts.URL, "").Health(t.Context()); err == nil {
		t.Fatal("Health() over an untrusted certificate = nil error, want one")
	}

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := newClient(t, ts.URL, "", client.WithCAFile(caFile)).Health(t.Context())
	if err != nil || got.Version != "tls" {
		t.Errorf("Health() with CA file = %+v, %v; want version tls", got, err)
	}

	notPEM := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(notPEM, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.New(ts.URL, "", client.WithCAFile(notPEM)); err == nil {
		t.Error("New(WithCAFile(junk)) = nil error, want one")
	}
}

func TestUserAgent(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()
	if _, err := newClient(t, ts.URL, "", client.WithUserAgent("loadout/1.2.3"), client.WithHTTPClient(ts.Client())).Health(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got != "loadout/1.2.3" {
		t.Errorf("User-Agent = %q, want loadout/1.2.3", got)
	}
}

func TestLayersAndInheritedPackages(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.CreateNamespace(t.Context(), "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateNamespace(t.Context(), "lime", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	project := e.client(t, "acme", registry.ScopePublish)
	lime := e.client(t, "lime", registry.ScopePublish)
	if _, err := project.Publish(t.Context(), newPack(t, "acme", "standards", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if _, err := lime.Publish(t.Context(), newPack(t, "lime", "planner", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	_, err := lime.Publish(t.Context(), newPack(t, "acme", "planner", "1.0.0"))
	wantAPIError(t, err, http.StatusForbidden, api.CodeForbidden)

	layers, err := lime.Layers(t.Context())
	if err != nil {
		t.Fatalf("Layers() error: %v", err)
	}
	want := []api.Layer{{Namespace: "lime", Kind: api.LayerTeam}, {Namespace: "acme", Kind: api.LayerProject, Depth: 1}}
	if !cmp.Equal(layers, want) {
		t.Errorf("Layers() = %+v, want %+v", layers, want)
	}

	rels, err := lime.Packages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range rels {
		ids = append(ids, r.ID)
	}
	if !cmp.Equal(ids, []string{"lime/planner", "acme/standards"}) {
		t.Errorf("Packages() = %v, want lime/planner then acme/standards", ids)
	}
	if _, err := lime.Fetch(t.Context(), mustID(t, "acme/standards"), "latest"); err != nil {
		t.Errorf("Fetch(acme/standards) with a lime token error: %v", err)
	}
}
