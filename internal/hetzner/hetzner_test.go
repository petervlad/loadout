package hetzner

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "hz-test-token-secret" //nolint:gosec // G101: a made-up token for the fake API

// fakeAPI answers like api.hetzner.cloud from the recorded shapes in
// testdata, and remembers what it was asked.
type fakeAPI struct {
	t      *testing.T
	server map[string]any // the server object, changed by tests

	mu       sync.Mutex
	requests []string
	// status, if set, answers every request with it.
	status int
	body   string
	delay  time.Duration
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()
	var doc struct {
		Server map[string]any `json:"server"`
	}
	if err := json.Unmarshal(readFile(t, "server.json"), &doc); err != nil {
		t.Fatal(err)
	}
	f := &fakeAPI{t: t, server: doc.Server}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	status, body, delay := f.status, f.body, f.delay
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(readFile(f.t, "unauthorized.json"))
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return
	}
	one := map[string]any{"server": f.server}
	list := map[string]any{"servers": []any{f.server}, "meta": map[string]any{"pagination": map[string]any{"page": 1, "per_page": 50, "next_page": nil, "last_page": 1, "total_entries": 1}}}
	switch {
	case r.URL.Path == "/servers/167857226":
		_ = json.NewEncoder(w).Encode(one)
	case r.URL.Path == "/servers" && r.URL.Query().Get("name") == "loadout-registry":
		_ = json.NewEncoder(w).Encode(list)
	case r.URL.Path == "/servers" && r.URL.Query().Get("name") != "":
		_, _ = w.Write([]byte(`{"servers": [], "meta": {"pagination": {"page": 1, "per_page": 25, "next_page": null, "last_page": 1, "total_entries": 0}}}`))
	case r.URL.Path == "/servers":
		_ = json.NewEncoder(w).Encode(list)
	case r.URL.Path == "/primary_ips/104851234":
		_, _ = w.Write(readFile(f.t, "primary_ip_ipv4.json"))
	case r.URL.Path == "/primary_ips/104851235":
		_, _ = w.Write(readFile(f.t, "primary_ip_ipv6.json"))
	case r.URL.Path == "/volumes/4711":
		_, _ = w.Write(readFile(f.t, "volume.json"))
	case r.URL.Path == "/pricing":
		_, _ = w.Write(readFile(f.t, "pricing.json"))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": {"code": "not_found", "message": "server with ID '1' not found", "details": {}}}`))
	}
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// clock is a settable Now.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newClient(t *testing.T, base, server string, mod func(*Config)) (*Client, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	cfg := Config{
		Token: testToken, Server: server, BaseURL: base, Now: clk.now,
		LocalIPs: func(context.Context) []netip.Addr { return []netip.Addr{netip.MustParseAddr("203.0.113.7")} },
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, clk
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestSummary(t *testing.T) {
	for _, server := range []string{"167857226", "loadout-registry", ""} {
		t.Run("server "+server, func(t *testing.T) {
			_, srv := newFakeAPI(t)
			c, _ := newClient(t, srv.URL, server, nil)
			s, err := c.Summary(t.Context())
			if err != nil {
				t.Fatalf("Summary() error: %v", err)
			}
			got := s.Server
			if got.ID != 167857226 || got.Name != "loadout-registry" || got.Status != "running" || got.Type.Name != "cx23" ||
				got.Type.Cores != 2 || got.Type.MemoryGB != 4 || got.Type.DiskGB != 40 || got.Location.Name != "nbg1" ||
				got.Location.City != "Nuremberg" || got.Backups || got.IPv4 != "203.0.113.7" {
				t.Errorf("Server = %+v", got)
			}
			if !got.TrafficKnown || got.OutgoingBytes != 1843289600 || got.IncludedBytes != 21990232555520 {
				t.Errorf("traffic = %v %d of %d", got.TrafficKnown, got.OutgoingBytes, got.IncludedBytes)
			}
			if len(s.PrimaryIPs) != 2 || s.PrimaryIPs[0].Type != "ipv4" || s.PrimaryIPs[1].Location != "nbg1" {
				t.Errorf("PrimaryIPs = %+v", s.PrimaryIPs)
			}
			if len(s.Volumes) != 0 {
				t.Errorf("Volumes = %+v, want none", s.Volumes)
			}
			cost := s.Cost
			if cost.Currency != "EUR" || cost.VATRate != "19" || len(cost.Lines) != 2 || !near(cost.Total, 4.1531+0.595) {
				t.Errorf("Cost = %+v, want the server and its IPv4, 4.7481 EUR", cost)
			}
			if !s.CheckedAt.Equal(time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)) {
				t.Errorf("CheckedAt = %v", s.CheckedAt)
			}
		})
	}
}

func TestSummaryBackupsAndVolumes(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.server["backup_window"] = "22-02"
	f.server["volumes"] = []int{4711}
	c, _ := newClient(t, srv.URL, "167857226", nil)
	s, err := c.Summary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Server.Backups || len(s.Volumes) != 1 || s.Volumes[0].SizeGB != 10 || s.Volumes[0].Name != "registry-data" {
		t.Fatalf("Summary() = %+v", s)
	}
	want := 4.1531 + 4.1531*0.2 + 10*0.05236 + 0.595
	if !near(s.Cost.Total, want) {
		t.Errorf("Total = %v, want %v (lines %+v)", s.Cost.Total, want, s.Cost.Lines)
	}
}

func TestSummaryFailures(t *testing.T) {
	tests := []struct {
		name   string
		server string
		token  string
		status int
		body   string
		ips    []netip.Addr
		want   string
	}{
		{name: "bad token", server: "167857226", token: "wrong", want: "Hetzner refused the token"},
		{name: "unknown id", server: "1", want: "No server with id 1"},
		{name: "unknown name", server: "nope", want: "No server named nope"},
		{name: "not this machine", ips: []netip.Addr{netip.MustParseAddr("203.0.113.99")}, want: "Set LOADOUT_HETZNER_SERVER"},
		{name: "forbidden", server: "167857226", status: http.StatusForbidden, body: `{"error":{"code":"forbidden","message":"insufficient permissions"}}`, want: "may not read"},
		{name: "rate limited", server: "167857226", status: http.StatusTooManyRequests, body: `{"error":{"code":"rate_limit_exceeded","message":"limit"}}`, want: "Too many requests"},
		{name: "server error", server: "167857226", status: http.StatusServiceUnavailable, body: `oops`, want: "it answered 503"},
		{name: "broken json", server: "167857226", status: http.StatusOK, body: `{"server": [`, want: "could not be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, srv := newFakeAPI(t)
			f.status, f.body = tt.status, tt.body
			c, _ := newClient(t, srv.URL, tt.server, func(cfg *Config) {
				if tt.token != "" {
					cfg.Token = tt.token
				}
				if tt.ips != nil {
					cfg.LocalIPs = func(context.Context) []netip.Addr { return tt.ips }
				}
			})
			_, err := c.Summary(t.Context())
			if err == nil {
				t.Fatal("Summary() error = nil, want one")
			}
			if got := Reason(err); !strings.Contains(got, tt.want) {
				t.Errorf("Reason() = %q, want it to contain %q", got, tt.want)
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(Reason(err), testToken) {
				t.Errorf("error %q shows the token", err)
			}
		})
	}
}

func TestSummaryTimesOut(t *testing.T) {
	f, srv := newFakeAPI(t)
	f.delay = 2 * time.Second
	c, _ := newClient(t, srv.URL, "167857226", func(cfg *Config) { cfg.HTTP = &http.Client{Timeout: 50 * time.Millisecond} })
	start := time.Now()
	_, err := c.Summary(t.Context())
	if err == nil || !strings.Contains(Reason(err), "did not answer within 5 seconds") {
		t.Errorf("Summary() = %v (%q), want a timeout", err, Reason(err))
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Summary() took %v, want it to give up quickly", d)
	}
}

func TestSummaryIsCached(t *testing.T) {
	f, srv := newFakeAPI(t)
	c, clk := newClient(t, srv.URL, "167857226", nil)
	if _, err := c.Summary(t.Context()); err != nil {
		t.Fatal(err)
	}
	n := f.count()
	clk.add(4 * time.Minute)
	if _, err := c.Summary(t.Context()); err != nil || f.count() != n {
		t.Errorf("second Summary() within 5 minutes made %d requests, want none (err %v)", f.count()-n, err)
	}
	clk.add(2 * time.Minute)
	if _, err := c.Summary(t.Context()); err != nil || f.count() == n {
		t.Errorf("Summary() after 6 minutes made no request, want a refresh (err %v)", err)
	}

	// A failure is kept for a shorter while.
	f.status, f.body = http.StatusServiceUnavailable, "down"
	clk.add(6 * time.Minute)
	if _, err := c.Summary(t.Context()); err == nil {
		t.Fatal("Summary() while the API is down = nil error")
	}
	n = f.count()
	f.status = 0
	if _, err := c.Summary(t.Context()); err == nil || f.count() != n {
		t.Errorf("Summary() right after a failure = %v with %d requests, want the cached failure", err, f.count()-n)
	}
	clk.add(31 * time.Second)
	if _, err := c.Summary(t.Context()); err != nil {
		t.Errorf("Summary() 31 s after a failure = %v, want a fresh answer", err)
	}
}

// TestSummaryCachesAfterCancel checks that a page load that gives up does
// not waste the refresh: it finishes and is cached.
func TestSummaryCachesAfterCancel(t *testing.T) {
	f, srv := newFakeAPI(t)
	c, _ := newClient(t, srv.URL, "167857226", nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Summary(ctx); err != nil {
		t.Fatalf("Summary(cancelled ctx) error: %v", err)
	}
	n := f.count()
	if _, err := c.Summary(t.Context()); err != nil || f.count() != n {
		t.Errorf("Summary() after it = %v with %d requests, want the cached answer", err, f.count()-n)
	}
}

func TestNeverFollowsRedirects(t *testing.T) {
	var leaked sync.Map
	elsewhere := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked.Store("auth", r.Header.Get("Authorization"))
	}))
	t.Cleanup(elsewhere.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/pricing", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	c, _ := newClient(t, redirect.URL, "167857226", nil)
	if _, err := c.Summary(t.Context()); err == nil {
		t.Error("Summary() through a redirect = nil error, want one")
	}
	if v, ok := leaked.Load("auth"); ok {
		t.Errorf("the redirect target got a request (Authorization %q)", v)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name, token, base string
		ok                bool
	}{
		{"default URL", "t", "", true},
		{"the API", "t", "https://api.hetzner.cloud/v1/", true},
		{"loopback for tests", "t", "http://127.0.0.1:7938", true},
		{"no token", " ", "", false},
		{"another host", "t", "https://evil.example/v1", false},
		{"a name, not a loopback address", "t", "http://localhost:7938", false},
		{"plain http elsewhere", "t", "http://10.0.0.1/v1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Config{Token: tt.token, BaseURL: tt.base})
			if (err == nil) != tt.ok {
				t.Errorf("New(%q) error = %v, want ok %v", tt.base, err, tt.ok)
			}
			if err != nil && strings.Contains(err.Error(), tt.token) && tt.token != "t" && tt.token != " " {
				t.Errorf("error %q shows the token", err)
			}
		})
	}
}
