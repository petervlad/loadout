package demo

import (
	"sync"
	"time"
)

// Clock is the time every service of the seed reads. The seed moves it
// from a few weeks ago to now, so rows are dated across the weeks.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock starts at t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now is the time the services use.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
