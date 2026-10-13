package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/config"
)

// TestRegistryOpenerCreatesMemoryDB: opening a data folder creates memory.db
// beside registry.db, and closing releases both.
func TestRegistryOpenerCreatesMemoryDB(t *testing.T) {
	dir := t.TempDir()
	open := registryOpener(nil, config.Login{}, config.Stats{}, config.Proxies{}, buildinfo.Build{}, nil)
	_, closer, err := open(t.Context(), dir)
	if err != nil {
		t.Fatalf("open() error: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
	for _, name := range []string{dbFile, memoryDBFile} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 600", name, fi.Mode().Perm())
		}
	}
}
