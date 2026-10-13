package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/config"
)

var memoryTokenRE = regexp.MustCompile(`ldm_[a-z2-7]{52}`)

// getAs sends a GET to the registry with a bearer token and returns the
// status and body.
func getAs(t *testing.T, base, path, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// TestMemoryTokenCreate: a person makes memory tokens on the web and with the
// CLI. The CLI stores its token for the bridge, the web shows its token once,
// and neither works anywhere but on the memory endpoints.
func TestMemoryTokenCreate(t *testing.T) {
	_, base, aliceWeb, _ := ownedRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}

	// CLI: stored, not printed.
	out := alice.must("", "memory", "token", "create", "--label", "work laptop")
	stored := alice.file("memory-credentials.yaml")
	cliSecret := memoryTokenRE.FindString(stored)
	if cliSecret == "" {
		t.Fatalf("memory-credentials.yaml has no token:\n%s", stored)
	}
	if !strings.Contains(out, "Made memory token") || !strings.Contains(out, "(work laptop)") || strings.Contains(out, cliSecret) {
		t.Errorf("create printed %q, want a summary without the secret", out)
	}
	fi, err := os.Stat(filepath.Join(alice.home, ".loadout", "memory-credentials.yaml"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("memory-credentials.yaml = %v, %v, want mode 600", fi, err)
	}
	if strings.Contains(alice.file("credentials.yaml"), cliSecret) {
		t.Error("the memory token was written to credentials.yaml")
	}

	// Only the memory endpoints take it.
	for _, path := range []string{"/v1/packages", "/v1/me", "/v1/layers", "/v1/memory/spaces", "/v1/memory/tokens"} {
		if status, _ := getAs(t, base, path, cliSecret); status != http.StatusUnauthorized {
			t.Errorf("GET %s with a memory token = %d, want 401", path, status)
		}
	}

	// A second create replaces the stored token and can print it once.
	out = alice.must("", "memory", "token", "create", "--show", "--label", "second")
	shown := memoryTokenRE.FindString(out)
	if shown == "" || shown == cliSecret || !strings.Contains(out, "replaces the memory token stored before") {
		t.Errorf("create --show = %q, want a new secret and the note about the old one", out)
	}
	if got := memoryTokenRE.FindString(alice.file("memory-credentials.yaml")); got != shown {
		t.Errorf("stored token = %q, want the new one %q", got, shown)
	}

	// Web: shown once on the page that answers, then only listed.
	aliceWeb.send(http.MethodGet, "/account", nil)
	status, _, page := aliceWeb.send(http.MethodPost, "/account/memory-tokens", url.Values{"label": {"from the web"}})
	webSecret := memoryTokenRE.FindString(page)
	if status != http.StatusOK || webSecret == "" || webSecret == cliSecret || webSecret == shown {
		t.Fatalf("web create = %d, want 200 and a new secret\n%s", status, page)
	}
	_, _, page = aliceWeb.send(http.MethodGet, "/account", nil)
	if strings.Contains(page, webSecret) || !strings.Contains(page, "from the web") || !strings.Contains(page, "work laptop") {
		t.Errorf("account page after create should list the tokens without secrets:\n%s", page)
	}

	// The CLI sees all three, and revokes one.
	list := alice.must("", "memory", "token", "list")
	for _, want := range []string{"work laptop", "second", "from the web"} {
		if !strings.Contains(list, want) {
			t.Errorf("memory token list lacks %q:\n%s", want, list)
		}
	}
	id := regexp.MustCompile(`(?m)^([0-9a-f]{12})\s+work laptop`).FindStringSubmatch(list)
	if id == nil {
		t.Fatalf("no id for work laptop in\n%s", list)
	}
	if out := alice.must("", "memory", "token", "revoke", id[1]); !strings.Contains(out, "Revoked memory token "+id[1]) {
		t.Errorf("revoke = %q", out)
	}
	if list := alice.must("", "memory", "token", "list"); strings.Contains(list, "work laptop") || !strings.Contains(list, "second") {
		t.Errorf("list after revoke:\n%s", list)
	}
	if _, errOut, code := alice.runIn("", "memory", "token", "revoke", id[1]); code == 0 || !strings.Contains(errOut, "not found") {
		t.Errorf("revoke twice: exit %d, stderr %q, want not found", code, errOut)
	}
}

// TestMemoryTokenCommandsNeedALogin: a machine token or no login at all gets
// a plain sentence, not a stack of HTTP.
func TestMemoryTokenCommandsNeedALogin(t *testing.T) {
	admin := &person{session: newSession(t)}
	base := startServer(t, admin)
	machine := createToken(t, admin, "publish")
	p := &person{session: newSession(t)}
	if _, errOut, code := p.runIn("", "memory", "token", "list"); code == 0 || !strings.Contains(errOut, "no remote") {
		t.Errorf("list with no remote: exit %d, stderr %q", code, errOut)
	}
	p.must(machine+"\n", "remote", "add", "plain", base, "--token-stdin")
	if _, errOut, code := p.runIn("", "memory", "token", "create"); code == 0 || errOut == "" {
		t.Errorf("create with a machine token (and a registry without memory): exit %d, stderr %q, want an error", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(p.home, ".loadout", "memory-credentials.yaml")); err == nil {
		t.Error("a failed create left a memory-credentials.yaml")
	}
}

// storedLoginToken reads the login token a person's loadout login stored.
func storedLoginToken(t *testing.T, p *person) string {
	t.Helper()
	tok := tokenRE.FindString(p.file("credentials.yaml"))
	if tok == "" {
		t.Fatal("no login token in credentials.yaml")
	}
	return tok
}

// TestNamespaceSetMemory: maintainers and owners switch team memory over
// HTTP, and the spaces list follows.
func TestNamespaceSetMemory(t *testing.T) {
	host, base, aliceWeb, olgaWeb := ownedRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	olga := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, olga, olgaWeb, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}
	spaces := func(p *person) []api.MemorySpace {
		t.Helper()
		status, body := getAs(t, base, "/v1/memory/spaces", storedLoginToken(t, p))
		var out api.MemorySpacesResponse
		if err := json.Unmarshal([]byte(body), &out); status != http.StatusOK || err != nil {
			t.Fatalf("GET /v1/memory/spaces = %d %s, %v", status, body, err)
		}
		return out.Spaces
	}
	if got := spaces(alice); len(got) != 0 {
		t.Fatalf("spaces before the switch = %+v, want none", got)
	}

	// Alice maintains lime, so she may. She is only a member of web.
	if out := alice.must("", "namespace", "set", "lime", "--memory", "on"); !strings.Contains(out, "lime now has team memory") {
		t.Errorf("set --memory on = %q", out)
	}
	if _, errOut, code := alice.runIn("", "namespace", "set", "web", "--memory", "on"); code == 0 || !strings.Contains(errOut, "only owners and maintainers") {
		t.Errorf("member turns memory on: exit %d, stderr %q", code, errOut)
	}
	got := spaces(alice)
	if len(got) != 1 || got[0].Namespace != "lime" || got[0].Endpoint != publicURL+"/mcp/t/lime" {
		t.Errorf("spaces after the switch = %+v, want lime at %s/mcp/t/lime", got, publicURL)
	}
	if got := spaces(olga); len(got) != 1 || got[0].Namespace != "lime" {
		t.Errorf("owner's spaces = %+v, want lime", got)
	}

	// Olga owns acme, so she may turn the project's memory too, and lime's off.
	olga.must("", "namespace", "set", "acme", "--memory", "on")
	if out := olga.must("", "namespace", "set", "lime", "--memory", "off"); !strings.Contains(out, "lime's team memory is off, and what was saved is kept") {
		t.Errorf("set --memory off = %q", out)
	}
	if got := spaces(alice); len(got) != 1 || got[0].Namespace != "acme" || got[0].Kind != "project" {
		t.Errorf("spaces with only the project on = %+v, want acme", got)
	}

	if _, errOut, code := alice.runIn("", "namespace", "set", "lime", "--memory", "maybe"); code == 0 || !strings.Contains(errOut, `--memory "maybe": want on or off`) {
		t.Errorf("bad value: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := alice.runIn("", "namespace", "set", "ghost", "--memory", "on"); code == 0 || !strings.Contains(errOut, "No team memory here") {
		t.Errorf("unknown namespace: exit %d, stderr %q", code, errOut)
	}
	// On the host the operator is nobody's role, so the sentence says where to go.
	if _, errOut, code := host.runIn("", "namespace", "set", "lime", "--memory", "on"); code == 0 || !strings.Contains(errOut, "--remote") {
		t.Errorf("operator turns memory on: exit %d, stderr %q, want a pointer to --remote", code, errOut)
	}
}

// TestBridgeMatchesHTTP: a client using `loadout mcp --team lime` sees the
// same tools, instructions and results as one using the endpoint directly.
// Each client saves, searches, updates and retracts a memory of its own, the
// results are equal once the client's own word and the numbers are hidden,
// and the calls that read the shared state are equal byte for byte.
func TestBridgeMatchesHTTP(t *testing.T) {
	_, alice, base, token := memoryTeam(t)
	direct := overHTTP(t, base, "lime", token)
	bridge := overBridge(t, alice, "lime")

	if a, b := direct.toolNames(t), bridge.toolNames(t); strings.Join(a, ",") != strings.Join(b, ",") || len(a) != 6 {
		t.Fatalf("tools over HTTP %v, through the bridge %v, want the same six", a, b)
	}
	if a, b := direct.InitializeResult().Instructions, bridge.InitializeResult().Instructions; a != b || !strings.Contains(a, "Team memory for lime") {
		t.Errorf("instructions differ:\nHTTP:   %q\nbridge: %q", a, b)
	}

	type step struct {
		tool string
		args func(word string, id int) map[string]any
	}
	steps := []step{
		{"save", func(w string, _ int) map[string]any {
			return map[string]any{"type": "reference", "name": w + " runner", "description": "where " + w + " jobs run", "body": "host ci-" + w}
		}},
		{"search", func(w string, _ int) map[string]any { return map[string]any{"query": w} }},
		{"update", func(w string, id int) map[string]any {
			return map[string]any{"id": id, "body": "host ci-" + w + "-2", "note": "moved"}
		}},
		{"get", func(_ string, id int) map[string]any { return map[string]any{"id": id} }},
	}
	run := func(c *memoryClient, word string) []string {
		var out []string
		id := 0
		for _, s := range steps {
			res := c.call(t, s.tool, s.args(word, id))
			if strings.Contains(res, `"isError":true`) {
				t.Fatalf("%s as %s failed: %s", s.tool, word, res)
			}
			if s.tool == "save" {
				id = firstID(t, res)
			}
			out = append(out, alike(res, word))
		}
		return out
	}
	viaBridge, viaHTTP := run(bridge, "zephyr"), run(direct, "quokka")
	for i, s := range steps {
		if viaBridge[i] != viaHTTP[i] {
			t.Errorf("%s differs:\nbridge: %s\nHTTP:   %s", s.tool, viaBridge[i], viaHTTP[i])
		}
	}

	// Both entries are there for both clients, and the answers are the same bytes.
	for _, q := range []struct {
		tool string
		args map[string]any
	}{{"search", map[string]any{"query": "runner"}}, {"recent", map[string]any{"days": 1}}} {
		a, b := direct.call(t, q.tool, q.args), bridge.call(t, q.tool, q.args)
		if a != b {
			t.Errorf("%s differs:\nHTTP:   %s\nbridge: %s", q.tool, a, b)
		}
		if !strings.Contains(a, "zephyr runner") || !strings.Contains(a, "quokka runner") {
			t.Errorf("%s = %s, want both entries", q.tool, a)
		}
	}

	// Retract one through each client, then the state is shared again.
	var retracted []string
	for _, c := range []struct {
		client *memoryClient
		word   string
	}{{bridge, "zephyr"}, {direct, "quokka"}} {
		found := c.client.call(t, "search", map[string]any{"query": c.word})
		retracted = append(retracted, alike(c.client.call(t, "retract", map[string]any{"id": firstID(t, found), "reason": "test"}), c.word))
	}
	if retracted[0] != retracted[1] {
		t.Errorf("retract differs:\nbridge: %s\nHTTP:   %s", retracted[0], retracted[1])
	}
	a, b := direct.call(t, "recent", map[string]any{"days": 1}), bridge.call(t, "recent", map[string]any{"days": 1})
	if a != b || strings.Contains(a, "runner") {
		t.Errorf("recent after retracting:\nHTTP:   %s\nbridge: %s\nwant equal and empty", a, b)
	}
}

// TestMemoryFlow is the whole life of a team memory from outside: an agent
// saves through the bridge, a colleague's client finds it over HTTP, updates
// it, then it is retracted. A file is imported, and the host purges it, after
// which no trace is left in the files of the server.
func TestMemoryFlow(t *testing.T) {
	host, alice, base, token := memoryTeam(t)
	bridge := overBridge(t, alice, "lime")
	direct := overHTTP(t, base, "lime", token)
	failed := func(res string) bool { return strings.Contains(res, `"isError":true`) }

	// Save through the bridge, find over HTTP.
	res := bridge.call(t, "save", map[string]any{"type": "reference", "name": "Nightly host", "description": "Where the nightly build runs", "body": "It runs on ci-7."})
	if failed(res) {
		t.Fatalf("save: %s", res)
	}
	id := firstID(t, res)
	if found := direct.call(t, "search", map[string]any{"query": "nightly"}); !strings.Contains(found, "Nightly host") || !strings.Contains(found, `author=\"alice@example.com\"`) {
		t.Fatalf("search over HTTP = %s, want the saved entry with its author", found)
	}

	// Update over HTTP, read through the bridge.
	if res := direct.call(t, "update", map[string]any{"id": id, "body": "It runs on ci-9 now.", "note": "moved"}); failed(res) {
		t.Fatalf("update: %s", res)
	}
	if got := bridge.call(t, "get", map[string]any{"id": id}); !strings.Contains(got, "ci-9 now") || strings.Contains(got, "ci-7") {
		t.Errorf("get after update = %s, want the new body", got)
	}
	if res := direct.call(t, "update", map[string]any{"id": id, "confirm": true}); failed(res) {
		t.Errorf("confirm: %s", res)
	}

	// Retract hides it from search and recent.
	if res := bridge.call(t, "retract", map[string]any{"id": id, "reason": "the host was renamed"}); failed(res) {
		t.Fatalf("retract: %s", res)
	}
	for _, q := range []struct {
		tool string
		args map[string]any
	}{{"search", map[string]any{"query": "nightly"}}, {"recent", map[string]any{"days": 1}}} {
		if got := direct.call(t, q.tool, q.args); strings.Contains(got, "Nightly host") {
			t.Errorf("%s after retract = %s, want the entry gone", q.tool, got)
		}
	}

	// Import one file, and find it.
	const marker = "PURGEME-8841"
	dir := writeMemoryDir(t, map[string]string{"a.md": memoryFile("reference", "Staging login page", "Where staging sign-in lives", "Open "+marker+" on the intranet.")})
	out := alice.must("", "memory", "import", dir, "--team", "lime", "--select", "1")
	m := regexp.MustCompile(`as (\d+)\.`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("import output has no id:\n%s", out)
	}
	if found := direct.call(t, "search", map[string]any{"query": "staging"}); !strings.Contains(found, marker) {
		t.Fatalf("search after import = %s", found)
	}

	// The operator purges it on the host.
	cfg, err := config.Load(func(k string) string { return map[string]string{"HOME": host.home}[k] })
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(cfg.StateDir, "server")
	if out := host.must("", "memory", "purge", "--id", m[1], "--data", data, "--yes"); !strings.Contains(out, "Purged memory "+m[1]) {
		t.Errorf("purge output = %s", out)
	}
	// Running it again for the same id cleans the files again and succeeds.
	if out := host.must("", "memory", "purge", "--id", m[1], "--data", data, "--yes"); !strings.Contains(out, "already purged") ||
		!strings.Contains(out, "index cache for up to 30 seconds") {
		t.Errorf("second purge output = %s", out)
	}
	if found := direct.call(t, "search", map[string]any{"query": "staging"}); strings.Contains(found, marker) {
		t.Errorf("search after purge = %s, want nothing", found)
	}
	if got := direct.call(t, "get", map[string]any{"id": json.Number(m[1])}); !failed(got) {
		t.Errorf("get after purge = %s, want an error", got)
	}
	files, err := filepath.Glob(filepath.Join(data, "memory.db*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("memory files = %v, %v", files, err)
	}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), marker) {
			t.Errorf("%s still holds the purged text", f)
		}
	}
}

// TestMcpNeedsAMemoryToken: without one the bridge says how to make one and
// writes nothing to stdout, which carries only MCP frames.
func TestMcpNeedsAMemoryToken(t *testing.T) {
	_, base, aliceWeb, _ := ownedRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	out, errOut, code := alice.runIn("", "mcp", "--team", "lime")
	if code == 0 || out != "" || !strings.Contains(errOut, "loadout memory token create") {
		t.Errorf("mcp without a token: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, errOut, code := alice.runIn("", "mcp"); code == 0 || !strings.Contains(errOut, "team") {
		t.Errorf("mcp without --team: exit %d, stderr %q", code, errOut)
	}
}
