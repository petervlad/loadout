package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

func newInstallCmd(d Deps) *cobra.Command {
	var (
		targets    []string
		scope      string
		policy     string
		remoteName string
		force      bool
		merge      bool
		all        bool
	)
	cmd := &cobra.Command{
		Use:   "install FOLDER | TEAM/NAME[@VERSION] | --all",
		Short: "Install a package into your AI tools, from a folder or a registry",
		Long: `Install one package (an agent, a skill or a knowledge base), or with --all
every package your token can read, from all its layers. When two layers
ship a package with the same name, the closer layer's package is installed
and the farther one is shadowed.

Agents go to each tool's agents folder. Skills and knowledge bases go to a
skill folder: ~/.claude/skills/NAME and ~/.copilot/skills/NAME for your
user, .claude/skills/NAME in a project, which both tools read.

A file you edited is never overwritten without --force. With --merge, an
installed package takes the new release and keeps your edits: each edited
file is merged with the release's changes, and where both changed the
same lines the file holds conflict markers for you to resolve.`,
		Example: `  loadout install payments/planner
  loadout install payments/planner@1.2.0 --scope project
  loadout install ./planner --target claude
  loadout install payments/planner --merge
  loadout install --all`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all != (len(args) == 0) {
				return errors.New("give one package or folder, or --all")
			}
			s, err := d.scopeFor(scope)
			if err != nil {
				return err
			}
			opts := install.Options{
				Scope:   s,
				Targets: targets,
				Policy:  lock.Policy(policy),
				Force:   force,
			}
			if merge {
				if all {
					return errors.New("--merge takes one package, not --all")
				}
				return d.installMerge(cmd, args[0], remoteName, opts)
			}
			if all {
				return d.installAll(cmd, remoteName, opts)
			}

			var p *pack.Pack
			if dir := d.abs(args[0]); isDir(dir) {
				loaded, err := pack.Load(dir)
				if err != nil {
					return err
				}
				p, opts.Source = loaded, dir
			} else {
				ref, version, _ := strings.Cut(args[0], "@")
				id, err := pack.ParseID(ref)
				if err != nil {
					return fmt.Errorf("%q is neither a package folder nor TEAM/NAME: %w", args[0], err)
				}
				if version == "" {
					version = "latest"
				}
				c, r, err := d.connectFor(remoteName, id.Team(), "")
				if err != nil {
					return err
				}
				if p, err = c.Fetch(cmd.Context(), id, version); err != nil {
					return err
				}
				layers, err := fetchLayers(cmd.Context(), c)
				if err != nil {
					return err
				}
				if opts.Models, err = fetchTiers(cmd.Context(), c); err != nil {
					return err
				}
				opts.Source, opts.Remote, opts.Depth = r.URL, r.Name, depthOf(layers, id.Team())
			}

			if len(opts.Targets) == 0 && s.Kind == target.ScopeUser {
				if opts.Targets, err = d.Detector.Targets(s, p.Manifest().Targets); err != nil {
					return err
				}
			}
			res, err := d.Installer.Install(cmd.Context(), p, opts)
			if err != nil {
				return err
			}
			printInstall(cmd.OutOrStdout(), s, res)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&targets, "target", nil, fmt.Sprintf("AI tools to install into (available: %v)", d.Installer.Targets()))
	f.StringVar(&scope, "scope", string(target.ScopeUser), "user (your home) or project (the current repo)")
	f.StringVar(&policy, "policy", "", "how updates arrive: auto, notify or pin (default notify, or the current policy)")
	f.StringVar(&remoteName, "remote", "", "registry to install from (default: the team's registry, else the default remote)")
	f.BoolVar(&force, "force", false, "overwrite files loadout did not write or that were edited")
	f.BoolVar(&merge, "merge", false, "take the new release and keep your edits, merging them; conflicts are marked in the files")
	f.BoolVar(&all, "all", false, "install every package your token can read, from all layers")
	return cmd
}

// installAll installs the latest release of every package the token can
// read, closest layer first. One failure does not stop the others.
func (d Deps) installAll(cmd *cobra.Command, remoteName string, base install.Options) error {
	ctx := cmd.Context()
	cfg, err := d.Remotes.Load()
	if err != nil {
		return err
	}
	c, r, err := d.connect(remoteName)
	if err != nil {
		return err
	}
	all, err := gather(ctx, c, viewsOf(cfg, r.Name))
	if err != nil {
		return err
	}
	rels := all.releases
	out := cmd.OutOrStdout()
	all.note(out)
	if len(rels) == 0 {
		fmt.Fprintf(out, "%s has no packages your token can read\n", r.Name)
		return nil
	}

	type job struct {
		id    pack.ID
		depth int
	}
	jobs := make([]job, 0, len(rels))
	for _, rel := range rels {
		id, err := pack.ParseID(rel.ID)
		if err != nil {
			return fmt.Errorf("registry listed %q: %w", rel.ID, err)
		}
		jobs = append(jobs, job{id: id, depth: all.depth[id.Team()]})
	}
	// Closest first, so a farther package finds its name already taken and
	// is shadowed instead of being written and then replaced.
	slices.SortStableFunc(jobs, func(a, b job) int { return cmp.Compare(a.depth, b.depth) })

	// Each package renders with the model table of the team it is read
	// through, as sync will later.
	tables := map[string]tier.Table{}
	modelsFor := func(id pack.ID) (tier.Table, error) {
		view := viewOf(cfg, r.Name, id.Team())
		if t, ok := tables[view]; ok {
			return t, nil
		}
		t, err := fetchTiers(ctx, c.In(view))
		if err != nil {
			return tier.Table{}, err
		}
		tables[view] = t
		return t, nil
	}

	var failed []error
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		p, err := c.Fetch(ctx, j.id, "latest")
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", j.id, err))
			continue
		}
		opts := base
		opts.Targets = slices.Clone(base.Targets)
		opts.Source, opts.Remote, opts.Depth = r.URL, r.Name, j.depth
		if opts.Models, err = modelsFor(j.id); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", j.id, err))
			continue
		}
		if len(opts.Targets) == 0 && opts.Scope.Kind == target.ScopeUser {
			if opts.Targets, err = d.Detector.Targets(opts.Scope, p.Manifest().Targets); err != nil {
				fmt.Fprintf(out, "skipped %s: %v\n", j.id, err)
				continue
			}
		}
		res, err := d.Installer.Install(ctx, p, opts)
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", j.id, err))
			continue
		}
		printInstall(out, opts.Scope, res)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d packages failed:\n%w", len(failed), len(jobs), errors.Join(failed...))
	}
	return nil
}

// printInstall reports one install result.
func printInstall(out io.Writer, s target.Scope, res install.Result) {
	e := res.Entry
	switch {
	case res.Unchanged:
		fmt.Fprintf(out, "%s@%s is already installed (%s scope), nothing to do\n", e.ID, e.Version, s.Kind)
		return
	case e.FullyShadowed():
		fmt.Fprintf(out, "skipped %s@%s: shadowed by %s (closer layer)\n", e.ID, e.Version, joinIDs(res.ShadowedBy()))
		return
	}
	fmt.Fprintf(out, "installed %s@%s (%s scope, updates: %s)\n", e.ID, e.Version, s.Kind, e.Policy)
	// A skill folder prints once, not file by file: a knowledge base can
	// hold hundreds of documents.
	seen := map[string]bool{}
	for _, f := range e.Files {
		path := f.Path
		if f.Unit != "" {
			path = f.Unit
		}
		if key := f.Target + " " + path; !seen[key] {
			seen[key] = true
			fmt.Fprintf(out, "  %-8s %s\n", f.Target, filepath.Join(s.Root, filepath.FromSlash(path)))
		}
	}
	for _, sh := range e.Shadowed {
		if key := sh.Target + " shadowed " + sh.By.String(); !seen[key] {
			seen[key] = true
			fmt.Fprintf(out, "  %-8s shadowed by %s (closer layer)\n", sh.Target, sh.By)
		}
	}
	if len(res.TookOver) > 0 {
		fmt.Fprintf(out, "  replaces %s (farther layer)\n", joinIDs(res.TookOver))
	}
}

func joinIDs(ids []pack.ID) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = id.String()
	}
	return strings.Join(s, ", ")
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
