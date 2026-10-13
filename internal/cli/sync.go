package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/update"
)

func newSyncCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Annotations: map[string]string{pushUpdates: "true"},
		Use:         "sync",
		Short:       "Update installed packages from their registries, following each one's policy",
		Long: `Check every package installed from a registry. With policy auto the new
version is installed, with notify it is reported, with pin nothing changes.
A change to the team's model settings is handled the same way for the
agents it changes. A file you edited is never overwritten. Safe to run
from cron.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := d.Remotes.Load()
			if err != nil {
				return err
			}
			// Each install updates from its team's registry, or else from
			// the remote it was installed from.
			connect := func(e lock.Entry) (update.Source, error) {
				r, err := cfg.ResolveFor("", e.ID.Team(), e.Remote)
				if err != nil {
					return update.Source{}, err
				}
				c, err := d.clientFor(r)
				if err != nil {
					return update.Source{}, err
				}
				c = c.In(viewOf(cfg, r.Name, e.ID.Team()))
				return update.Source{Registry: registryAdapter{c: c, team: e.ID.Team()}, Remote: r.Name, URL: r.URL}, nil
			}
			results, err := d.NewSyncer(connect).Sync(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			d.ensureBuiltins(cmd, false)
			d.ensureMemory(cmd)
			if len(results) == 0 {
				fmt.Fprintln(out, "nothing installed from a registry; try: loadout install TEAM/NAME")
				return nil
			}
			err = printSync(out, results)
			if slices.ContainsFunc(results, func(r update.Result) bool { return len(r.Held) > 0 }) {
				d.hookTip(out)
			}
			return err
		},
	}
}

// printSync prints the results table, what holds updates back with the
// ways forward, and the failures. A failed check is exit 1; otherwise an
// auto update held back is exit 2, so cron notices.
func printSync(out io.Writer, results []update.Result) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PACKAGE\tSCOPE\tINSTALLED\tLATEST\tRESULT")
	failed, held := 0, 0
	for _, r := range results {
		latest := "-"
		if r.Outcome != update.OutcomeFailed && r.Outcome != update.OutcomeGone {
			latest = r.Latest.String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Entry.ID, r.Entry.Scope.Kind, r.Entry.Version, latest, syncResult(r))
		switch r.Outcome {
		case update.OutcomeFailed:
			failed++
		case update.OutcomeBlocked:
			held++
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, r := range results {
		switch {
		case len(r.Held) > 0:
			writeHeld(out, r)
		case r.Outcome == update.OutcomeFailed && r.Err != nil:
			fmt.Fprintf(out, "\n%s: %v\n", r.Entry.ID, r.Err)
		case r.Outcome == update.OutcomeGone:
			writeGone(out, r)
		case r.Outcome == update.OutcomeModelsChanged:
			fmt.Fprintf(out, "\n%s: your team changed which model it runs. Apply it with: loadout install %s --scope %s\n", r.Entry.ID, r.Entry.ID, r.Entry.Scope.Kind)
		case r.Outcome == update.OutcomeTemplateChanged:
			fmt.Fprintf(out, "\n%s: your team published %s. Apply it with: loadout install %s --scope %s\n", r.Entry.ID, r.Template, r.Entry.ID, r.Entry.Scope.Kind)
		}
	}
	for _, r := range results {
		for _, n := range r.Notes {
			fmt.Fprintf(out, "\n%s\n", n)
		}
	}
	switch {
	case failed > 0:
		return fmt.Errorf("%d of %d checks failed", failed, len(results))
	case held == 1:
		return exitError{err: errors.New("1 update is held back by your edits; see above"), code: exitHeld}
	case held > 1:
		return exitError{err: fmt.Errorf("%d updates are held back by your edits; see above", held), code: exitHeld}
	}
	return nil
}

// writeGone says a package left its registry and how to remove the files
// loadout keeps. It is not an error: nothing failed, and exit stays 0.
func writeGone(w io.Writer, r update.Result) {
	e := r.Entry
	scope := string(e.Scope.Kind)
	fmt.Fprintf(w, "\n%s is no longer on its registry. loadout keeps your files; remove them with: loadout uninstall %s --scope %s\n", e.ID, e.ID, scope)
	if e.Scope.Kind == target.ScopeProject {
		fmt.Fprintf(w, "Run it in %s.\n", e.Scope.Root)
	}
}

func newPolicyCmd(d Deps) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "policy TEAM/NAME auto|notify|pin",
		Short: "Choose how updates to an installed package arrive",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pack.ParseID(args[0])
			if err != nil {
				return err
			}
			kind, err := scopeKindFilter(scope)
			if err != nil {
				return err
			}
			n, err := d.Installer.SetPolicy(cmd.Context(), id, kind, lock.Policy(args[1]))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: updates now %s (%d install(s))\n", id, args[1], n)
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "only installs in this scope: user or project (default: all)")
	return cmd
}
