package mcpbridge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petervlad/loadout/internal/mcpbridge"
	"github.com/petervlad/loadout/internal/mcpmemory"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
)

const secret = "ldm_dana"

type roles struct{}

func (roles) MemoryRights(_ context.Context, userID, ns string) (memory.Rights, error) {
	if userID == "dana" && ns == "lime" {
		return memory.Rights{Read: true, Write: true, Layers: []string{"lime"}}, nil
	}
	return memory.Rights{}, nil
}

type auth struct{}

func (auth) Authenticate(_ context.Context, s string) (memory.Caller, error) {
	if s == secret {
		return memory.Caller{UserID: "dana", Label: "dana@example.com"}, nil
	}
	return memory.Caller{}, memory.ErrUnauthorized
}

// remote is a registry's memory endpoint behind a TLS server whose CA is not
// in the system roots.
type remote struct {
	srv    *httptest.Server
	caFile string
}

func newRemote(t *testing.T) *remote {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
	store, switches := memorytest.NewMemStore(), memorytest.NewMemSwitchStore()
	svc, err := memory.NewService(store, switches, roles{}, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := switches.Set(t.Context(), "lime", true, "dana", now()); err != nil {
		t.Fatal(err)
	}
	d, err := memory.NewDraft("lime", memory.TypeReference, "TRK Tracker host", "TRK tickets live on tracker.example", "never atc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Insert(t.Context(), d, memory.Caller{UserID: "dana", Label: "dana@example.com"}, now()); err != nil {
		t.Fatal(err)
	}
	h, err := mcpmemory.New(svc, auth{}, mcpmemory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp/t/{ns}", h)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return &remote{srv: srv, caFile: caFile}
}

// direct connects to the endpoint without the bridge.
func (r *remote) direct(t *testing.T) *mcp.ClientSession {
	t.Helper()
	hc := r.srv.Client()
	hc.Transport = bearerTrip{next: hc.Transport}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "direct", Version: "1"}, nil).Connect(t.Context(),
		&mcp.StreamableClientTransport{Endpoint: r.srv.URL + "/mcp/t/lime", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type bearerTrip struct{ next http.RoundTripper }

func (b bearerTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+secret)
	return b.next.RoundTrip(req)
}

// lockedBuffer is a bytes.Buffer safe for the bridge and the test.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// running is a bridge wired to a test client by pipes.
type running struct {
	*mcp.ClientSession
	stdout *lockedBuffer
	logs   *lockedBuffer
	done   chan error
}

func start(t *testing.T, cfg mcpbridge.Config) *running {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	r := &running{stdout: &lockedBuffer{}, logs: &lockedBuffer{}, done: make(chan error, 1)}
	cfg.Stdin = inR
	cfg.Stdout = io.MultiWriter(outW, r.stdout)
	cfg.Log = slog.New(slog.NewJSONHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b, err := mcpbridge.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		err := b.Run(t.Context())
		_ = outW.Close()
		r.done <- err
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "tool", Version: "1"}, nil).Connect(t.Context(),
		&mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.ClientSession = cs
	t.Cleanup(func() {
		_ = cs.Close()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			t.Error("bridge did not stop when stdin closed")
		}
	})
	return r
}

func (r *remote) config() mcpbridge.Config {
	return mcpbridge.Config{URL: r.srv.URL, Team: "lime", Token: secret, CAFile: r.caFile}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBridgeMatchesHTTP(t *testing.T) {
	r := newRemote(t)
	direct := r.direct(t)
	b := start(t, r.config())

	if got, want := b.InitializeResult().Instructions, direct.InitializeResult().Instructions; got != want || !strings.Contains(got, "TRK Tracker host") {
		t.Errorf("instructions differ or lack the index:\nbridge: %q\ndirect: %q", got, want)
	}
	bt, err := b.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	dt, err := direct.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if marshal(t, bt) != marshal(t, dt) || len(bt.Tools) != 6 {
		t.Errorf("tool lists differ:\nbridge: %s\ndirect: %s", marshal(t, bt), marshal(t, dt))
	}

	if res, err := b.CallTool(t.Context(), &mcp.CallToolParams{Name: "save", Arguments: map[string]any{
		"type": "feedback", "name": "Bridge save", "description": "saved through the bridge", "body": "text",
	}}); err != nil || res.IsError {
		t.Fatalf("save through the bridge = %+v, %v", res, err)
	}
	calls := []mcp.CallToolParams{
		{Name: "search", Arguments: map[string]any{"query": "bridge"}},
		{Name: "search", Arguments: map[string]any{"query": "tracker"}},
		{Name: "recent", Arguments: map[string]any{}},
		{Name: "get", Arguments: map[string]any{"id": 1}},
		{Name: "get", Arguments: map[string]any{"id": 999}},
		{Name: "save", Arguments: map[string]any{"type": "nonsense"}},
	}
	for _, c := range calls {
		viaBridge, err1 := b.CallTool(t.Context(), &mcp.CallToolParams{Name: c.Name, Arguments: c.Arguments})
		viaHTTP, err2 := direct.CallTool(t.Context(), &mcp.CallToolParams{Name: c.Name, Arguments: c.Arguments})
		if err1 != nil || err2 != nil {
			t.Fatalf("%s: bridge %v, direct %v", c.Name, err1, err2)
		}
		if got, want := marshal(t, viaBridge), marshal(t, viaHTTP); got != want {
			t.Errorf("%s %v differs:\nbridge: %s\ndirect: %s", c.Name, c.Arguments, got, want)
		}
	}
}

func TestUsesCAFile(t *testing.T) {
	r := newRemote(t)

	cfg := r.config()
	cfg.CAFile = ""
	cfg.ConnectTimeout = time.Second
	without := start(t, cfg)
	if strings.Contains(without.InitializeResult().Instructions, "TRK Tracker host") {
		t.Error("bridge without the CA file reached a server it should not trust")
	}

	with := start(t, r.config())
	if !strings.Contains(with.InitializeResult().Instructions, "TRK Tracker host") {
		t.Errorf("bridge with the CA file did not reach the registry: %q", with.InitializeResult().Instructions)
	}

	bad := r.config()
	bad.CAFile = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := mcpbridge.New(bad); err == nil {
		t.Error("New with a missing CA file = nil error, want one")
	}
}

func TestOfflineStart(t *testing.T) {
	// A listener that never answers: the start must give up on its own.
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer hang.Close()
	defer close(release)
	cfg := mcpbridge.Config{URL: hang.URL, Team: "lime", Token: secret, ConnectTimeout: 300 * time.Millisecond}
	begun := time.Now()
	b := start(t, cfg)
	if took := time.Since(begun); took > 4*time.Second {
		t.Errorf("start took %v, want about the timeout", took)
	}

	ins := b.InitializeResult().Instructions
	if !strings.Contains(ins, "Memory is unreachable") || !strings.Contains(ins, "Team memory for lime") {
		t.Errorf("offline instructions = %q", ins)
	}
	tools, err := b.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if got := strings.Join(names, ","); got != "search,get,save,update,retract,recent" && len(names) != 6 {
		t.Errorf("tools = %s, want the six", got)
	}
	for _, name := range names {
		res, err := b.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("%s: protocol error %v", name, err)
		}
		tc, ok := res.Content[0].(*mcp.TextContent)
		if !res.IsError || len(res.Content) != 1 || !ok || strings.Count(tc.Text, ".") != 1 {
			t.Errorf("%s offline = %+v, want isError with one sentence", name, res)
		}
	}
}

func TestOfflineWhenRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	b := start(t, mcpbridge.Config{URL: url, Team: "lime", Token: secret})
	if !strings.Contains(b.InitializeResult().Instructions, "Memory is unreachable") {
		t.Errorf("instructions = %q", b.InitializeResult().Instructions)
	}
}

func TestStdoutOnlyFrames(t *testing.T) {
	r := newRemote(t)
	b := start(t, r.config())
	if _, err := b.CallTool(t.Context(), &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "tracker secret-query-text"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ListTools(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(b.stdout.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("stdout has %d lines, want the initialize, list and call replies", len(lines))
	}
	for _, line := range lines {
		var f struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal([]byte(line), &f); err != nil || f.JSONRPC != "2.0" {
			t.Errorf("stdout line is not an MCP frame: %q", line)
		}
	}
	logs := b.logs.String()
	if logs == "" {
		t.Error("no debug log lines written")
	}
	for _, leak := range []string{"secret-query-text", secret, "never atc"} {
		if strings.Contains(logs, leak) {
			t.Errorf("log holds %q: %s", leak, logs)
		}
	}
}

// TestRefusesRedirects: a registry that answers with a redirect never gets
// its target asked, so the memory token stays where it was sent.
func TestRefusesRedirects(t *testing.T) {
	hit := make(chan string, 4)
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { hit <- r.Header.Get("Authorization") }))
	defer other.Close()
	from := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer from.Close()

	b := start(t, mcpbridge.Config{URL: from.URL, Team: "lime", Token: secret, ConnectTimeout: time.Second})
	if !strings.Contains(b.InitializeResult().Instructions, "Memory is unreachable") {
		t.Errorf("instructions after a redirect = %q, want the offline ones", b.InitializeResult().Instructions)
	}
	select {
	case got := <-hit:
		t.Errorf("the redirect target was contacted (Authorization %q)", got)
	default:
	}
}

// TestRefusesPlainHTTP: a memory token is never sent over http, except to
// this machine.
func TestRefusesPlainHTTP(t *testing.T) {
	for _, tc := range []struct {
		url  string
		fail bool
	}{
		{"http://registry.example.com", true},
		{"http://10.0.4.12", true},
		{"https://registry.example.com", false},
		{"http://127.0.0.1:7420", false},
		{"http://localhost:7420", false},
		{"http://[::1]:7420", false},
	} {
		_, err := mcpbridge.New(mcpbridge.Config{URL: tc.url, Team: "lime", Token: secret})
		if (err != nil) != tc.fail {
			t.Errorf("New(%s) = %v, want failure %v", tc.url, err, tc.fail)
		}
	}
}

func TestNewChecksConfig(t *testing.T) {
	ok := mcpbridge.Config{URL: "https://r.example.com", Team: "lime", Token: secret}
	if _, err := mcpbridge.New(ok); err != nil {
		t.Fatalf("New(valid) = %v", err)
	}
	for name, mod := range map[string]func(*mcpbridge.Config){
		"no team":  func(c *mcpbridge.Config) { c.Team = "" },
		"bad team": func(c *mcpbridge.Config) { c.Team = "a/b" },
		"no token": func(c *mcpbridge.Config) { c.Token = "" },
		"bad url":  func(c *mcpbridge.Config) { c.URL = "ftp://x" },
	} {
		c := ok
		mod(&c)
		if _, err := mcpbridge.New(c); err == nil {
			t.Errorf("%s: New = nil error, want one", name)
		}
	}
}
