package mcpmemory

import (
	"testing"
	"time"
)

// TestCacheIgnoresASetFromBeforeADrop: a request reads the index, a write
// drops the cache, then the request sets what it read. That text is old, so
// it must not be kept.
func TestCacheIgnoresASetFromBeforeADrop(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	c := newIndexCache(time.Minute, func() time.Time { return now })

	gen := c.generation()
	c.drop() // a write happens while the request is reading
	c.set("lime", "old index", gen)
	if text, ok := c.get("lime"); ok {
		t.Errorf("get() = %q after a stale set, want a miss", text)
	}

	// A read that started after the drop is kept.
	c.set("lime", "new index", c.generation())
	if text, ok := c.get("lime"); !ok || text != "new index" {
		t.Errorf("get() = %q, %v, want the new index", text, ok)
	}

	// Another namespace read before a drop is lost too: the drop is global.
	gen = c.generation()
	c.drop()
	c.set("acme", "stale", gen)
	if _, ok := c.get("acme"); ok {
		t.Error("a set from before a drop was kept")
	}
}
