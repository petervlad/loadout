package web

import (
	"context"
	"testing"
	"time"
)

// With every slot held, a download waits and then gives up; a free slot
// is taken at once.
func TestZipSlots(t *testing.T) {
	h := &handler{zips: make(chan struct{}, maxZips)}
	for range maxZips {
		if !h.zipSlot(t.Context()) {
			t.Fatal("zipSlot() = false with a slot free")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if h.zipSlot(ctx) {
		t.Error("zipSlot() = true with every slot held")
	}
	<-h.zips
	if !h.zipSlot(t.Context()) {
		t.Error("zipSlot() = false after a slot was given back")
	}
}
