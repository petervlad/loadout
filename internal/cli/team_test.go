package cli

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/invite"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/remote"
	"github.com/petervlad/loadout/internal/target/claudecode"
)

const testToken = "ldt_testtoken"

// fakeRegistry answers health, packages and, when layersStatus is 200,
// layers. It is enough for team join.
func fakeRegistry(t *testing.T, layersStatus int, layers []api.Layer) string {
	t.Helper()
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Error(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, api.HealthResponse{Status: "ok"})
	})
	mux.HandleFunc("GET /v1/packages", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, api.PackagesResponse{})
	})
	if layersStatus != http.StatusNotFound {
		mux.HandleFunc("GET /v1/layers", func(w http.ResponseWriter, _ *http.Request) {
			if layersStatus != http.StatusOK {
				w.WriteHeader(layersStatus)
				return
			}
			writeJSON(w, api.LayersResponse{Layers: layers})
		})
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}

func testDeps(t *testing.T) (Deps, *remote.Store) {
	t.Helper()
	home, state := t.TempDir(), t.TempDir()
	log := slog.New(slog.DiscardHandler)
	store := remote.NewStore(state, func(string) string { return "" })
	return Deps{
		Installer: install.New(lock.NewStore(filepath.Join(state, "lock.yaml"), home), log, claudecode.New()),
		Remotes:   store,
		Log:       log,
		StateDir:  state,
		Home:      home,
		WorkDir:   home,
		Version:   "test",
	}, store
}

func runCmd(t *testing.T, d Deps, stdin string, args ...string) (string, error) {
	t.Helper()
	root := NewRoot(d)
	var out bytes.Buffer
	root.SetArgs(args)
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.ExecuteContext(t.Context())
	return out.String(), err
}

func newInvite(t *testing.T, url, namespace string) string {
	t.Helper()
	s, err := invite.Invite{URL: url, Namespace: namespace, Token: testToken, Role: invite.RolePublish}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTeamJoinBindsLayers(t *testing.T) {
	tests := []struct {
		name         string
		layersStatus int
		layers       []api.Layer
		want         []remote.Binding // Remote is filled in by the test
		wantErr      string
	}{
		{
			name:         "ancestors bound too",
			layersStatus: http.StatusOK,
			layers: []api.Layer{
				{Namespace: "org", Kind: api.LayerProject, Depth: 2},
				{Namespace: "lime", Kind: api.LayerTeam, Depth: 0},
				{Namespace: "bro", Kind: api.LayerProject, Depth: 1},
			},
			want: []remote.Binding{
				{Namespace: "lime", Role: remote.RolePublish},
				{Namespace: "bro", Role: remote.RoleRead, Via: []string{"lime"}},
				{Namespace: "org", Role: remote.RoleRead, Via: []string{"lime"}},
			},
		},
		{
			name:         "registry without layers",
			layersStatus: http.StatusNotFound,
			want:         []remote.Binding{{Namespace: "lime", Role: remote.RolePublish}},
		},
		{
			name:         "token of another team",
			layersStatus: http.StatusOK,
			layers:       []api.Layer{{Namespace: "mango", Kind: api.LayerTeam}},
			wantErr:      "belongs to mango",
		},
		{
			name:         "layers fail",
			layersStatus: http.StatusInternalServerError,
			wantErr:      "layers",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url := fakeRegistry(t, tc.layersStatus, tc.layers)
			d, store := testDeps(t)
			_, err := runCmd(t, d, newInvite(t, url, "lime")+"\n", "team", "join")
			cfg, loadErr := store.Load()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("team join error = %v, want %q", err, tc.wantErr)
				}
				if len(cfg.Remotes) != 0 || len(cfg.Bindings) != 0 {
					t.Errorf("a failed join saved %+v", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("team join error: %v", err)
			}
			if len(cfg.Remotes) != 1 {
				t.Fatalf("remotes = %+v, want one", cfg.Remotes)
			}
			for i := range tc.want {
				tc.want[i].Remote = cfg.Remotes[0].Name
			}
			if diff := cmp.Diff(tc.want, cfg.Bindings); diff != "" {
				t.Errorf("bindings mismatch (-want +got):\n%s", diff)
			}
			if tok, err := store.Token(cfg.Remotes[0].Name); err != nil || tok != testToken {
				t.Errorf("stored token = %q, %v, want the invite's", tok, err)
			}
		})
	}
}

func TestTeamJoinReusesOrSeparatesRemotes(t *testing.T) {
	url := fakeRegistry(t, http.StatusNotFound, nil)
	d, store := testDeps(t)

	// A remote added by hand with no token is reused.
	if _, err := runCmd(t, d, "", "remote", "add", "office", url); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, d, newInvite(t, url, "lime")+"\n", "team", "join"); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := cfg.Binding("lime"); len(cfg.Remotes) != 1 || b.Remote != "office" {
		t.Errorf("after join: remotes %+v, lime on %q; want office reused", cfg.Remotes, b.Remote)
	}

	// Another team's token on the same registry gets its own remote, so each
	// stored token keeps belonging to one team.
	if err := store.SetToken("office", "ldt_limeonly"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, d, newInvite(t, url, "north")+"\n", "team", "join"); err != nil {
		t.Fatal(err)
	}
	if cfg, err = store.Load(); err != nil {
		t.Fatal(err)
	}
	b, _ := cfg.Binding("north")
	if len(cfg.Remotes) != 2 || b.Remote == "office" {
		t.Errorf("after second join: remotes %+v, north on %q; want a new remote", cfg.Remotes, b.Remote)
	}
	if tok, _ := store.Token("office"); tok != "ldt_limeonly" {
		t.Errorf("office token = %q, want lime's left alone", tok)
	}

	// team leave keeps a remote another team still uses.
	out, err := runCmd(t, d, "", "team", "leave", "north")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed remote "+b.Remote) {
		t.Errorf("leave north = %q, want its remote removed", out)
	}
	if cfg, err = store.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Get("office"); !ok {
		t.Error("leaving north removed lime's remote")
	}
}

func TestTeamJoinReadsOnlyStdin(t *testing.T) {
	url := fakeRegistry(t, http.StatusNotFound, nil)
	d, _ := testDeps(t)
	if _, err := runCmd(t, d, "", "team", "join", newInvite(t, url, "lime")); err == nil {
		t.Error("team join accepted the invite as an argument")
	}
	if _, err := runCmd(t, d, "", "team", "join"); err == nil || !strings.Contains(err.Error(), "no invite") {
		t.Errorf("team join with empty stdin error = %v, want no invite", err)
	}
}
