package accounttest

import (
	"context"
	"sync"
)

// Snapshots is an account.Snapshots that records the labels it was asked
// for and fails on demand, so tests can check that a refused or failed
// copy stops a delete.
type Snapshots struct {
	// Err, when set before use, is returned by every Take.
	Err error

	mu     sync.Mutex
	labels []string
}

// Take records label and returns a file name made from it.
func (s *Snapshots) Take(_ context.Context, label string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return "", s.Err
	}
	s.labels = append(s.labels, label)
	return label + ".db", nil
}

// Labels returns the labels of the copies taken so far.
func (s *Snapshots) Labels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.labels...)
}
