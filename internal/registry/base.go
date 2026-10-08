package registry

import (
	"fmt"

	"github.com/petervlad/loadout/internal/semver"
)

// Base names one release of a package, or none: what a proposal was made
// from, or a package's latest release. The zero Base is none (before the
// first release), so "no release yet" never needs a fake 0.0.0.
type Base struct {
	version semver.Version
	set     bool
}

// BaseAt returns the Base naming release v.
func BaseAt(v semver.Version) Base { return Base{version: v, set: true} }

// ParseBase reads a version; "" is none.
func ParseBase(s string) (Base, error) {
	if s == "" {
		return Base{}, nil
	}
	v, err := semver.Parse(s)
	if err != nil {
		return Base{}, fmt.Errorf("base: %w", err)
	}
	return BaseAt(v), nil
}

// Version returns the release b names, and false for none.
func (b Base) Version() (semver.Version, bool) { return b.version, b.set }

// IsZero reports whether b is none.
func (b Base) IsZero() bool { return !b.set }

// String returns the version, or "" for none.
func (b Base) String() string {
	if !b.set {
		return ""
	}
	return b.version.String()
}

// Before reports whether b is older than o; none is older than any release.
func (b Base) Before(o Base) bool {
	switch {
	case !o.set:
		return false
	case !b.set:
		return true
	}
	return b.version.Compare(o.version) < 0
}

// Equal reports whether b and o name the same release, or are both none.
func (b Base) Equal(o Base) bool {
	return b.set == o.set && b.version.Compare(o.version) == 0
}

// Standing is where a proposal stands against its package's releases now.
// It is computed when a proposal is read, never stored, since releases keep
// coming.
type Standing struct {
	Base   Base // what the proposal was made from
	Latest Base // the package's latest release now
}

// Behind reports whether a release came out after the proposal's base.
func (s Standing) Behind() bool { return s.Base.Before(s.Latest) }

// Blocks reports whether the latest release is at or above v, so the
// proposal can no longer be approved as that version at all.
func (s Standing) Blocks(v semver.Version) bool {
	latest, ok := s.Latest.Version()
	return ok && latest.Compare(v) >= 0
}
