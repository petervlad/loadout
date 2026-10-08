// Package reviewplan decides what `loadout review PKG` compares: which two
// versions of the package, and which of the team's packages sit beside each
// side. It reads the registry and the install lock and runs nothing, so the
// CLI stays free of selection rules and review stays free of the registry.
package reviewplan

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"regexp"
	"slices"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// Registry is the part of the registry a plan reads. *client.Client
// satisfies it; Packages must answer in the view of the package's own team,
// so that it lists that team and the layers above, closest first.
type Registry interface {
	Releases(ctx context.Context, id pack.ID) ([]api.Release, error)
	Fetch(ctx context.Context, id pack.ID, version string) (*pack.Pack, error)
	Proposals(ctx context.Context, pkg, status string) ([]api.Proposal, error)
	Proposal(ctx context.Context, id string) (api.Proposal, error)
	FetchProposal(ctx context.Context, p api.Proposal) (*pack.Pack, error)
	Packages(ctx context.Context) ([]api.Release, error)
}

// Installs lists the installs of one package. *release.Service satisfies
// it; an error wrapping install.ErrNotInstalled means there are none.
type Installs interface {
	Installed(ctx context.Context, id pack.ID) ([]lock.Entry, error)
}

// Source says where a side came from. The values match review.Version.Source.
type Source string

// The sources of a side.
const (
	SourceRelease   Source = "release"
	SourceInstalled Source = "installed"
	SourceProposal  Source = "proposal"
)

// Request is what the reviewer asked for.
type Request struct {
	Package pack.ID
	// From and To are "" for the default, "latest" for the latest release
	// (for From, nothing when there is none yet, as with the default), a
	// version (a release first, then an open proposal of that version), or
	// a proposal id. A version always has dots and an id never does, so the
	// two cannot clash.
	From, To string
	// NoContext skips the team's packages: Plan.Context stays nil and the
	// registry is not asked for them.
	NoContext bool
}

// Side is one version under comparison.
type Side struct {
	// Pack is nil only for From when the package has no release yet; then
	// only To runs.
	Pack   *pack.Pack
	Source Source
	// ProposalID is set when Source is SourceProposal.
	ProposalID string
}

// Version returns the side's version, or "" for an empty side.
func (s Side) Version() string {
	if s.Pack == nil {
		return ""
	}
	return s.Pack.Version().String()
}

// Plan is a resolved request.
type Plan struct {
	From, To Side
	// Proposal is set when To is a proposal: its prompts, its author, and
	// where a report is uploaded. Its status may be closed when the reviewer
	// named it by id.
	Proposal *api.Proposal
	// ContextReleases lists the packages beside each side, closest layer
	// first; nil with Request.NoContext.
	ContextReleases []api.Release
	// Context downloads ContextReleases one at a time, each digest-checked,
	// and stops at the first error. Walking it again downloads again. It
	// uses the context given to Resolve, so it must be walked before that
	// context ends.
	Context iter.Seq2[*pack.Pack, error]
	// Notes are sentences for the reviewer about choices made for them.
	Notes []string
}

// Planner resolves review requests against one registry and this machine's
// installs.
type Planner struct {
	reg      Registry
	installs Installs
}

// NewPlanner returns a planner. A nil installs means nothing is installed,
// so From defaults to the latest release.
func NewPlanner(reg Registry, installs Installs) *Planner {
	return &Planner{reg: reg, installs: installs}
}

// proposalID is the form of the ids the registry makes: 12 hex characters.
var proposalID = regexp.MustCompile(`^[0-9a-f]{12}$`)

// Resolve picks both sides and the context of req.
func (p *Planner) Resolve(ctx context.Context, req Request) (Plan, error) {
	if req.Package.IsZero() {
		return Plan{}, errors.New("name the package to review, for example payments/planner")
	}
	r := &resolver{reg: p.reg, installs: p.installs, id: req.Package}
	if err := r.loadReleases(ctx); err != nil {
		return Plan{}, err
	}
	to, prop, err := r.to(ctx, req.To)
	if err != nil {
		return Plan{}, err
	}
	from, err := r.from(ctx, req.From)
	if err != nil {
		return Plan{}, err
	}
	if from.Pack != nil && from.Pack.Digest() == to.Pack.Digest() {
		return Plan{}, fmt.Errorf("both sides are %s %s; pass --from with another version or proposal id", r.id, to.Version())
	}
	if prop != nil && prop.Behind() {
		r.noteBehind(*prop, from)
	}
	plan := Plan{From: from, To: to, Proposal: prop}
	if !req.NoContext {
		rels, err := r.context(ctx)
		if err != nil {
			return Plan{}, err
		}
		plan.ContextReleases = rels
		plan.Context = download(ctx, p.reg, slices.Clone(rels))
	}
	plan.Notes = r.notes
	return plan, nil
}

// resolver holds what one Resolve has read, so each list is fetched once.
type resolver struct {
	reg      Registry
	installs Installs
	id       pack.ID
	releases []api.Release // lowest version first
	open     []api.Proposal
	openRead bool
	notes    []string
}

// noteBehind tells the reviewer that a release came out after the
// proposal was made, and what the report compares it with, since that is
// not what approving it would replace.
func (r *resolver) noteBehind(p api.Proposal, from Side) {
	made := "is based on " + p.Base
	if p.Base == "" {
		made = "was made before the first release"
	}
	text := fmt.Sprintf("Proposal %s %s; %s was released since, so approving it would release %s without %s's changes.", p.ID, made, p.Latest, p.Version, p.Latest)
	switch from.Source {
	case SourceInstalled:
		text += fmt.Sprintf(" The report compares it with what you have installed, %s.", from.Version())
	case SourceRelease:
		text += fmt.Sprintf(" The report compares it with the release %s.", from.Version())
	case SourceProposal:
		text += fmt.Sprintf(" The report compares it with proposal %s.", from.ProposalID)
	}
	r.notes = append(r.notes, text)
}

func (r *resolver) note(format string, args ...any) {
	r.notes = append(r.notes, fmt.Sprintf(format, args...))
}

func (r *resolver) loadReleases(ctx context.Context) error {
	rels, err := r.reg.Releases(ctx, r.id)
	if errors.Is(err, api.ErrNotFound) {
		return nil // no release yet; a proposal may still exist
	}
	if err != nil {
		return fmt.Errorf("list releases of %s: %w", r.id, err)
	}
	r.releases = rels
	return nil
}

func (r *resolver) latest() (api.Release, bool) {
	if len(r.releases) == 0 {
		return api.Release{}, false
	}
	return r.releases[len(r.releases)-1], true
}

func (r *resolver) release(version string) (api.Release, bool) {
	i := slices.IndexFunc(r.releases, func(rel api.Release) bool { return rel.Version == version })
	if i < 0 {
		return api.Release{}, false
	}
	return r.releases[i], true
}

// openProposals lists the package's open proposals, newest first.
func (r *resolver) openProposals(ctx context.Context) ([]api.Proposal, error) {
	if r.openRead {
		return r.open, nil
	}
	props, err := r.reg.Proposals(ctx, r.id.String(), api.StatusOpen)
	if err != nil {
		return nil, fmt.Errorf("list proposals of %s: %w", r.id, err)
	}
	// The registry already answers newest first; sorting keeps that true
	// for any Registry, and a stable sort keeps ties in its order.
	slices.SortStableFunc(props, func(a, b api.Proposal) int { return b.CreatedAt.Compare(a.CreatedAt) })
	r.open, r.openRead = props, true
	return props, nil
}

// to resolves the side under review: by default the newest open proposal,
// else the latest release.
func (r *resolver) to(ctx context.Context, spec string) (Side, *api.Proposal, error) {
	if spec != "" {
		return r.pick(ctx, spec)
	}
	props, err := r.openProposals(ctx)
	if err != nil {
		return Side{}, nil, err
	}
	if len(props) > 0 {
		if len(props) > 1 {
			r.note("%s has %d open proposals; reviewing the newest, %s (%s). Pick another with --to.", r.id, len(props), props[0].ID, props[0].Version)
		}
		return r.proposalSide(ctx, props[0])
	}
	rel, ok := r.latest()
	if !ok {
		return Side{}, nil, fmt.Errorf("%s has no release and no open proposal to review", r.id)
	}
	r.note("%s has no open proposal; reviewing the latest release %s.", r.id, rel.Version)
	side, err := r.releaseSide(ctx, rel, SourceRelease)
	return side, nil, err
}

// from resolves the side compared against: by default the reviewer's
// installed version, else the latest release.
func (r *resolver) from(ctx context.Context, spec string) (Side, error) {
	if spec == "latest" {
		if _, ok := r.latest(); !ok {
			// Like the default: a first version is reviewed on its own.
			r.note("%s has no release yet, so only the version under review runs.", r.id)
			return Side{}, nil
		}
	}
	if spec != "" {
		side, _, err := r.pick(ctx, spec)
		return side, err
	}
	entries, err := r.installed(ctx)
	if err != nil {
		return Side{}, err
	}
	// A folder install has no registry version to fetch.
	fromRegistry := slices.DeleteFunc(slices.Clone(entries), func(e lock.Entry) bool { return e.Remote == "" })
	var high string
	if len(fromRegistry) > 0 {
		high = r.highest(fromRegistry)
		if rel, ok := r.release(high); ok {
			return r.releaseSide(ctx, rel, SourceInstalled)
		}
	}
	latest, ok := r.latest()
	if !ok {
		r.note("%s has no release yet, so only the version under review runs.", r.id)
		return Side{}, nil
	}
	switch {
	case high != "":
		r.note("Your installed %s %s is no longer on the registry; comparing with the latest release %s.", r.id, high, latest.Version)
	case len(entries) > 0:
		r.note("Your install of %s comes from a folder; comparing with the latest release %s.", r.id, latest.Version)
	default:
		r.note("You have not installed %s; comparing with the latest release %s.", r.id, latest.Version)
	}
	return r.releaseSide(ctx, latest, SourceRelease)
}

func (r *resolver) installed(ctx context.Context) ([]lock.Entry, error) {
	if r.installs == nil {
		return nil, nil
	}
	entries, err := r.installs.Installed(ctx, r.id)
	if errors.Is(err, install.ErrNotInstalled) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read installs of %s: %w", r.id, err)
	}
	return entries, nil
}

// highest returns the highest version among entries, and says so when the
// installs in different places differ.
func (r *resolver) highest(entries []lock.Entry) string {
	high := slices.MaxFunc(entries, func(a, b lock.Entry) int { return a.Version.Compare(b.Version) }).Version
	if slices.ContainsFunc(entries, func(e lock.Entry) bool { return e.Version != high }) {
		r.note("You have %s installed at different versions in different places; comparing with the highest, %s.", r.id, high)
	}
	return high.String()
}

// pick resolves an explicit --from or --to value.
func (r *resolver) pick(ctx context.Context, spec string) (Side, *api.Proposal, error) {
	if spec == "latest" {
		rel, ok := r.latest()
		if !ok {
			return Side{}, nil, fmt.Errorf("%s has no release yet", r.id)
		}
		side, err := r.releaseSide(ctx, rel, SourceRelease)
		return side, nil, err
	}
	if proposalID.MatchString(spec) {
		return r.proposalByID(ctx, spec)
	}
	v, err := semver.Parse(spec)
	if err != nil {
		return Side{}, nil, fmt.Errorf("%q is neither a version such as 1.2.0 nor a proposal id such as 3f2a9c01b7de", spec)
	}
	if rel, ok := r.release(v.String()); ok {
		side, err := r.releaseSide(ctx, rel, SourceRelease)
		return side, nil, err
	}
	props, err := r.openProposals(ctx)
	if err != nil {
		return Side{}, nil, err
	}
	if i := slices.IndexFunc(props, func(p api.Proposal) bool { return p.Version == v.String() }); i >= 0 {
		return r.proposalSide(ctx, props[i])
	}
	return Side{}, nil, fmt.Errorf("%s has no release or open proposal %s; see them with: loadout review list --package %s", r.id, v, r.id)
}

func (r *resolver) proposalByID(ctx context.Context, id string) (Side, *api.Proposal, error) {
	prop, err := r.reg.Proposal(ctx, id)
	if errors.Is(err, api.ErrNotFound) {
		return Side{}, nil, fmt.Errorf("there is no proposal %s; see them with: loadout review list --package %s", id, r.id)
	}
	if err != nil {
		return Side{}, nil, fmt.Errorf("read proposal %s: %w", id, err)
	}
	if prop.Package != r.id.String() {
		return Side{}, nil, fmt.Errorf("proposal %s is for %s, not %s; review it with: loadout review %s --to %s", id, prop.Package, r.id, prop.Package, id)
	}
	return r.proposalSide(ctx, prop)
}

func (r *resolver) proposalSide(ctx context.Context, prop api.Proposal) (Side, *api.Proposal, error) {
	p, err := r.reg.FetchProposal(ctx, prop)
	if err != nil {
		return Side{}, nil, err
	}
	return Side{Pack: p, Source: SourceProposal, ProposalID: prop.ID}, &prop, nil
}

func (r *resolver) releaseSide(ctx context.Context, rel api.Release, src Source) (Side, error) {
	p, err := fetchRelease(ctx, r.reg, r.id, rel)
	if err != nil {
		return Side{}, err
	}
	return Side{Pack: p, Source: src}, nil
}

// context lists the latest release of every package the team reads, the
// closest layer first, without the package under review. Two packages with
// one name would render to the same place, so the first (closest) keeps the
// name and the package under review always keeps its own.
func (r *resolver) context(ctx context.Context) ([]api.Release, error) {
	all, err := r.reg.Packages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the packages beside %s: %w", r.id, err)
	}
	owner := map[string]pack.ID{r.id.Name(): r.id}
	var out []api.Release
	for _, rel := range all {
		id, err := pack.ParseID(rel.ID)
		if err != nil {
			return nil, fmt.Errorf("registry listed %q: %w", rel.ID, err)
		}
		if id == r.id {
			continue
		}
		if o, taken := owner[id.Name()]; taken {
			r.note("Left %s out of the context: %s has the same name.", id, o)
			continue
		}
		owner[id.Name()] = id
		out = append(out, rel)
	}
	return out, nil
}

// download fetches rels in order, one at a time, so memory holds one
// package while the consumer renders it.
func download(ctx context.Context, reg Registry, rels []api.Release) iter.Seq2[*pack.Pack, error] {
	return func(yield func(*pack.Pack, error) bool) {
		for _, rel := range rels {
			p, err := fetchListed(ctx, reg, rel)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(p, nil) {
				return
			}
		}
	}
}

func fetchListed(ctx context.Context, reg Registry, rel api.Release) (*pack.Pack, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := pack.ParseID(rel.ID)
	if err != nil {
		return nil, fmt.Errorf("registry listed %q: %w", rel.ID, err)
	}
	return fetchRelease(ctx, reg, id, rel)
}

// fetchRelease downloads rel and checks it is the content the listing
// named, so the report lists exactly what ran.
func fetchRelease(ctx context.Context, reg Registry, id pack.ID, rel api.Release) (*pack.Pack, error) {
	p, err := reg.Fetch(ctx, id, rel.Version)
	if err != nil {
		return nil, fmt.Errorf("fetch %s@%s: %w", id, rel.Version, err)
	}
	if got := string(p.Digest()); rel.Digest != "" && got != rel.Digest {
		return nil, fmt.Errorf("fetch %s@%s: content is %s, the registry listed %s", id, rel.Version, got, rel.Digest)
	}
	return p, nil
}
