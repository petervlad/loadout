package syncstate

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

// formatVersion is bumped when the file layout changes incompatibly.
const formatVersion = 1

// file is the on-disk layout.
type file struct {
	Version int      `yaml:"version"`
	Behind  []Behind `yaml:"behind,omitempty"`
	Merged  []Merged `yaml:"merged,omitempty"`
}

// Store reads and changes the state file under a cross-process lock.
//
// User-scope records are saved without their root and read back under the
// current home, as the lock does, so a copied ~/.loadout never points into
// another home.
type Store struct {
	path string
	home string
}

// NewStore returns a store for the state file at path; home is the current
// user's home folder.
func NewStore(path, home string) *Store { return &Store{path: path, home: home} }

// Path returns the state file's path.
func (s *Store) Path() string { return s.path }

// Load reads the state. A missing or empty file is the empty state.
func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read %s: %w", s.path, err)
	}
	var f file
	if err := strictyaml.Decode(data, &f); errors.Is(err, strictyaml.ErrEmpty) {
		return State{}, nil
	} else if err != nil {
		return State{}, s.damaged(err)
	}
	if f.Version != formatVersion {
		return State{}, s.damaged(fmt.Errorf("format version %d, this loadout understands %d", f.Version, formatVersion))
	}
	var st State
	for _, b := range f.Behind {
		if b.Scope.Kind == target.ScopeUser {
			b.Scope.Root = s.home
		}
		st.Put(b)
	}
	for _, m := range f.Merged {
		if m.Scope.Kind == target.ScopeUser {
			m.Scope.Root = s.home
		}
		st.PutMerged(m)
	}
	return st, nil
}

func (s *Store) damaged(err error) error {
	return fmt.Errorf("%s is damaged (%w); remove it, the next loadout sync writes it again", s.path, err)
}

// Update runs fn on the current state and saves the result, holding a lock
// across processes so two loadouts never lose each other's changes. If fn
// returns an error nothing is saved. A damaged file is started afresh: the
// state only holds what the next sync learns again.
func (s *Store) Update(ctx context.Context, fn func(*State) error) (err error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("update %s: %w", s.path, err)
	}
	release, err := flock.Acquire(ctx, s.path+".lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()

	st, err := s.Load()
	if err != nil {
		st = State{}
	}
	if err := fn(&st); err != nil {
		return err
	}
	return s.save(st)
}

// save writes the state atomically, private like the lock: it lists the
// paths of edited files.
func (s *Store) save(st State) error {
	all := st.All()
	for i := range all {
		if all[i].Scope.Kind == target.ScopeUser {
			all[i].Scope.Root = ""
		}
	}
	merged := st.AllMerged()
	for i := range merged {
		if merged[i].Scope.Kind == target.ScopeUser {
			merged[i].Scope.Root = ""
		}
	}
	data, err := yaml.Marshal(file{Version: formatVersion, Behind: all, Merged: merged})
	if err != nil {
		return fmt.Errorf("encode %s: %w", s.path, err)
	}
	if err := atomicfile.Write(s.path, data, 0o600); err != nil {
		return fmt.Errorf("save %s: %w", s.path, err)
	}
	return nil
}
