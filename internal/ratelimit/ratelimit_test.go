package ratelimit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestBurstThenRefill(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	l := New(3, time.Minute, c.now)
	for i := range 3 {
		if !l.Allow("ip:1") {
			t.Fatalf("Allow() #%d = false, want true within the burst", i+1)
		}
	}
	if l.Allow("ip:1") {
		t.Fatal("Allow() after the burst = true, want false")
	}
	if !l.Allow("ip:2") {
		t.Error("another key is limited too; want separate buckets")
	}
	c.t = c.t.Add(30 * time.Second)
	if l.Allow("ip:1") {
		t.Error("Allow() after half an interval = true, want false")
	}
	c.t = c.t.Add(30 * time.Second)
	if !l.Allow("ip:1") {
		t.Error("Allow() after one interval = false, want one new token")
	}
	c.t = c.t.Add(time.Hour)
	for i := range 3 {
		if !l.Allow("ip:1") {
			t.Fatalf("Allow() #%d after an hour = false, want the burst back (and no more)", i+1)
		}
	}
	if l.Allow("ip:1") {
		t.Error("tokens grew past the burst")
	}
}

func TestEvictKeepsMemoryBounded(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	l := New(1, time.Minute, c.now)
	for i := range maxKeys + 10 {
		l.Allow(string(rune(i)))
	}
	if n := len(l.buckets); n > maxKeys {
		t.Errorf("buckets = %d, want at most %d", n, maxKeys)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		remote, realIP, want string
	}{
		{"203.0.113.7:5000", "", "203.0.113.7"},
		{"203.0.113.7:5000", "198.51.100.1", "203.0.113.7"}, // not from the local proxy
		{"127.0.0.1:5000", "198.51.100.1", "198.51.100.1"},
		{"[::1]:5000", "2001:db8::1", "2001:db8::1"},
		{"127.0.0.1:5000", "not an ip", "127.0.0.1"},
	}
	for _, tt := range tests {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.RemoteAddr = tt.remote
		if tt.realIP != "" {
			r.Header.Set("X-Real-IP", tt.realIP)
		}
		if got := ClientIP(r); got != tt.want {
			t.Errorf("ClientIP(%s, X-Real-IP %q) = %s, want %s", tt.remote, tt.realIP, got, tt.want)
		}
	}
}

// TestFloodDoesNotResetOthers: filling the table with new keys must not
// drop a bucket that still limits its key.
func TestFloodDoesNotResetOthers(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	l := New(1, time.Minute, c.now)
	if !l.Allow("victim") {
		t.Fatal("first Allow(victim) = false")
	}
	for i := range 4 * maxKeys {
		l.Allow(fmt.Sprintf("flood-%d", i))
	}
	if l.Allow("victim") {
		t.Fatal("Allow(victim) after a flood of new keys = true, want its bucket kept empty")
	}
	if n := len(l.buckets); n > maxKeys {
		t.Errorf("buckets = %d, want at most %d", n, maxKeys)
	}
	if l.Allow("newcomer") {
		t.Error("Allow(new key) on a full table with nothing refilled = true, want refused")
	}
	c.t = c.t.Add(time.Minute)
	if !l.Allow("newcomer") {
		t.Error("Allow(new key) once buckets refilled = false, want room made")
	}
}

func TestPrefix(t *testing.T) {
	tests := []struct{ in, want string }{
		{"203.0.113.7", "203.0.113.7"},
		{"::ffff:203.0.113.7", "203.0.113.7"},
		{"2001:db8:1:2::1", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::/64"},
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},
		{"fe80::1%eth0", "fe80::/64"},
		{"not an ip", "not an ip"},
	}
	for _, tt := range tests {
		if got := Prefix(tt.in); got != tt.want {
			t.Errorf("Prefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
