// Package release decides which version of a package to move to: the next
// version to publish, and the version a rollback goes back to.
package release

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// Registry lists a package's releases, oldest first.
type Registry interface {
	Releases(ctx context.Context, id pack.ID) ([]api.Release, error)
}

// Installs lists what is installed on this machine.
type Installs interface {
	Entries(ctx context.Context) ([]lock.Entry, error)
}

// ErrNoOlder means a rollback found no release older than the installed one.
var ErrNoOlder = errors.New("no version older than the installed one")

// NotOlderError means a rollback was asked to move forward.
type NotOlderError struct {
	ID        pack.ID
	Target    semver.Version
	Installed semver.Version
}

func (e *NotOlderError) Error() string {
	return fmt.Sprintf("%s@%s is not older than the installed %s; to move forward use: loadout install %s@%s", e.ID, e.Target, e.Installed, e.ID, e.Target)
}

// Service answers version questions about installed packages.
type Service struct {
	installs Installs
}

// New returns a Service reading installs from installs.
func New(installs Installs) *Service { return &Service{installs: installs} }

// Installed returns every install of id, in any scope, newest version first.
// It wraps install.ErrNotInstalled when there is none.
func (s *Service) Installed(ctx context.Context, id pack.ID) ([]lock.Entry, error) {
	entries, err := s.installs.Entries(ctx)
	if err != nil {
		return nil, err
	}
	entries = slices.DeleteFunc(entries, func(e lock.Entry) bool { return e.ID != id })
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s: %w", id, install.ErrNotInstalled)
	}
	slices.SortStableFunc(entries, func(a, b lock.Entry) int { return b.Version.Compare(a.Version) })
	return entries, nil
}

// Next returns local bumped by part, starting from the latest published
// version when that is higher. A package that was never published bumps
// from local.
func Next(ctx context.Context, reg Registry, id pack.ID, local semver.Version, part semver.Part) (semver.Version, error) {
	rels, err := reg.Releases(ctx, id)
	if errors.Is(err, api.ErrNotFound) {
		rels, err = nil, nil // first publish
	}
	if err != nil {
		return semver.Version{}, err
	}
	base := local
	if n := len(rels); n > 0 {
		latest, err := semver.Parse(rels[n-1].Version)
		if err != nil {
			return semver.Version{}, err
		}
		if latest.Compare(base) > 0 {
			base = latest
		}
	}
	return base.Bump(part), nil
}

// Fetcher lists releases and downloads one, as a registry client does.
type Fetcher interface {
	Registry
	Fetch(ctx context.Context, id pack.ID, version string) (*pack.Pack, error)
}

// Resolve fetches the highest release of req.ID whose major is req.Major:
// the template a pipeline binding is rendered from.
func Resolve(ctx context.Context, reg Fetcher, req pack.Requirement) (*pack.Pack, error) {
	rels, err := reg.Releases(ctx, req.ID)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return nil, err
	}
	var best semver.Version
	found := false
	for _, rel := range rels {
		v, err := semver.Parse(rel.Version)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", req.ID, err)
		}
		if v.Major() == req.Major && (!found || v.Compare(best) > 0) {
			best, found = v, true
		}
	}
	if !found {
		return nil, fmt.Errorf("%s has no release with major %d: %w", req.ID, req.Major, api.ErrNotFound)
	}
	return reg.Fetch(ctx, req.ID, best.String())
}

// Plan is what a rollback does.
type Plan struct {
	// Version is the version to go back to.
	Version semver.Version
	// Rollback are the installs newer than Version; they go back to it.
	Rollback []lock.Entry
	// Keep are the installs already at or below Version. A rollback never
	// moves an install forward, so they are left alone.
	Keep []lock.Entry
}

// PlanRollback decides where a rollback of installs goes. to is the version
// asked for; nil means the highest release older than the newest install.
// It refuses when no install is newer than the target.
func PlanRollback(ctx context.Context, reg Registry, installs []lock.Entry, to *semver.Version) (Plan, error) {
	if len(installs) == 0 {
		return Plan{}, install.ErrNotInstalled
	}
	id := installs[0].ID
	newest := slices.MaxFunc(installs, func(a, b lock.Entry) int { return a.Version.Compare(b.Version) }).Version

	var target semver.Version
	if to != nil {
		target = *to
	} else {
		rels, err := reg.Releases(ctx, id)
		if err != nil {
			return Plan{}, err
		}
		if target, err = previous(rels, newest); err != nil {
			return Plan{}, fmt.Errorf("%s: %w", id, err)
		}
	}
	if target.Compare(newest) >= 0 {
		return Plan{}, &NotOlderError{ID: id, Target: target, Installed: newest}
	}

	plan := Plan{Version: target}
	for _, e := range installs {
		if e.Version.Compare(target) > 0 {
			plan.Rollback = append(plan.Rollback, e)
		} else {
			plan.Keep = append(plan.Keep, e)
		}
	}
	return plan, nil
}

// previous returns the highest release lower than current.
func previous(rels []api.Release, current semver.Version) (semver.Version, error) {
	var older []semver.Version
	for _, rel := range rels {
		v, err := semver.Parse(rel.Version)
		if err != nil {
			return semver.Version{}, err
		}
		if v.Compare(current) < 0 {
			older = append(older, v)
		}
	}
	if len(older) == 0 {
		return semver.Version{}, ErrNoOlder
	}
	return slices.MaxFunc(older, semver.Version.Compare), nil
}
