package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/importer"
	"github.com/petervlad/loadout/internal/semver"
)

// importFlags are the flags every import subcommand takes.
type importFlags struct {
	team, out, version, name string
}

func (f *importFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.team, "team", "", "team the packages belong to (required)")
	fl.StringVar(&f.out, "out", "", "folder to create the package folders in (default: the current folder)")
	fl.StringVar(&f.version, "version", "0.1.0", "first version of the packages")
	fl.StringVar(&f.name, "name", "", "package name, when importing one input (default: the name it gives)")
	_ = cmd.MarkFlagRequired("team") // only fails for an unknown flag name
}

func (f *importFlags) options(d Deps) (importer.Options, error) {
	v, err := semver.Parse(f.version)
	if err != nil {
		return importer.Options{}, fmt.Errorf("--version: %w", err)
	}
	out := d.WorkDir
	if f.out != "" {
		out = d.abs(f.out)
	}
	return importer.Options{Team: f.team, Out: out, Version: v, Name: f.name}, nil
}

// importHint adds what to do next to an import error.
func importHint(err error) error {
	if _, ok := errors.AsType[*importer.UnknownToolsError](err); ok {
		return fmt.Errorf("%w\nremove those tools from the file, or pass --drop-unknown-tools to import without them", err)
	}
	if _, ok := errors.AsType[*importer.CollisionError](err); ok {
		return fmt.Errorf("%w\nimport one of them on its own with --name to give it another name, or use another --out", err)
	}
	if errors.Is(err, importer.ErrNameForMany) {
		return fmt.Errorf("%w\nimport that input on its own to give it --name", err)
	}
	return err
}

func newImportCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Turn existing agents, skills and documents into package folders",
		Long: `Turn files written for Claude Code or Copilot into loadout package
folders, one per package, ready to publish. Import only writes folders;
nothing leaves this machine until you run loadout publish.`,
	}
	cmd.AddCommand(newImportAgentCmd(d), newImportSkillCmd(d), newImportKBCmd(d))
	return cmd
}

func newImportAgentCmd(d Deps) *cobra.Command {
	var (
		f    importFlags
		drop bool
	)
	cmd := &cobra.Command{
		Use:   "agent FILE...",
		Short: "Import Claude Code or Copilot agent files",
		Long: `Import Claude Code (.claude/agents/NAME.md) or Copilot
(.github/agents/NAME.agent.md) agent files. Tool names become loadout's
tools, and the models haiku, sonnet and opus become the hints fast,
balanced and deep. Anything that cannot be carried over is listed.`,
		Example: `  loadout import agent .claude/agents/*.md --team payments
  loadout import agent .github/agents/reviewer.agent.md --team payments --out packages`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := f.options(d)
			if err != nil {
				return err
			}
			files := make([]string, len(args))
			for i, a := range args {
				files[i] = d.abs(a)
			}
			results, err := importer.Agents(files, importer.AgentOptions{Options: opts, DropUnknownTools: drop})
			printImported(cmd.OutOrStdout(), d, results)
			return importHint(err)
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&drop, "drop-unknown-tools", false, "import agents without the tools loadout has no equivalent for, instead of refusing them")
	return cmd
}

func newImportSkillCmd(d Deps) *cobra.Command {
	var f importFlags
	cmd := &cobra.Command{
		Use:   "skill DIR...",
		Short: "Import skill folders (SKILL.md and its files)",
		Long: `Import skill folders as they are: SKILL.md, with every field in it, and
its supporting files. Hidden files and folders are left out; symbolic
links are refused.`,
		Example: `  loadout import skill .claude/skills/release-notes --team payments
  loadout import skill .agents/skills/* --team payments --out packages`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := f.options(d)
			if err != nil {
				return err
			}
			dirs := make([]string, len(args))
			for i, a := range args {
				dirs[i] = d.abs(a)
			}
			results, err := importer.Skills(dirs, opts)
			printImported(cmd.OutOrStdout(), d, results)
			return importHint(err)
		},
	}
	f.register(cmd)
	return cmd
}

func newImportKBCmd(d Deps) *cobra.Command {
	var (
		f           importFlags
		description string
	)
	cmd := &cobra.Command{
		Use:   "kb DIR",
		Short: "Import a folder of documents as a knowledge base",
		Long: `Import a folder of Markdown or text documents, in folders if you like,
as a knowledge base. The description tells each person's AI tool when to
look in it.`,
		Example: `  loadout import kb docs/runbooks --team payments --description "How we deploy and operate the services."`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := f.options(d)
			if err != nil {
				return err
			}
			r, err := importer.KB(d.abs(args[0]), importer.KBOptions{Options: opts, Description: description})
			if err != nil {
				return importHint(err)
			}
			printImported(cmd.OutOrStdout(), d, []importer.Result{r})
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&description, "description", "", "what the documents cover, so tools know when to read them (required)")
	_ = cmd.MarkFlagRequired("description") // only fails for an unknown flag name
	return cmd
}

// printImported lists each package folder written, what to check in it,
// and how to publish it.
func printImported(w io.Writer, d Deps, results []importer.Result) {
	for _, r := range results {
		fmt.Fprintf(w, "imported %s as %s (%s) in %s\n", d.rel(r.Source), r.ID, r.Kind.Describe(), d.rel(r.Dir))
		for _, n := range r.Notices {
			fmt.Fprintf(w, "  note: %s\n", n)
		}
	}
	for _, r := range results {
		fmt.Fprintf(w, "next: check %s, then run: loadout publish %s\n", d.rel(r.Dir), d.rel(r.Dir))
	}
}

// rel shortens p to a path relative to the working directory when it is
// inside it.
func (d Deps) rel(p string) string {
	r, err := filepath.Rel(d.WorkDir, p)
	if err != nil || !filepath.IsLocal(r) {
		return p
	}
	return r
}
