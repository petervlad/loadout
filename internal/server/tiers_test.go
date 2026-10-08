package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/api"
)

func TestTiersRoutes(t *testing.T) {
	f := newManageFixture(t)
	project := `{"cells":[{"tool":"copilot","hint":"deep","model":"Claude Sonnet 5","effort":"high"},{"tool":"copilot","hint":"fast","model":"Claude Haiku 4.5"}]}`
	team := `{"cells":[{"tool":"copilot","hint":"deep","effort":"max"}]}`
	tests := []struct {
		who, path, body string
		want            int
	}{
		{"machine", "/v1/namespaces/web/tiers", team, http.StatusForbidden},
		{"member", "/v1/namespaces/web/tiers", team, http.StatusForbidden},
		{"maintainer", "/v1/namespaces/acme/tiers", team, http.StatusForbidden},
		{"maintainer", "/v1/namespaces/web/tiers", team, http.StatusOK},
		{"owner", "/v1/namespaces/acme/tiers", `{"cells":[{"tool":"copilot","hint":"huge","model":"x"}]}`, http.StatusBadRequest},
		{"owner", "/v1/namespaces/acme/tiers", `{"cells":[{"tool":"copilot","hint":"deep","model":"a\nb"}]}`, http.StatusBadRequest},
		{"owner", "/v1/namespaces/acme/tiers", project, http.StatusOK},
		{"owner", "/v1/namespaces/web/tiers", team, http.StatusOK},
		{"owner", "/v1/namespaces/ghost/tiers", team, http.StatusNotFound},
	}
	for _, tt := range tests {
		tok := f.tokens[tt.who]
		if tt.who == "machine" {
			tok = f.machine
		}
		if got, body := f.call(t, tok, "PUT", tt.path, tt.body); got != tt.want {
			t.Errorf("PUT %s %s as %s = %d, want %d\n%s", tt.path, tt.body, tt.who, got, tt.want, body)
		}
	}

	// Every token that reads web gets its resolved table, a machine token too.
	want := api.TiersResponse{
		Namespace: "web",
		Cells: []api.TierCell{
			{Tool: "copilot", Hint: "fast", Model: "Claude Haiku 4.5", ModelFrom: "acme"},
			{Tool: "copilot", Hint: "deep", Model: "Claude Sonnet 5", Effort: "max", ModelFrom: "acme", EffortFrom: "web"},
		},
	}
	for _, who := range []string{"member", "machine"} {
		tok := f.tokens[who]
		if who == "machine" {
			tok = f.machine
		}
		code, body := f.call(t, tok, "GET", "/v1/tiers", "")
		if code != http.StatusOK {
			t.Fatalf("GET /v1/tiers as %s = %d\n%s", who, code, body)
		}
		var got api.TiersResponse
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		if got.Digest == "" {
			t.Errorf("GET /v1/tiers as %s has no digest", who)
		}
		got.Digest = ""
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GET /v1/tiers as %s mismatch (-want +got):\n%s", who, diff)
		}
	}
}
