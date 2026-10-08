package cliupdate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/flock"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// formatVersion is bumped when the file layout changes incompatibly.
const formatVersion = 1

// Limits of what the file remembers: a registry not heard from in a month
// stops speaking, and a person who tried many registries keeps the newest.
const (
	maxAge        = 30 * 24 * time.Hour
	maxRegistries = 16
)

// Advice is one registry's word about loadout releases, parsed.
type Advice struct {
	URL     string
	Latest  semver.Version // zero: it named none
	Minimum semver.Version // zero: it named none
	Seen    time.Time
}

// Told is the last time an audience heard that a version is out.
type Told struct {
	Version semver.Version
	At      time.Time
}

// State is what the file remembers: each registry's last advice, and who
// was told what when. Its methods return copies.
type State struct {
	registries []Advice
	told       map[clock]Told
}

// Registries returns the remembered advice, most recently seen first.
func (st State) Registries() []Advice { return slices.Clone(st.registries) }

// clone returns a copy that shares nothing with st.
func (st State) clone() State {
	return State{registries: slices.Clone(st.registries), told: maps.Clone(st.told)}
}

// put records a, replacing the advice of the same registry.
func (st *State) put(a Advice) {
	st.registries = slices.DeleteFunc(st.registries, func(o Advice) bool { return o.URL == a.URL })
	st.registries = append(st.registries, a)
}

// prune drops advice older than maxAge at now, and the least recently seen
// beyond maxRegistries, and orders the rest most recent first.
func (st *State) prune(now time.Time) {
	st.registries = slices.DeleteFunc(st.registries, func(a Advice) bool { return now.Sub(a.Seen) > maxAge })
	slices.SortStableFunc(st.registries, func(a, b Advice) int { return b.Seen.Compare(a.Seen) })
	if len(st.registries) > maxRegistries {
		st.registries = st.registries[:maxRegistries]
	}
}

// file is the on-disk layout. Versions are strings so an absent one is
// omitted, and parsed on the way in.
type file struct {
	Version    int                 `yaml:"version"`
	Registries []fileAdvice        `yaml:"registries,omitempty"`
	Told       map[string]fileTold `yaml:"told,omitempty"`
}

type fileAdvice struct {
	URL     string    `yaml:"url"`
	Latest  string    `yaml:"latest,omitempty"`
	Minimum string    `yaml:"minimum,omitempty"`
	Seen    time.Time `yaml:"seen"`
}

type fileTold struct {
	Version string    `yaml:"version"`
	At      time.Time `yaml:"at"`
}

// Store reads and changes ~/.loadout/cliupdate.yaml under a cross-process
// lock: agents run several loadout commands at once.
type Store struct{ path string }

// NewStore returns the store of the file at path.
func NewStore(path string) *Store { return &Store{path: path} }

// Load reads the state. A missing or empty file is the empty state; a
// damaged one is an error, which callers treat as the empty state.
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
	st, err := f.state()
	if err != nil {
		return State{}, s.damaged(err)
	}
	return st, nil
}

func (f file) state() (State, error) {
	var st State
	for _, fa := range f.Registries {
		a := Advice{URL: fa.URL, Seen: fa.Seen}
		var err error
		if a.Latest, err = optional(fa.Latest); err != nil {
			return State{}, err
		}
		if a.Minimum, err = optional(fa.Minimum); err != nil {
			return State{}, err
		}
		st.put(a)
	}
	for name, ft := range f.Told {
		c, ok := clockNamed(name)
		if !ok {
			return State{}, fmt.Errorf("unknown audience %q", name)
		}
		v, err := semver.Parse(ft.Version)
		if err != nil {
			return State{}, err
		}
		st.tell(c, Told{Version: v, At: ft.At})
	}
	return st, nil
}

// optional parses a version that may be absent.
func optional(s string) (semver.Version, error) {
	if s == "" {
		return semver.Version{}, nil
	}
	return semver.Parse(s)
}

func (s *Store) damaged(err error) error {
	return fmt.Errorf("%s is damaged (%w); remove it, loadout writes it again", s.path, err)
}

// Update runs fn on the current state and saves the result, holding a lock
// across processes. If fn returns an error nothing is saved. A damaged
// file is started afresh: it costs one repeated line at most.
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

func (s *Store) save(st State) error {
	f := file{Version: formatVersion}
	for _, a := range st.registries {
		fa := fileAdvice{URL: a.URL, Seen: a.Seen.UTC()}
		if a.Latest != (semver.Version{}) {
			fa.Latest = a.Latest.String()
		}
		if a.Minimum != (semver.Version{}) {
			fa.Minimum = a.Minimum.String()
		}
		f.Registries = append(f.Registries, fa)
	}
	for c, t := range st.told {
		if f.Told == nil {
			f.Told = map[string]fileTold{}
		}
		f.Told[c.String()] = fileTold{Version: t.Version.String(), At: t.At.UTC()}
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode %s: %w", s.path, err)
	}
	if err := atomicfile.Write(s.path, data, 0o600); err != nil {
		return fmt.Errorf("save %s: %w", s.path, err)
	}
	return nil
}
