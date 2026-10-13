package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tests that need a real process build the binary once and remove it
// when the package's tests end.
var (
	binOnce  sync.Once
	binPath  string
	errBuild error
	binDir   string
)

func TestMain(m *testing.M) {
	// A test that offers memory to Claude Code runs the claude on the PATH.
	// Without this a developer's own claude would add servers to their real
	// config (CLAUDE_CONFIG_DIR wins over the test home).
	_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
	stubDir := ""
	if runtime.GOOS != "windows" {
		if dir, err := os.MkdirTemp("", "loadout-claude-"); err == nil {
			stubDir = dir
			if os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755) == nil { //nolint:gosec // a test program that must run
				_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
		}
	}
	code := m.Run()
	if stubDir != "" {
		_ = os.RemoveAll(stubDir)
	}
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// loadoutBinary returns the path of a built loadout.
func loadoutBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		binDir, errBuild = os.MkdirTemp("", "loadout-bin-")
		if errBuild != nil {
			return
		}
		binPath = filepath.Join(binDir, "loadout")
		out, err := exec.CommandContext(context.Background(), "go", "build", "-o", binPath, ".").CombinedOutput()
		if err != nil {
			errBuild = &buildError{err: err, out: string(out)}
		}
	})
	if errBuild != nil {
		t.Fatalf("build loadout: %v", errBuild)
	}
	return binPath
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

// memoryClient is an MCP client on a team's memory, over HTTP or through
// `loadout mcp` on stdio.
type memoryClient struct {
	*mcp.ClientSession
}

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// overHTTP connects straight to the endpoint with the memory token.
func overHTTP(t *testing.T, base, team, token string) *memoryClient {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "http-client", Version: "1"}, nil)
	cs, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   base + "/mcp/t/" + team,
		HTTPClient: &http.Client{Transport: bearerRT{token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect over HTTP: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &memoryClient{cs}
}

// overBridge starts the real binary as `loadout mcp --team TEAM` with p's home.
func overBridge(t *testing.T, p *person, team string) *memoryClient {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), loadoutBinary(t), "mcp", "--team", team) //nolint:gosec // the binary is the one this test built
	cmd.Env = []string{"HOME=" + p.home, "PATH=" + os.Getenv("PATH")}
	cmd.Dir = p.wd
	c := mcp.NewClient(&mcp.Implementation{Name: "bridge-client", Version: "1"}, nil)
	cs, err := c.Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect through loadout mcp: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &memoryClient{cs}
}

// call runs a tool and returns the result as JSON bytes. A tool error is a
// result with isError, so only a protocol failure stops the test.
func (m *memoryClient) call(t *testing.T, tool string, args map[string]any) string {
	t.Helper()
	res, err := m.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// toolNames lists the tools the server offers, in the order sent.
func (m *memoryClient) toolNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for tool, err := range m.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	return names
}

var digits = regexp.MustCompile(`\d+`)

// alike hides what differs between two runs of the same call by two clients:
// the word that names the entry, and every number (ids, versions).
func alike(s, word string) string {
	s = strings.ReplaceAll(strings.ToLower(s), strings.ToLower(word), "WORD")
	return digits.ReplaceAllString(s, "#")
}

// firstID reads the id of the memory in a tool result.
func firstID(t *testing.T, result string) int {
	t.Helper()
	m := regexp.MustCompile(`id=\\"(\d+)\\"`).FindStringSubmatch(result)
	if m == nil {
		t.Fatalf("no memory id in %s", result)
	}
	var id int
	if err := json.Unmarshal([]byte(m[1]), &id); err != nil {
		t.Fatal(err)
	}
	return id
}

// memoryTeam is a registry with lime's memory on and a person with a memory
// token stored, ready for a client.
func memoryTeam(t *testing.T) (host, alice *person, base, token string) {
	t.Helper()
	host, base, aliceWeb, _ := ownedRegistry(t)
	alice = &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	alice.must("", "namespace", "set", "lime", "--memory", "on")
	alice.must("", "memory", "token", "create")
	token = memoryTokenRE.FindString(alice.file("memory-credentials.yaml"))
	if token == "" {
		t.Fatal("no memory token stored")
	}
	return host, alice, base, token
}
