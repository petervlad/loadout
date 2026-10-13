package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/demo"
)

// DemoSeeder writes the demo world. cmd/loadout builds it over the stores.
type DemoSeeder interface {
	// Marked says whether dataDir holds a registry that Seed made.
	Marked(ctx context.Context, dataDir string) (bool, error)
	// Seed writes the demo world into an empty dataDir and marks it.
	Seed(ctx context.Context, dataDir string) error
}

func newDemoCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Run a registry full of fake teams and data, to try loadout",
		Long: `The demo is a registry with an invented world in it: a project, two teams,
four people, agents, a skill, knowledge bases, proposals, a pipeline, team
memory, a wiki and a course. Nothing in it is real, and every account shares
one public password, so it only runs on this machine.

  loadout demo seed --data DIR   write the world into an empty folder
  loadout demo up --data DIR     seed it if needed, then serve it

docker compose up in the repository does both and adds a shell to try the CLI.`,
	}
	cmd.AddCommand(newDemoSeedCmd(d), newDemoUpCmd(d))
	return cmd
}

func newDemoSeedCmd(d Deps) *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Write the demo world into an empty folder",
		Long: `Write the demo world straight into a registry folder, with no server and no
tokens. A folder that already holds the demo is left as it is. A folder with
anything else in it is refused, so the demo never lands in a real registry.`,
		Example: "  loadout demo seed --data ./demo-data",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			did, err := d.seedDemo(cmd.Context(), dataDir)
			if err != nil {
				return err
			}
			if did {
				fmt.Fprintf(cmd.OutOrStdout(), "seeded the demo registry in %s\n", dataDir)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already holds the demo registry, left as it is\n", dataDir)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dataDir, "data", filepath.Join(d.StateDir, "demo"), "folder for the demo registry")
	return cmd
}

func newDemoUpCmd(d Deps) *cobra.Command {
	var dataDir, addr string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Seed the demo registry if needed, then serve it",
		Long: `Seed the folder when it is empty, then serve it. It needs LOADOUT_DEMO=1 and a
LOADOUT_PUBLIC_URL on localhost, which serve checks again, so the shared
password never reaches a registry anyone else can open.`,
		Example: "  LOADOUT_DEMO=1 LOADOUT_PUBLIC_URL=http://localhost:7420 loadout demo up --data ./demo-data",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !d.Config.Login.Demo {
				return errors.New("set LOADOUT_DEMO=1 and LOADOUT_PUBLIC_URL=http://localhost:7420 (or your port) to run the demo")
			}
			if _, err := d.seedDemo(cmd.Context(), dataDir); err != nil {
				return err
			}
			printDemoAccounts(cmd.ErrOrStderr(), d.Config.Login.PublicURL)
			return d.serve(cmd, dataDir, addr, "", "")
		},
	}
	f := cmd.Flags()
	f.StringVar(&dataDir, "data", filepath.Join(d.StateDir, "demo"), "folder for the demo registry")
	f.StringVar(&addr, "addr", ":7420", "address to listen on")
	return quiet(cmd)
}

// seedDemo seeds dataDir unless it already holds the demo, and reports
// whether it wrote anything. A folder with anything else in it is refused.
func (d Deps) seedDemo(ctx context.Context, dataDir string) (bool, error) {
	if d.Demo == nil {
		return false, errors.New("this build cannot seed a demo registry")
	}
	marked, err := d.Demo.Marked(ctx, dataDir)
	if err != nil || marked {
		return false, err
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", dataDir, err)
	}
	if len(entries) > 0 {
		return false, fmt.Errorf("%s is not empty and the demo did not make it: seed an empty folder, so the demo never lands in a real registry", dataDir)
	}
	if err := d.Demo.Seed(ctx, dataDir); err != nil {
		return false, err
	}
	return true, nil
}

// printDemoAccounts tells a person at the terminal where to go and who to
// be, which is the first thing docker compose up shows.
func printDemoAccounts(w io.Writer, url string) {
	fmt.Fprintf(w, "\nloadout demo registry: %s\nFake data. Sign in with any of these accounts, all with the password %s\n", url, demo.Password)
	for _, a := range demo.Accounts() {
		fmt.Fprintf(w, "  %-20s %s\n", a.Email, a.Role)
	}
	fmt.Fprintln(w)
}
