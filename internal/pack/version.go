package pack

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/semver"
)

// WithVersion returns a copy of p whose manifest says version v. Nothing is
// written to disk, so a failed publish leaves the author's folder untouched.
func WithVersion(p *Pack, v semver.Version) (*Pack, error) {
	files := p.Files()
	data, err := manifestWithVersion(files[ManifestFile], v)
	if err != nil {
		return nil, err
	}
	files[ManifestFile] = data
	return FromFiles(files)
}

// SetVersion rewrites the version in dir's manifest, keeping the rest of the
// file (comments, order) as the author wrote it. The file is replaced
// atomically, so a crash never leaves a half-written manifest.
func SetVersion(dir string, v semver.Version) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("set version: %w", err)
	}
	defer root.Close()

	data, err := root.ReadFile(ManifestFile)
	if err != nil {
		return fmt.Errorf("set version: %w", err)
	}
	updated, err := manifestWithVersion(data, v)
	if err != nil {
		return err
	}
	info, err := root.Stat(ManifestFile)
	if err != nil {
		return fmt.Errorf("set version: %w", err)
	}
	if err := atomicfile.WriteIn(root, ManifestFile, updated, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set version: %w", err)
	}
	return nil
}

func manifestWithVersion(data []byte, v semver.Version) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("set version: parse %s: %w", ManifestFile, err)
	}
	if err := setMappingValue(&doc, "version", v.String()); err != nil {
		return nil, fmt.Errorf("set version: %s: %w", ManifestFile, err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("set version: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("set version: %w", err)
	}
	return buf.Bytes(), nil
}

func setMappingValue(doc *yaml.Node, key, value string) error {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("not a YAML mapping")
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Value = value
			m.Content[i+1].Tag = "!!str"
			m.Content[i+1].Style = 0
			return nil
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
	return nil
}
