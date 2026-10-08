package builtin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// stateVersion is bumped when the state file changes incompatibly.
const stateVersion = 1

// state is what the person asked for, kept apart from the lock so an older
// loadout, which reads the lock strictly, is not confused by it.
type state struct {
	Version int `yaml:"version"`
	// Wanted is set by login and setup --skills: the person wants the
	// skills wherever an AI tool appears. Without it sync adds nothing.
	Wanted bool `yaml:"wanted"`
	// Declined names the skills the person uninstalled; login and sync
	// never add them back. setup --skills clears it.
	Declined []string `yaml:"declined,omitempty,flow"`
}

func (s state) declined(name string) bool { return slices.Contains(s.Declined, name) }

// stateFile reads and writes the state at a fixed path.
type stateFile struct {
	path string
}

// load reads the state; a missing or empty file is the zero state.
func (f stateFile) load() (state, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return state{}, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("read %s: %w", f.path, err)
	}
	var s state
	if err := strictyaml.Decode(data, &s); errors.Is(err, strictyaml.ErrEmpty) {
		return state{}, nil
	} else if err != nil {
		return state{}, fmt.Errorf("%s is damaged (%w); remove it and run loadout setup --skills", f.path, err)
	}
	if s.Version != stateVersion {
		return state{}, fmt.Errorf("%s has format version %d, this loadout understands %d; remove it and run loadout setup --skills", f.path, s.Version, stateVersion)
	}
	return s, nil
}

// save writes the state atomically.
func (f stateFile) save(s state) error {
	s.Version = stateVersion
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode %s: %w", f.path, err)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return fmt.Errorf("save %s: %w", f.path, err)
	}
	if err := atomicfile.Write(f.path, data, 0o600); err != nil {
		return fmt.Errorf("save %s: %w", f.path, err)
	}
	return nil
}

// update applies fn to the state and saves it.
func (f stateFile) update(fn func(*state)) (state, error) {
	s, err := f.load()
	if err != nil {
		return state{}, err
	}
	fn(&s)
	return s, f.save(s)
}
