// Package target holds the types shared by the installer and the adapters
// that render agents for each AI tool.
package target

import (
	"fmt"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// ScopeKind says whether an agent is installed for the person or for a repo.
type ScopeKind string

// Scope kinds: the person's home directory, or one project.
const (
	ScopeUser    ScopeKind = "user"
	ScopeProject ScopeKind = "project"
)

// Valid reports whether k is a known scope kind.
func (k ScopeKind) Valid() bool { return k == ScopeUser || k == ScopeProject }

// Scope is where an install lands: the user's home or a project folder.
type Scope struct {
	Kind ScopeKind `yaml:"kind"`
	// Root is the absolute directory adapter paths are relative to. The lock
	// omits it for user scope and resolves it to the current home.
	Root string `yaml:"root,omitempty"`
}

// String formats the scope for display, e.g. "project:/src/app".
func (s Scope) String() string { return fmt.Sprintf("%s:%s", s.Kind, s.Root) }

// Stamp identifies the package version a rendered file came from.
type Stamp struct {
	ID      pack.ID
	Version semver.Version
	Digest  pack.Digest
}

// String formats the stamp as team/name@version.
func (s Stamp) String() string { return fmt.Sprintf("%s@%s", s.ID, s.Version) }

// File is one rendered file.
type File struct {
	// Path is slash-separated and relative to the scope root.
	Path string
	Data []byte
}
