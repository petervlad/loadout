package pack

import (
	"fmt"
	"strings"

	"github.com/petervlad/loadout/internal/ident"
)

// ID names a package as team/name, for example "payments/planner".
type ID struct {
	team, name string
}

// NewID returns the ID team/name after validating both parts.
func NewID(team, name string) (ID, error) {
	if err := ident.Check("team", team); err != nil {
		return ID{}, err
	}
	if err := ident.Check("package", name); err != nil {
		return ID{}, err
	}
	return ID{team: team, name: name}, nil
}

// ParseID reads an ID written as "team/name".
func ParseID(s string) (ID, error) {
	team, name, ok := strings.Cut(s, "/")
	if !ok {
		return ID{}, fmt.Errorf("invalid package id %q: want team/name", s)
	}
	return NewID(team, name)
}

// Team returns the team part.
func (id ID) Team() string { return id.team }

// Name returns the package name part.
func (id ID) Name() string { return id.name }

// IsZero reports whether id is unset.
func (id ID) IsZero() bool { return id == ID{} }

// String formats the id as team/name.
func (id ID) String() string { return id.team + "/" + id.name }

// MarshalText implements encoding.TextMarshaler.
func (id ID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (id *ID) UnmarshalText(b []byte) error {
	parsed, err := ParseID(string(b))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
