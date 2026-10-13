package mcpmemory_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petervlad/loadout/internal/mcpmemory"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
)

// fakeRoles answers rights from a table keyed by user and namespace. A pair
// that is not in the table has no rights, as an unknown team would.
type fakeRoles map[[2]string]memory.Rights

func (f fakeRoles) MemoryRights(_ context.Context, userID, ns string) (memory.Rights, error) {
	return f[[2]string{userID, ns}], nil
}

// fakeAuth maps secrets to people. Anything else is memory.ErrUnauthorized.
// It accepts an ldt_ secret on purpose, to show the server refuses it first.
type fakeAuth map[string]memory.Caller

func (f fakeAuth) Authenticate(_ context.Context, secret string) (memory.Caller, error) {
	if c, ok := f[secret]; ok {
		return c, nil
	}
	return memory.Caller{}, memory.ErrUnauthorized
}

var (
	dana = memory.Caller{UserID: "dana", Label: "dana@example.com"}
	lee  = memory.Caller{UserID: "lee", Label: "lee@example.com"}
	boss = memory.Caller{UserID: "boss", Label: "boss@example.com"}
)

// Secrets of the fixture.
const (
	secDana     = "ldm_dana"
	secLee      = "ldm_lee"
	secBoss     = "ldm_boss"
	secPM       = "ldm_pm"      // member of the project only
	secAdmin    = "ldm_admin"   // reads everything
	secOutside  = "ldm_out"     // a valid token, no membership
	secLogin    = "ldt_dana"    // a login token of a real person
	secOtherSub = "ldm_reader2" // member of lime, read only
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type rig struct {
	svc      *memory.Service
	store    memory.Store
	switches memory.SwitchStore
	clock    *clock
	logs     *bytes.Buffer
	srv      *mcpmemory.Server
	http     *httptest.Server
}

// newRig has team lime under project acme, both with memory on, served at
// /mcp/t/{ns}. dana and lee write, boss moderates, pm is in acme only.
func newRig(t *testing.T, opts ...func(*mcpmemory.Options)) *rig {
	t.Helper()
	return newRigOn(t, memorytest.NewMemStore(), memorytest.NewMemSwitchStore(), opts...)
}

// newRigOn is newRig over the stores given, such as a real SQLite file.
func newRigOn(t *testing.T, store memory.Store, switches memory.SwitchStore, opts ...func(*mcpmemory.Options)) *rig {
	t.Helper()
	lime := []string{"lime", "acme"}
	roles := fakeRoles{
		{"dana", "lime"}:    {Read: true, Write: true, Layers: lime},
		{"lee", "lime"}:     {Read: true, Write: true, Layers: lime},
		{"reader2", "lime"}: {Read: true, Layers: lime},
		{"boss", "lime"}:    {Read: true, Write: true, Moderate: true, Switch: true, Layers: lime},
		{"boss", "acme"}:    {Read: true, Write: true, Moderate: true, Switch: true, Layers: []string{"acme"}},
		{"pm", "acme"}:      {Read: true, Write: true, Layers: []string{"acme"}},
		{"admin", "lime"}:   {Read: true, Write: true, Moderate: true, Switch: true, Layers: lime},
		{"admin", "acme"}:   {Read: true, Write: true, Moderate: true, Switch: true, Layers: []string{"acme"}},
	}
	auth := fakeAuth{
		secDana: dana, secLee: lee, secBoss: boss, secLogin: dana,
		secPM:       {UserID: "pm", Label: "pm@example.com"},
		secAdmin:    {UserID: "admin", Label: "admin@example.com"},
		secOutside:  {UserID: "outside", Label: "outside@example.com"},
		secOtherSub: {UserID: "reader2", Label: "reader2@example.com"},
	}
	r := &rig{
		store:    store,
		switches: switches,
		clock:    &clock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)},
		logs:     &bytes.Buffer{},
	}
	log := slog.New(slog.NewJSONHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc, err := memory.NewService(r.store, r.switches, roles, r.clock.now, log)
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	for _, ns := range lime {
		if err := r.switches.Set(t.Context(), ns, true, "boss", r.clock.now()); err != nil {
			t.Fatal(err)
		}
	}
	o := mcpmemory.Options{Log: log}
	for _, f := range opts {
		f(&o)
	}
	if r.srv, err = mcpmemory.New(svc, auth, o); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp/t/{ns}", r.srv)
	r.http = httptest.NewServer(mux)
	t.Cleanup(r.http.Close)
	return r
}

// seed saves an entry straight into the store, bypassing the tools.
func (r *rig) seed(t *testing.T, ns, name, desc, body string, by memory.Caller) memory.Entry {
	t.Helper()
	d, err := memory.NewDraft(ns, memory.TypeReference, name, desc, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, err := r.store.Insert(t.Context(), d, by, r.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (r *rig) url(ns string) string { return r.http.URL + "/mcp/t/" + ns }

// bearerClient adds the secret to every request and keeps what came back.
type bearerClient struct {
	secret string
	base   http.RoundTripper

	mu     sync.Mutex
	bodies [][]byte
}

func (b *bearerClient) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if b.secret != "" {
		req.Header.Set("Authorization", "Bearer "+b.secret)
	}
	resp, err := b.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	b.mu.Lock()
	b.bodies = append(b.bodies, body)
	b.mu.Unlock()
	return resp, nil
}

// everything is all the response bytes seen so far.
func (b *bearerClient) everything() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.Join(b.bodies, []byte("\n")))
}

// session is a go-sdk client connected to one endpoint.
type session struct {
	*mcp.ClientSession
	wire *bearerClient
}

func (r *rig) connect(t *testing.T, secret, ns string) *session {
	t.Helper()
	cs, wire, err := r.dial(t.Context(), secret, ns)
	if err != nil {
		t.Fatalf("connect %s to %s: %v", secret, ns, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &session{cs, wire}
}

func (r *rig) dial(ctx context.Context, secret, ns string) (*mcp.ClientSession, *bearerClient, error) {
	wire := &bearerClient{secret: secret, base: http.DefaultTransport}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   r.url(ns),
		HTTPClient: &http.Client{Transport: wire},
	}, nil)
	return cs, wire, err
}

// call runs a tool and returns its result, failing the test on a protocol
// error. A tool error is a result with IsError.
func (s *session) call(t *testing.T, tool string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return res
}

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("content has %d blocks, want 1", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}
	return tc.Text
}

// post sends one raw JSON-RPC body and returns the status, headers and body.
func (r *rig) post(t *testing.T, secret, ns, body string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.url(ns), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

func rpc(id int, method, params string) string {
	if params == "" {
		params = "{}"
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params)
}

const initParams = `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"golden","version":"1"}}`
