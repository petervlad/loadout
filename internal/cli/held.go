package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/update"
)

// exitError is an error with its own exit code; main asks for ExitCode.
type exitError struct {
	err  error
	code int
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }
func (e exitError) ExitCode() int { return e.code }

// exitHeld is sync's exit code while an auto update is held back, so cron
// and scripts can tell it from a registry that failed (1).
const exitHeld = 2

// syncResult is the RESULT column of sync for r.
func syncResult(r update.Result) string {
	switch {
	case r.Outcome == update.OutcomeBlocked && !edits(r.Held):
		return "held back by files in the way"
	case r.Outcome == update.OutcomeBlocked && r.ModelsOnly():
		return "model settings held back by your edits"
	case r.Outcome == update.OutcomeAvailable && len(r.Held) > 0:
		return "update available, you edited " + plural(len(r.Held), "file", "files")
	case r.Outcome == update.OutcomeBlocked && r.TemplateOnly():
		return "pipeline template held back by your edits"
	case r.Outcome == update.OutcomeTemplateChanged && len(r.Held) > 0:
		return "pipeline template changed, you edited " + plural(len(r.Held), "file", "files")
	case r.Outcome == update.OutcomeModelsChanged && len(r.Held) > 0:
		return "model settings changed, you edited " + plural(len(r.Held), "file", "files")
	}
	return string(r.Outcome)
}

// writeHeld says which files keep r's update out and the ways forward.
func writeHeld(w io.Writer, r update.Result) {
	e := r.Entry
	next := r.Latest.String()
	switch {
	case r.TemplateOnly():
		next = "the new template"
		fmt.Fprintf(w, "\n%s: the new pipeline template %s is held back.\n", e.ID, r.Template)
	case r.ModelsOnly():
		next = "the new settings"
		fmt.Fprintf(w, "\n%s: your team's new model settings are held back.\n", e.ID)
	case r.Outcome == update.OutcomeAvailable:
		fmt.Fprintf(w, "\n%s %s is out, and your edits would hold it back.\n", e.ID, r.Latest)
	default:
		fmt.Fprintf(w, "\n%s %s is held back.\n", e.ID, r.Latest)
	}
	for _, g := range groupBlockers(r.Held) {
		fmt.Fprintf(w, "%s:\n", g.heading)
		for _, p := range g.paths {
			fmt.Fprintf(w, "  %s\n", shownPath(e.Scope, p))
		}
	}

	scope := ""
	if e.Scope.Kind == target.ScopeProject {
		scope = " --scope project"
	}
	var steps [][2]string
	// A merge keeps edits; files loadout did not write still refuse it,
	// new model settings have no release to merge, and a conductor is
	// generated from its binding and template, so it is never merged.
	if !r.ModelsOnly() && !r.TemplateOnly() && onlyEdits(r.Held) && len(e.Requires) == 0 {
		steps = append(steps, [2]string{"Keep your edits and take " + next + ":", fmt.Sprintf("loadout install %s --merge%s", e.ID, scope)})
	}
	forceable := !slices.ContainsFunc(r.Held, func(b syncstate.Blocker) bool { return install.Reason(b.Reason).Blocking() })
	if forceable {
		label := "Replace your edits with " + next + ":"
		if !onlyEdits(r.Held) {
			label = "Replace these files with " + next + ":"
		}
		steps = append(steps, [2]string{label, fmt.Sprintf("loadout install %s --force%s", e.ID, scope)})
	}
	steps = append(steps, [2]string{"Keep your version, stop updates:", fmt.Sprintf("loadout policy %s pin%s", e.ID, scope)})
	if !r.ModelsOnly() && !r.TemplateOnly() {
		steps = append(steps, [2]string{"See what changed:", fmt.Sprintf("loadout diff %s", e.ID)})
	}
	width := 0
	for _, s := range steps {
		width = max(width, len(s[0]))
	}
	for _, s := range steps {
		fmt.Fprintf(w, "%-*s  %s\n", width, s[0], s[1])
	}
	if !forceable {
		fmt.Fprintln(w, "--force cannot replace all of these files; move them out of the way first.")
	}
	if e.Scope.Kind == target.ScopeProject {
		fmt.Fprintf(w, "Run these in %s.\n", e.Scope.Root)
	}
}

// blockerGroup is the files in the way for one reason.
type blockerGroup struct {
	heading string
	paths   []string
}

// groupBlockers groups files by why they are in the way, in first-seen
// order: "Edited since loadout wrote it", "Not written by loadout", ...
func groupBlockers(held []syncstate.Blocker) []blockerGroup {
	var out []blockerGroup
	for _, b := range held {
		heading := strings.ToUpper(b.Reason[:1]) + b.Reason[1:]
		if b.Owner != "" {
			heading += " " + b.Owner
		}
		i := slices.IndexFunc(out, func(g blockerGroup) bool { return g.heading == heading })
		if i < 0 {
			out = append(out, blockerGroup{heading: heading})
			i = len(out) - 1
		}
		out[i].paths = append(out[i].paths, b.Path)
	}
	return out
}

// edits reports whether any file is in the way because the person edited it.
func edits(held []syncstate.Blocker) bool {
	return slices.ContainsFunc(held, func(b syncstate.Blocker) bool { return b.Reason == string(install.ReasonModified) })
}

// onlyEdits reports whether every file is in the way because it was edited.
func onlyEdits(held []syncstate.Blocker) bool {
	return !slices.ContainsFunc(held, func(b syncstate.Blocker) bool { return b.Reason != string(install.ReasonModified) })
}

// shownPath shows a file of a scope the way the person knows it: under ~
// for user scope, as a full path in a project.
func shownPath(s target.Scope, rel string) string {
	if s.Kind == target.ScopeUser {
		return "~/" + rel
	}
	return filepath.Join(s.Root, filepath.FromSlash(rel))
}
