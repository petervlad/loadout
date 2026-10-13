package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/remote"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/update"
)

// connect resolves a remote by name (empty = default) and returns a client
// for it.
func (d Deps) connect(name string) (*client.Client, remote.Remote, error) {
	return d.connectFor(name, "", "")
}

// connectFor returns a client for the registry of a package in namespace:
// the explicit remote (--remote), else the namespace's team registry, else
// the remote it was installed from (recorded), else the default.
func (d Deps) connectFor(explicit, namespace, recorded string) (*client.Client, remote.Remote, error) {
	cfg, err := d.Remotes.Load()
	if err != nil {
		return nil, remote.Remote{}, err
	}
	r, err := cfg.ResolveFor(explicit, namespace, recorded)
	if err != nil {
		return nil, remote.Remote{}, err
	}
	c, err := d.clientFor(r)
	if err != nil {
		return nil, remote.Remote{}, err
	}
	return c.In(viewOf(cfg, r.Name, namespace)), r, nil
}

// viewOf is the namespace whose view a request to the named remote works
// in, for a package in namespace ns: ns itself when it was joined, the team
// that brought it in when it is a layer above, else (no ns) the remote's
// first joined team. An account token needs it for listings; a namespace
// token ignores it.
func viewOf(cfg *remote.Config, remoteName, ns string) string {
	if b, ok := cfg.Binding(ns); ok && ns != "" && b.Remote == remoteName {
		if b.Direct() {
			return b.Namespace
		}
		return b.Via[0]
	}
	if views := viewsOf(cfg, remoteName); len(views) > 0 {
		return views[0]
	}
	return ""
}

// viewsOf lists the teams joined on the named remote, in the order joined.
// Each is one view: its packages and the layers above it.
func viewsOf(cfg *remote.Config, remoteName string) []string {
	var out []string
	for _, b := range cfg.Bindings {
		if b.Remote == remoteName && b.Direct() {
			out = append(out, b.Namespace)
		}
	}
	return out
}

// clientFor returns a client for r with its stored token.
func (d Deps) clientFor(r remote.Remote) (*client.Client, error) {
	token, err := d.Remotes.Token(r.Name)
	if err != nil {
		return nil, err
	}
	return d.newClient(r, token)
}

func (d Deps) newClient(r remote.Remote, token string) (*client.Client, error) {
	opts := []client.Option{client.WithUserAgent("loadout/" + d.Version)}
	if d.Updates != nil {
		opts = append(opts, client.WithCLIAdvice(func(a client.CLIAdvice) { d.Updates.Saw(r.URL, a) }))
	}
	if r.CAFile != "" {
		opts = append(opts, client.WithCAFile(r.CAFile))
	}
	c, err := client.New(r.URL, token, opts...)
	if err != nil {
		return nil, fmt.Errorf("remote %s: %w", r.Name, err)
	}
	return c, nil
}

// registryAdapter lets the update service use a client. team is the
// namespace of the packages it serves, the view to fall back to when the
// person left the team that brought it in.
type registryAdapter struct {
	c    *client.Client
	team string
}

var _ update.Registry = registryAdapter{}

func (a registryAdapter) Latest(ctx context.Context, id pack.ID) (semver.Version, error) {
	rels, err := a.c.Releases(ctx, id)
	// Only not_found reads as gone; a 403 (a team the person left) stays a
	// failure. A package with no release is as good as deleted.
	if errors.Is(err, api.ErrNotFound) {
		return semver.Version{}, fmt.Errorf("%s: %w", id, update.ErrGone)
	}
	if err != nil {
		return semver.Version{}, err
	}
	if len(rels) == 0 {
		return semver.Version{}, fmt.Errorf("%s has no releases: %w", id, update.ErrGone)
	}
	return semver.Parse(rels[len(rels)-1].Version)
}

func (a registryAdapter) Fetch(ctx context.Context, id pack.ID, v semver.Version) (*pack.Pack, error) {
	return a.c.Fetch(ctx, id, v.String())
}
