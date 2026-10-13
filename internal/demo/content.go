package demo

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
)

// content holds the demo's packages as folders, team/name@version, and the
// wiki pages. They are real package folders, so the seed publishes exactly
// what `loadout publish` would.
//
//go:embed all:content
var content embed.FS

// bundleOf returns the bundle of one package folder, such as
// "lime/planner@2.3.0".
func bundleOf(ref string) ([]byte, error) {
	dir := path.Join("content", ref)
	files := map[string][]byte{}
	err := fs.WalkDir(content, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := content.ReadFile(p)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(p, dir+"/")] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("demo package %s: %w", ref, err)
	}
	p, err := pack.FromFiles(files)
	if err != nil {
		return nil, fmt.Errorf("demo package %s: %w", ref, err)
	}
	return bundle.Encode(p)
}

// text returns one embedded file under content/.
func text(name string) (string, error) {
	data, err := content.ReadFile(path.Join("content", name))
	if err != nil {
		return "", fmt.Errorf("demo text %s: %w", name, err)
	}
	return string(data), nil
}
