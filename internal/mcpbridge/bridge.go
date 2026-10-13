// Package mcpbridge is the stdio MCP server that a person's AI tool launches
// to reach a team memory. It forwards every call to the registry's Streamable
// HTTP endpoint with the person's memory token and trusts the registry's CA.
// It stores nothing and keeps no state beyond the connection. When the
// registry cannot be reached at start it still serves the six tools, each
// answering with one sentence, so a tool never fails to start because the VPN
// is down.
package mcpbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/mcpmemory"
)

const (
	// MaxLineLength bounds one inbound frame from the tool.
	MaxLineLength = 1 << 20
	// ConnectTimeout is how long the start waits for the registry.
	ConnectTimeout = 5 * time.Second

	// unreachable is the answer of every call while offline.
	unreachable = "Team memory cannot be reached right now, so check your connection or the VPN and restart your AI tool."
	// offlineIndex stands where the index would be.
	offlineIndex = "Memory is unreachable, so there is no index. Restart your AI tool when the connection is back."

	// The registry answered, but refused. Telling a person with a revoked or
	// expired token to check the VPN would send them the wrong way.
	tokenRefused = "Your memory token was refused, so make a new one with loadout memory token create and restart your AI tool."
	memoryGone   = "Team memory is off for this team or you are no longer a member, so ask a maintainer and restart your AI tool."
)

// Config says what to connect to. URL, Team and Token are required.
type Config struct {
	URL   string // the registry, for example https://registry.example.com
	Team  string // the namespace whose memory is served
	Token string // the memory token, sent as a bearer
	// CAFile adds PEM certificates to the roots, or is empty.
	CAFile string
	// Log gets debug lines: tool names and timings, never arguments or
	// results. Nil discards.
	Log *slog.Logger
	// Stdin and Stdout carry the MCP frames. Both nil means the process's
	// own, which is what a tool launching the bridge uses.
	Stdin  io.Reader
	Stdout io.Writer
	// ConnectTimeout overrides the 5 second wait at start, for tests.
	ConnectTimeout time.Duration
}

// Bridge is one stdio server for one team memory.
type Bridge struct {
	cfg      Config
	endpoint string
	http     *http.Client
	log      *slog.Logger
	// refused is the last 401 or 404 the registry sent, or 0.
	refused *atomic.Int32
}

// New checks cfg and reads the CA file, so a mistake shows before any frame
// is sent.
func New(cfg Config) (*Bridge, error) {
	if cfg.Team == "" || strings.ContainsAny(cfg.Team, "/?#") {
		return nil, errors.New("memory bridge needs a team name")
	}
	if cfg.Token == "" {
		return nil, errors.New("memory bridge needs a memory token: make one on your account page")
	}
	// The token goes to this address in every request, so it must be https
	// unless the registry runs on this machine.
	if err := client.CheckMemoryURL(cfg.URL); err != nil {
		return nil, fmt.Errorf("memory bridge: %w", err)
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("memory bridge: registry url %q: %w", cfg.URL, err)
	}
	hc, err := client.HTTPClient(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("memory bridge: %w", err)
	}
	refused := new(atomic.Int32)
	hc.Transport = bearer{token: cfg.Token, next: hc.Transport, refused: refused}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = ConnectTimeout
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Bridge{
		cfg:      cfg,
		endpoint: strings.TrimRight(u.String(), "/") + "/mcp/t/" + url.PathEscape(cfg.Team),
		http:     hc,
		log:      log,
		refused:  refused,
	}, nil
}

// bearer adds the memory token to every request.
type bearer struct {
	token   string
	next    http.RoundTripper
	refused *atomic.Int32
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	resp, err := b.next.RoundTrip(r)
	if err == nil {
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusNotFound:
			b.refused.Store(int32(resp.StatusCode)) //nolint:gosec // an HTTP status fits
		default:
			b.refused.Store(0)
		}
	}
	return resp, err
}

// unreachableText is what a call answers when the registry cannot serve it:
// why, if the registry said so, else the connection.
func (b *Bridge) unreachableText() string {
	switch b.refused.Load() {
	case http.StatusUnauthorized:
		return tokenRefused
	case http.StatusNotFound:
		return memoryGone
	}
	return unreachable
}

// Run serves until the tool closes stdin or ctx ends. A closed stdin is a
// clean end, not an error.
func (b *Bridge) Run(ctx context.Context) error {
	cs, stop := b.connect(ctx)
	defer stop()
	srv := b.server(ctx, cs)
	b.log.DebugContext(ctx, "memory bridge serving", "team", b.cfg.Team, "online", cs != nil)
	err := srv.Run(ctx, b.transport())
	if err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("serve memory bridge: %w", err)
	}
	return nil
}

// connect returns a session, or nil when the registry did not answer within
// the timeout. The SDK can take longer than its context to give up on a
// server that accepts the connection and never replies, so the wait is
// raced here and a late result is closed. stop ends the session.
func (b *Bridge) connect(ctx context.Context) (cs *mcp.ClientSession, stop func()) {
	cctx, cancel := context.WithCancel(ctx)
	type result struct {
		cs  *mcp.ClientSession
		err error
	}
	got := make(chan result, 1)
	go func() {
		c := mcp.NewClient(&mcp.Implementation{Name: "loadout-memory-bridge", Version: mcpmemory.Version}, &mcp.ClientOptions{Logger: b.log})
		cs, err := c.Connect(cctx, &mcp.StreamableClientTransport{Endpoint: b.endpoint, HTTPClient: b.http, DisableStandaloneSSE: true}, nil)
		got <- result{cs, err}
	}()

	timer := time.NewTimer(b.cfg.ConnectTimeout)
	defer timer.Stop()
	select {
	case r := <-got:
		if r.err == nil {
			return r.cs, func() { _ = r.cs.Close(); cancel() }
		}
		b.log.DebugContext(ctx, "memory unreachable", "team", b.cfg.Team, "error", r.err)
	case <-timer.C:
		b.log.DebugContext(ctx, "memory did not answer in time", "team", b.cfg.Team, "timeout", b.cfg.ConnectTimeout)
		go func() {
			if r := <-got; r.cs != nil {
				_ = r.cs.Close()
			}
		}()
	case <-ctx.Done():
	}
	cancel()
	return nil, func() {}
}

// server builds the stdio server: the remote's instructions and tools when
// online, the fixed ones when not.
func (b *Bridge) server(ctx context.Context, cs *mcp.ClientSession) *mcp.Server {
	var instructions string
	var tools []*mcp.Tool
	if cs != nil {
		if init := cs.InitializeResult(); init != nil {
			instructions = init.Instructions
		}
		for t, err := range cs.Tools(ctx, nil) {
			if err != nil {
				b.log.DebugContext(ctx, "memory tool list failed", "error", err)
				cs, tools = nil, nil
				break
			}
			tools = append(tools, t)
		}
	}
	if cs == nil {
		instructions = mcpmemory.Instructions(b.cfg.Team, "", offlineIndex)
		tools = mcpmemory.Tools()
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "loadout-memory", Version: mcpmemory.Version}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       b.log,
	})
	for _, t := range tools {
		srv.AddTool(t, b.forward(cs))
	}
	return srv
}

// forward returns the handler of one tool: the call goes to the registry as
// it came and the result comes back unchanged.
func (b *Bridge) forward(cs *mcp.ClientSession) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.Params.Name
		if cs == nil {
			b.log.DebugContext(ctx, "memory call refused offline", "tool", name)
			return failure(b.unreachableText()), nil
		}
		start := time.Now()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: req.Params.Arguments})
		if err != nil {
			b.log.DebugContext(ctx, "memory call failed", "tool", name, "error", err, "took", time.Since(start))
			return failure(b.unreachableText()), nil
		}
		b.log.DebugContext(ctx, "memory call", "tool", name, "is_error", res.IsError, "took", time.Since(start))
		return res, nil
	}
}

func failure(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func (b *Bridge) transport() mcp.Transport {
	if b.cfg.Stdin == nil && b.cfg.Stdout == nil {
		return &mcp.StdioTransport{MaxLineLength: MaxLineLength}
	}
	in, out := b.cfg.Stdin, b.cfg.Stdout
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	return &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopCloser{out}, MaxLineLength: MaxLineLength}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
