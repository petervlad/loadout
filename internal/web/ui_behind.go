package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// A proposal is made from one release of its package, its base. When a
// newer release comes out while it is open, approving it releases files
// without that release's changes. The proposal page says so above its
// actions, and the diff page shows the three comparisons that explain it.

// standing is where p stands against its package's latest release now.
func (h *handler) standing(r *http.Request, t team, p registry.Proposal) (registry.Standing, error) {
	st, err := h.ui.Proposals.Standings(r.Context(), t.pr, []registry.Proposal{p})
	if err != nil {
		return registry.Standing{}, err
	}
	return st[0], nil
}

// proposalSides are the versions of a package a proposal is compared
// with: its base (nil before the first release), the proposal itself and,
// only when it is open and behind, the latest release.
type proposalSides struct {
	base, prop, latest *pack.Pack
}

// sides reads the bundles p's pages compare.
func (h *handler) sides(r *http.Request, t team, p registry.Proposal, st registry.Standing) (proposalSides, error) {
	var out proposalSides
	_, data, err := h.ui.Proposals.ProposalBundle(r.Context(), t.pr, p.ID)
	if err != nil {
		return out, err
	}
	if out.prop, err = bundle.Decode(data); err != nil {
		return out, fmt.Errorf("proposal %s: %w", p.ID, err)
	}
	if !p.Base.IsZero() {
		if out.base, err = h.release(r, t, p.Package, p.Base.String()); err != nil {
			return out, err
		}
	}
	if behindOf(p, st) != "" {
		if out.latest, err = h.release(r, t, p.Package, st.Latest.String()); err != nil {
			return out, err
		}
	}
	return out, nil
}

// release reads one release's files.
func (h *handler) release(r *http.Request, t team, id pack.ID, version string) (*pack.Pack, error) {
	_, data, err := h.ui.Registry.Fetch(r.Context(), t.pr, id, version)
	if err != nil {
		return nil, err
	}
	p, err := bundle.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("release %s@%s: %w", id, version, err)
	}
	return p, nil
}

// releasesSince lists the releases of p's package after its base, newest
// first.
func (h *handler) releasesSince(r *http.Request, t team, p registry.Proposal) ([]registry.Release, error) {
	rels, err := h.ui.Registry.Releases(r.Context(), t.pr, p.Package)
	if errors.Is(err, registry.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []registry.Release
	for _, rel := range rels {
		if p.Base.Before(registry.BaseAt(rel.Version)) {
			out = append(out, rel)
		}
	}
	slices.SortFunc(out, func(a, b registry.Release) int { return b.Version.Compare(a.Version) })
	return out, nil
}

// maxNamed is how many releases, and how many files, the notice names
// before it says "and N more".
const maxNamed = 5

// behindView is the notice on a proposal that is behind.
type behindView struct {
	Base    string // "" when it was made before the first release
	Latest  string
	To      string
	Blocked bool
	// Releases are the releases since the base, newest first, at most
	// maxNamed of them; MoreReleases counts the rest.
	Releases     []sinceItem
	MoreReleases int
	// Files are the files the releases since changed, at most maxNamed;
	// Changed counts all of them and Overlap those the proposal also
	// changes.
	Files   []listItem
	Changed int
	Overlap int
	Author  bool
	// Diff is the path of the proposal's diff page.
	Diff string
	// ID is the proposal's, for the author's rebase command.
	ID string
}

// Count is how many releases came out since the base.
func (b *behindView) Count() int { return len(b.Releases) + b.MoreReleases }

// One reports whether a single release came out since the base.
func (b *behindView) One() bool { return b.Count() == 1 }

// MoreFiles is how many changed files the notice does not name.
func (b *behindView) MoreFiles() int { return b.Changed - len(b.Files) }

// sinceItem is one release in the notice: "3.0.0 by Dana, 2 days ago".
// Sep comes before it in the sentence; By is "" when nobody is known.
type sinceItem struct {
	Sep     string
	Version string
	By      string
	At      time.Time
}

// listItem is one name in a sentence, with the words before it: "", ", "
// or " and ".
type listItem struct {
	Sep  string
	Name string
}

// behindNote fills the notice of an open proposal that is behind.
func behindNote(t team, p registry.Proposal, st registry.Standing, sides proposalSides, since []registry.Release, names map[string]string) *behindView {
	v := &behindView{
		Base:    p.Base.String(),
		Latest:  st.Latest.String(),
		To:      p.Version.String(),
		Blocked: st.Blocks(p.Version),
		Author:  t.is(p.Author),
		Diff:    "/t/" + t.ns() + "/reviews/" + p.ID + "/diff",
		ID:      p.ID,
	}
	for i, rel := range since {
		if i == maxNamed {
			v.MoreReleases = len(since) - maxNamed
			break
		}
		item := sinceItem{Version: rel.Version.String(), By: publisher(rel.PublishedBy, names), At: rel.PublishedAt}
		if i > 0 {
			item.Sep = "; "
		}
		v.Releases = append(v.Releases, item)
	}
	changed := changedFiles(sides.base, sides.latest)
	v.Changed = len(changed)
	mine := changedFiles(sides.base, sides.prop)
	for _, name := range changed {
		if slices.Contains(mine, name) {
			v.Overlap++
		}
	}
	shown := changed[:min(len(changed), maxNamed)]
	for i, name := range shown {
		item := listItem{Name: name}
		switch {
		case i == 0:
		case i == len(shown)-1 && v.Changed == len(shown):
			item.Sep = " and "
		default:
			item.Sep = ", "
		}
		v.Files = append(v.Files, item)
	}
	return v
}

// publisher names who released a version, "" when nobody is known (every
// direct publish from before releases recorded it).
func publisher(a registry.Actor, names map[string]string) string {
	if a == (registry.Actor{}) {
		return ""
	}
	return who(a, names)
}

// changedFiles lists the paths whose content differs between two versions
// of a package (from is nil before the first release), in to's reading
// order. It compares content only, which is cheap: whether lines conflict
// is for the author's own merge. The manifest counts only when more than
// its version changed, since the version always does.
func changedFiles(from, to *pack.Pack) []string {
	base := map[string][]byte{}
	if from != nil {
		base = from.Files()
	}
	files := to.Files()
	var out []string
	for _, name := range diffOrder(to, base) {
		old, cur := base[name], files[name]
		if name == pack.ManifestFile {
			old, cur = withoutVersion(old), withoutVersion(cur)
		}
		if !bytes.Equal(old, cur) {
			out = append(out, name)
		}
	}
	return out
}

// withoutVersion drops the manifest's top-level version line.
func withoutVersion(manifest []byte) []byte {
	var out [][]byte
	for line := range bytes.Lines(manifest) {
		if !bytes.HasPrefix(line, []byte("version:")) {
			out = append(out, line)
		}
	}
	return bytes.Join(out, nil)
}

// proposalLink names another proposal of the page: the one this proposal
// replaced, or the one that replaced it.
type proposalLink struct {
	ID   string
	Path string
	// Onto is the release the replacement was made from, when it differs
	// from this proposal's base (a rebase moved it), else "".
	Onto string
	// Reports is true when the replaced proposal has comparison reports,
	// which compare its older content.
	Reports bool
}

// replacement fills the links between p and the proposals it replaced or
// was replaced by. A linked proposal that cannot be read keeps its id.
func (h *handler) replacement(r *http.Request, t team, p registry.Proposal, v *proposalView) error {
	reviews := "/t/" + t.ns() + "/reviews/"
	if p.Replaces != "" {
		v.Replaces = &proposalLink{ID: p.Replaces, Path: reviews + p.Replaces}
		old, err := h.ui.Proposals.Proposal(r.Context(), t.pr, p.Replaces)
		if err != nil && !errors.Is(err, registry.ErrNotFound) {
			return err
		}
		v.Replaces.Reports = err == nil && len(old.Reviews) > 0
	}
	if p.ReplacedBy != "" {
		v.ReplacedBy = &proposalLink{ID: p.ReplacedBy, Path: reviews + p.ReplacedBy}
		next, err := h.ui.Proposals.Proposal(r.Context(), t.pr, p.ReplacedBy)
		if err != nil && !errors.Is(err, registry.ErrNotFound) {
			return err
		}
		if err == nil && !next.Base.Equal(p.Base) {
			v.ReplacedBy.Onto = next.Base.String()
		}
	}
	return nil
}
