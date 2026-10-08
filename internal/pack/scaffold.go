package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/semver"
)

// ErrExists is returned when Scaffold would overwrite an existing folder.
var ErrExists = errors.New("folder already exists")

// Scaffold creates a new package of kind for id in dir, which must not
// exist. The result loads cleanly, so the author can edit and publish
// straight away.
func Scaffold(dir string, id ID, kind Kind) (*Pack, error) {
	spec, ok := kinds[kind]
	if !ok {
		return nil, fmt.Errorf("scaffold: kind %q is not supported: want %s", kind, joinKinds())
	}
	manifest, err := MarshalManifest(Manifest{
		ID:          id,
		Kind:        kind,
		Version:     semver.New(0, 1, 0),
		Description: "Describe what " + id.Name() + " is for in one sentence.",
	})
	if err != nil {
		return nil, fmt.Errorf("scaffold manifest: %w", err)
	}
	files, err := spec.scaffold(id)
	if err != nil {
		return nil, fmt.Errorf("scaffold %s: %w", kind, err)
	}
	files[ManifestFile] = manifest
	if _, err := FromFiles(files); err != nil {
		return nil, fmt.Errorf("scaffold is invalid, this is a bug: %w", err)
	}
	return Create(dir, files)
}

// MarshalManifest returns m as the content of loadout.yaml.
func MarshalManifest(m Manifest) ([]byte, error) {
	return yaml.Marshal(m)
}

// Create validates files as a package and writes them to dir, which must
// not exist. The package is returned as read back from dir, so what
// Create returns is what publish will upload.
func Create(dir string, files map[string][]byte) (*Pack, error) {
	if _, err := FromFiles(files); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("create %s: %w", dir, ErrExists)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := writeFiles(dir, files); err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	p, err := Load(dir)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	return p, nil
}

func writeFiles(dir string, files map[string][]byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	defer root.Close()
	for name, data := range files {
		if d := path.Dir(name); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", name, err)
			}
		}
		if err := root.WriteFile(name, data, 0o644); err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
	}
	return nil
}
