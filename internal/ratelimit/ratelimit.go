// Package ratelimit is an in-memory token bucket per key (an IP address, an
// email), for slowing down guessing on the login endpoints of one registry
// process.
package ratelimit

import (
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// maxKeys bounds memory; see Allow.
const maxKeys = 50000

// Limiter allows each key a burst of events, refilled at a steady rate. It
// is safe for concurrent use.
type Limiter struct {
	every   time.Duration
	burst   float64
	now     func() time.Time
	maxKeys int

	mu      sync.Mutex
	buckets map[string]*bucket
	// nextEvict is the earliest time a bucket is full again, when a full
	// table had nothing to evict; until then new keys are refused without
	// scanning.
	nextEvict time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a limiter that allows burst events at once per key, and one
// more every interval. now is time.Now in production.
func New(burst int, every time.Duration, now func() time.Time) *Limiter {
	return &Limiter{every: every, burst: float64(burst), now: now, maxKeys: maxKeys, buckets: map[string]*bucket{}}
}

// Allow takes one token from key's bucket and reports whether there was one.
// When the table is full, a new key is only let in once some bucket has
// refilled completely; until then new keys are refused, so flooding it
// with keys never resets anyone else's limit.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys && !l.evict(now) {
			return false
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	l.refill(b, now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *Limiter) refill(b *bucket, now time.Time) {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+float64(elapsed)/float64(l.every))
		b.last = now
	}
}

// evict drops the buckets that have refilled completely, which behave like
// new ones, and reports whether that made room. It never drops a bucket
// that still limits its key. l.mu must be held.
func (l *Limiter) evict(now time.Time) bool {
	if now.Before(l.nextEvict) {
		return false
	}
	var soonest time.Time
	for k, b := range l.buckets {
		l.refill(b, now)
		if b.tokens >= l.burst {
			delete(l.buckets, k)
			continue
		}
		full := now.Add(time.Duration((l.burst - b.tokens) * float64(l.every)))
		if soonest.IsZero() || full.Before(soonest) {
			soonest = full
		}
	}
	if len(l.buckets) < l.maxKeys {
		return true
	}
	l.nextEvict = soonest
	return false
}

// ClientKey returns the rate-limit key of a request's client: its IPv4
// address, or the /64 of its IPv6 address, since one IPv6 client usually
// holds a whole /64 and could rotate through it.
func ClientKey(r *http.Request) string { return Prefix(ClientIP(r)) }

// Prefix returns the rate-limit key of an address: an IPv4 address (also
// one written as IPv4-mapped IPv6) as it is, an IPv6 address as its /64.
// Text that is not an address is returned unchanged.
func Prefix(addr string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.WithZone("").Prefix(64)
	if err != nil {
		return addr
	}
	return p.String()
}

// ClientIP returns the address a request came from. Behind a proxy on the
// same machine (Caddy in the deployment) it trusts the proxy's X-Real-IP
// header; from anywhere else it uses the connection's address, so a client
// cannot pick its own key.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err == nil && addr.IsLoopback() {
		if realIP, err := netip.ParseAddr(r.Header.Get("X-Real-IP")); err == nil {
			return realIP.String()
		}
	}
	return host
}
