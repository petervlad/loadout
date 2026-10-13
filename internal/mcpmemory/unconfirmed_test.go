package mcpmemory_test

import (
	"strings"
	"testing"
	"time"
)

// TestUnconfirmedMarkerInGetAndRecent covers the tools that the index and
// search test leaves out: an old memory is marked, never hidden.
func TestUnconfirmedMarkerInGetAndRecent(t *testing.T) {
	r := newRig(t)
	old := r.seed(t, "lime", "Old fact", "from March", "body", dana)
	r.clock.advance(200 * 24 * time.Hour)
	s := r.connect(t, secLee, "lime")
	got := text(t, s.call(t, "get", map[string]any{"id": old.ID}))
	if !strings.Contains(got, `unconfirmed_since="2026-10-09"`) || !strings.Contains(got, "body") {
		t.Errorf("get does not mark an old memory:\n%s", got)
	}
	// seed stamps the entry with the clock at seeding, so it is 200 days
	// old and outside a 30 day window: recent must not claim it. A newer
	// memory is not marked.
	r.seed(t, "lime", "New fact", "today", "", dana)
	if got := text(t, s.call(t, "recent", map[string]any{"days": 30})); !strings.Contains(got, "New fact") || strings.Contains(got, "unconfirmed_since") {
		t.Errorf("recent marks a fresh memory or lacks it:\n%s", got)
	}
}
