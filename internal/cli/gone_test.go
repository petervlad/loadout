package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/update"
)

func TestPrintSyncGone(t *testing.T) {
	for _, tt := range []struct {
		name string
		res  update.Result
		want []string
	}{
		{
			"user scope",
			heldResult(t, update.OutcomeGone, userScope),
			[]string{
				"removed from the registry",
				"payments/planner is no longer on its registry. loadout keeps your files; remove them with: loadout uninstall payments/planner --scope user\n",
			},
		},
		{
			"project scope names the folder",
			heldResult(t, update.OutcomeGone, projectScope),
			[]string{"loadout uninstall payments/planner --scope project\n", "Run it in /src/billing.\n"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			if err := printSync(&out, []update.Result{tt.res}); err != nil {
				t.Errorf("printSync() error = %v, want nil: a package that is gone is not a failure", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			if strings.Contains(out.String(), "1.3.0") {
				t.Errorf("output shows a latest version for a package that is gone:\n%s", out.String())
			}
		})
	}
}

// Only the registry's not_found reads as gone; a team the person left (403)
// and an outage stay failures.
func TestRegistryAdapterLatestGone(t *testing.T) {
	id, err := pack.ParseID("lime/planner")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name     string
		status   int
		body     string
		wantGone bool
	}{
		{"not found", http.StatusNotFound, `{"error":{"code":"not_found","message":"lime/planner was not found"}}`, true},
		{"forbidden", http.StatusForbidden, `{"error":{"code":"forbidden","message":"not your team"}}`, false},
		{"no releases", http.StatusOK, `{"releases":[]}`, true},
		{"server error", http.StatusInternalServerError, `{"error":{"code":"internal","message":"boom"}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(ts.Close)
			c, err := client.New(ts.URL, "ldt_x")
			if err != nil {
				t.Fatal(err)
			}
			_, err = registryAdapter{c: c, team: "lime"}.Releases(t.Context(), id)
			if err == nil {
				t.Fatal("Releases() succeeded, want an error")
			}
			if got := errors.Is(err, update.ErrGone); got != tt.wantGone {
				t.Errorf("Releases() error = %v, gone = %v, want %v", err, got, tt.wantGone)
			}
		})
	}
}
