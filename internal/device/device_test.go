package device

import (
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"
)

// clock is a settable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newFlow(t *testing.T) (*Flow, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	return New(c.now), c
}

var userCodeRE = regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$`)

func TestStart(t *testing.T) {
	f, c := newFlow(t)
	a, err := f.Start("lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if !userCodeRE.MatchString(a.UserCode) {
		t.Errorf("UserCode = %q, want ABCD-EFGH from the consonant alphabet", a.UserCode)
	}
	if len(a.DeviceCode) < 40 {
		t.Errorf("DeviceCode = %q, want a 256-bit secret", a.DeviceCode)
	}
	if !a.ExpiresAt.Equal(c.t.Add(DefaultTTL)) || a.Interval != DefaultInterval {
		t.Errorf("Start() = %+v, want expiry in %s and interval %s", a, DefaultTTL, DefaultInterval)
	}
	b, err := f.Start("lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if a.UserCode == b.UserCode || a.DeviceCode == b.DeviceCode {
		t.Error("two authorizations share a code")
	}
}

func TestApproveThenPoll(t *testing.T) {
	f, c := newFlow(t)
	a, err := f.Start("lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if r := f.Poll(a.DeviceCode); r.Status != StatusPending {
		t.Fatalf("Poll() before approval = %s, want pending", r.Status)
	}

	// Codes are matched ignoring case and the dash.
	typed := normalize(a.UserCode)
	req, err := f.Pending(typed[:4] + " " + typed[4:])
	if err != nil || req.Namespace != "lime" || req.UserCode != a.UserCode {
		t.Fatalf("Pending(typed code) = %+v, %v; want the lime request", req, err)
	}
	if err := f.Approve(a.UserCode, Approval{UserID: "u1", Namespace: "acme"}); err == nil {
		t.Error("Approve(another namespace) succeeded, want an error")
	}
	if err := f.Approve(a.UserCode, Approval{UserID: "u1", Namespace: "lime"}); err != nil {
		t.Fatalf("Approve() error: %v", err)
	}
	if _, err := f.Pending(a.UserCode); !errors.Is(err, ErrNotFound) {
		t.Errorf("Pending() after approval error = %v, want ErrNotFound", err)
	}

	c.advance(DefaultInterval)
	r := f.Poll(a.DeviceCode)
	if r.Status != StatusApproved || r.Approval != (Approval{UserID: "u1", Namespace: "lime"}) {
		t.Fatalf("Poll() after approval = %+v, want approved by u1 for lime", r)
	}
	// The approval is handed out once.
	if r := f.Poll(a.DeviceCode); r.Status != StatusExpired {
		t.Errorf("second Poll() after approval = %s, want expired", r.Status)
	}
}

func TestChooseNamespaceOnApproval(t *testing.T) {
	f, _ := newFlow(t)
	a, err := f.Start("", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Approve(a.UserCode, Approval{UserID: "u1"}); err == nil {
		t.Error("Approve() without a namespace succeeded, want an error")
	}
	if err := f.Approve(a.UserCode, Approval{UserID: "u1", Namespace: "acme"}); err != nil {
		t.Fatal(err)
	}
	if r := f.Poll(a.DeviceCode); r.Approval.Namespace != "acme" {
		t.Errorf("Poll() = %+v, want the chosen namespace acme", r)
	}
}

func TestDeny(t *testing.T) {
	f, _ := newFlow(t)
	a, err := f.Start("lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Deny(a.UserCode); err != nil {
		t.Fatal(err)
	}
	if err := f.Approve(a.UserCode, Approval{UserID: "u1", Namespace: "lime"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Approve() after deny error = %v, want ErrNotFound", err)
	}
	if r := f.Poll(a.DeviceCode); r.Status != StatusDenied {
		t.Errorf("Poll() after deny = %s, want denied", r.Status)
	}
}

func TestSlowDown(t *testing.T) {
	f, c := newFlow(t)
	a, err := f.Start("lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	f.Poll(a.DeviceCode)
	c.advance(time.Second)
	if r := f.Poll(a.DeviceCode); r.Status != StatusSlowDown {
		t.Fatalf("Poll() after 1s = %s, want slow_down", r.Status)
	}
	// The interval grew to 10s, counted from the last poll.
	c.advance(DefaultInterval)
	if r := f.Poll(a.DeviceCode); r.Status != StatusSlowDown {
		t.Errorf("Poll() 5s later = %s, want slow_down again (interval is now 10s)", r.Status)
	}
	c.advance(15 * time.Second)
	if r := f.Poll(a.DeviceCode); r.Status != StatusPending {
		t.Errorf("Poll() after waiting = %s, want pending", r.Status)
	}
}

func TestExpiry(t *testing.T) {
	f, c := newFlow(t)
	a, err := f.Start("lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	c.advance(DefaultTTL)
	if _, err := f.Pending(a.UserCode); !errors.Is(err, ErrNotFound) {
		t.Errorf("Pending() after expiry error = %v, want ErrNotFound", err)
	}
	if err := f.Approve(a.UserCode, Approval{UserID: "u1", Namespace: "lime"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Approve() after expiry error = %v, want ErrNotFound", err)
	}
	if r := f.Poll(a.DeviceCode); r.Status != StatusExpired {
		t.Errorf("Poll() after expiry = %s, want expired", r.Status)
	}
	if r := f.Poll("unknown"); r.Status != StatusExpired {
		t.Errorf("Poll(unknown) = %s, want expired", r.Status)
	}

	// Start prunes expired authorizations.
	if _, err := f.Start("lime", "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.byDevice); n != 1 {
		t.Errorf("pending after prune = %d, want 1", n)
	}
}

func TestUserCodeIsUnbiasedAlphabet(t *testing.T) {
	seen := map[rune]int{}
	for range 500 {
		code, err := randomUserCode()
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range code {
			seen[r]++
		}
	}
	if len(seen) != len(userCodeAlphabet) {
		t.Errorf("4000 random letters used %d distinct letters, want all %d", len(seen), len(userCodeAlphabet))
	}
}

func TestPerClientCap(t *testing.T) {
	f, c := newFlow(t)
	for i := range MaxPerClient {
		if _, err := f.Start("lime", "203.0.113.7"); err != nil {
			t.Fatalf("Start() #%d error: %v", i+1, err)
		}
	}
	if _, err := f.Start("lime", "203.0.113.7"); !errors.Is(err, ErrTooMany) {
		t.Errorf("Start() over the per-client cap error = %v, want ErrTooMany", err)
	}
	if _, err := f.Start("lime", "2001:db8::/64"); err != nil {
		t.Errorf("Start() from another client error: %v", err)
	}
	c.advance(DefaultTTL)
	if _, err := f.Start("lime", "203.0.113.7"); err != nil {
		t.Errorf("Start() once the old ones expired error: %v", err)
	}
}

func TestFullTableDropsOldestUnapproved(t *testing.T) {
	f, c := newFlow(t)
	f.maxPending = 3
	approved, err := f.Start("lime", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Approve(approved.UserCode, Approval{UserID: "u1", Namespace: "lime"}); err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, client := range []string{"client-2", "client-3"} {
		c.advance(time.Second)
		a, err := f.Start("lime", client)
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, a.UserCode)
	}
	c.advance(time.Second)
	if _, err := f.Start("lime", "client-4"); err != nil {
		t.Fatalf("Start() on a full table error = %v, want the oldest unapproved one dropped", err)
	}
	if _, err := f.Pending(codes[0]); !errors.Is(err, ErrNotFound) {
		t.Errorf("oldest unapproved authorization still pending: %v", err)
	}
	if _, err := f.Pending(codes[1]); err != nil {
		t.Errorf("newer authorization dropped: %v", err)
	}
	if r := f.Poll(approved.DeviceCode); r.Status != StatusApproved {
		t.Errorf("approved authorization polls %s, want approved (never dropped)", r.Status)
	}

	// Nothing unapproved left to drop: the table refuses.
	g, err := newFullyApproved(t, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Start("lime", "client-9"); !errors.Is(err, ErrFull) {
		t.Errorf("Start() on a table of approved authorizations error = %v, want ErrFull", err)
	}
}

// newFullyApproved returns a flow of size n full of approved authorizations.
func newFullyApproved(t *testing.T, n int) (*Flow, error) {
	t.Helper()
	f, _ := newFlow(t)
	f.maxPending = n
	for i := range n {
		a, err := f.Start("lime", fmt.Sprintf("client-%d", i))
		if err != nil {
			return nil, err
		}
		if err := f.Approve(a.UserCode, Approval{UserID: "u1", Namespace: "lime"}); err != nil {
			return nil, err
		}
	}
	return f, nil
}
