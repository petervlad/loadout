package memory

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestBackupsHolding(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"loadout-20261001T020000Z.tar.gz", // before
		"loadout-20261005T020000Z.tar.gz", // after creation
		"loadout-20261007T020000Z.tar.gz", // after creation
		"loadout-20261020T020000Z.tar.gz", // after now
		"loadout-notadate.tar.gz",
		"loadout-20261006T020000Z.tar.gz.tmp",
		"other.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "loadout-20261006T020000Z.tar.gz.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 3, 9, 0, 0, 123, time.UTC)
	to := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	got, err := BackupsHolding(dir, from, to)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"loadout-20261005T020000Z.tar.gz", "loadout-20261007T020000Z.tar.gz"}
	if !slices.Equal(got, want) {
		t.Errorf("BackupsHolding() = %v, want %v", got, want)
	}
	if _, err := BackupsHolding(filepath.Join(dir, "missing"), from, to); err == nil {
		t.Error("missing folder: want an error")
	}
}
