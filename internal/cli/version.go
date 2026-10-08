package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/cliupdate"
)

func newVersionCmd(d Deps) *cobra.Command {
	return quiet(&cobra.Command{
		Use:   "version",
		Short: "Print the loadout version, and whether a newer one is out",
		Long: `Print the loadout version on the first line. Below it, from what a
registry said the last time loadout talked to one, whether a newer
loadout is out and how to update. It never asks a registry itself; any
command that does (sync, list, install) brings the news up to date.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintln(out, "loadout", d.Build); err != nil {
				return err
			}
			writeUpdate(out, d, cliupdate.Status.Version)
			return nil
		},
	})
}
