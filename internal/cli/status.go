package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/cliupdate"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/notice"
)

func newStatusCmd(d Deps) *cobra.Command {
	var files bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show installed packages and whether their files are intact",
		Long: `Show one line per agent file and per skill folder, for each AI tool. A
skill folder is ok when every file in it is intact; otherwise it says how
many files are modified or missing. List every file with --files or -v.

Below the table, status says which updates the last loadout sync found:
held back by your edits, or available, and whether a registry said that
a newer loadout is out. It never asks a registry; run loadout sync for
news.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				files = true
			}
			statuses, err := d.Installer.Status(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(statuses) == 0 {
				fmt.Fprintln(out, "nothing installed yet; install a team package with: loadout install TEAM/NAME")
				writeUpdate(out, d, cliupdate.Status.Footer)
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PACKAGE\tVERSION\tSCOPE\tUPDATES\tTARGET\tSTATE\tPATH")
			for _, st := range statuses {
				e := st.Entry
				for _, l := range statusLines(st.Files, files) {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.ID, e.Version, e.Scope.Kind, e.Policy, l.target, l.state, l.path)
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			writeUses(out, statuses)
			if d.Notices != nil {
				ns, err := d.Notices.List(cmd.Context(), notice.Filter{})
				if err != nil {
					return err
				}
				if err := writeNotices(out, ns); err != nil {
					return err
				}
			}
			writeUpdate(out, d, func(s cliupdate.Status) string {
				if f := s.Footer(); f != "" {
					return "\n" + f
				}
				return ""
			})
			return nil
		},
	}
	cmd.Flags().BoolVar(&files, "files", false, "list every file of a skill folder")
	return quiet(cmd)
}

// statusLine is one row of status: a file, or a skill folder for one tool.
type statusLine struct {
	target, state, path string
	// counts of the folder's files by state, for the rolled-up state
	total  int
	counts map[install.State]int
	by     string // the closer package, when shadowed
}

// statusLines rolls the files of each skill folder up into one line per
// tool, in first-seen order, unless every file is wanted.
func statusLines(files []install.FileStatus, every bool) []statusLine {
	var out []statusLine
	index := map[string]int{}
	for _, f := range files {
		by := ""
		if f.State == install.StateShadowed {
			by = f.ShadowedBy.String()
		}
		if every || f.Unit == "" {
			state := string(f.State)
			if by != "" {
				state += " by " + by
			}
			out = append(out, statusLine{target: f.Target, state: state, path: f.Path})
			continue
		}
		key := f.Target + "\x00" + f.Unit
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, statusLine{target: f.Target, path: f.Unit, counts: map[install.State]int{}})
		}
		l := &out[i]
		l.total++
		l.counts[f.State]++
		if by != "" {
			l.by = by
		}
	}
	for i := range out {
		if out[i].counts != nil {
			out[i].state = rollUp(out[i])
		}
	}
	return out
}

// rollUp gives a skill folder's state: the problems with their counts,
// worst first, or ok (or shadowed) when every file is intact.
func rollUp(l statusLine) string {
	var problems []string
	for _, s := range []install.State{install.StateMissing, install.StateModified} {
		if n := l.counts[s]; n > 0 {
			problems = append(problems, fmt.Sprintf("%s (%d of %d files)", s, n, l.total))
		}
	}
	switch {
	case len(problems) > 0:
		return strings.Join(problems, ", ")
	case l.counts[install.StateShadowed] == l.total:
		return string(install.StateShadowed) + " by " + l.by
	}
	return string(install.StateOK)
}

// writeUses says which release of another package each rendered install
// (a conductor) comes from, once per package and release.
func writeUses(out io.Writer, statuses []install.Status) {
	seen := map[string]bool{}
	first := true
	for _, st := range statuses {
		for _, r := range st.Entry.Requires {
			line := fmt.Sprintf("%s uses %s@%s", st.Entry.ID, r.ID, r.Version)
			if seen[line] {
				continue
			}
			seen[line] = true
			if first {
				fmt.Fprintln(out)
				first = false
			}
			fmt.Fprintln(out, line)
		}
	}
}
