package demo

import (
	"embed"
	"fmt"
	"maps"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
)

// content holds the demo's long texts and the packages that are easier to
// keep as folders (team/name@version) than to build in Go, and the wiki
// pages. A folder is a real package folder, so the seed publishes exactly
// what `loadout publish` would.
//
//go:embed all:content
var content embed.FS

// bundleOf encodes a package folder.
func bundleOf(f files) ([]byte, error) {
	p, err := pack.FromFiles(maps.Clone(f))
	if err != nil {
		return nil, fmt.Errorf("demo package: %w", err)
	}
	return bundle.Encode(p)
}
