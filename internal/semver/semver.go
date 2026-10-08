// Package semver implements the MAJOR.MINOR.PATCH versions loadout packages use.
//
// Pre-release and build metadata are deliberately not supported: a package
// version is either published or it is not.
package semver

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// maxComponent bounds each number so Bump can never overflow.
const maxComponent = 1<<31 - 1

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Version is an immutable MAJOR.MINOR.PATCH version. The zero value is 0.0.0.
type Version struct {
	major, minor, patch int
}

// Part selects which component Bump increments.
type Part int

// Version components, from least to most significant.
const (
	Patch Part = iota + 1
	Minor
	Major
)

// New returns the version major.minor.patch.
func New(major, minor, patch int) Version {
	return Version{major: major, minor: minor, patch: patch}
}

// Parse reads a version such as "1.3.0". A leading "v" is accepted.
func Parse(s string) (Version, error) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q: want MAJOR.MINOR.PATCH", s)
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || !allDigits(p) || (len(p) > 1 && p[0] == '0') {
			return Version{}, fmt.Errorf("invalid version %q: %q is not a non-negative integer without leading zeros", s, p)
		}
		if n > maxComponent {
			return Version{}, fmt.Errorf("invalid version %q: %d is larger than %d", s, n, maxComponent)
		}
		nums[i] = n
	}
	return New(nums[0], nums[1], nums[2]), nil
}

// String formats the version as MAJOR.MINOR.PATCH.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

// Bump returns the next version, resetting the lower components.
func (v Version) Bump(p Part) Version {
	switch p {
	case Major:
		return New(v.major+1, 0, 0)
	case Minor:
		return New(v.major, v.minor+1, 0)
	default:
		return New(v.major, v.minor, v.patch+1)
	}
}

// Compare returns -1, 0 or +1 when v is lower than, equal to or higher than w.
func (v Version) Compare(w Version) int {
	return cmp.Or(
		cmp.Compare(v.major, w.major),
		cmp.Compare(v.minor, w.minor),
		cmp.Compare(v.patch, w.patch),
	)
}

// MarshalText implements encoding.TextMarshaler so versions serialise as strings.
func (v Version) MarshalText() ([]byte, error) {
	return []byte(v.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (v *Version) UnmarshalText(b []byte) error {
	parsed, err := Parse(string(b))
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}
