package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/mcpsetup"
	"github.com/petervlad/loadout/internal/remote"
)

// registryReplying serves GET /v1/memory/spaces with status, and with spaces
// when the status is 200.
func registryReplying(t *testing.T, status int, spaces ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/memory/spaces" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			http.Error(w, `{"error":"no"}`, status)
			return
		}
		var out api.MemorySpacesResponse
		for _, ns := range spaces {
			out.Spaces = append(out.Spaces, api.MemorySpace{Namespace: ns, Endpoint: "http://x/mcp/t/" + ns})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// depsOnServers has one signed-in remote for each server, named r1, r2, ...
func depsOnServers(t *testing.T, servers ...*httptest.Server) Deps {
	t.Helper()
	store := remote.NewStore(t.TempDir(), func(string) string { return "" })
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range servers {
		name := "r" + string(rune('1'+i))
		if err := cfg.Add(remote.Remote{Name: name, URL: s.URL}); err != nil {
			t.Fatal(err)
		}
		if err := store.SetToken(name, "ldt_"+name); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return Deps{Remotes: store}
}

// TestSpacesNameTheirRemoteWhenThereAreSeveral: the entry for a team names the
// registry that listed it once the person has more than one.
func TestSpacesNameTheirRemoteWhenThereAreSeveral(t *testing.T) {
	one := depsOnServers(t, registryReplying(t, http.StatusOK, "lime"))
	spaces, _, _, err := one.memorySpaces(t.Context())
	if err != nil || len(spaces) != 1 || spaces[0].Several || spaces[0].Remote != "r1" {
		t.Errorf("memorySpaces(one registry) = %+v, %v, want r1 and Several false", spaces, err)
	}

	two := depsOnServers(t, registryReplying(t, http.StatusOK, "lime"), registryReplying(t, http.StatusOK, "acme"))
	spaces, asked, _, err := two.memorySpaces(t.Context())
	if err != nil || asked != 2 || len(spaces) != 2 {
		t.Fatalf("memorySpaces(two registries) = %+v, %d, %v", spaces, asked, err)
	}
	for _, sp := range spaces {
		if !sp.Several || sp.Remote == "" {
			t.Errorf("space %+v: want Several and a remote name", sp)
		}
	}
}

// TestPrintMemorySaysTheLimitIsForEveryServer: the output about the
// description limit says plainly that it is not limited to loadout's server.
func TestPrintMemorySaysTheLimitIsForEveryServer(t *testing.T) {
	var out strings.Builder
	printMemory(&out, mcpsetup.Report{Env: []mcpsetup.EnvOutcome{{
		Product: "Claude Code", Where: "~/.claude/settings.json",
		Name: "CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH", Value: "24000", Action: mcpsetup.ActionAdded,
	}}})
	for _, want := range []string{"CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH=24000", "~/.claude/settings.json", "raises the limit for every MCP server in Claude Code"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// TestNoMemoryAnswerIsNotACompleteListing: a 404 or 403 from a registry, or a
// remote with no login, must not let sync remove the entries it may have
// added. Only registries that all answered with a list, even an empty one,
// count as complete.
func TestNoMemoryAnswerIsNotACompleteListing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		complete bool
	}{
		{"empty list", http.StatusOK, true},
		{"older registry", http.StatusNotFound, false},
		{"token refused", http.StatusForbidden, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := depsOnServers(t, registryReplying(t, tc.status))
			_, asked, complete, err := d.memorySpaces(t.Context())
			if err != nil || asked != 1 || complete != tc.complete {
				t.Errorf("memorySpaces() = asked %d, complete %v, %v, want complete %v", asked, complete, err, tc.complete)
			}
		})
	}

	// One registry listing and one that does not know team memory.
	d := depsOnServers(t, registryReplying(t, http.StatusOK, "lime"), registryReplying(t, http.StatusNotFound))
	spaces, _, complete, err := d.memorySpaces(t.Context())
	if err != nil || len(spaces) != 1 || complete {
		t.Errorf("memorySpaces(mixed) = %+v, complete %v, %v, want one space and not complete", spaces, complete, err)
	}

	// A remote the person is not signed in to.
	signedOut := depsOnServers(t, registryReplying(t, http.StatusOK), registryReplying(t, http.StatusOK))
	if err := signedOut.Remotes.SetToken("r2", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, complete, err := signedOut.memorySpaces(t.Context()); err != nil || complete {
		t.Errorf("memorySpaces(signed out of r2) complete = %v, %v, want false", complete, err)
	}
}
