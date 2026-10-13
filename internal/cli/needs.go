package cli

import (
	"context"
	"fmt"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/release"
)

// withNeeds fills opts with what p needs to render: the release of each
// package it names, and the definition of each agent it names that its view
// has. c reads the view p is installed through. listed and depth describe
// that view when the caller already has them, and nil makes withNeeds ask.
func withNeeds(ctx context.Context, c *client.Client, p *pack.Pack, opts install.Options, listed []api.Release, depth map[string]int) (install.Options, error) {
	needs := p.Needs()
	if len(needs.Packages) == 0 && len(needs.Agents) == 0 {
		return opts, nil
	}
	for _, req := range needs.Packages {
		r, err := release.Resolve(ctx, c, req)
		if err != nil {
			return opts, fmt.Errorf("%s needs %s: %w", p.ID(), req, err)
		}
		opts.Requires = append(opts.Requires, r)
	}
	if len(needs.Agents) == 0 {
		return opts, nil
	}
	if listed == nil {
		var err error
		if listed, err = c.Packages(ctx); err != nil {
			return opts, err
		}
		layers, err := fetchLayers(ctx, c)
		if err != nil {
			return opts, err
		}
		depth = map[string]int{}
		for _, l := range layers {
			depth[l.Namespace] = l.Depth
		}
	}
	for _, name := range needs.Agents {
		id, ok := closestAgent(listed, depth, name)
		if !ok {
			continue // install warns that the agent is not installed
		}
		a, err := c.Fetch(ctx, id, "latest")
		if err != nil {
			return opts, err
		}
		if def, ok := a.Agent(); ok {
			opts.Agents = append(opts.Agents, def)
		}
	}
	return opts, nil
}

// closestAgent finds the agent package called name in the nearest layer.
// At equal depth the first one listed wins.
func closestAgent(listed []api.Release, depth map[string]int, name string) (pack.ID, bool) {
	var best pack.ID
	found := false
	for _, rel := range listed {
		if releaseKind(rel) != string(pack.KindAgent) {
			continue
		}
		id, err := pack.ParseID(rel.ID)
		if err != nil || id.Name() != name {
			continue
		}
		if !found || depth[id.Team()] < depth[best.Team()] {
			best, found = id, true
		}
	}
	return best, found
}

// deferNeeding reports whether p should install after the other packages:
// the agents it needs are installed by then, so install does not warn.
func deferNeeding(p *pack.Pack) bool { return len(p.Needs().Agents) > 0 }
