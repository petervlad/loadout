package server_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// initBody is the first message of an MCP session.
const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

// mcpPost sends one JSON-RPC message to a team's endpoint and returns the
// status and body.
func (f *memFixture) mcpPost(t *testing.T, token, ns string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp/t/"+ns, strings.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func (f *memFixture) switchWeb(t *testing.T, on string) {
	t.Helper()
	if code, body := f.call(t, f.tokens["maintainer"], http.MethodPatch, "/v1/namespaces/web", `{"memory":`+on+`}`); code != http.StatusOK {
		t.Fatalf("switch memory %s = %d %s", on, code, body)
	}
}

// TestMemoryRoutes404WhileOff: criterion 1. While a namespace's switch is
// off its MCP endpoint answers 404 to a person who would be allowed, and the
// spaces list leaves it out. Turning it on opens both.
func TestMemoryRoutes404WhileOff(t *testing.T) {
	f := buildMemFixture(t, true, true)
	tok := f.makeToken(t, "member").Token

	code, offBody := f.mcpPost(t, tok, "web")
	if code != http.StatusNotFound {
		t.Fatalf("MCP while off = %d %s, want 404", code, offBody)
	}
	if code, body := f.call(t, f.tokens["member"], http.MethodGet, "/v1/memory/spaces", ""); code != http.StatusOK || strings.Contains(body, "web") {
		t.Errorf("spaces while off = %d %s, want 200 without web", code, body)
	}

	f.switchWeb(t, "true")
	if code, body := f.mcpPost(t, tok, "web"); code != http.StatusOK {
		t.Fatalf("MCP while on = %d %s, want 200", code, body)
	}
	if _, body := f.call(t, f.tokens["member"], http.MethodGet, "/v1/memory/spaces", ""); !strings.Contains(body, "/mcp/t/web") {
		t.Errorf("spaces while on = %s, want the web endpoint", body)
	}

	f.switchWeb(t, "false")
	if code, body := f.mcpPost(t, tok, "web"); code != http.StatusNotFound || body != offBody {
		t.Errorf("MCP after off again = %d %s, want the same 404 as before", code, body)
	}
}

// TestMCPTakesMemoryTokensOnly: criterion 2, the other direction. Only a
// memory token opens the endpoint. A login token, a team token, a machine
// token, none and garbage are all refused with one body.
func TestMCPTakesMemoryTokensOnly(t *testing.T) {
	f := buildMemFixture(t, true, true)
	f.switchWeb(t, "true")
	mem := f.makeToken(t, "member").Token
	if code, body := f.mcpPost(t, mem, "web"); code != http.StatusOK {
		t.Fatalf("memory token = %d %s, want 200", code, body)
	}
	var first string
	for name, tok := range map[string]string{
		"account token": f.tokens["member"],
		"team token":    f.team,
		"machine token": f.machine,
		"none":          "",
		"garbage":       "ldm_" + strings.Repeat("a", 52),
	} {
		code, body := f.mcpPost(t, tok, "web")
		if code != http.StatusUnauthorized {
			t.Errorf("%s = %d %s, want 401", name, code, body)
		}
		if first == "" {
			first = body
		} else if body != first {
			t.Errorf("%s body = %q, want the same as the others %q", name, body, first)
		}
	}
}

// TestMCPHidesWhoIsAMember: criterion 3. A person who is not in the team and
// a team that does not exist get the same status and bytes, and so does a
// member while the switch is off.
func TestMCPHidesWhoIsAMember(t *testing.T) {
	f := buildMemFixture(t, true, true)
	stranger := f.makeToken(t, "stranger").Token
	member := f.makeToken(t, "member").Token

	offCode, off := f.mcpPost(t, member, "web")
	f.switchWeb(t, "true")

	code, outsider := f.mcpPost(t, stranger, "web")
	if code != http.StatusNotFound {
		t.Fatalf("non-member = %d %s, want 404", code, outsider)
	}
	if _, ghost := f.mcpPost(t, stranger, "nowhere"); ghost != outsider {
		t.Errorf("unknown team body = %q, want the non-member's %q", ghost, outsider)
	}
	if _, ghost := f.mcpPost(t, member, "nowhere"); ghost != outsider {
		t.Errorf("unknown team for a member = %q, want %q", ghost, outsider)
	}
	if offCode != code || off != outsider {
		t.Errorf("switch off = %d %q, want the same as a non-member %d %q", offCode, off, code, outsider)
	}
	if code, body := f.mcpPost(t, member, "web"); code != http.StatusOK || body == outsider {
		t.Errorf("member = %d %s, want 200 and a different body", code, body)
	}
}

// TestMCPHasSixTools: criterion 5, through a real client over Streamable
// HTTP, and criteria 3 and 4 in use: a member saves and finds it again, and
// the entry names its namespace.
func TestMCPHasSixTools(t *testing.T) {
	f := buildMemFixture(t, true, true)
	f.switchWeb(t, "true")
	srv := httptest.NewServer(f.h)
	t.Cleanup(srv.Close)

	tok := f.makeToken(t, "member").Token
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp/t/web",
		HTTPClient: &http.Client{Transport: bearer{tok}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	if ins := cs.InitializeResult().Instructions; !strings.Contains(ins, "Team memory for web") {
		t.Errorf("instructions = %q, want them to name the team", ins)
	}
	var names []string
	for tool, err := range cs.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if want := []string{"get", "recent", "retract", "save", "search", "update"}; !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}

	save, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "save", Arguments: map[string]any{
		"type": "reference", "name": "Build host", "description": "Where the nightly build runs", "body": "ci-7",
	}})
	if err != nil || save.IsError {
		t.Fatalf("save = %+v, %v", save, err)
	}
	found, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "nightly"}})
	if err != nil || found.IsError {
		t.Fatalf("search = %+v, %v", found, err)
	}
	out := found.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(out, `namespace="web"`) || !strings.Contains(out, "Build host") {
		t.Errorf("search found %q, want the saved entry with its namespace", out)
	}
}

// bearer adds a token to every request.
type bearer struct{ token string }

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}
