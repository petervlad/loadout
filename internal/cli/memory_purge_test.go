package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// purgeHost is a MemoryHost with one entry whose first purge was busy: the
// entry went, the cleanup of the files did not.
type purgeHost struct {
	present bool
	purged  *memory.Purged
	busy    bool
	calls   int
}

func (h *purgeHost) Inspect(_ context.Context, id int64) (memory.Entry, int, error) {
	if !h.present {
		return memory.Entry{}, 0, fmt.Errorf("memory %d: %w", id, registry.ErrNotFound)
	}
	return memory.Entry{ID: id, Namespace: "lime", AuthorLabel: "dana@example.com"}, 2, nil
}

func (h *purgeHost) Purge(_ context.Context, id int64) (memory.Purged, error) {
	h.calls++
	p := memory.Purged{EntryID: id, Namespace: "lime", Versions: 2, At: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	if h.present {
		h.present, h.purged = false, &p
		if h.busy {
			return memory.Purged{}, fmt.Errorf("the database is busy. Run the purge command again")
		}
		return p, nil
	}
	if h.purged == nil {
		return memory.Purged{}, registry.ErrNotFound
	}
	return p, nil
}

func (h *purgeHost) WasPurged(context.Context, int64) (memory.Purged, bool, error) {
	if h.purged == nil {
		return memory.Purged{}, false, nil
	}
	return *h.purged, true, nil
}

// TestPurgeCanBeRepeated: a purge that failed on the busy log leaves the entry
// gone, and the same command then succeeds instead of failing at Inspect.
func TestPurgeCanBeRepeated(t *testing.T) {
	h := &purgeHost{present: true, busy: true}
	var out bytes.Buffer
	if err := purgeMemory(t.Context(), &out, h, 7, true, ""); err == nil {
		t.Fatal("the busy purge reported success")
	}
	out.Reset()
	if err := purgeMemory(t.Context(), &out, h, 7, true, ""); err != nil {
		t.Fatalf("second purgeMemory() error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"already purged", "Cleaned the database files again", "index cache for up to 30 seconds"} {
		if !strings.Contains(got, want) {
			t.Errorf("second run lacks %q:\n%s", want, got)
		}
	}
	// Without --yes it only says what it found.
	out.Reset()
	calls := h.calls
	if err := purgeMemory(t.Context(), &out, h, 7, false, ""); err != nil || h.calls != calls {
		t.Errorf("purgeMemory(no --yes) = %v after %d more calls, want a report only", err, h.calls-calls)
	}
	// An id that never existed is still an error.
	if err := purgeMemory(t.Context(), &out, &purgeHost{}, 9, true, ""); err == nil {
		t.Error("purgeMemory(unknown id) succeeded")
	}
}

// TestPurgeSaysTheIndexIsCached: the first successful purge also names the
// index cache, which a running registry keeps for 30 seconds.
func TestPurgeSaysTheIndexIsCached(t *testing.T) {
	h := &purgeHost{present: true}
	var out bytes.Buffer
	if err := purgeMemory(t.Context(), &out, h, 7, true, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "index cache for up to 30 seconds") {
		t.Errorf("output lacks the cache note:\n%s", out.String())
	}
}
