package flock

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireWaitsForTheHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	release, err := Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, path); err == nil {
		t.Fatal("a second Acquire got the lock while it was held")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(t.Context(), path)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
}
