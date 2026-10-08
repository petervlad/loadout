// Package buildinfo says what this loadout binary is: the release it is or
// was built after, and how it was installed, so it can be compared with the
// release a registry advertises.
package buildinfo

import (
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/semver"
)

// Build is this binary's version. The zero value is an unknown build, which
// never counts as behind anything.
type Build struct {
	raw      string         // as given: the ldflags or Go's module version
	release  semver.Version // the release it is, or the last one before it
	after    bool           // built from a commit after release
	modified bool           // built from a tree with uncommitted changes
	known    bool           // release is set
	metadata bool           // a tag with build metadata (v1.2.3+build): not a clean release
	revision string         // the commit, shortened, when the build says it
	when     time.Time      // the commit time, when the build says it
}

// The forms a version takes besides MAJOR.MINOR.PATCH, after the leading
// "v" and a "+dirty" or "-dirty" suffix are taken off.
var (
	// git describe after a tag: 0.4.0-3-gabc1234.
	describeAfter = regexp.MustCompile(`^(\d+\.\d+\.\d+)-\d+-g([0-9a-f]{4,40})$`)
	// Go's pseudo-version after a release X.Y.Z: X.Y.(Z+1)-0.TIME-HASH.
	pseudoAfter = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)-0\.(\d{14})-([0-9a-f]{12})$`)
	// Go's pseudo-version with no tag reachable: X.0.0-TIME-HASH.
	pseudoNone = regexp.MustCompile(`^\d+\.\d+\.\d+-(\d{14})-([0-9a-f]{12})$`)
	// A bare commit hash, as git describe --always gives without tags.
	bareHash = regexp.MustCompile(`^([0-9a-f]{7,40})$`)
)

// pseudoTime is the layout of the time in a Go pseudo-version.
const pseudoTime = "20060102150405"

// shortHash is how many characters of a commit hash loadout shows.
const shortHash = 7

// Parse reads a version string in any of the forms loadout is built with:
// a release tag, git describe after a tag, Go's pseudo-versions, a bare
// commit hash, "dev". Anything it does not recognise is an unknown build
// that keeps the string.
func Parse(s string) Build {
	s = strings.TrimSpace(s)
	b := Build{raw: s}
	rest := strings.TrimPrefix(s, "v")
	for _, dirty := range []string{"+dirty", "-dirty"} {
		if cut, ok := strings.CutSuffix(rest, dirty); ok {
			rest, b.modified = cut, true
		}
	}
	// Other build metadata ("+build", "+incompatible") is compared without
	// it, but such a build is never the release itself: deploy.sh does not
	// advertise it either.
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		rest, b.metadata = rest[:i], true
	}
	if v, err := semver.Parse(rest); err == nil {
		if v != (semver.Version{}) {
			b.release, b.known = v, true
		}
		return b
	}
	if m := describeAfter.FindStringSubmatch(rest); m != nil {
		if v, err := semver.Parse(m[1]); err == nil {
			b.release, b.known, b.after = v, true, true
			b.revision = short(m[2])
		}
		return b
	}
	if m := pseudoAfter.FindStringSubmatch(rest); m != nil {
		b.when, _ = time.Parse(pseudoTime, m[4])
		b.revision = short(m[5])
		if v, ok := before(m[1], m[2], m[3]); ok {
			b.release, b.known, b.after = v, true, true
		}
		return b
	}
	if m := pseudoNone.FindStringSubmatch(rest); m != nil {
		b.when, _ = time.Parse(pseudoTime, m[1])
		b.revision = short(m[2])
		return b
	}
	if m := bareHash.FindStringSubmatch(rest); m != nil {
		b.revision = short(m[1])
	}
	return b
}

// before is the release a pseudo-version X.Y.Z-0.TIME-HASH was built after:
// X.Y.(Z-1), by Go's rule for pseudo-versions.
func before(major, minor, patch string) (semver.Version, bool) {
	if _, err := semver.Parse(major + "." + minor + "." + patch); err != nil {
		return semver.Version{}, false
	}
	// Parse checked each part: digits without leading zeros, within range.
	x, _ := strconv.Atoi(major)
	y, _ := strconv.Atoi(minor)
	z, _ := strconv.Atoi(patch)
	if z == 0 || (x == 0 && y == 0 && z == 1) {
		return semver.Version{}, false
	}
	return semver.New(x, y, z-1), true
}

func short(hash string) string {
	if len(hash) > shortHash {
		return hash[:shortHash]
	}
	return hash
}

// Current is the running binary: the ldflags version when it names a
// release, else the module version and vcs settings Go stamped into the
// binary, else the ldflags string as an unknown build. info and ok are
// debug.ReadBuildInfo's, passed in for tests.
func Current(ldflags string, info *debug.BuildInfo, ok bool) Build {
	ldflags = strings.TrimSpace(ldflags)
	if b := Parse(ldflags); b.known {
		return b
	}
	var b Build
	if ok && info != nil {
		b = fromModule(info)
	}
	if b.known || b.revision != "" {
		return b
	}
	if ldflags != "" && ldflags != "dev" {
		return Parse(ldflags)
	}
	b.raw = "dev"
	return b
}

// fromModule reads the module version and the vcs settings `go build` and
// `go install` stamp into a binary built from a git checkout.
func fromModule(info *debug.BuildInfo) Build {
	b := Parse(info.Main.Version)
	if v := info.Main.Version; v == "" || v == "(devel)" {
		b = Build{}
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if b.revision == "" {
				b.revision = short(s.Value)
			}
		case "vcs.time":
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				b.when = t
			}
		case "vcs.modified":
			if s.Value == "true" {
				b.modified = true
			}
		}
	}
	if b.raw == "" && b.revision != "" {
		b.raw = b.revision
		if b.modified {
			b.raw += "-dirty"
		}
	}
	return b
}

// Raw is the version as it was given, for machines: the User-Agent, the
// health answer, the log.
func (b Build) Raw() string {
	if b.raw == "" {
		return "dev"
	}
	return b.raw
}

// Release is the release this build is, or the last one before it; false
// for an unknown build.
func (b Build) Release() (semver.Version, bool) {
	return b.release, b.known
}

// Modified reports whether the build came from a tree with uncommitted
// changes.
func (b Build) Modified() bool { return b.modified }

// Exact reports whether the build is a release itself: its tag, nothing
// after it and no uncommitted changes.
func (b Build) Exact() bool { return b.known && !b.after && !b.modified && !b.metadata }

// Behind reports whether v is a newer release than b. An unknown or
// modified build is never behind, so a developer is never nagged; a build
// after 0.4.0 is not behind 0.4.0, only behind 0.5.0.
func (b Build) Behind(v semver.Version) bool {
	return b.known && !b.modified && b.release.Compare(v) < 0
}

// String is the build for people: "0.4.0", "0.4.0 (modified)", "after
// 0.4.0 (built 4 Oct 2026 from b42ddb9)", "dev (built 4 Oct 2026 from
// b42ddb9)" or "dev".
func (b Build) String() string {
	name := "dev"
	if b.known {
		name = b.release.String()
		if b.after {
			name = "after " + name
		}
	}
	var notes []string
	switch {
	case !b.when.IsZero() && b.revision != "":
		notes = append(notes, "built "+b.when.UTC().Format("2 Jan 2006")+" from "+b.revision)
	case b.revision != "":
		notes = append(notes, "from "+b.revision)
	}
	if b.modified {
		notes = append(notes, "modified")
	}
	if len(notes) == 0 {
		return name
	}
	return name + " (" + strings.Join(notes, ", ") + ")"
}
