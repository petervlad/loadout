package lock

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/flock"
	"github.com/petervlad/loadout/internal/strictyaml"
	"github.com/petervlad/loadout/internal/target"
)

// formatVersion is bumped when the lock file layout changes incompatibly.
const formatVersion = 1

// file is the on-disk layout of the lock.
type file struct {
	Version int     `yaml:"version"`
	Entries []Entry `yaml:"entries"`
}

// Store reads and writes the lock file at a fixed path.
//
// User-scope entries are saved without their root and read back relative to
// the current home, so a ~/.loadout copied or synced to another machine or
// account never writes into the old home.
type Store struct {
	path string
	home string
}

// NewStore returns a store for the lock file at path; home is the current
// user's home directory.
func NewStore(path, home string) *Store { return &Store{path: path, home: home} }

// Path returns the lock file path.
func (s *Store) Path() string { return s.path }

// Load reads a snapshot of the lock. A missing or empty file is an empty
// lock. Use Update to change it.
func (s *Store) Load() (*Lock, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Lock{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lock: %w", err)
	}

	var f file
	if err := strictyaml.Decode(data, &f); errors.Is(err, strictyaml.ErrEmpty) {
		return &Lock{}, nil
	} else if err != nil {
		return nil, s.damaged(err)
	}
	if f.Version != formatVersion {
		return nil, s.damaged(fmt.Errorf("format version %d, this loadout understands %d", f.Version, formatVersion))
	}
	l := &Lock{}
	for _, e := range f.Entries {
		if e.Scope.Kind == target.ScopeUser {
			e.Scope.Root = s.home
		}
		l.Put(e)
	}
	return l, nil
}

func (s *Store) damaged(err error) error {
	return fmt.Errorf("%s is damaged (%w); fix it, or move it aside (mv %s %s.bak) and reinstall your agents", s.path, err, s.path, s.path)
}

// Update runs fn on the current lock and saves the result, holding an
// exclusive lock across processes so concurrent loadout commands never lose
// each other's changes. If fn returns an error nothing is saved.
func (s *Store) Update(ctx context.Context, fn func(*Lock) error) (err error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("update lock: %w", err)
	}
	release, err := flock.Acquire(ctx, s.path+".lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()

	l, err := s.Load()
	if err != nil {
		return err
	}
	if err := fn(l); err != nil {
		return err
	}
	return s.save(l)
}

// save writes the lock atomically: a crash leaves either the old or the new
// file, never a half-written one.
func (s *Store) save(l *Lock) error {
	entries := l.Entries()
	for i := range entries {
		if entries[i].Scope.Kind == target.ScopeUser {
			entries[i].Scope.Root = ""
		}
	}
	data, err := yaml.Marshal(file{Version: formatVersion, Entries: entries})
	if err != nil {
		return fmt.Errorf("encode lock: %w", err)
	}
	// Private: the lock lists every path loadout writes to.
	if err := atomicfile.Write(s.path, data, 0o600); err != nil {
		return fmt.Errorf("save lock: %w", err)
	}
	return nil
}
