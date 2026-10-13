package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/pipeline"
	"github.com/petervlad/loadout/internal/remote"
)

// pipelineRegistry serves the releases it is given, newest last, and the
// bundle of each.
func pipelineRegistry(t *testing.T, packs ...*pack.Pack) string {
	t.Helper()
	mux := http.NewServeMux()
	for _, p := range packs {
		mux.HandleFunc("GET /v1/packages/"+p.ID().Team()+"/"+p.ID().Name()+"/"+p.Version().String(), func(w http.ResponseWriter, _ *http.Request) {
			data, err := bundle.Encode(p)
			if err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", bundle.ContentType)
			w.Header().Set(api.HeaderDigest, string(p.Digest()))
			_, _ = w.Write(data)
		})
	}
	mux.HandleFunc("GET /v1/packages/{team}/{name}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("team") + "/" + r.PathValue("name")
		var rels []api.Release
		for _, p := range packs {
			if p.ID().String() == id {
				rels = append(rels, api.Release{ID: id, Version: p.Version().String(), Digest: string(p.Digest()), Kind: string(p.Kind())})
			}
		}
		if rels == nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "not_found", "message": "no such package"}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ReleasesResponse{Releases: rels})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}

func pipelinePack(t *testing.T, id, version, body string) *pack.Pack {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + id + "\nkind: pipeline\nversion: " + version + "\ndescription: A pipeline.\n"),
		pack.PipelineFile: []byte(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func withRemote(t *testing.T, store *remote.Store, url string) {
	t.Helper()
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Add(remote.Remote{Name: "office", URL: url}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.SetToken("office", testToken); err != nil {
		t.Fatal(err)
	}
}

func TestNewPipelineTemplate(t *testing.T) {
	d, _ := testDeps(t)
	out, err := runCmd(t, d, "", "new", "pipeline", "acme/dev-loop")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(d.WorkDir, "dev-loop")
	for _, want := range []string{"created " + dir, "edit " + filepath.Join(dir, "pipeline.yaml"), "loadout publish " + dir} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "pipeline.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(pipeline.ScaffoldTemplate()) {
		t.Errorf("pipeline.yaml is not the example template:\n%s", data)
	}
}

func TestNewPipelineUses(t *testing.T) {
	tmpl := func(v string) *pack.Pack {
		return pipelinePack(t, "acme/dev-loop", v, string(pipeline.ScaffoldTemplate()))
	}
	binding := pipelinePack(t, "acme/bound", "1.0.0", "uses: acme/dev-loop@1\nbind:\n  draft: a\n  check: b\nwork_folder: explorations/\n")
	url := pipelineRegistry(t, tmpl("1.0.0"), tmpl("1.2.0"), tmpl("2.0.0"), binding, pipelinePack(t, "acme/early", "0.3.0", string(pipeline.ScaffoldTemplate())), pipelinePack(t, "acme/mixed", "0.3.0", string(pipeline.ScaffoldTemplate())), pipelinePack(t, "acme/mixed", "1.0.0", string(pipeline.ScaffoldTemplate())))

	t.Run("binding lists every slot empty with the edges as comments", func(t *testing.T) {
		d, store := testDeps(t)
		withRemote(t, store, url)
		out, err := runCmd(t, d, "", "new", "pipeline", "lime/dev-loop", "--uses", "acme/dev-loop@1")
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(d.WorkDir, "dev-loop")
		for _, want := range []string{"created " + dir, "fill bind:", "loadout publish " + dir} {
			if !strings.Contains(out, want) {
				t.Errorf("output is missing %q:\n%s", want, out)
			}
		}
		data, err := os.ReadFile(filepath.Join(dir, "pipeline.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"uses: acme/dev-loop@1\n", "  draft:\n", "  check:\n", "#   check on CHANGES REQUESTED -> draft (gate, max 2 rounds)"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("pipeline.yaml is missing %q:\n%s", want, data)
			}
		}
	})

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"major with no release", []string{"--uses", "acme/dev-loop@3"}, "acme/dev-loop has no release of major 3"},
		{"unknown template", []string{"--uses", "acme/nothing@1"}, "acme/nothing has no release that your team can read"},
		{"other majors exist", []string{"--uses", "acme/dev-loop@3"}, "only major 1, 2"},
		{"only a 0.x release", []string{"--uses", "acme/early@1"}, "has only 0.x releases, which a team cannot use. Its maintainers need to publish 1.0.0 first, with: loadout publish DIR --bump major"},
		{"0.x and a usable major", []string{"--uses", "acme/mixed@3"}, "only major 1. Name one"},
		{"a binding is not a template", []string{"--uses", "acme/bound@1"}, "acme/bound is a binding, not a template"},
		{"malformed reference", []string{"--uses", "dev-loop"}, `--uses "dev-loop": want LAYER/NAME@MAJOR`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, store := testDeps(t)
			withRemote(t, store, url)
			_, err := runCmd(t, d, "", append([]string{"new", "pipeline", "lime/dev-loop"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "uses uses") {
				t.Errorf("error says uses twice: %v", err)
			}
			if strings.Contains(err.Error(), "@0") {
				t.Errorf("error suggests a major that is refused: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(d.WorkDir, "dev-loop")); statErr == nil {
				t.Error("a folder was created for a refused command")
			}
		})
	}
}
