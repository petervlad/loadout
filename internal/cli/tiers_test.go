package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
)

func TestFetchTiers(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantEmpty bool
		wantErr   bool
	}{
		{name: "older registry", status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"no route"}}`, wantEmpty: true},
		{name: "table", status: http.StatusOK, body: `{"namespace":"lime","cells":[{"tool":"copilot","hint":"deep","model":"Claude Sonnet 5","model_from":"acme"}],"digest":"x"}`},
		{name: "a value the registry should never send", status: http.StatusOK, body: `{"namespace":"lime","cells":[{"tool":"copilot","hint":"huge","model":"x"}]}`, wantErr: true},
		{name: "server error", status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			c, err := client.New(ts.URL, "ldt_x")
			if err != nil {
				t.Fatal(err)
			}
			got, err := fetchTiers(t.Context(), c)
			if (err != nil) != tc.wantErr {
				t.Fatalf("fetchTiers() error = %v, want error: %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got.Empty() != tc.wantEmpty {
				t.Errorf("fetchTiers() = %+v, want empty: %v", got.Cells(), tc.wantEmpty)
			}
			if !tc.wantEmpty {
				if ch, _ := got.Get("copilot", agent.ModelDeep); ch.Model != "Claude Sonnet 5" {
					t.Errorf("copilot deep = %+v", ch)
				}
			}
		})
	}
}

func TestOwnValuesOfANamespace(t *testing.T) {
	cells := []api.TierCell{
		{Tool: "copilot", Hint: "deep", Model: "Claude Sonnet 5", Effort: "max", ModelFrom: "acme", EffortFrom: "lime"},
		{Tool: "copilot", Hint: "fast", Model: "Claude Haiku 4.5", ModelFrom: "acme"},
	}
	own, err := tableOf(cells, "lime")
	if err != nil {
		t.Fatal(err)
	}
	got := own.Cells()
	if len(got) != 1 || got[0].Model != "" || got[0].Effort != "max" {
		t.Errorf("lime's own values = %+v, want only its effort on copilot deep", got)
	}
}
