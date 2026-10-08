package client_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
)

func TestLayers(t *testing.T) {
	want := []api.Layer{
		{Namespace: "lime", Kind: api.LayerTeam, Depth: 0},
		{Namespace: "bro", Kind: api.LayerProject, Depth: 1},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/layers", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ldt_x" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(api.LayersResponse{Layers: want}); err != nil {
			t.Error(err)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	got, err := newClient(t, ts.URL, "ldt_x").Layers(t.Context())
	if err != nil {
		t.Fatalf("Layers() error: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Layers() mismatch (-want +got):\n%s", diff)
	}
}

func TestLayersOnARegistryWithoutTheRoute(t *testing.T) {
	e := newEnv(t)
	c := e.client(t, "lime", "read")
	_, err := c.Layers(t.Context())
	if apiErr, ok := errors.AsType[*client.APIError](err); !ok || apiErr.Status != http.StatusNotFound {
		// A registry with GET /v1/layers answers; one without it must 404.
		if err == nil {
			t.Skip("this registry serves GET /v1/layers")
		}
		t.Errorf("Layers() on an old registry error = %v, want a 404 *APIError", err)
	}
}
