package password

import (
	"context"
	"errors"
	"time"
)

// Defaults for a Hasher: each hash takes about 19 MiB and tens of
// milliseconds, so a few at once keep memory and CPU bounded.
const (
	DefaultConcurrent = 4
	DefaultWait       = 2 * time.Second
)

// ErrBusy means too many passwords are being hashed at once and no slot
// freed up in time; the caller should try again shortly.
var ErrBusy = errors.New("the server is busy; try again in a moment")

// Hasher runs Hash, Verify and VerifyDummy with a cap on how many run at
// once, so a burst of sign-ins cannot exhaust memory. A call waits a short
// while for a slot, then fails with ErrBusy. It is safe for concurrent use.
type Hasher struct {
	slots chan struct{}
	wait  time.Duration
}

// NewHasher returns a hasher that runs at most concurrent hashes at once
// and waits at most wait for a slot. With concurrent 0 every call is busy.
func NewHasher(concurrent int, wait time.Duration) *Hasher {
	return &Hasher{slots: make(chan struct{}, max(concurrent, 0)), wait: wait}
}

// Hash is Hash within the cap.
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	return Hash(pw)
}

// Verify is Verify within the cap.
func (h *Hasher) Verify(ctx context.Context, pw, phc string) (bool, error) {
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	return Verify(pw, phc)
}

// VerifyDummy is VerifyDummy within the cap, so decoy work is bounded too.
func (h *Hasher) VerifyDummy(ctx context.Context, pw string) error {
	if err := h.acquire(ctx); err != nil {
		return err
	}
	defer h.release()
	VerifyDummy(pw)
	return nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	default:
	}
	t := time.NewTimer(h.wait)
	defer t.Stop()
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-t.C:
		return ErrBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() { <-h.slots }
