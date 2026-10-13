package mcpsetup

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

// wishVersion is bumped when the wish file changes incompatibly.
const wishVersion = 1

// Wish is what the person asked for.
type Wish struct {
	// Declined names the tools (claude, copilot) whose team memory entries
	// the person does not want: setup --no-memory.
	Declined []string `yaml:"declined,omitempty"`
}

// Has reports whether tool is declined.
func (w Wish) Has(tool string) bool { return slices.Contains(w.Declined, tool) }

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
		return Wish{}, fmt.Errorf("%s is damaged (%w); remove it and run loadout setup --memory or --no-memory", f.path, err)
	}
	if d.Version != wishVersion {
		return Wish{}, fmt.Errorf("%s has format version %d, this loadout understands %d; remove it and run loadout setup --memory or --no-memory", f.path, d.Version, wishVersion)
	}
	slices.Sort(d.Declined)
	return d.Wish, nil
}

func (f wishFile) save(w Wish) error {
	slices.Sort(w.Declined)
	w.Declined = slices.Compact(w.Declined)
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
