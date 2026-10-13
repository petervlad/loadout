package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/memimport"
)

func newMemoryImportCmd(d Deps) *cobra.Command {
	var team, remoteName, selection string
	var allowDup bool
	cmd := &cobra.Command{
		Use:   "import DIR --team TEAM [--select N,...] [--new]",
		Short: "Offer your personal memory files to a team's memory",
		Long: `Read the memory files in DIR (not its sub-folders) and offer the ones that
look like facts your team needs. Files with type: user are never offered.
Likely team facts are pre-selected, and nothing is saved until you confirm.
Text that holds a secret is refused here and never leaves your machine.

In a terminal you get a numbered list and choose. With --select the listed
numbers are saved without asking, which is how scripts use it. Without a
terminal and without --select the list is printed and nothing is saved.

Each entry goes through the team's save tool, so it gets the same checks as
one an agent saves. A likely duplicate is shown and skipped unless you pass
--new. It needs a memory token: loadout memory token create.`,
		Example: "  loadout memory import ~/.claude/projects/me/memory --team lime\n  loadout memory import ./notes --team lime --select 1,3",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nums, err := parseSelection(selection)
			if err != nil {
				return err
			}
			list, err := memimport.Read(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			printListing(out, list)
			if len(list.Entries) == 0 {
				fmt.Fprintln(out, "\nThere is nothing to import here.")
				return nil
			}
			if nums == nil {
				if !d.stdinIsTerminal(cmd.InOrStdin()) {
					fmt.Fprintln(out, "\nNothing was saved. Choose entries with --select N,... or run this in a terminal.")
					return nil
				}
				if nums, err = chooseInteractively(cmd.InOrStdin(), out, list); err != nil {
					return err
				}
				if len(nums) == 0 {
					fmt.Fprintln(out, "Nothing was saved.")
					return nil
				}
			}
			cfg, err := d.bridgeConfig(team, remoteName)
			if err != nil {
				return err
			}
			saver, err := memimport.NewMCPSaver(cmd.Context(), cfg.URL, team, cfg.Token, cfg.CAFile)
			if err != nil {
				return err
			}
			defer saver.Close()
			reports, err := memimport.Run(cmd.Context(), list, nums, saver, allowDup)
			if err != nil {
				return err
			}
			return printReports(out, team, reports)
		},
	}
	f := cmd.Flags()
	f.StringVar(&team, "team", "", "the team whose memory to save to")
	f.StringVar(&remoteName, "remote", "", "the registry (default: the one the team is joined on, else the default remote)")
	f.StringVar(&selection, "select", "", "save exactly these numbers from the list, for example 1,3,4")
	f.BoolVar(&allowDup, "new", false, "save an entry even when a similar memory exists")
	_ = cmd.MarkFlagRequired("team")
	return cmd
}

// stdinIsTerminal says whether in is a person at a keyboard. Tests set
// Deps.Terminal.
func (d Deps) stdinIsTerminal(in io.Reader) bool {
	if d.Terminal != nil {
		return d.Terminal(in)
	}
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// parseSelection reads "1,3 4" into numbers. Empty is nil, meaning "ask".
func parseSelection(s string) ([]int, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	if len(fields) == 0 {
		return nil, nil
	}
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("--select %q: %q is not an entry number", s, f)
		}
		out = append(out, n)
	}
	return out, nil
}

func printListing(out io.Writer, l memimport.Listing) {
	for _, e := range l.Entries {
		mark := "[ ]"
		if e.Selected {
			mark = "[x]"
		}
		if e.Refusal != nil {
			fmt.Fprintf(out, "%3d  --   %s: refused, %s in the %s at line %d, column %d. It is never sent.\n", e.Number, e.File, e.Refusal.Rule, e.Refusal.Field, e.Refusal.Line, e.Refusal.Column)
			continue
		}
		fmt.Fprintf(out, "%3d  %s  %-9s %s: %s\n", e.Number, mark, e.Kind, e.Name, e.Description)
		for _, why := range e.Why {
			fmt.Fprintf(out, "          %s\n", why)
		}
	}
	for _, s := range l.Skipped {
		fmt.Fprintf(out, "Skipped %s: %s.\n", s.File, strings.TrimSuffix(s.Reason, "."))
	}
}

// chooseInteractively lets the person toggle numbers until they confirm. An
// empty line confirms, q quits without saving. Refused entries cannot be
// toggled.
func chooseInteractively(in io.Reader, out io.Writer, l memimport.Listing) ([]int, error) {
	chosen := map[int]bool{}
	for _, n := range l.Numbers() {
		chosen[n] = true
	}
	r := bufio.NewReader(in)
	for {
		fmt.Fprintf(out, "\nSelected: %s\nType numbers to toggle them, Enter to save the selected, q to quit: ", numberList(chosen))
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if errors.Is(err, io.EOF) && line == "" {
			return nil, nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		switch strings.ToLower(line) {
		case "":
			return sortedKeys(chosen), nil
		case "q", "quit":
			return nil, nil
		}
		nums, perr := parseSelection(line)
		if perr != nil {
			fmt.Fprintln(out, "Type entry numbers from the list, like 1 3.")
			continue
		}
		for _, n := range nums {
			e, ok := entryNumber(l, n)
			switch {
			case !ok:
				fmt.Fprintf(out, "There is no entry %d.\n", n)
			case e.Refusal != nil:
				fmt.Fprintf(out, "Entry %d holds a secret and cannot be sent.\n", n)
			default:
				chosen[n] = !chosen[n]
			}
		}
	}
}

func entryNumber(l memimport.Listing, n int) (memimport.Entry, bool) {
	for _, e := range l.Entries {
		if e.Number == n {
			return e, true
		}
	}
	return memimport.Entry{}, false
}

func sortedKeys(m map[int]bool) []int {
	var out []int
	for n, on := range m {
		if on {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

func numberList(m map[int]bool) string {
	nums := sortedKeys(m)
	if len(nums) == 0 {
		return "none"
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, " ")
}

// printReports says how each selected entry ended, and fails when any failed
// to reach the team.
func printReports(out io.Writer, team string, reports []memimport.Report) error {
	var saved, failed int
	for _, r := range reports {
		switch r.Outcome {
		case memimport.Saved:
			saved++
			fmt.Fprintf(out, "Saved %s to %s's memory as %d.\n", r.File, team, r.Result.ID)
		case memimport.Duplicate:
			fmt.Fprintf(out, "Not saved, %s looks like %s. Use --new to save it anyway.\n", r.File, r.Result.Detail)
		case memimport.Refused:
			fmt.Fprintf(out, "Refused %s: %s in the %s at line %d, column %d. Nothing was sent.\n", r.File, r.Finding.Rule, r.Finding.Field, r.Finding.Line, r.Finding.Column)
		case memimport.Failed:
			failed++
			fmt.Fprintf(out, "Failed %s: %v\n", r.File, r.Err)
		}
	}
	fmt.Fprintf(out, "%d of %d saved.\n", saved, len(reports))
	if failed > 0 {
		return fmt.Errorf("%d entries could not be saved", failed)
	}
	return nil
}
