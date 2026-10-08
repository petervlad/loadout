package password

import (
	"errors"
	"testing"
	"time"
)

// TestHasherCapsConcurrentHashes: with every slot taken, each kind of
// call, the decoy included, gives up with ErrBusy after a short wait.
func TestHasherCapsConcurrentHashes(t *testing.T) {
	h := NewHasher(1, 10*time.Millisecond)
	phc, err := h.Hash(t.Context(), "tangerine kettle river")
	if err != nil {
		t.Fatal(err)
	}
	h.slots <- struct{}{} // a hash in progress
	if _, err := h.Hash(t.Context(), "tangerine kettle river"); !errors.Is(err, ErrBusy) {
		t.Errorf("Hash() while busy error = %v, want ErrBusy", err)
	}
	if _, err := h.Verify(t.Context(), "tangerine kettle river", phc); !errors.Is(err, ErrBusy) {
		t.Errorf("Verify() while busy error = %v, want ErrBusy", err)
	}
	if err := h.VerifyDummy(t.Context(), "x"); !errors.Is(err, ErrBusy) {
		t.Errorf("VerifyDummy() while busy error = %v, want ErrBusy", err)
	}
	h.release()
	if ok, err := h.Verify(t.Context(), "tangerine kettle river", phc); err != nil || !ok {
		t.Errorf("Verify() once free = %v, %v; want true", ok, err)
	}
	if n := len(h.slots); n != 0 {
		t.Errorf("slots held after the calls = %d, want 0", n)
	}
}

func TestHasherWaitsForASlot(t *testing.T) {
	h := NewHasher(1, 5*time.Second)
	h.slots <- struct{}{}
	go func() {
		time.Sleep(20 * time.Millisecond)
		h.release()
	}()
	if _, err := h.Hash(t.Context(), "tangerine kettle river"); err != nil {
		t.Errorf("Hash() after a slot freed up error: %v", err)
	}
}
