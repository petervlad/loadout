package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// Package commands run on the registry host only: deleting is irreversible
// and no role in a namespace outranks it, so only the operator does it.
func newPackageCmd(d Deps) *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use:   "package",
		Short: "Manage packages in the registry (run on the registry host)",
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data", d.defaultDataDir(), "registry data folder")

	var yes bool
	var reason string
	del := &cobra.Command{
		Use:   "delete TEAM/NAME",
		Short: "List what a package holds and, with --yes, delete it with its releases and proposals",
		Long: `Delete a whole package from the registry: every release, every proposal with
its reviews and prompts (open ones included), and its place as a wiki course.
Without --yes it only lists what would go and deletes nothing. It cannot be
undone except by restoring a backup, so back up the database first. The
registry keeps a record of the delete (who, why and what went) in its list
of deletions; --reason says why, and the default is "Deleted on the
registry host.".

Machines that have the package installed keep their files; their next sync
says it was removed from the registry and how to uninstall it.`,
		Example: `  loadout package delete lime/lime-app-dev --data /var/lib/loadout
  loadout package delete lime/lime-app-dev --data /var/lib/loadout --yes --reason "replaced by lime/planner"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pack.ParseID(args[0])
			if err != nil {
				return err
			}
			var fp registry.PackageFootprint
			err = withRegistry(cmd.Context(), d, dataDir, func(reg Registry) error {
				var err error
				if yes {
					fp, err = reg.Accounts.DeletePackage(cmd.Context(), account.Operator(), id, reason)
				} else {
					fp, err = reg.Accounts.PackageFootprint(cmd.Context(), account.Operator(), id)
				}
				return err
			})
			if errors.Is(err, registry.ErrNotFound) {
				return fmt.Errorf("%s is not on this registry.", id) //nolint:revive,staticcheck // a sentence a person reads
			}
			if err != nil {
				return err
			}
			if yes {
				writeDeleted(cmd.OutOrStdout(), fp)
			} else {
				writeFootprint(cmd.OutOrStdout(), fp)
			}
			return nil
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, "delete it; without this the command only lists what would go")
	del.Flags().StringVar(&reason, "reason", "", "why it is deleted, kept in the registry's record (with --yes)")
	cmd.AddCommand(del)
	return cmd
}

// writeFootprint prints what a delete would remove: the dry run.
func writeFootprint(w io.Writer, fp registry.PackageFootprint) {
	title := fp.ID.String()
	if fp.Kind != "" {
		title += " (" + string(fp.Kind) + ")"
	}
	fmt.Fprintf(w, "%s would be deleted:\n", title)

	releases := fmt.Sprint(len(fp.Releases))
	if len(fp.Releases) > 0 {
		vs := make([]string, len(fp.Releases))
		for i, v := range fp.Releases {
			vs[i] = v.String()
		}
		releases += ": " + strings.Join(vs, ", ")
	}
	proposals := fmt.Sprintf("%d, %d open", fp.Proposals, len(fp.Open))
	if len(fp.Open) > 0 {
		proposals += ": " + strings.Join(fp.Open, ", ")
	}
	course := "none"
	if len(fp.Courses) > 0 {
		course = strings.Join(fp.Courses, ", ")
	}
	fmt.Fprintf(w, "  releases   %s\n", releases)
	fmt.Fprintf(w, "  proposals  %s\n", proposals)
	fmt.Fprintf(w, "  reviews    %d\n", fp.Reviews)
	fmt.Fprintf(w, "  prompts    %d\n", fp.Prompts)
	fmt.Fprintf(w, "  course of  %s\n", course)
	if len(fp.Deletions) > 0 {
		fmt.Fprintf(w, "  requests   %d to delete it, closed as gone: %s\n", len(fp.Deletions), strings.Join(fp.Deletions, ", "))
	}
	fmt.Fprintln(w, "Nothing was deleted. Run it again with --yes to delete it. Back up the database first.")
}

// writeDeleted prints the counts a delete really removed.
func writeDeleted(w io.Writer, fp registry.PackageFootprint) {
	fmt.Fprintf(w, "deleted %s: %s, %s, %s, %s; cleared it as the course of %s\n", fp.ID,
		plural(len(fp.Releases), "release", "releases"), plural(fp.Proposals, "proposal", "proposals"),
		plural(fp.Reviews, "review", "reviews"), plural(fp.Prompts, "prompt", "prompts"), plural(len(fp.Courses), "namespace", "namespaces"))
}
