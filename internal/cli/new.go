package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/skill"
)

// newKind is what differs between "new agent", "new skill" and "new kb".
type newKind struct {
	kind    pack.Kind
	long    string
	example string
	// next says what to edit in the new folder.
	next func(dir string) string
}

var newKinds = []newKind{
	{
		kind:    pack.KindAgent,
		example: "payments/planner",
		next: func(dir string) string {
			return "edit " + filepath.Join(dir, pack.AgentFile)
		},
	},
	{
		kind: pack.KindSkill,
		long: `A skill is SKILL.md plus optional supporting files in folders. Keep
SKILL.md to the steps and put long material in references/, so it loads
only when a step needs it.`,
		example: "payments/release-checklist",
		next: func(dir string) string {
			return fmt.Sprintf("edit %s and put long material in %s", filepath.Join(dir, skill.File), filepath.Join(dir, "references"))
		},
	},
	{
		kind: pack.KindKB,
		long: `A knowledge base is Markdown or text documents, in folders if you like.
It installs as a skill whose SKILL.md loadout writes: an index of the
documents and their titles. Do not add a SKILL.md yourself.`,
		example: "payments/runbooks",
		next: func(dir string) string {
			return fmt.Sprintf("add Markdown or text documents to %s (folders are fine; loadout writes the %s index)", dir, skill.File)
		},
	},
	{
		kind: pack.KindPipeline,
		long: `A pipeline template is the slots of a process and the edges between them.
Without --uses this writes a small example template to edit. With --uses
it writes a binding for your team: it fetches the template and lists every
slot, which you fill with one of your agents before you publish.`,
		example: "acme/dev-loop\n  loadout new pipeline lime/dev-loop --uses acme/dev-loop@1",
		next: func(dir string) string {
			return "edit " + filepath.Join(dir, pack.PipelineFile) + " to name your slots and edges"
		},
	},
}

func newNewCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new package: an agent, a skill, a knowledge base or a pipeline",
	}
	for _, k := range newKinds {
		cmd.AddCommand(newNewKindCmd(d, k))
	}
	return cmd
}

func newNewKindCmd(d Deps, k newKind) *cobra.Command {
	var dir, uses, remoteName string
	cmd := &cobra.Command{
		Use:     string(k.kind) + " TEAM/NAME",
		Short:   "Create a new " + k.kind.Describe() + " package folder",
		Long:    k.long,
		Example: "  loadout new " + string(k.kind) + " " + k.example,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pack.ParseID(args[0])
			if err != nil {
				return err
			}
			if dir == "" {
				dir = id.Name()
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(d.WorkDir, dir)
			}
			if uses != "" {
				return d.newBinding(cmd, id, dir, uses, remoteName)
			}
			if _, err := pack.Scaffold(dir, id, k.kind); err != nil {
				return err
			}
			if k.kind == pack.KindPipeline {
				fmt.Fprintf(cmd.OutOrStdout(), "created %s\n%s, then run: loadout publish %s\n", dir, k.next(dir), dir)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created %s\n%s, then run: loadout install %s\n", dir, k.next(dir), dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "folder to create (default ./NAME)")
	if k.kind == pack.KindPipeline {
		cmd.Flags().StringVar(&uses, "uses", "", "template to bind, as NS/NAME@MAJOR, for example acme/dev-loop@1")
		cmd.Flags().StringVar(&remoteName, "remote", "", "registry to fetch the template from (default: the team's registry, else the default remote)")
	}
	return cmd
}
