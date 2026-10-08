// Package mailtest provides a mailer that records messages, for tests that
// read a login code from the mail the registry sent.
package mailtest

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/mail"
)

// Recorder keeps every message sent through it. It is safe for concurrent
// use.
type Recorder struct {
	mu   sync.Mutex
	sent []mail.Message
}

// Send implements the mailer interface of the account service.
func (r *Recorder) Send(_ context.Context, m mail.Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return nil
}

// Sent returns the messages sent so far, oldest first.
func (r *Recorder) Sent() []mail.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.sent)
}

// Count returns how many messages were sent to addr.
func (r *Recorder) Count(addr string) int {
	n := 0
	for _, m := range r.Sent() {
		if m.To == addr {
			n++
		}
	}
	return n
}

// WaitFor returns the (n+1)th message to addr, waiting up to ten seconds
// for it: mail may be sent in the background.
func (r *Recorder) WaitFor(t *testing.T, addr string, n int) mail.Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var to []mail.Message
		for _, m := range r.Sent() {
			if m.To == addr {
				to = append(to, m)
			}
		}
		if len(to) > n {
			return to[n]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no mail #%d to %s after 10s (got %d)", n+1, addr, len(to))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
