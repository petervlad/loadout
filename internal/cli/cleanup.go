package cli

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/pack"
)

func newCleanupCmd(d Deps) *cobra.Command {
	var (
		remoteName string
		tool       string
		apply      bool
		duplicates bool
	)
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Find agents, skills and knowledge bases an older setup left, and move them aside",
		Long: `Looks in the folders your AI tool reads for agents, skills and knowledge
bases that have the name of a package your registry offers but that loadout
did not write: links and copies from a dotfiles repo or an install script.
They shadow or duplicate what loadout installs (Copilot lists both).

Without --apply it only lists them. With --apply it moves each into
~/.loadout/backup/<date>/; nothing is deleted, and a link's target is never
touched. With --duplicates it also finds agents and skills that sit in
more than one folder the tool reads, even when no registry package has
that name (an old setup that installed other teams' files too), keeps one
copy (a loadout one first, else the first folder: ~/.copilot, ~/.agents,
~/.claude) and lists the rest. Run it for the tool you installed with: copilot (also looks in
~/.agents, which Copilot reads), claude, or vscode (VS Code reads ~/.copilot,
~/.agents and ~/.claude, so it also looks in ~/.claude; only use it when
Claude Code does not need those old copies, or when you installed for both).`,
		Example: `  loadout cleanup
  loadout cleanup --apply
  loadout cleanup --tool vscode --duplicates
  loadout cleanup --tool claude --apply`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tool != "copilot" && tool != "claude" && tool != "vscode" {
				return errors.New("--tool is copilot, claude or vscode")
			}
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
			var wanted []install.Wanted
			for _, rel := range all.releases {
				id, err := pack.ParseID(rel.ID)
				if err != nil {
					return fmt.Errorf("registry listed %q: %w", rel.ID, err)
				}
				wanted = append(wanted, install.Wanted{Name: id.Name(), Agent: rel.Kind == "" || rel.Kind == "agent"})
			}
			s, err := d.scopeFor("user")
			if err != nil {
				return err
			}
			found, err := install.FindLegacy(s.Root, wanted, []string{tool})
			if err != nil {
				return err
			}
			if duplicates {
				dups, err := install.FindDuplicates(s.Root, []string{tool})
				if err != nil {
					return err
				}
				found = mergeLegacy(found, dups)
			}
			out := cmd.OutOrStdout()
			if len(found) == 0 {
				fmt.Fprintln(out, "nothing to clean up")
				return nil
			}
			for _, l := range found {
				line := l.Path
				if l.LinkTo != "" {
					line += " -> " + l.LinkTo
				}
				if !apply {
					fmt.Fprintln(out, line)
					continue
				}
				to, err := install.Backup(s.Root, l.Path)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "moved %s\n   to %s\n", line, to)
			}
			fmt.Fprintf(out, "\n%d %s: %s\n", len(found), map[bool]string{true: "moved", false: "found"}[apply], countByFolder(s.Root, found))
			if !apply {
				fmt.Fprintln(out, "Nothing was changed. Run the same command with --apply to move them into ~/.loadout/backup.")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&remoteName, "remote", "", "registry (default: the default remote)")
	f.StringVar(&tool, "tool", "copilot", "which tool's folders to clean: copilot, claude or vscode")
	f.BoolVar(&duplicates, "duplicates", false, "also find copies of the same name in more than one folder, whatever the name, and keep one")
	f.BoolVar(&apply, "apply", false, "move what was found into ~/.loadout/backup")
	return cmd
}

// mergeLegacy joins two lists without repeating a path.
func mergeLegacy(a, b []install.Legacy) []install.Legacy {
	seen := map[string]bool{}
	var out []install.Legacy
	for _, l := range slices.Concat(a, b) {
		if !seen[l.Path] {
			seen[l.Path] = true
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(x, y install.Legacy) int { return strings.Compare(x.Path, y.Path) })
	return out
}

// countByFolder says how many entries sit in each tool folder, such as
// "12 in ~/.agents, 5 in ~/.claude".
func countByFolder(root string, found []install.Legacy) string {
	counts := map[string]int{}
	for _, l := range found {
		rel, err := filepath.Rel(root, l.Path)
		if err != nil {
			continue
		}
		counts["~/"+strings.SplitN(rel, string(filepath.Separator), 2)[0]]++
	}
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%d in %s", counts[k], k))
	}
	return strings.Join(parts, ", ")
}
