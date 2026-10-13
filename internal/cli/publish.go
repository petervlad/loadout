package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/release"
	"github.com/petervlad/loadout/internal/semver"
)

func newPublishCmd(d Deps) *cobra.Command {
	var remoteName, bump, base string
	cmd := &cobra.Command{
		Use:   "publish [FOLDER]",
		Short: "Publish a package folder to a registry",
		Long: `Publish a package folder as a new release. A folder made from an older
release than the latest is refused, since publishing it would drop the newer
release's changes for everyone: bring them in with loadout rebase first, or
say the folder already has them with --base (--base latest in a CI job whose
checkout always holds the latest content).`,
		Example: `  loadout publish ./planner
  loadout publish ./planner --bump minor
  loadout publish ./planner --bump patch --base latest`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			folder := ""
			if len(args) == 1 {
				folder = args[0]
			}
			dir := d.WorkDir
			if folder != "" {
				dir = d.abs(folder)
			}
			p, err := pack.Load(dir)
			if err != nil {
				return err
			}
			c, r, err := d.connectFor(remoteName, p.ID().Team(), "")
			if err != nil {
				return err
			}
			// Where the folder came from, read before --bump changes its
			// version: publishing a folder behind the latest release would
			// drop that release's changes for everyone.
			o, rb, err := d.origin(cmd.Context(), c, r, dir, p, base)
			if err != nil {
				return err
			}
			if err := checkReady(rb, dir, o, "publish", folder); err != nil {
				return err
			}

			if bump != "" {
				part, err := parsePart(bump)
				if err != nil {
					return err
				}
				next, err := release.Next(cmd.Context(), c, p.ID(), p.Version(), part)
				if err != nil {
					return err
				}
				// Bump in memory; the folder changes only after the upload
				// succeeds, so a failed publish never skips a version.
				if p, err = pack.WithVersion(p, next); err != nil {
					return err
				}
			}

			rel, err := c.Publish(cmd.Context(), p)
			if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Code == api.CodeVersionConflict {
				return fmt.Errorf("%w\nraise the version in %s or publish with --bump patch", err, filepath.Join(dir, pack.ManifestFile))
			}
			if err != nil {
				return err
			}
			if bump != "" {
				if err := pack.SetVersion(dir, p.Version()); err != nil {
					return fmt.Errorf("published %s@%s, but could not update %s: %w", rel.ID, rel.Version, pack.ManifestFile, err)
				}
			}
			if rb != nil {
				if err := rb.Done(dir); err != nil {
					d.Log.Warn("forget the rebase record", "dir", dir, "err", err)
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "published %s@%s to %s\n", rel.ID, rel.Version, r.Name)
			writeFindings(out, p)
			if o.Replaces != "" {
				fmt.Fprintf(out, "your proposal %s stays open and is now %s %s; withdraw it with: loadout review withdraw %s\n",
					o.Replaces, standingAfter(cmd.Context(), c, o.Replaces, rel.Version), rel.Version, o.Replaces)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&remoteName, "remote", "", "registry to publish to (default: the team's registry, else the default remote)")
	f.StringVar(&bump, "bump", "", "raise the version first: patch, minor or major (from the highest of local and published)")
	f.StringVar(&base, "base", "", "the release the folder was made from, or latest (default: worked out; see loadout rebase --help)")
	return cmd
}

func parsePart(s string) (semver.Part, error) {
	switch s {
	case "patch":
		return semver.Patch, nil
	case "minor":
		return semver.Minor, nil
	case "major":
		return semver.Major, nil
	}
	return 0, fmt.Errorf("--bump %q: want patch, minor or major", s)
}

func newListCmd(d Deps) *cobra.Command {
	var remoteName string
	var opts listOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the packages your tokens can read, from every layer of every team registry",
		Long: `Without --remote, list every team registry you joined, grouped by
registry; with no teams joined, list the default remote. KIND is agent,
skill or kb (knowledge base). LAYER says whether a package comes from your
team (L1) or a project above it (L0).`,
		Example: `  loadout list --kind skill
  loadout list --layer payments
  loadout list --sort updated`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.kind != "" && !pack.Kind(opts.kind).Valid() {
				return fmt.Errorf("--kind %q: want agent, skill or kb", opts.kind)
			}
			if !slices.Contains(listSorts, opts.sort) {
				return fmt.Errorf("--sort %q: want %s", opts.sort, strings.Join(listSorts, ", "))
			}
			cfg, err := d.Remotes.Load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if remoteName != "" || len(cfg.Bindings) == 0 {
				c, r, err := d.connect(remoteName)
				if err != nil {
					return err
				}
				return listPackages(cmd, out, c, r.Name, viewsOf(cfg, r.Name), opts)
			}

			var errs []error
			for i, g := range bindingGroups(cfg) {
				if i > 0 {
					fmt.Fprintln(out)
				}
				fmt.Fprintf(out, "%s: %s (%s)\n", strings.Join(g.namespaces, ", "), g.remote.Name, g.remote.URL)
				c, err := d.clientFor(g.remote)
				if err == nil {
					err = listPackages(cmd, out, c, g.remote.Name, viewsOf(cfg, g.remote.Name), opts)
				}
				if err != nil {
					fmt.Fprintf(out, "  %v\n", err)
					errs = append(errs, fmt.Errorf("list %s: %w", g.remote.Name, err))
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("%d registries could not be listed", len(errs))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&remoteName, "remote", "", "list only this registry (default: every team registry)")
	cmd.Flags().StringVar(&opts.kind, "kind", "", "list only this kind: agent, skill or kb")
	cmd.Flags().StringVar(&opts.layer, "layer", "", "list only this layer's packages, by name (your team or a project above it)")
	cmd.Flags().StringVar(&opts.sort, "sort", "layer", "order: "+strings.Join(listSorts, ", "))
	cmd.Flags().BoolVar(&opts.wide, "wide", false, fmt.Sprintf("show descriptions in full (default: the first %d characters)", listDescription))
	return cmd
}

// listOptions filter and order loadout list.
type listOptions struct {
	kind  string
	layer string
	sort  string
	// wide shows descriptions in full instead of cut to one short line.
	wide bool
}

// what names the packages opts keeps: "skill packages in payments".
func (o listOptions) what() string {
	s := "packages"
	if o.kind != "" {
		s = o.kind + " " + s
	}
	if o.layer != "" {
		s += " in " + o.layer
	}
	return s
}

// listDescription is how many characters of a description loadout list
// shows without --wide, so a row fits one terminal line.
const listDescription = 60

// short cuts s to n characters, ending in "…" when it was longer.
func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// listSorts are the orders of loadout list; layer (the registry's own
// order: your team, then the layers above) is the default.
var listSorts = []string{"layer", "name", "kind", "updated"}

func listPackages(cmd *cobra.Command, out io.Writer, c *client.Client, remoteName string, views []string, opts listOptions) error {
	all, err := gather(cmd.Context(), c, views)
	if err != nil {
		return err
	}
	all.note(out)
	if len(all.releases) == 0 {
		fmt.Fprintf(out, "%s has no packages yet; publish one with: loadout publish ./FOLDER\n", remoteName)
		return nil
	}
	rels := filterReleases(all.releases, opts)
	if len(rels) == 0 {
		fmt.Fprintf(out, "%s has no %s\n", remoteName, opts.what())
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PACKAGE\tKIND\tLAYER\tLATEST\tPUBLISHED\tDESCRIPTION")
	for _, rel := range rels {
		ns, _, _ := strings.Cut(rel.ID, "/")
		desc := rel.Description
		if !opts.wide {
			desc = short(desc, listDescription)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", rel.ID, releaseKind(rel), cmp.Or(all.labels[ns], "-"), rel.Version, rel.PublishedAt.Local().Format("2006-01-02 15:04"), desc)
	}
	return w.Flush()
}

// gathered is what a registry offers across the views of the teams joined
// on it: each package once, with its layer.
type gathered struct {
	// releases are the latest release of each package, each team's own
	// first, then the layers above, in the order of the views.
	releases []api.Release
	// labels describe each namespace's layer, such as "L0 project".
	labels map[string]string
	// depth is each namespace's distance from the closest joined team
	// that reads it: 0 for a joined team.
	depth map[string]int
	// gone are the joined teams the account is no longer a member of.
	gone []string
}

// note tells the user about teams they no longer belong to.
func (g gathered) note(out io.Writer) {
	if len(g.gone) > 0 {
		fmt.Fprintf(out, "you are no longer a member of %s; run loadout login again to update this machine\n", strings.Join(g.gone, ", "))
	}
}

// gather lists the packages of every view (or the token's own, with none),
// each package once.
func gather(ctx context.Context, c *client.Client, views []string) (gathered, error) {
	g := gathered{labels: map[string]string{}, depth: map[string]int{}}
	if len(views) == 0 {
		views = []string{""}
	}
	seen := map[string]bool{}
	var lastErr error
	for _, v := range views {
		vc := c.In(v)
		rels, err := vc.Packages(ctx)
		// A team the account has left fails only its own view, as long as
		// another one still works.
		if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Status == http.StatusForbidden && v != "" {
			g.gone = append(g.gone, v)
			lastErr = err
			continue
		}
		if err != nil {
			return gathered{}, err
		}
		layers, err := fetchLayers(ctx, vc)
		if err != nil {
			return gathered{}, err
		}
		for _, l := range layers {
			g.labels[l.Namespace] = layerLabel(layers, l.Namespace)
			if d, ok := g.depth[l.Namespace]; !ok || l.Depth < d {
				g.depth[l.Namespace] = l.Depth
			}
		}
		for _, rel := range rels {
			if !seen[rel.ID] {
				seen[rel.ID] = true
				g.releases = append(g.releases, rel)
			}
		}
	}
	if len(g.gone) == len(views) {
		return gathered{}, lastErr
	}
	return g, nil
}

// releaseKind is a release's kind; older registries do not send it.
func releaseKind(rel api.Release) string { return cmp.Or(rel.Kind, string(pack.KindAgent)) }

// filterReleases keeps the releases of opts.kind and orders them by
// opts.sort, keeping the registry's order among equals.
func filterReleases(rels []api.Release, opts listOptions) []api.Release {
	out := slices.DeleteFunc(slices.Clone(rels), func(r api.Release) bool {
		team, _, _ := strings.Cut(r.ID, "/")
		return (opts.kind != "" && releaseKind(r) != opts.kind) || (opts.layer != "" && team != opts.layer)
	})
	name := func(r api.Release) string { _, n, _ := strings.Cut(r.ID, "/"); return n }
	switch opts.sort {
	case "name":
		slices.SortStableFunc(out, func(a, b api.Release) int { return strings.Compare(name(a), name(b)) })
	case "kind":
		order := map[string]int{}
		for i, k := range pack.Kinds() {
			order[string(k)] = i
		}
		slices.SortStableFunc(out, func(a, b api.Release) int { return order[releaseKind(a)] - order[releaseKind(b)] })
	case "updated":
		slices.SortStableFunc(out, func(a, b api.Release) int { return b.PublishedAt.Compare(a.PublishedAt) })
	}
	return out
}

// standingAfter says how a release at version leaves the open proposal id:
// "blocked by" when it is at or above the proposal's version, so it can no
// longer be approved, else "behind". It only words a note, so a failed
// read says "behind".
func standingAfter(ctx context.Context, c *client.Client, id, version string) string {
	prop, err := c.Proposal(ctx, id)
	if err != nil {
		return "behind"
	}
	pv, err1 := semver.Parse(prop.Version)
	rv, err2 := semver.Parse(version)
	if err1 == nil && err2 == nil && rv.Compare(pv) >= 0 {
		return "blocked by"
	}
	return "behind"
}

// writeFindings prints the lint warnings of p. They never fail the command.
func writeFindings(out io.Writer, p *pack.Pack) {
	for _, f := range p.Lint() {
		fmt.Fprintln(out, f)
	}
}
