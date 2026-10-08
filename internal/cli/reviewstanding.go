package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/semver"
)

// maxSince is how many releases since a proposal's base review show names
// before it says "and N more".
const maxSince = 5

// standing is where a proposal stands against its package's releases, as
// review show tells it: what it was made from, what came out since, and
// which version its diff starts from.
type standing struct {
	prop api.Proposal
	rels []api.Release // lowest version first
}

func newStanding(p api.Proposal, rels []api.Release) standing {
	return standing{prop: p, rels: rels}
}

// known reports whether the registry says where the proposal stands. An
// older registry sends neither base nor latest; so does a package with no
// release at all, which has nothing to say either.
func (s standing) known() bool { return s.prop.Base != "" || s.prop.Latest != "" }

// diffBase is the release the diff starts from: the recorded base, else
// nothing for a proposal made before the first release, else (an older
// registry) the latest release, as review show always did.
func (s standing) diffBase() (api.Release, bool) {
	switch {
	case s.prop.Base != "":
		return s.release(s.prop.Base)
	case s.known():
		return api.Release{}, false
	case len(s.rels) > 0:
		return s.rels[len(s.rels)-1], true
	}
	return api.Release{}, false
}

func (s standing) release(v string) (api.Release, bool) {
	for _, r := range s.rels {
		if r.Version == v {
			return r, true
		}
	}
	return api.Release{}, false
}

// diffLabel names the diff's starting side.
func (s standing) diffLabel() string {
	if base, ok := s.diffBase(); ok {
		return base.Version
	}
	return "(none)"
}

// diffTitle says what the diff shows.
func (s standing) diffTitle() string {
	base, ok := s.diffBase()
	switch {
	case !ok:
		return "Files of the proposal (there was no release when it was made)"
	case s.known():
		return "What the author changed since " + base.Version
	}
	return "Changes from the latest release " + base.Version
}

// since lists the releases after the base, lowest first.
func (s standing) since() []api.Release {
	base, err := semver.Parse(s.prop.Base)
	var out []api.Release
	for _, r := range s.rels {
		v, perr := semver.Parse(r.Version)
		if perr == nil && (err != nil || v.Compare(base) > 0) {
			out = append(out, r)
		}
	}
	return out
}

// write says what the proposal was made from and, for an open one that is
// behind, what approving it would drop.
func (s standing) write(out io.Writer) {
	if !s.known() {
		return
	}
	p := s.prop
	made := "Made before the first release"
	if p.Base != "" {
		made = "Made from " + p.Base
	}
	fmt.Fprintf(out, "\n%s, proposes %s.", made, p.Version)
	since := s.since()
	if len(since) == 0 { // a release landed between the two reads
		since = []api.Release{{Version: p.Latest}}
	}
	switch {
	case !p.Behind():
		fmt.Fprintln(out)
	case p.Blocked():
		fmt.Fprintf(out, " %s was released after it, so it cannot be approved as %s; the author brings it up to date with loadout rebase --proposal %s, which raises its version, then proposes again.\n", releaseName(since[len(since)-1]), p.Version, p.ID)
	case len(since) == 1:
		fmt.Fprintf(out, " The latest release is %s, released after it: approving releases %s without %s's changes.\n", releaseName(since[0]), p.Version, since[0].Version)
	default:
		fmt.Fprintf(out, " Released after it: %s. Approving releases %s without their changes.\n", releaseNames(since), p.Version)
	}
}

// writeNext gives the commands that show the rest of a behind proposal.
func (s standing) writeNext(out io.Writer) {
	if !s.prop.Behind() {
		return
	}
	if s.prop.Base != "" {
		fmt.Fprintf(out, "\nWhat was released since: loadout diff %s %s %s\n", s.prop.Package, s.prop.Base, s.prop.Latest)
	}
	fmt.Fprintf(out, "The author brings it up to date with: loadout rebase --proposal %s\n", s.prop.ID)
}

// releaseName is a release's version and who published it, or the version
// alone when nobody is known.
func releaseName(r api.Release) string {
	if r.PublishedBy == nil {
		return r.Version
	}
	return r.Version + " by " + authorName(*r.PublishedBy)
}

// releaseNames names up to maxSince releases, then how many more.
func releaseNames(rels []api.Release) string {
	names := make([]string, 0, maxSince)
	for _, r := range rels[:min(len(rels), maxSince)] {
		names = append(names, releaseName(r))
	}
	out := strings.Join(names, ", ")
	if more := len(rels) - maxSince; more > 0 {
		out += fmt.Sprintf(" and %d more", more)
	}
	return out
}
