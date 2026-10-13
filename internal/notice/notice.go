// Package notice turns what the last sync learned into what to tell the
// person: an update their edits hold back, one that is available, or a
// merge that left conflicts.
//
// It never opens a network connection. It is built from the sync state and
// the installer only, so the offline promise is a property of its
// constructor: status and the session hooks run it on every call, and a
// hook must answer in well under a second.
package notice

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/builtin"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/textdiff"
)

// Files is what notice needs from the installer.
type Files interface {
	Entries(ctx context.Context) ([]lock.Entry, error)
	Edited(ctx context.Context, e lock.Entry, only []string) ([]string, error)
	// Marked returns the paths among paths that still hold conflict
	// markers with labels l.
	Marked(ctx context.Context, e lock.Entry, paths []string, l textdiff.Labels) ([]string, error)
}

var _ Files = (*install.Installer)(nil)

// Memory is what the last sync learned, and the merges left with
// conflicts; *syncstate.Store is one.
type Memory interface {
	Load() (syncstate.State, error)
	Update(ctx context.Context, fn func(*syncstate.State) error) error
}

var _ Memory = (*syncstate.Store)(nil)

// Kind says what a notice is about.
type Kind int

// Notice kinds, from least to most pressing.
const (
	// Available is a newer release (or model settings) with nothing in
	// the way.
	Available Kind = iota + 1
	// Held is a newer release (or model settings) the person's edits
	// keep out.
	Held
	// Unresolved is a merge (install --merge) that left conflict markers
	// in files the person's tools read.
	Unresolved
)

// Notice is one thing to tell, about one install.
type Notice struct {
	Kind      Kind
	ID        pack.ID
	Scope     target.Scope
	Installed semver.Version
	Latest    semver.Version
	// Models is set when only the team's model settings are new.
	Models bool
	// Template is the template release ("acme/dev-loop@1.1.0") a conductor
	// is behind on, when that is all that is new.
	Template string
	// Files are the files still in the way, as the person knows them:
	// "planner.md (claude)", "SKILL.md (claude, copilot)".
	Files []string
	// Edits is set when the files are in the way because the person
	// edited them; otherwise loadout did not write them.
	Edits bool
	// Builtin marks one of loadout's own skills, whose way out is setup.
	Builtin bool
	// Checked is when the last sync looked.
	Checked time.Time
}

// Sentence is the line status and the plain notices print, e.g.
// "1.3.0 held back: you edited planner.md (claude)".
func (n Notice) Sentence() string {
	what := n.Latest.String()
	if n.Models {
		what = "model settings"
	}
	if n.Template != "" {
		what = "template " + n.Template
	}
	switch n.Kind {
	case Unresolved:
		return fmt.Sprintf("%s merged: resolve the conflicts in %s", n.Latest, list(n.Files))
	case Held:
		if n.Edits {
			return fmt.Sprintf("%s held back: you edited %s", what, list(n.Files))
		}
		return fmt.Sprintf("%s held back: %s in the way", what, list(n.Files))
	case Available:
		verb := "available"
		if n.Models || n.Template != "" {
			verb = "changed"
		}
		return fmt.Sprintf("%s %s: %s", what, verb, n.Command())
	}
	return ""
}

// Command is the one command that takes the update.
func (n Notice) Command() string {
	if n.Builtin {
		return "loadout setup --skills"
	}
	cmd := "loadout install " + n.ID.String()
	if n.Scope.Kind == target.ScopeProject {
		cmd += " --scope project"
	}
	return cmd
}

// Name names the install and what it is behind: "payments/planner 1.3.0",
// "payments/planner model settings".
func (n Notice) Name() string {
	if n.Models {
		return n.ID.String() + " model settings"
	}
	if n.Template != "" {
		return n.ID.String() + " template"
	}
	return n.ID.String() + " " + n.Latest.String()
}

// list names at most three things, then how many more.
func list(items []string) string {
	if len(items) <= 3 {
		return join(items)
	}
	return strings.Join(items[:3], ", ") + fmt.Sprintf(" and %d more", len(items)-3)
}

// join is "a", "a and b", "a, b and c".
func join(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// Filter narrows the notices.
type Filter struct {
	// Kinds keeps only these kinds; empty keeps every kind.
	Kinds []Kind
	// Dir is the folder a session runs in: user-scope installs always
	// match, a project install when Dir is inside its root. Empty matches
	// everything.
	Dir string
}

func (f Filter) matches(n Notice) bool {
	if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, n.Kind) {
		return false
	}
	if f.Dir == "" || n.Scope.Kind == target.ScopeUser {
		return true
	}
	// A tool may name the folder through a symbolic link (/tmp is
	// /private/tmp on macOS), so the folders are compared with links
	// followed too.
	return inside(n.Scope.Root, f.Dir) || inside(resolved(n.Scope.Root), resolved(f.Dir))
}

// inside reports whether dir is root or a folder below it.
func inside(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolved is p with its symbolic links followed, or p when that fails.
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// mayShow reports whether any record could give a notice of the kinds f
// asks for. A record without files in the way can only be Available.
func (f Filter) mayShow(records []syncstate.Behind, merged []syncstate.Merged) bool {
	if len(merged) > 0 && (len(f.Kinds) == 0 || slices.Contains(f.Kinds, Unresolved)) {
		return true
	}
	if len(f.Kinds) == 0 || slices.Contains(f.Kinds, Available) {
		return len(records) > 0
	}
	return slices.ContainsFunc(records, syncstate.Behind.IsHeld)
}

// Service lists notices.
type Service struct {
	memory Memory
	files  Files
}

// New returns a service reading what sync learned from memory and checking
// it against the installs in files.
func New(memory Memory, files Files) *Service {
	return &Service{memory: memory, files: files}
}

// List returns the notices for installs that match f, most pressing first.
// A record shows only while it is still true: the install is still in the
// lock, still behind, and for a held update, still edited. So an install
// that caught up, edits that were reverted (a held update becomes
// available), a forced install and an uninstall stop showing without
// anyone writing to the state; so does a pin. A damaged state file reads
// as empty.
func (s *Service) List(ctx context.Context, f Filter) ([]Notice, error) {
	st, err := s.memory.Load()
	if err != nil {
		return nil, nil //nolint:nilerr // the state is a hint; the next sync rewrites it
	}
	records, merged := st.All(), st.AllMerged()
	if !f.mayShow(records, merged) {
		// Reading the lock is most of the cost; a session start with
		// nothing held back skips it.
		return nil, nil
	}
	entries, err := s.files.Entries(ctx)
	if err != nil {
		return nil, err
	}
	var out []Notice
	for _, b := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		i := slices.IndexFunc(entries, func(e lock.Entry) bool { return e.ID == b.ID && e.Scope == b.Scope })
		// Pin is the quiet way out: it takes effect now, not at the next sync.
		if i < 0 || entries[i].Policy == lock.PolicyPin || !behind(entries[i], b) {
			continue
		}
		n, err := s.notice(ctx, entries[i], b)
		if err != nil {
			return nil, err
		}
		if f.matches(n) {
			out = append(out, n)
		}
	}
	for _, m := range merged {
		n, ok, err := s.unresolved(ctx, entries, m)
		if err != nil {
			return nil, err
		}
		if ok && f.matches(n) {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, func(a, b Notice) int {
		return cmp.Or(cmp.Compare(b.Kind, a.Kind), cmp.Compare(a.ID.String(), b.ID.String()), cmp.Compare(a.Scope.Root, b.Scope.Root))
	})
	return out, nil
}

// unresolved checks a merge record against the install and its files: it
// shows while a file still holds the merge's markers, whatever happened to
// the install since, so markers never go unreported.
func (s *Service) unresolved(ctx context.Context, entries []lock.Entry, m syncstate.Merged) (Notice, bool, error) {
	i := slices.IndexFunc(entries, func(e lock.Entry) bool { return e.ID == m.ID && e.Scope == m.Scope })
	if i < 0 {
		return Notice{}, false, nil
	}
	e := entries[i]
	still, err := s.files.Marked(ctx, e, m.Paths, m.Labels)
	if err != nil || len(still) == 0 {
		return Notice{}, false, err
	}
	n := Notice{Kind: Unresolved, ID: e.ID, Scope: e.Scope, Installed: e.Version, Latest: m.Version, Checked: m.At}
	for _, p := range still {
		n.Files = append(n.Files, shown(e, p))
	}
	return n, true, nil
}

// Unresolved returns the files an earlier install --merge of id in scope
// left with conflict markers that are still there, as the person knows
// them; none when there is no such merge.
func (s *Service) Unresolved(ctx context.Context, id pack.ID, scope target.Scope) ([]string, error) {
	st, err := s.memory.Load()
	if err != nil {
		return nil, nil //nolint:nilerr // the state is a hint; a damaged one has no merge to resolve
	}
	m, ok := st.FindMerged(syncstate.Key{ID: id, Scope: scope})
	if !ok {
		return nil, nil
	}
	entries, err := s.files.Entries(ctx)
	if err != nil {
		return nil, err
	}
	n, ok, err := s.unresolved(ctx, entries, m)
	if err != nil || !ok {
		return nil, err
	}
	return n.Files, nil
}

// RecordMerge remembers the files a merge left with conflict markers, so
// status and the hooks say so until they are resolved. A merge without
// conflicts forgets an earlier one only once its markers are gone.
func (s *Service) RecordMerge(ctx context.Context, m syncstate.Merged) error {
	if len(m.Paths) == 0 {
		left, err := s.Unresolved(ctx, m.ID, m.Scope)
		if err != nil || len(left) > 0 {
			return err
		}
	}
	return s.memory.Update(ctx, func(st *syncstate.State) error {
		if len(m.Paths) == 0 {
			st.DropMerged(m.Key())
			return nil
		}
		st.PutMerged(m)
		return nil
	})
}

// behind reports whether e is still behind what b recorded.
func behind(e lock.Entry, b syncstate.Behind) bool {
	if b.Template != "" {
		return !slices.ContainsFunc(e.Requires, func(r lock.Requirement) bool { return r.ID.String()+"@"+r.Version.String() == b.Template })
	}
	if b.ModelsOnly() {
		return e.Version.Compare(b.Version) == 0 && e.Models != b.Models
	}
	return e.Version.Compare(b.Latest) < 0
}

// notice checks b's files again: only the recorded paths, which keeps a
// knowledge base of thousands of files cheap. Files in the way for another
// reason than an edit (loadout did not write them) cannot be checked
// against the lock and keep holding until the next sync looks.
func (s *Service) notice(ctx context.Context, e lock.Entry, b syncstate.Behind) (Notice, error) {
	n := Notice{
		Kind: Available, ID: e.ID, Scope: e.Scope, Installed: e.Version, Latest: b.Latest,
		Models: b.ModelsOnly(), Template: b.Template, Builtin: e.Source == builtin.Source, Checked: b.Checked,
	}
	var edited, other []string
	for _, h := range b.Held {
		if h.Reason == string(install.ReasonModified) {
			edited = append(edited, h.Path)
		} else {
			other = append(other, h.Path)
		}
	}
	if len(edited) > 0 {
		still, err := s.files.Edited(ctx, e, edited)
		if err != nil {
			return Notice{}, err
		}
		edited = still
	}
	if len(edited) == 0 && len(other) == 0 {
		return n, nil
	}
	n.Kind, n.Edits = Held, len(edited) > 0
	for _, p := range append(edited, other...) {
		n.Files = append(n.Files, shown(e, p))
	}
	return n, nil
}

// shown names a file the way the person knows it: its name in the skill
// folder, or the agent file's name, and the tools that read it.
func shown(e lock.Entry, p string) string {
	name, tools := path.Base(p), ""
	for _, f := range e.Files {
		if f.Path != p {
			continue
		}
		if f.Unit != "" {
			name = strings.TrimPrefix(p, f.Unit+"/")
		}
		tools = strings.ReplaceAll(f.Target, ",", ", ")
	}
	if tools == "" {
		return name
	}
	return name + " (" + tools + ")"
}

// Message is what the session hooks say about held and unresolved
// notices: a line for the person and one for their agent. It is empty
// when there is neither.
func Message(ns []Notice) target.SessionMessage {
	var held, open []Notice
	for _, n := range ns {
		switch n.Kind {
		case Held:
			held = append(held, n)
		case Unresolved:
			open = append(open, n)
		}
	}
	var person, agent []string
	if len(held) > 0 {
		names := make([]string, len(held))
		updates := make([]string, len(held))
		for i, n := range held {
			names[i] = n.Name()
			updates[i] = n.ID.String() + " to " + n.Latest.String()
			if n.Models {
				updates[i] = n.ID.String() + " to its team's new model settings"
			}
		}
		count := "1 update is"
		if len(held) > 1 {
			count = fmt.Sprintf("%d updates are", len(held))
		}
		person = append(person, fmt.Sprintf("%s held back by your edits (%s)", count, list(names)))
		line := fmt.Sprintf("loadout could not update %s because the user edited the installed files.", list(updates))
		// The merge takes a newer release over edits; it refuses new model
		// settings, loadout's own skills and files loadout did not write.
		if slices.ContainsFunc(held, func(n Notice) bool { return n.Edits && !n.Models && !n.Builtin }) {
			line = strings.TrimSuffix(line, ".") + "; loadout install NAME --merge takes the update and keeps their edits."
		}
		agent = append(agent, line)
	}
	if len(open) > 0 {
		names := make([]string, len(open))
		var files []string
		for i, n := range open {
			names[i] = n.Name()
			files = append(files, n.Files...)
		}
		person = append(person, fmt.Sprintf("merge conflicts to resolve in %s (%s)", list(files), list(names)))
		agent = append(agent, fmt.Sprintf("loadout install --merge left conflict markers (<<<<<<<, |||||||, =======, >>>>>>>) in %s, installed files you read; offer to help the user resolve them.", list(files)))
	}
	if len(person) == 0 {
		return target.SessionMessage{}
	}
	return target.SessionMessage{
		Person: "loadout: " + strings.Join(person, "; ") + ". Run loadout status.",
		Agent: strings.Join(agent, " ") + " Mention it to the user once, in one sentence, in your first reply, and suggest loadout status. " +
			"The loadout-setup skill explains the choices; never run loadout install --force without asking.",
	}
}
