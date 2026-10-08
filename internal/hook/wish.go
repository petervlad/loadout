package hook

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// wishVersion is bumped when the wish file changes incompatibly.
const wishVersion = 1

// Wish is what the person asked for. It lives apart from builtin.yaml so
// an older loadout still reads that file strictly.
type Wish struct {
	// Wanted is set by login and setup --hooks.
	Wanted bool `yaml:"wanted"`
	// Declined is set by setup --no-hooks: login leaves the hooks out.
	Declined bool `yaml:"declined"`
}

type wishDoc struct {
	Version int `yaml:"version"`
	Wish    `yaml:",inline"`
}

// wishFile reads and writes the wish at a fixed path.
type wishFile struct {
	path string
}

func (f wishFile) load() (Wish, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Wish{}, nil
	}
	if err != nil {
		return Wish{}, fmt.Errorf("read %s: %w", f.path, err)
	}
	var d wishDoc
	if err := strictyaml.Decode(data, &d); errors.Is(err, strictyaml.ErrEmpty) {
		return Wish{}, nil
	} else if err != nil {
		return Wish{}, fmt.Errorf("%s is damaged (%w); remove it and run loadout setup --hooks or --no-hooks", f.path, err)
	}
	if d.Version != wishVersion {
		return Wish{}, fmt.Errorf("%s has format version %d, this loadout understands %d; remove it and run loadout setup --hooks or --no-hooks", f.path, d.Version, wishVersion)
	}
	return d.Wish, nil
}

func (f wishFile) save(w Wish) error {
	data, err := yaml.Marshal(wishDoc{Version: wishVersion, Wish: w})
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
