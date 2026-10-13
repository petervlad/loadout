package registry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
)

// checker runs the checks a package passes before it becomes a release or a
// proposal: what it needs exists in the team's view, and it keeps the rules
// of its major. It reads the stores and knows no kind, only Pack.Needs,
// Pack.CheckNeeds and pack.Compatible.
type checker struct {
	store      Store
	namespaces NamespaceStore
}

// check runs both checks and returns every failure as one ErrInvalid.
func (c checker) check(ctx context.Context, p *pack.Pack) error {
	var msgs []string
	needs, err := c.checkNeeds(ctx, p)
	if err != nil {
		return err
	}
	msgs = append(msgs, needs...)
	compat, err := c.checkCompatible(ctx, p)
	if err != nil {
		return err
	}
	msgs = append(msgs, compat...)
	if len(msgs) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(msgs, " "))
	}
	return nil
}

// checkNeeds returns one sentence per problem with what p needs. The view is
// the namespace of p and its ancestors, as Chain resolves it.
func (c checker) checkNeeds(ctx context.Context, p *pack.Pack) ([]string, error) {
	needs := p.Needs()
	if len(needs.Packages) == 0 && len(needs.Agents) == 0 {
		return nil, nil
	}
	if c.namespaces == nil {
		return nil, fmt.Errorf("publish %s: the registry cannot check what %s needs", p.ID(), p.ID())
	}
	team := p.ID().Team()
	chain, err := Chain(ctx, c.namespaces, team)
	if errors.Is(err, ErrNotFound) {
		chain, err = []Layer{{Namespace: team, Kind: KindTeam}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", p.ID(), err)
	}
	names := make([]string, len(chain))
	for i, l := range chain {
		names[i] = l.Namespace
	}
	view := strings.Join(names, ", ")

	var msgs []string
	var required []*pack.Pack
	for _, req := range needs.Packages {
		r, msg, err := c.required(ctx, p, req, names, view)
		if err != nil {
			return nil, err
		}
		if msg != "" {
			msgs = append(msgs, msg)
			continue
		}
		required = append(required, r)
	}
	if len(required) == len(needs.Packages) {
		if err := p.CheckNeeds(required); err != nil {
			msgs = append(msgs, p.ID().String()+": "+sentences(err))
		}
	}

	if len(needs.Agents) > 0 {
		have, err := c.agentsIn(ctx, names)
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", p.ID(), err)
		}
		for _, name := range needs.Agents {
			if name != "" && !have[name] {
				msgs = append(msgs, fmt.Sprintf("The agent %s is not published in %s (the layers %s sees). Publish it there first.", name, view, team))
			}
		}
	}
	return msgs, nil
}

// required loads the highest release of req's major from the view. A
// problem with it comes back as a sentence, a store failure as an error.
func (c checker) required(ctx context.Context, p *pack.Pack, req pack.Requirement, view []string, shown string) (*pack.Pack, string, error) {
	if !slices.Contains(view, req.ID.Team()) {
		return nil, fmt.Sprintf("%s needs %s, but %s is not one of the layers %s sees (%s).", p.ID(), req, req.ID.Team(), p.ID().Team(), shown), nil
	}
	rels, err := c.store.Releases(ctx, req.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, "", fmt.Errorf("check %s: %w", p.ID(), err)
	}
	var best *Release
	for i := range rels {
		if rels[i].Version.Major() == req.Major {
			best = &rels[i]
		}
	}
	if best == nil {
		return nil, fmt.Sprintf("%s needs %s, but %s has no release with major %d.", p.ID(), req, req.ID, req.Major), nil
	}
	_, data, err := c.store.Release(ctx, req.ID, best.Version)
	if err != nil {
		return nil, "", fmt.Errorf("check %s: %w", p.ID(), err)
	}
	r, err := bundle.Decode(data)
	if err != nil {
		return nil, "", fmt.Errorf("check %s: stored %s@%s: %w", p.ID(), req.ID, best.Version, err)
	}
	return r, "", nil
}

// agentsIn returns the names of the agent packages published in namespaces.
func (c checker) agentsIn(ctx context.Context, namespaces []string) (map[string]bool, error) {
	have := map[string]bool{}
	for _, ns := range namespaces {
		rels, err := c.store.Packages(ctx, ns)
		if err != nil {
			return nil, err
		}
		for _, r := range rels {
			if r.Kind == pack.KindAgent {
				have[r.ID.Name()] = true
			}
		}
	}
	return have, nil
}

// checkCompatible compares p with the highest release of the same major, if
// there is one: inside a major a package may only change in ways pack.Compatible
// accepts.
func (c checker) checkCompatible(ctx context.Context, p *pack.Pack) ([]string, error) {
	if !p.Kind().ChecksCompatibility() {
		return nil, nil
	}
	rels, err := c.store.Releases(ctx, p.ID())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("check %s: %w", p.ID(), err)
	}
	var prev *Release
	for i := range rels {
		if rels[i].Version.Major() == p.Version().Major() {
			prev = &rels[i]
		}
	}
	if prev == nil {
		return nil, nil
	}
	_, data, err := c.store.Release(ctx, p.ID(), prev.Version)
	if err != nil {
		return nil, fmt.Errorf("check %s: %w", p.ID(), err)
	}
	old, err := bundle.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("check %s: stored %s@%s: %w", p.ID(), p.ID(), prev.Version, err)
	}
	if err := pack.Compatible(old, p); err != nil {
		return []string{sentences(err) + fmt.Sprintf(" That is a major change: publish %d.0.0.", p.Version().Major()+1)}, nil
	}
	return nil, nil
}

// sentences turns an error, which may join several lines, into sentences
// that each end with a full stop.
func sentences(err error) string {
	var out []string
	for line := range strings.SplitSeq(err.Error(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, strings.TrimSuffix(line, ".")+".")
		}
	}
	return strings.Join(out, " ")
}
