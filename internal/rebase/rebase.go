// Package rebase brings a package folder, or a proposal, up to date with
// its package's latest release: it works out what the folder was made from
// (its base), and merges the folder's changes since then onto the latest
// release, file by file and line by line (textdiff.Merge3). Conflicts are
// written with markers for the author to resolve in their own editor or AI
// tool. It runs on the author's machine and never calls a model.
package rebase

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// Registry is the part of the registry a rebase reads. *client.Client
// satisfies it.
type Registry interface {
	Releases(ctx context.Context, id pack.ID) ([]api.Release, error)
	Fetch(ctx context.Context, id pack.ID, version string) (*pack.Pack, error)
	Proposals(ctx context.Context, pkg, status string) ([]api.Proposal, error)
	Proposal(ctx context.Context, id string) (api.Proposal, error)
	FetchProposal(ctx context.Context, p api.Proposal) (*pack.Pack, error)
}

// Service locates and rebases package folders against one registry.
type Service struct {
	reg     Registry
	records *Records
	remote  string // the registry's URL, which records are kept for
	now     func() time.Time
}

// New returns a Service reading reg, the registry at remote, and keeping
// its records in records.
func New(reg Registry, records *Records, remote string, now func() time.Time) *Service {
	return &Service{reg: reg, records: records, remote: remote, now: now}
}

// Source says how a folder's base was found.
type Source int

// The ways a base is found, in the order Locate tries them.
const (
	SourceFlag     Source = iota + 1 // --base
	SourceRecord                     // an earlier rebase of this folder
	SourceProposal                   // the open proposal at the folder's version
	SourceRelease                    // the release at the folder's version
	SourceGuess                      // the newest release below the folder's version
	SourceNone                       // no release at or below the folder's version
)

// Origin is what a package folder was made from, and where that stands.
type Origin struct {
	Package pack.ID
	// Base is the release the folder was made from, "" for none; Latest is
	// the package's latest release, "" before the first one.
	Base, Latest string
	// LatestBy names who published Latest, "" when nobody is known.
	LatestBy string
	// Replaces is the caller's own open proposal the folder updates, if
	// any. Only its author replaces a proposal.
	Replaces string
	// Start is the proposal named with --proposal, whose files are the
	// author's side when there is no folder; anyone's.
	Start string
	From  Source
}

// Behind reports whether a release came out after the folder's base, so
// its changes are not in the folder.
func (o Origin) Behind() bool { return o.Latest != o.Base }

// Guessed reports whether the base is only the newest release below the
// folder's version.
func (o Origin) Guessed() bool { return o.From == SourceGuess }

// Options are what the person said about a folder's origin.
type Options struct {
	// Base is --base: "", a version that must be a release, or "latest".
	Base string
	// Proposal is --proposal: the open proposal the folder (or, when
	// there is no folder, the result) updates.
	Proposal string
}

// ErrBehind matches the error of Origin.Refusal.
var ErrBehind = errors.New("the folder is behind the latest release")

// BehindError refuses to propose or publish a folder that is behind.
type BehindError struct {
	Origin Origin
	Verb   string // propose or publish
	Folder string // as the person typed it, "" for the current one
}

func (e *BehindError) Error() string {
	o := e.Origin
	made := "is based on " + o.Base
	if o.Base == "" {
		made = "was made before the first release"
	}
	if o.Guessed() {
		made += " (the newest release below its version)"
	}
	latest := o.Latest
	if o.LatestBy != "" {
		latest += " by " + o.LatestBy
	}
	cmd := "loadout rebase"
	if e.Folder != "" {
		cmd += " " + e.Folder
	}
	return fmt.Sprintf("%s %s, and %s was released since; its changes are not in your folder. Bring them in with: %s. If your folder already has them, %s with --base %s",
		o.Package.Name(), made, latest, cmd, e.Verb, o.Latest)
}

// Is matches ErrBehind.
func (e *BehindError) Is(target error) bool { return target == ErrBehind }

// Refusal is the error that stops verb (propose or publish) on a folder
// that is behind; propose and publish share it so they cannot drift.
func (o Origin) Refusal(verb, folder string) error {
	return &BehindError{Origin: o, Verb: verb, Folder: folder}
}

// Locate works out a folder's origin. p is the folder as loaded, before
// any --bump changes its version, or nil when there is no folder yet
// (then opts.Proposal names where to start). dir is the folder's absolute
// path, which rebase records are kept by.
//
// The base is the first of: --base; this folder's rebase record; the open
// proposal named or at the folder's version (its recorded base); the
// release at the folder's version; the newest release below it, as a
// guess. The proposal replaced is the caller's own: the one named, else
// the record's, else the open one at the folder's version. Someone else's
// proposal is never replaced and never gives the base of the caller's
// folder (the registry checks the author again).
func (s *Service) Locate(ctx context.Context, dir string, p *pack.Pack, opts Options) (Origin, error) {
	var named *api.Proposal
	if opts.Proposal != "" {
		prop, err := s.reg.Proposal(ctx, opts.Proposal)
		if err != nil {
			return Origin{}, fmt.Errorf("proposal %s: %w", opts.Proposal, err)
		}
		if prop.Status != api.StatusOpen {
			return Origin{}, fmt.Errorf("proposal %s is %s; only an open proposal is brought up to date", prop.ID, prop.Status)
		}
		named = &prop
	}
	id, err := packageOf(p, named)
	if err != nil {
		return Origin{}, err
	}
	rels, err := s.reg.Releases(ctx, id)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return Origin{}, fmt.Errorf("releases of %s: %w", id, err)
	}
	o := Origin{Package: id}
	if n := len(rels); n > 0 {
		o.Latest = rels[n-1].Version
		if by := rels[n-1].PublishedBy; by != nil {
			o.LatestBy = name(*by)
		}
	}
	rec, recorded, err := s.record(dir, id)
	if err != nil {
		return Origin{}, err
	}
	open := named
	if open == nil && p != nil {
		if open, err = s.openAt(ctx, id, p.Version()); err != nil {
			return Origin{}, err
		}
	}
	if named != nil {
		o.Start = named.ID
	}
	switch {
	case named != nil && named.ByCaller():
		o.Replaces = named.ID
	case named != nil:
		// Someone else's: the result is the caller's own proposal.
	case recorded && rec.Replaces != "":
		o.Replaces = rec.Replaces
	case open != nil:
		o.Replaces = open.ID
	}

	switch {
	case opts.Base != "":
		o.From = SourceFlag
		if o.Base, err = flagBase(id, rels, opts.Base); err != nil {
			return Origin{}, err
		}
	case recorded:
		o.From, o.Base = SourceRecord, rec.Onto
	case open != nil && (open.Base != "" || open.Latest != ""):
		// A registry that sends neither knows no base; fall through to the
		// folder's version.
		o.From, o.Base = SourceProposal, open.Base
	case p == nil:
		return Origin{}, fmt.Errorf("the registry does not say what proposal %s was made from; give it with --base VERSION", named.ID)
	default:
		o.From, o.Base = fromVersion(rels, p.Version())
	}
	return o, nil
}

// packageOf is the package a rebase is about: the folder's, or the named
// proposal's when there is no folder; both must agree.
func packageOf(p *pack.Pack, named *api.Proposal) (pack.ID, error) {
	switch {
	case p == nil && named == nil:
		return pack.ID{}, errors.New("no package folder here; give the folder, or --proposal ID to start from a proposal")
	case named == nil:
		return p.ID(), nil
	}
	id, err := pack.ParseID(named.Package)
	if err != nil {
		return pack.ID{}, fmt.Errorf("proposal %s: %w", named.ID, err)
	}
	if p != nil && p.ID() != id {
		return pack.ID{}, fmt.Errorf("proposal %s is for %s, but the folder holds %s", named.ID, id, p.ID())
	}
	return id, nil
}

// record returns the folder's rebase record when it is about id on this
// registry.
func (s *Service) record(dir string, id pack.ID) (Record, bool, error) {
	if s.records == nil || dir == "" {
		return Record{}, false, nil
	}
	rec, ok, err := s.records.Get(dir)
	if err != nil || !ok || rec.Remote != s.remote || rec.Package != id.String() {
		return Record{}, false, err
	}
	return rec, true, nil
}

// openAt returns the caller's own open proposal of id at version v, or
// nil. Someone else's says nothing about the caller's folder (and an older
// registry does not say whose it is, so none counts, as before).
func (s *Service) openAt(ctx context.Context, id pack.ID, v semver.Version) (*api.Proposal, error) {
	props, err := s.reg.Proposals(ctx, id.String(), api.StatusOpen)
	if err != nil {
		return nil, fmt.Errorf("open proposals of %s: %w", id, err)
	}
	i := slices.IndexFunc(props, func(p api.Proposal) bool { return p.Version == v.String() && p.ByCaller() })
	if i < 0 {
		return nil, nil
	}
	return &props[i], nil
}

// flagBase resolves --base: latest, or a version that is a release.
func flagBase(id pack.ID, rels []api.Release, flag string) (string, error) {
	if flag == "latest" {
		if n := len(rels); n > 0 {
			return rels[n-1].Version, nil
		}
		return "", nil
	}
	v, err := semver.Parse(flag)
	if err != nil {
		return "", fmt.Errorf("--base: %w; give a release version or latest", err)
	}
	if !slices.ContainsFunc(rels, func(r api.Release) bool { return r.Version == v.String() }) {
		return "", fmt.Errorf("--base %s is not a release of %s; %s", v, id, releaseList(rels))
	}
	return v.String(), nil
}

// releaseList says which releases there are, the newest ten at most.
func releaseList(rels []api.Release) string {
	if len(rels) == 0 {
		return "it has none yet, so leave --base out"
	}
	vs := make([]string, 0, 10)
	for _, r := range rels[max(0, len(rels)-10):] {
		vs = append(vs, r.Version)
	}
	return "its latest releases are " + strings.Join(vs, ", ")
}

// fromVersion finds the base from the folder's version: the release at
// it, else the newest release below it.
func fromVersion(rels []api.Release, v semver.Version) (Source, string) {
	from, base := SourceNone, ""
	for _, r := range rels { // lowest first
		rv, err := semver.Parse(r.Version)
		if err != nil {
			continue
		}
		switch c := rv.Compare(v); {
		case c == 0:
			return SourceRelease, r.Version
		case c < 0:
			from, base = SourceGuess, r.Version
		}
	}
	return from, base
}

// name names an actor by its token label without the way the token was
// made: "alice@example.com via loadout login" is alice@example.com.
func name(a api.Actor) string {
	if a.Label == "" {
		return ""
	}
	n, _, _ := strings.Cut(a.Label, " via ")
	return n
}
