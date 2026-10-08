package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
)

// installMerge runs install --merge: the installed version of a registry
// package is the base, the release asked for (latest by default) the
// other side, and the person's files are kept with both changes in them.
func (d Deps) installMerge(cmd *cobra.Command, arg, remoteName string, opts install.Options) error {
	if opts.Force {
		return install.ErrMergeForce
	}
	if isDir(d.abs(arg)) {
		return errors.New("--merge needs a package from a registry: a folder install has no release to merge from; edit the folder and install it again, or use --force")
	}
	ref, version, _ := strings.Cut(arg, "@")
	id, err := pack.ParseID(ref)
	if err != nil {
		return fmt.Errorf("%q is not TEAM/NAME: %w", arg, err)
	}
	if d.Builtins != nil && d.Builtins.IsBuiltin(id) {
		return fmt.Errorf("loadout's own skills have no merge (the old text is not in this loadout); replace your edit with: loadout setup --skills --force")
	}
	installs, err := d.Releases.Installed(cmd.Context(), id)
	if err != nil {
		return fmt.Errorf("%w; there is nothing to merge, install it with: loadout install %s", err, id)
	}
	i := slices.IndexFunc(installs, func(e lock.Entry) bool { return e.Scope == opts.Scope })
	if i < 0 {
		return fmt.Errorf("%s is not installed in %s scope, so there is nothing to merge; install it with: loadout install %s%s", id, opts.Scope.Kind, id, scopeFlag(opts.Scope))
	}
	e := installs[i]
	if d.Notices != nil {
		left, err := d.Notices.Unresolved(cmd.Context(), id, opts.Scope)
		if err != nil {
			return err
		}
		if len(left) > 0 {
			return fmt.Errorf("%s still has conflict markers from the last loadout install --merge in %s; resolve them first (keep what should stay and delete the marker lines), or discard your edits with: loadout install %s --force%s",
				id, strings.Join(left, ", "), id, scopeFlag(opts.Scope))
		}
	}
	if e.Remote == "" {
		return errors.New("--merge needs a package from a registry: this one was installed from a folder; edit the folder and install it again, or use --force")
	}
	c, r, err := d.connectFor(remoteName, id.Team(), e.Remote)
	if err != nil {
		return err
	}
	if version == "" {
		version = "latest"
	}
	ctx := cmd.Context()
	next, err := c.Fetch(ctx, id, version)
	if err != nil {
		return err
	}
	if next.Version() == e.Version {
		return fmt.Errorf("%s %s is installed already; nothing to merge (to apply new model settings over your edits, use --force)", id, e.Version)
	}
	base, err := c.Fetch(ctx, id, e.Version.String())
	if err != nil {
		return fmt.Errorf("fetch the installed version to merge from: %w", err)
	}
	layers, err := fetchLayers(ctx, c)
	if err != nil {
		return err
	}
	if opts.Models, err = fetchTiers(ctx, c); err != nil {
		return err
	}
	if len(opts.Targets) == 0 {
		opts.Targets = e.Targets()
	}
	opts.Source, opts.Remote, opts.Depth = r.URL, r.Name, depthOf(layers, id.Team())
	res, err := d.Installer.Merge(ctx, base, next, opts)
	if err != nil {
		return err
	}
	if d.Notices != nil {
		m := syncstate.Merged{ID: id, Scope: opts.Scope, Version: next.Version(), Labels: res.Labels, Paths: res.Conflicted(), At: time.Now()}
		if err := d.Notices.RecordMerge(ctx, m); err != nil {
			d.Log.Warn("could not remember the conflicts for loadout status; check the files yourself", "err", err)
		}
	}
	printMerge(cmd.OutOrStdout(), e, res)
	return nil
}

// printMerge says what happened to each file, and what is left to do.
func printMerge(out io.Writer, from lock.Entry, res install.MergeResult) {
	e := res.Entry
	fmt.Fprintf(out, "updated %s %s to %s (%s scope), keeping your edits:\n", e.ID, from.Version, e.Version, e.Scope.Kind)
	took := 0
	for _, f := range res.Files {
		what := string(f.Action)
		switch {
		case f.Action == install.MergeTook:
			took++ // the release's file, as any install writes it
			continue
		case f.Action == install.MergeConflicted && f.Whole:
			what = "both whole"
		case f.Action == install.MergeConflicted:
			what = plural(f.Conflicts, "conflict", "conflicts")
		}
		fmt.Fprintf(out, "  %-16s %s\n", what, shownPath(e.Scope, f.Rel))
	}
	if took > 0 {
		fmt.Fprintf(out, "  %-16s %s you had not edited\n", "took the release", plural(took, "file", "files"))
	}
	for _, p := range res.Kept {
		fmt.Fprintf(out, "  %-16s %s (the release no longer has it; loadout leaves it to you)\n", "kept yours", p)
	}
	if n := len(res.Conflicted()); n > 0 {
		fmt.Fprintf(out, "%s conflict markers (<<<<<<< %s ... >>>>>>> %s). Resolve them in your editor or AI tool and delete the marker lines; until then your AI tool reads them. loadout status says what is left.\n",
			plural(n, "file has", "files have"), res.Labels.Ours, res.Labels.Theirs)
	}
}

// scopeFlag is the --scope a command needs for s.
func scopeFlag(s target.Scope) string {
	if s.Kind == target.ScopeProject {
		return " --scope project"
	}
	return ""
}
