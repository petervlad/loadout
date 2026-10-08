// Package device keeps the state of OAuth-style device authorizations
// (RFC 8628), the way `loadout login` signs a terminal in: the CLI starts an
// authorization, the person approves its short user code in a browser where
// they are signed in, and the CLI polls until it is approved.
//
// State lives in memory. An authorization lasts minutes, so a registry
// restart only means running `loadout login` again.
package device

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Defaults for a Flow.
const (
	DefaultTTL      = 15 * time.Minute
	DefaultInterval = 5 * time.Second
	// MaxPerClient caps the live authorizations one client (an IPv4
	// address or an IPv6 /64) may have started.
	MaxPerClient = 5
	// maxPending caps authorizations in memory; beyond it the oldest
	// unapproved one makes room.
	maxPending = 10000
)

// userCodeAlphabet has no vowels, so codes never spell words, and no
// letters easily mistaken for digits (RFC 8628, section 6.1).
const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

// Errors callers match with errors.Is.
var (
	// ErrNotFound means there is no pending authorization for a user code:
	// it was mistyped, has expired or was already decided.
	ErrNotFound = errors.New("no pending authorization with this code")
	// ErrFull means too many authorizations are pending and none can make
	// room.
	ErrFull = errors.New("too many pending authorizations")
	// ErrTooMany means the client started MaxPerClient authorizations that
	// are still live.
	ErrTooMany = errors.New("too many sign-ins started from your address; finish one or wait a few minutes")
)

// Status is where an authorization stands, as seen by the polling CLI.
type Status string

// Statuses. Approved is returned once, with the approval; after that the
// device code is unknown, which reads as expired.
const (
	StatusPending  Status = "authorization_pending"
	StatusSlowDown Status = "slow_down"
	StatusExpired  Status = "expired"
	StatusDenied   Status = "denied"
	StatusApproved Status = "approved"
)

// Authorization is what the CLI gets when it starts.
type Authorization struct {
	// DeviceCode is the CLI's secret for polling.
	DeviceCode string
	// UserCode is what the person types or confirms in the browser,
	// formatted ABCD-EFGH.
	UserCode  string
	ExpiresAt time.Time
	Interval  time.Duration
}

// Request is a pending authorization as the approving person sees it.
type Request struct {
	UserCode string
	// Namespace is the namespace the CLI asked for; empty lets the person
	// choose one of their memberships.
	Namespace string
	ExpiresAt time.Time
}

// Approval records who approved and for which namespace.
type Approval struct {
	UserID    string
	Namespace string
}

// Result is the answer to one poll.
type Result struct {
	Status Status
	// Approval is set when Status is StatusApproved.
	Approval Approval
}

type grant struct {
	deviceCode string
	userCode   string
	namespace  string
	client     string
	createdAt  time.Time
	expiresAt  time.Time
	interval   time.Duration
	lastPoll   time.Time
	denied     bool
	approval   *Approval
}

// Flow holds pending authorizations. It is safe for concurrent use.
type Flow struct {
	now        func() time.Time
	ttl        time.Duration
	interval   time.Duration
	maxPending int
	perClient  int

	mu       sync.Mutex
	byDevice map[string]*grant
	byUser   map[string]*grant
}

// New returns an empty flow with the default lifetime and poll interval.
// now is time.Now in production.
func New(now func() time.Time) *Flow {
	return &Flow{
		now:        now,
		ttl:        DefaultTTL,
		interval:   DefaultInterval,
		maxPending: maxPending,
		perClient:  MaxPerClient,
		byDevice:   map[string]*grant{},
		byUser:     map[string]*grant{},
	}
}

// Start begins an authorization for namespace (empty: chosen on approval)
// on behalf of client, the rate-limit key of the caller's address. One
// client may have MaxPerClient live authorizations. When the table is full,
// the oldest unapproved authorization is dropped to make room, so a flood
// from many addresses delays sign-ins but never refuses everyone.
func (f *Flow) Start(namespace, client string) (Authorization, error) {
	deviceCode, err := randomToken()
	if err != nil {
		return Authorization{}, fmt.Errorf("start device authorization: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	f.prune(now)
	n := 0
	for _, g := range f.byDevice {
		if g.client == client {
			n++
		}
	}
	if n >= f.perClient {
		return Authorization{}, ErrTooMany
	}
	if len(f.byDevice) >= f.maxPending && !f.dropOldest() {
		return Authorization{}, ErrFull
	}
	var userCode string
	for {
		userCode, err = randomUserCode()
		if err != nil {
			return Authorization{}, fmt.Errorf("start device authorization: %w", err)
		}
		if _, taken := f.byUser[userCode]; !taken {
			break
		}
	}
	g := &grant{
		deviceCode: deviceCode,
		userCode:   userCode,
		namespace:  namespace,
		client:     client,
		createdAt:  now,
		expiresAt:  now.Add(f.ttl),
		interval:   f.interval,
	}
	f.byDevice[deviceCode] = g
	f.byUser[userCode] = g
	return Authorization{DeviceCode: deviceCode, UserCode: format(userCode), ExpiresAt: g.expiresAt, Interval: g.interval}, nil
}

// Pending returns the undecided, unexpired authorization for a user code.
// The code is matched ignoring case, spaces and dashes.
func (f *Flow) Pending(userCode string) (Request, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, err := f.pending(userCode)
	if err != nil {
		return Request{}, err
	}
	return Request{UserCode: format(g.userCode), Namespace: g.namespace, ExpiresAt: g.expiresAt}, nil
}

// Approve approves the pending authorization for a user code. The namespace
// must be the one the CLI asked for, if it asked for one.
func (f *Flow) Approve(userCode string, a Approval) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, err := f.pending(userCode)
	if err != nil {
		return err
	}
	if g.namespace != "" && a.Namespace != g.namespace {
		return fmt.Errorf("approve for namespace %s: the CLI asked for %s", a.Namespace, g.namespace)
	}
	if a.UserID == "" || a.Namespace == "" {
		return errors.New("approve: user and namespace are required")
	}
	g.approval = &a
	return nil
}

// Deny refuses the pending authorization for a user code.
func (f *Flow) Deny(userCode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, err := f.pending(userCode)
	if err != nil {
		return err
	}
	g.denied = true
	return nil
}

// Poll reports the state of the authorization behind deviceCode. Polling
// faster than the interval slows the CLI down; each slow_down adds five
// seconds. An approval or denial is reported once and then forgotten.
func (f *Flow) Poll(deviceCode string) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	g, ok := f.byDevice[deviceCode]
	if !ok || !now.Before(g.expiresAt) {
		if ok {
			f.remove(g)
		}
		return Result{Status: StatusExpired}
	}
	switch {
	case g.approval != nil:
		f.remove(g)
		return Result{Status: StatusApproved, Approval: *g.approval}
	case g.denied:
		f.remove(g)
		return Result{Status: StatusDenied}
	}
	tooSoon := !g.lastPoll.IsZero() && now.Sub(g.lastPoll) < g.interval
	g.lastPoll = now
	if tooSoon {
		g.interval += 5 * time.Second
		return Result{Status: StatusSlowDown}
	}
	return Result{Status: StatusPending}
}

// pending finds an undecided, unexpired grant by user code. f.mu must be
// held.
func (f *Flow) pending(userCode string) (*grant, error) {
	g, ok := f.byUser[normalize(userCode)]
	if !ok || g.approval != nil || g.denied || !f.now().Before(g.expiresAt) {
		return nil, ErrNotFound
	}
	return g, nil
}

// prune forgets expired grants. f.mu must be held.
func (f *Flow) prune(now time.Time) {
	for _, g := range f.byDevice {
		if !now.Before(g.expiresAt) {
			f.remove(g)
		}
	}
}

// dropOldest forgets the oldest authorization nobody approved yet and
// reports whether there was one. f.mu must be held.
func (f *Flow) dropOldest() bool {
	var oldest *grant
	for _, g := range f.byDevice {
		if g.approval == nil && (oldest == nil || g.createdAt.Before(oldest.createdAt)) {
			oldest = g
		}
	}
	if oldest == nil {
		return false
	}
	f.remove(oldest)
	return true
}

func (f *Flow) remove(g *grant) {
	delete(f.byDevice, g.deviceCode)
	delete(f.byUser, g.userCode)
}

// normalize turns "abcd-efgh", "ABCD EFGH" or "abcdefgh" into "ABCDEFGH".
func normalize(code string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r >= 'A' && r <= 'Z' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func format(code string) string { return code[:4] + "-" + code[4:] }

func randomUserCode() (string, error) {
	// 256 is not a multiple of 20: bytes of 240 and above are skipped so
	// every letter is equally likely.
	const limit = 240
	out := make([]byte, 0, 8)
	buf := make([]byte, 16)
	for len(out) < 8 {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b < limit && len(out) < 8 {
				out = append(out, userCodeAlphabet[int(b)%len(userCodeAlphabet)])
			}
		}
	}
	return string(out), nil
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
