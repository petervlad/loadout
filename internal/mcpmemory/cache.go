package mcpmemory

import (
	"sync"
	"time"
)

// indexCache keeps the index text of each namespace for a short time, so a
// busy team's tool calls do not each read 150 entries. Writes through the
// server drop it, since a write in one namespace can also change the index of
// the teams below it.
type indexCache struct {
	ttl time.Duration
	now func() time.Time

	mu sync.Mutex
	m  map[string]cachedIndex
	// gen counts drops. A reader notes it before it reads the index and sets
	// the text only if no drop came since, so an index read before a write
	// can never be cached after that write dropped the cache.
	gen uint64
}

type cachedIndex struct {
	text    string
	expires time.Time
}

func newIndexCache(ttl time.Duration, now func() time.Time) *indexCache {
	return &indexCache{ttl: ttl, now: now, m: map[string]cachedIndex{}}
}

func (c *indexCache) get(ns string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[ns]
	if !ok || !c.now().Before(e.expires) {
		delete(c.m, ns)
		return "", false
	}
	return e.text, true
}

// generation is what a reader passes to set, taken before it reads.
func (c *indexCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// set keeps text for ns unless the cache was dropped since gen was taken.
func (c *indexCache) set(ns, text string, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	c.m[ns] = cachedIndex{text: text, expires: c.now().Add(c.ttl)}
}

func (c *indexCache) drop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	clear(c.m)
}
