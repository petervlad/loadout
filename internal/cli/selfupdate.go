package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/semver"
)

// selfUpdating names the install methods whose command loadout may run
// for the person; the others (a release download, a clone) need a human.
var selfUpdating = map[string]bool{"registry": true, "homebrew": true}

func newUpdateCmd(d Deps) *cobra.Command {
	var check, force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update this loadout CLI the way it was installed",
		Long: `Update the loadout program itself (sync updates your packages). A CLI
installed from a registry runs that registry's install.sh again, one
installed with Homebrew runs brew upgrade, and a release download or a
build from a clone prints what to do, since loadout cannot do it for
you.

It asks no registry itself: it goes by what registries said the last time
loadout talked to one, so run it after any command that does (sync, list,
status). When that says you already have the newest, nothing is run
unless you pass --force.`,
		Example: `  loadout update
  loadout update --check`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if d.Updates == nil {
				return errors.New("this loadout was built without update news")
			}
			out := cmd.OutOrStdout()
			st := d.Updates.Status()
			m := st.Method
			if !st.Behind() && len(st.TooOld) == 0 && !force && st.Latest != (semver.Version{}) {
				fmt.Fprintf(out, "No registry has announced anything newer than your build (%s). Use --force to run the update anyway.\n", st.Build)
				return nil
			}
			if !selfUpdating[m.Name] {
				fmt.Fprintln(out, m.Update())
				return nil
			}
			if strings.Contains(m.Command, buildinfo.UnknownRegistry) {
				// No news saved yet (just installed): the configured
				// registry is the one this CLI came from.
				if url := d.defaultRemoteURL(); url != "" {
					m.Command = strings.ReplaceAll(m.Command, buildinfo.UnknownRegistry, buildinfo.RegistryPlaceholder)
					m = m.For(url)
				}
			}
			if strings.Contains(m.Command, buildinfo.UnknownRegistry) {
				return errors.New("loadout does not know which registry to update from yet. Run loadout login, or any command that asks a registry (loadout list), then try again")
			}
			if check {
				fmt.Fprintf(out, "This would run: %s\n", m.Command)
				return nil
			}
			fmt.Fprintf(out, "Running: %s\n", m.Command)
			run := d.RunShell
			if run == nil {
				run = runShell
			}
			if err := run(cmd.Context(), m.Command, out, cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("the update failed: %w. Nothing was changed if the download did not finish; run loadout update again, or paste that command yourself", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "print the command that would run, and run nothing")
	cmd.Flags().BoolVar(&force, "force", false, "run the update even when you already have the newest release")
	return quiet(cmd)
}

// runShell runs one of loadout's own command texts (never text a registry
// sent) with the terminal's output.
func runShell(ctx context.Context, command string, stdout, stderr io.Writer) error {
	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Stdout, c.Stderr = stdout, stderr
	return c.Run()
}

// defaultRemoteURL is the address of the registry loadout talks to by
// default, or "" when none is set up.
func (d Deps) defaultRemoteURL() string {
	if d.Remotes == nil {
		return ""
	}
	cfg, err := d.Remotes.Load()
	if err != nil || len(cfg.Remotes) == 0 {
		return ""
	}
	for _, r := range cfg.Remotes {
		if r.Name == cfg.Default {
			return r.URL
		}
	}
	return cfg.Remotes[0].URL
}
