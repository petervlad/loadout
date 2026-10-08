package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/pack"
)

func newUninstallCmd(d Deps) *cobra.Command {
	var scope string
	var force bool
	cmd := &cobra.Command{
		Use:   "uninstall TEAM/NAME",
		Short: "Remove an installed package from your AI tools",
		Long: `Remove the package's files (an agent file, or a whole skill folder) and
forget it. Files you edited are kept (pass --force to remove them too).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pack.ParseID(args[0])
			if err != nil {
				return err
			}
			kind, err := scopeKindFilter(scope)
			if err != nil {
				return err
			}
			removed, err := d.Installer.Uninstall(cmd.Context(), id, kind, force)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, r := range removed {
				fmt.Fprintf(out, "uninstalled %s (%s scope)\n", id, r.Scope.Kind)
				for _, k := range r.Kept {
					fmt.Fprintf(out, "  kept %s: you edited it (use --force to remove)\n", k)
				}
				for _, u := range r.Unshadowed {
					fmt.Fprintf(out, "  %s is no longer shadowed; restore its files with: loadout install %s\n", u, u)
				}
			}
			if set, ok := d.builtinSet(id); ok {
				if err := d.Builtins.Decline(cmd.Context(), id); err != nil {
					return err
				}
				fmt.Fprintf(out, "  loadout will not add it again; bring it back with: loadout setup %s\n", setFlag(set.Name))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "only this scope: user or project (default: all)")
	cmd.Flags().BoolVar(&force, "force", false, "also remove files you edited")
	return cmd
}
