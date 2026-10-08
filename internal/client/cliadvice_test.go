package client_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/server"
)

// heard collects the advice a client passes on.
type heard struct {
	mu  sync.Mutex
	got []client.CLIAdvice
}

func (h *heard) add(a client.CLIAdvice) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.got = append(h.got, a)
}

func (h *heard) take() []client.CLIAdvice {
	h.mu.Lock()
	defer h.mu.Unlock()
	got := h.got
	h.got = nil
	return got
}

// TestCLIAdviceReachesTheCallback: JSON calls, bundle downloads and errors
// all pass the registry's CLI headers on, as sent.
func TestCLIAdviceReachesTheCallback(t *testing.T) {
	store := registrytest.NewMemStore()
	svc := registry.NewService(store, store, store, func() time.Time { return publishedAt }, slog.New(slog.DiscardHandler))
	ts := httptest.NewServer(server.New(svc, slog.New(slog.DiscardHandler), "v0.5.0", server.WithCLI(semver.New(0, 5, 0), semver.New(0, 3, 0))))
	t.Cleanup(ts.Close)
	pub, _, err := svc.CreateToken(t.Context(), "lime", registry.ScopePublish, "test")
	if err != nil {
		t.Fatal(err)
	}
	read, _, err := svc.CreateToken(t.Context(), "lime", registry.ScopeRead, "test")
	if err != nil {
		t.Fatal(err)
	}
	h := &heard{}
	if _, err := newClient(t, ts.URL, pub, client.WithCLIAdvice(h.add)).Publish(t.Context(), newPack(t, "lime", "planner", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	h.take()
	c := newClient(t, ts.URL, read, client.WithCLIAdvice(h.add))
	want := client.CLIAdvice{Latest: "0.5.0", Minimum: "0.3.0"}

	if _, err := c.Packages(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := h.take(); len(got) != 1 || got[0] != want {
		t.Errorf("after a JSON call heard %v, want [%v]", got, want)
	}
	if _, err := c.Fetch(t.Context(), mustID(t, "lime/planner"), "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := h.take(); len(got) != 1 || got[0] != want {
		t.Errorf("after a bundle download heard %v, want [%v]", got, want)
	}
	if _, err := c.Publish(t.Context(), newPack(t, "lime", "planner", "1.1.0")); err == nil {
		t.Fatal("publish with a read token succeeded")
	}
	if got := h.take(); len(got) != 1 || got[0] != want {
		t.Errorf("after an error heard %v, want [%v]", got, want)
	}

	// Without the option nothing is called, and nothing breaks.
	if _, err := newClient(t, ts.URL, read).Packages(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := h.take(); len(got) != 0 {
		t.Errorf("a client without the option called back: %v", got)
	}
}

// TestCLIAdviceDropsLongValues: a header longer than any version is
// dropped, and a response with no advice left calls nothing.
func TestCLIAdviceDropsLongValues(t *testing.T) {
	long := strings.Repeat("9", 33)
	tests := []struct {
		latest, minimum string
		want            []client.CLIAdvice
	}{
		{"0.5.0", long, []client.CLIAdvice{{Latest: "0.5.0"}}},
		{long, "0.3.0", []client.CLIAdvice{{Minimum: "0.3.0"}}},
		{long, long, nil},
		{"", "", nil},
		{strings.Repeat("1", 32), "", []client.CLIAdvice{{Latest: strings.Repeat("1", 32)}}},
	}
	for _, tc := range tests {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.latest != "" {
				w.Header().Set(api.HeaderCLILatest, tc.latest)
			}
			if tc.minimum != "" {
				w.Header().Set(api.HeaderCLIMinimum, tc.minimum)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"packages":[]}`))
		}))
		h := &heard{}
		if _, err := newClient(t, ts.URL, "ldt_x", client.WithCLIAdvice(h.add)).Packages(t.Context()); err != nil {
			t.Fatal(err)
		}
		ts.Close()
		if got := h.take(); len(got) != len(tc.want) || (len(got) == 1 && got[0] != tc.want[0]) {
			t.Errorf("headers %q, %q: heard %v, want %v", tc.latest, tc.minimum, got, tc.want)
		}
	}
}
