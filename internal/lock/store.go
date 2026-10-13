package lock

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/flock"
	"github.com/petervlad/loadout/internal/pack"
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
	log  *slog.Logger
	warn sync.Once
}

// NewStore returns a store for the lock file at path; home is the current
// user's home directory.
func NewStore(path, home string) *Store { return &Store{path: path, home: home} }

// WithLog sets where Load says that it ignored a damaged requires file. The
// default is the default logger.
func (s *Store) WithLog(log *slog.Logger) *Store {
	s.log = log
	return s
}

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
	// What a conductor was rendered from is a cache of the registry, so a
	// damaged file is ignored (once, with a warning) and sync renders again.
	req, err := s.loadRequires()
	if err != nil {
		s.warn.Do(func() {
			log := s.log
			if log == nil {
				log = slog.Default()
			}
			log.Warn("ignored the record of what installed conductors were made from, the next sync renders them again", "path", s.RequiresPath(), "err", err)
		})
		req = nil
	}
	l := &Lock{}
	for _, e := range f.Entries {
		e.Requires = req[requiresKey(e.ID, e.Scope)]
		if e.Scope.Kind == target.ScopeUser {
			e.Scope.Root = s.home
		}
		l.Put(e)
	}
	return l, nil
}

// requiresFile is the on-disk layout of the requires file, next to the
// lock. Older CLIs read the lock strictly and refuse a field they do not
// know, so what a rendered install was made from lives here instead.
type requiresFile struct {
	Version int             `yaml:"version"`
	Entries []requiresEntry `yaml:"entries"`
}

type requiresEntry struct {
	ID       pack.ID       `yaml:"id"`
	Scope    target.Scope  `yaml:"scope"`
	Requires []Requirement `yaml:"requires"`
}

// RequiresPath returns the path of the requires file.
func (s *Store) RequiresPath() string {
	return strings.TrimSuffix(s.path, filepath.Ext(s.path)) + ".requires.yaml"
}

// requiresKey names a lock entry the way the lock keys it. A user scope's
// root is not saved, as in the lock.
func requiresKey(id pack.ID, scope target.Scope) string {
	root := scope.Root
	if scope.Kind == target.ScopeUser {
		root = ""
	}
	return id.String() + "|" + string(scope.Kind) + "|" + root
}

func (s *Store) loadRequires() (map[string][]Requirement, error) {
	data, err := os.ReadFile(s.RequiresPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.RequiresPath(), err)
	}
	var f requiresFile
	if err := strictyaml.Decode(data, &f); errors.Is(err, strictyaml.ErrEmpty) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("%s is damaged (%w); move it aside (mv %s %s.bak) and install your conductors again", s.RequiresPath(), err, s.RequiresPath(), s.RequiresPath())
	}
	if f.Version != formatVersion {
		return nil, fmt.Errorf("%s has format version %d, this loadout understands %d", s.RequiresPath(), f.Version, formatVersion)
	}
	out := map[string][]Requirement{}
	for _, e := range f.Entries {
		out[requiresKey(e.ID, e.Scope)] = e.Requires
	}
	return out, nil
}

// saveRequires writes the requires of the entries that have any. The file
// goes first, so an entry in the lock never lacks its requires.
func (s *Store) saveRequires(entries []Entry) error {
	f := requiresFile{Version: formatVersion}
	for _, e := range entries {
		if len(e.Requires) == 0 {
			continue
		}
		scope := e.Scope
		if scope.Kind == target.ScopeUser {
			scope.Root = ""
		}
		f.Entries = append(f.Entries, requiresEntry{ID: e.ID, Scope: scope, Requires: e.Requires})
	}
	if _, err := os.Stat(s.RequiresPath()); len(f.Entries) == 0 && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode requires: %w", err)
	}
	if err := atomicfile.Write(s.RequiresPath(), data, 0o600); err != nil {
		return fmt.Errorf("save requires: %w", err)
	}
	return nil
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
	if err := s.saveRequires(entries); err != nil {
		return err
	}
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
