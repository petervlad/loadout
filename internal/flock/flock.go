// Package flock takes an exclusive lock on a file across processes, so two
// loadout commands changing the same state file never lose each other's
// changes.
package flock

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"time"
)

// errWouldBlock is returned by tryLock when another process holds the lock.
var errWouldBlock = errors.New("lock is held")

// lockTimeout bounds how long a command waits for another loadout process.
const lockTimeout = 30 * time.Second

// Acquire takes an exclusive OS lock on path, waiting while another loadout
// process holds it. The OS releases the lock if the process dies, so a crash
// never leaves a stale lock behind.
func Acquire(ctx context.Context, path string) (release func() error, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	// Waiters back off with jitter: started together (an agent runs several
	// commands at once) they would otherwise all poll at the same moments,
	// and only one in each round could win.
	for delay := 10 * time.Millisecond; ; delay = min(delay*2, 100*time.Millisecond) {
		err := tryLock(f)
		if err == nil {
			return func() error { return errors.Join(unlock(f), f.Close()) }, nil
		}
		if !errors.Is(err, errWouldBlock) {
			_ = f.Close() // the lock error is the one to report
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close() // the timeout is the one to report
			return nil, fmt.Errorf("another loadout command is still running (waited for %s): %w", path, ctx.Err())
		case <-time.After(delay/2 + rand.N(delay/2+1)): //nolint:gosec // G404: jitter, not a secret
		}
	}
}
