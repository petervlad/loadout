package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/release"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/textdiff"
)

func newDiffCmd(d Deps) *cobra.Command {
	var remoteName string
	cmd := &cobra.Command{
		Use:   "diff TEAM/NAME [FROM [TO]]",
		Short: "Show what changed in a package between two versions",
		Long: `Show every file that changed, the manifest and main file first. With no
versions, compare the installed version with the latest one. With one
version, compare it with the latest.`,
		Example: `  loadout diff payments/planner
  loadout diff payments/planner 1.2.0 1.3.0`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pack.ParseID(args[0])
			if err != nil {
				return err
			}
			from, to := "", "latest"
			recorded := ""
			installs, installedErr := d.Releases.Installed(cmd.Context(), id)
			if installedErr == nil {
				recorded = installs[0].Remote // compare against where it came from
			}
			c, _, err := d.connectFor(remoteName, id.Team(), recorded)
			if err != nil {
				return err
			}
			switch len(args) {
			case 1:
				if errors.Is(installedErr, install.ErrNotInstalled) {
					return fmt.Errorf("%w; give versions explicitly", installedErr)
				}
				if installedErr != nil {
					return installedErr
				}
				from = installs[0].Version.String()
			case 2:
				from = args[1]
			case 3:
				from, to = args[1], args[2]
			}

			a, err := c.Fetch(cmd.Context(), id, from)
			if err != nil {
				return err
			}
			b, err := c.Fetch(cmd.Context(), id, to)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if a.Digest() == b.Digest() {
				fmt.Fprintf(out, "%s@%s and @%s are identical\n", id, a.Version(), b.Version())
				return nil
			}
			return writeDiff(out,
				fmt.Sprintf("%s@%s", id, a.Version()), fmt.Sprintf("%s@%s", id, b.Version()), a, b)
		},
	}
	cmd.Flags().StringVar(&remoteName, "remote", "", "registry (default: the team's registry, else where it was installed from)")
	return cmd
}

// writeDiff writes a unified diff of every file of a and b in reading
// order: the manifest, the main file, then the rest sorted. a is nil when
// there is no earlier version; unchanged files print nothing.
func writeDiff(out io.Writer, aLabel, bLabel string, a, b *pack.Pack) error {
	fa, fb := map[string][]byte{}, b.Files()
	rank := map[string]int{pack.ManifestFile: 0, b.Kind().MainFile(): 1}
	if a != nil {
		fa = a.Files()
		rank[a.Kind().MainFile()] = 1
	}
	paths := slices.Collect(maps.Keys(fb))
	for p := range fa {
		if _, ok := fb[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.SortFunc(paths, func(x, y string) int {
		rx, okx := rank[x]
		ry, oky := rank[y]
		if !okx {
			rx = 2
		}
		if !oky {
			ry = 2
		}
		return cmp.Or(cmp.Compare(rx, ry), strings.Compare(x, y))
	})
	for _, name := range paths {
		text, err := textdiff.Unified(aLabel+"/"+name, bLabel+"/"+name, string(fa[name]), string(fb[name]), 3)
		if err != nil {
			return err
		}
		fmt.Fprint(out, text)
	}
	return nil
}

func newRollbackCmd(d Deps) *cobra.Command {
	var remoteName string
	cmd := &cobra.Command{
		Use:   "rollback TEAM/NAME [VERSION]",
		Short: "Go back to the previous (or a given) version and pin it",
		Long: `Reinstall an older version everywhere the package is installed, then pin
it so sync does not bring the newer version straight back. Without VERSION
it goes to the release before the newest install. Installs already at or
below that version are left alone. Resume updates with:
loadout policy TEAM/NAME auto`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pack.ParseID(args[0])
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			installs, err := d.Releases.Installed(ctx, id)
			if err != nil {
				return err
			}
			// The newest install says where the package came from.
			c, r, err := d.connectFor(remoteName, id.Team(), installs[0].Remote)
			if err != nil {
				return err
			}
			var to *semver.Version
			if len(args) == 2 {
				v, err := semver.Parse(args[1])
				if err != nil {
					return err
				}
				to = &v
			}
			plan, err := release.PlanRollback(ctx, c, installs, to)
			if err != nil {
				return err
			}

			p, err := c.Fetch(ctx, id, plan.Version.String())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, e := range plan.Rollback {
				opts, err := withNeeds(ctx, c, p, install.Options{
					Scope: e.Scope, Targets: e.Targets(), Source: r.URL, Remote: r.Name, Policy: lock.PolicyPin, Depth: e.Depth,
				}, nil, nil)
				if err != nil {
					return err
				}
				if _, err := d.Installer.Install(ctx, p, opts); err != nil {
					return err
				}
				fmt.Fprintf(out, "rolled back %s to %s (%s scope) and pinned it\n", id, plan.Version, e.Scope.Kind)
			}
			for _, e := range plan.Keep {
				fmt.Fprintf(out, "left %s at %s (%s scope): not newer than %s\n", id, e.Version, e.Scope.Kind, plan.Version)
			}
			fmt.Fprintf(out, "resume updates with: loadout policy %s auto\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&remoteName, "remote", "", "registry (default: the team's registry, else where it was installed from)")
	return cmd
}
