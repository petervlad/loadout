package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/rebase"
	"github.com/petervlad/loadout/internal/remote"
)

// Rebaser works out what a package folder was made from and brings it up
// to date with the latest release; *rebase.Service is one.
type Rebaser interface {
	Locate(ctx context.Context, dir string, p *pack.Pack, opts rebase.Options) (rebase.Origin, error)
	Rebase(ctx context.Context, dir string, p *pack.Pack, o rebase.Origin) (rebase.Result, error)
	Unresolved(dir string) (rebase.Pending, error)
	MarkResolved(dir string, files []string) error
	Done(dir string) error
}

var _ Rebaser = (*rebase.Service)(nil)

// rebaser returns the Rebaser for the registry r reached through c, or
// an error when this build has none.
func (d Deps) rebaser(c *client.Client, r remote.Remote) (Rebaser, error) {
	if d.NewRebaser == nil {
		return nil, errors.New("this loadout cannot rebase")
	}
	return d.NewRebaser(c, r.URL), nil
}

func newRebaseCmd(d Deps) *cobra.Command {
	var remoteName string
	var opts rebase.Options
	var resolved []string
	cmd := &cobra.Command{
		Use:   "rebase [FOLDER]",
		Short: "Bring a package folder or a proposal up to date with the latest release",
		Long: `Merge the changes made in a package folder since the release it was made
from onto the package's latest release, file by file and line by line, on
this machine. Changes to different lines both apply; changes to the same
lines are written into the file between <<<<<<< and >>>>>>> markers, for
you to resolve in your editor or AI tool. Commit the folder first: rebase
rewrites its files.

loadout works out what the folder was made from: --base, else its last
rebase, else the open proposal at its version, else the release at its
version, else the newest release below it. With --proposal ID and no
folder, it starts from that proposal's files in a new folder.

A file deleted on one side and changed on the other is left for you to
decide: delete it, or keep it with --resolved FILE (a path inside the
package). propose and publish refuse the folder until every conflict is
decided.

Then propose it again: loadout propose replaces your open proposal in one
step, and its prompts come along.`,
		Example: `  loadout rebase planner
  loadout rebase --proposal 3f2a9c01b7de
  loadout rebase planner --base 2.0.0
  loadout rebase planner --resolved references/old.md`,
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
			var p *pack.Pack
			var err error
			// Started from a proposal outside any package folder: the
			// folder is made (or found) below, named after the package.
			if folder != "" || opts.Proposal == "" || hasManifest(dir) {
				if p, err = loadIfThere(dir); err != nil {
					return err
				}
			}
			if len(resolved) > 0 {
				return markResolved(cmd, d, remoteName, dir, p, folder, resolved)
			}
			ns := ""
			if p != nil {
				ns = p.ID().Team()
			}
			c, r, err := d.connectFor(remoteName, ns, "")
			if err != nil {
				return err
			}
			rb, err := d.rebaser(c, r)
			if err != nil {
				return err
			}
			o, err := rb.Locate(cmd.Context(), dir, p, opts)
			if err != nil {
				return err
			}
			if p == nil && folder == "" {
				// Not in a package folder: start from the proposal in a
				// folder named after the package, or use the one there.
				folder = o.Package.Name()
				dir = filepath.Join(d.WorkDir, folder)
				if p, err = loadIfThere(dir); err != nil {
					return err
				}
				if p != nil { // the folder is there: it is the author's side
					if o, err = rb.Locate(cmd.Context(), dir, p, opts); err != nil {
						return err
					}
				}
			}
			res, err := rb.Rebase(cmd.Context(), dir, p, o)
			if err != nil {
				return err
			}
			writeRebase(cmd.OutOrStdout(), o, res, folder)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&remoteName, "remote", "", "registry of the package (default: the team's registry, else the default remote)")
	f.StringVar(&opts.Base, "base", "", "the release the folder was made from, or latest (default: worked out, see above)")
	f.StringVar(&opts.Proposal, "proposal", "", "the open proposal to bring up to date; without a folder, start from its files")
	f.StringArrayVar(&resolved, "resolved", nil, "keep FILE, which a rebase left deleted on one side and changed on the other (repeatable); nothing is merged")
	return cmd
}

// loadIfThere loads the package in dir, or returns nil when dir is
// missing or empty, so a rebase can start from a proposal there.
func loadIfThere(dir string) (*pack.Pack, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && len(entries) == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pack.Load(dir)
}

// hasManifest reports whether dir holds a package manifest.
func hasManifest(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, pack.ManifestFile))
	return err == nil
}

// writeRebase prints where the folder came from, each file the rebase
// touched, and what to do next.
func writeRebase(out io.Writer, o rebase.Origin, res rebase.Result, folder string) {
	name := o.Package.Name()
	if res.UpToDate {
		if o.Latest == "" {
			fmt.Fprintf(out, "%s has no release yet; nothing to rebase.\n", name)
			return
		}
		fmt.Fprintf(out, "%s is up to date with %s; nothing to rebase.\n", name, o.Latest)
		return
	}
	fmt.Fprintf(out, "%s: %s, onto %s\n", name, originText(o), o.Latest)
	width := 0 // one column for the change, as wide as its longest
	for _, f := range res.Files {
		width = max(width, len(changeText(f, o)))
	}
	for _, f := range res.Files {
		fmt.Fprintf(out, "  %-*s  %s%s\n", width, changeText(f, o), f.Path, conflictText(f))
	}
	if len(res.Undecided) > 0 {
		fmt.Fprintf(out, "Decide each file deleted on one side and changed on the other: delete it, or keep it with: %s\n", resolvedCmd(folder, res.Undecided))
	}
	if res.Bumped {
		fmt.Fprintf(out, "The version is now %s, the next after %s.\n", res.Version, res.Onto)
	}
	arg := ""
	if folder != "" {
		arg = " " + folder
	}
	next, or := "loadout propose"+arg, ""
	if o.Replaces == "" { // a folder from a release may also be published
		or = " (or publish it with: loadout publish" + arg + ")"
	}
	if len(res.Conflicts) == 0 {
		fmt.Fprintf(out, "No conflicts; propose it with: %s%s\n", next, or)
		return
	}
	// Undecided files have no markers; the line above says what to do.
	marked := slices.DeleteFunc(slices.Clone(res.Conflicts), func(p string) bool { return slices.Contains(res.Undecided, p) })
	if len(marked) == 0 {
		fmt.Fprintf(out, "Once each is decided, propose it with: %s%s\n", next, or)
		return
	}
	fmt.Fprintf(out, "%s conflicts, marked with <<<<<<<: %s. Resolve them in your editor or AI tool, then: %s%s\n",
		plural(len(marked), "file has", "files have"), strings.Join(marked, ", "), next, or)
}

// originText says what the folder was made from and how loadout knows.
func originText(o rebase.Origin) string {
	made := "made from " + o.Base
	if o.Base == "" {
		made = "made before the first release"
	}
	switch o.From {
	case rebase.SourceFlag:
		made += " (--base)"
	case rebase.SourceRecord:
		made += " (your last rebase)"
	case rebase.SourceGuess:
		made += " (a guess: the newest release below its version; say otherwise with --base)"
	}
	switch {
	case o.Replaces != "":
		return "proposal " + o.Replaces + ", " + made
	case o.Start != "":
		return "proposal " + o.Start + " by someone else (proposing makes one of your own), " + made
	}
	return made
}

func changeText(f rebase.File, o rebase.Origin) string {
	switch f.Change {
	case rebase.Yours:
		return "yours"
	case rebase.YouDeleted:
		return "you deleted"
	case rebase.FromRelease:
		return "from " + o.Latest
	case rebase.Deleted:
		return "deleted in " + o.Latest
	case rebase.Merged:
		return "merged"
	}
	return "conflict"
}

func conflictText(f rebase.File) string {
	switch {
	case f.Note != "":
		return ": " + f.Note
	case f.Conflicts > 0:
		return " (" + plural(f.Conflicts, "conflict", "conflicts") + ")"
	}
	return ""
}

// plural is "1 thing" or "N things".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// markResolved keeps the files a rebase left undecided, without merging.
func markResolved(cmd *cobra.Command, d Deps, remoteName, dir string, p *pack.Pack, folder string, files []string) error {
	if p == nil {
		return fmt.Errorf("no package folder at %s; give the folder the files are in", dir)
	}
	c, r, err := d.connectFor(remoteName, p.ID().Team(), "")
	if err != nil {
		return err
	}
	rb, err := d.rebaser(c, r)
	if err != nil {
		return err
	}
	if err := rb.MarkResolved(dir, files); err != nil {
		return err
	}
	next := "loadout propose"
	if folder != "" {
		next += " " + folder
	}
	fmt.Fprintf(cmd.OutOrStdout(), "kept %s; once every conflict is resolved, propose it with: %s\n", strings.Join(files, ", "), next)
	return nil
}
