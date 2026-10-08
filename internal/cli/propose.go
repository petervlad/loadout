package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/rebase"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/release"
	"github.com/petervlad/loadout/internal/remote"
)

// promptSeparator splits a --prompts-file into prompts.
const promptSeparator = "---"

func newProposeCmd(d Deps) *cobra.Command {
	var remoteName, bump, note, promptsFile, base string
	var prompts []string
	cmd := &cobra.Command{
		Use:   "propose [FOLDER]",
		Short: "Propose a new version for review instead of publishing it",
		Long: `Upload a candidate version with the prompts you tested it with. It is not a
release: nobody gets it through sync until a teammate with a publish token
approves it. Any token of the team can propose.

Reviewers see it with: loadout review list

A folder made from an older release than the latest is refused: bring the
newer changes in with loadout rebase first, or say the folder already has
them with --base. Proposing a folder whose open proposal you made replaces
that proposal in one step, and its prompts come along.`,
		Example: `  loadout propose ./planner --bump minor --note "cites tickets" \
    --prompt "Plan PROJ-1234" --prompt "Plan a DB migration"
  loadout propose ./planner --prompts-file prompts.md`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			folder := ""
			if len(args) == 1 {
				folder = args[0]
			}
			dir := d.WorkDir
			if folder != "" {
				dir = d.abs(folder)
			}
			if promptsFile != "" {
				more, err := readPromptsFile(d.abs(promptsFile))
				if err != nil {
					return err
				}
				prompts = append(prompts, more...)
			}
			p, err := pack.Load(dir)
			if err != nil {
				return err
			}
			c, r, err := d.connectFor(remoteName, p.ID().Team(), "")
			if err != nil {
				return err
			}
			// Where the folder came from, read before --bump changes its
			// version.
			o, rb, err := d.origin(cmd.Context(), c, r, dir, p, base)
			if err != nil {
				return err
			}
			if err := checkReady(rb, dir, o, "propose", folder); err != nil {
				return err
			}
			if o.Replaces == "" || len(prompts) > 0 {
				if err := checkPrompts(prompts); err != nil {
					return err
				}
			}
			if bump != "" {
				part, err := parsePart(bump)
				if err != nil {
					return err
				}
				next, err := release.Next(cmd.Context(), c, p.ID(), p.Version(), part)
				if err != nil {
					return err
				}
				if p, err = pack.WithVersion(p, next); err != nil {
					return err
				}
			}

			prop, err := c.Propose(cmd.Context(), p, api.ProposalRequest{Note: note, Prompts: prompts, Base: o.Base, Replaces: o.Replaces})
			if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Code == api.CodeVersionConflict {
				return fmt.Errorf("%w\nraise the version in %s or propose with --bump patch", err, filepath.Join(dir, pack.ManifestFile))
			}
			if err != nil {
				return err
			}
			if bump != "" {
				if err := pack.SetVersion(dir, p.Version()); err != nil {
					return fmt.Errorf("proposed %s@%s, but could not update %s: %w", prop.Package, prop.Version, pack.ManifestFile, err)
				}
			}
			if rb != nil {
				if err := rb.Done(dir); err != nil {
					d.Log.Warn("forget the rebase record", "dir", dir, "err", err)
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "proposed %s@%s to %s as %s with %d prompt(s)\n", prop.Package, prop.Version, r.Name, prop.ID, len(prop.Prompts))
			writeProposed(out, o, prop)
			fmt.Fprintf(out, "a teammate reviews it with: loadout review show %s\n", prop.ID)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&remoteName, "remote", "", "registry to propose to (default: the one bound to the package's team, else the default remote)")
	f.StringVar(&bump, "bump", "", "raise the version first: patch, minor or major (from the highest of local and published)")
	f.StringVar(&note, "note", "", "what changed and why, for the reviewer")
	f.StringArrayVar(&prompts, "prompt", nil, "a prompt you tested with (repeatable)")
	f.StringVar(&promptsFile, "prompts-file", "", "file of prompts separated by lines containing only ---")
	f.StringVar(&base, "base", "", "the release the folder was made from, or latest (default: worked out; see loadout rebase --help)")
	return cmd
}

// readPromptsFile reads prompts separated by "---" lines.
func readPromptsFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--prompts-file: %w", err)
	}
	var out []string
	var cur []string
	flush := func() {
		if p := strings.TrimSpace(strings.Join(cur, "\n")); p != "" {
			out = append(out, p)
		}
		cur = cur[:0]
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == promptSeparator {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out, nil
}

// checkPrompts gives the registry's limits a local, friendlier message.
func checkPrompts(prompts []string) error {
	switch n := len(prompts); {
	case n == 0:
		return errors.New("give the prompts you tested with: --prompt TEXT (repeatable) or --prompts-file FILE")
	case n > registry.MaxPrompts:
		return fmt.Errorf("%d prompts: at most %d", n, registry.MaxPrompts)
	}
	for i, p := range prompts {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("prompt %d is empty", i+1)
		}
		if len(p) > registry.MaxPromptSize {
			return fmt.Errorf("prompt %d is %d bytes: at most %d", i+1, len(p), registry.MaxPromptSize)
		}
	}
	return nil
}

// origin works out what the folder in dir was made from, before --bump
// changes its version. Without a Rebaser it says nothing (no base, not
// behind), as an older loadout did.
func (d Deps) origin(ctx context.Context, c *client.Client, r remote.Remote, dir string, p *pack.Pack, base string) (rebase.Origin, Rebaser, error) {
	if d.NewRebaser == nil {
		return rebase.Origin{Package: p.ID()}, nil, nil
	}
	rb, err := d.rebaser(c, r)
	if err != nil {
		return rebase.Origin{}, nil, err
	}
	o, err := rb.Locate(ctx, dir, p, rebase.Options{Base: base})
	if err != nil {
		return rebase.Origin{}, nil, err
	}
	return o, rb, nil
}

// checkReady refuses to propose or publish (verb) a folder that still
// holds conflict markers from loadout rebase, or that is behind.
func checkReady(rb Rebaser, dir string, o rebase.Origin, verb, folder string) error {
	if rb == nil {
		return nil
	}
	pending, err := rb.Unresolved(dir)
	if err != nil {
		return err
	}
	if len(pending.Markers) > 0 {
		return fmt.Errorf("%s from loadout rebase (<<<<<<<): %s; resolve them, then %s again",
			plural(len(pending.Markers), "file still has conflict markers", "files still have conflict markers"), strings.Join(pending.Markers, ", "), verb)
	}
	if len(pending.Undecided) > 0 {
		return fmt.Errorf("%s deleted on one side and changed on the other when loadout rebase merged: %s. Delete %s, or keep %s with: %s; then %s again",
			plural(len(pending.Undecided), "file was", "files were"), strings.Join(pending.Undecided, ", "),
			pick(len(pending.Undecided), "it", "them"), pick(len(pending.Undecided), "it", "them"), resolvedCmd(folder, pending.Undecided), verb)
	}
	if o.Behind() {
		return o.Refusal(verb, folder)
	}
	return nil
}

// writeProposed says what the new proposal was made from and what it
// replaced, or that an older registry left the old one open.
func writeProposed(out io.Writer, o rebase.Origin, prop api.Proposal) {
	switch {
	case prop.Replaces != "":
		fmt.Fprintf(out, "replaces %s (closed; its prompts came along)\n", prop.Replaces)
	case o.Replaces != "":
		fmt.Fprintf(out, "this registry is older than replacing proposals, so %s stays open; withdraw it with: loadout review withdraw %s\n", o.Replaces, o.Replaces)
	}
	if prop.Base != "" {
		fmt.Fprintf(out, "based on %s\n", prop.Base)
	}
}

// resolvedCmd is the command that keeps files a rebase left undecided.
func resolvedCmd(folder string, files []string) string {
	parts := []string{"loadout rebase"}
	if folder != "" {
		parts = append(parts, folder)
	}
	for _, f := range files {
		parts = append(parts, "--resolved", f)
	}
	return strings.Join(parts, " ")
}

// pick is one when n is 1, else many.
func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
