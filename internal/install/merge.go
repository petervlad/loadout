package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/textdiff"
	"github.com/petervlad/loadout/internal/tier"
)

// MergeAction is what Merge did to one file.
type MergeAction string

// What Merge does with each file of the release.
const (
	// MergeTook writes the release's file: it was not edited.
	MergeTook MergeAction = "took the release"
	// MergeKept leaves the person's edit: the release did not change the
	// file.
	MergeKept MergeAction = "kept yours"
	// MergeMerged merged both changes cleanly.
	MergeMerged MergeAction = "merged"
	// MergeConflicted wrote conflict markers where both changed the same
	// lines.
	MergeConflicted MergeAction = "conflicts"
)

// MergedFile is what Merge did to one file.
type MergedFile struct {
	// Path is absolute; Rel is slash-separated, relative to the scope root.
	Path, Rel string
	Target    string
	Action    MergeAction
	// Conflicts counts the conflicts marked in the file.
	Conflicts int
	// Whole is set when the file holds both versions whole: too long to
	// merge by line, or no known base.
	Whole bool
}

// MergeResult says what Merge did.
type MergeResult struct {
	Entry lock.Entry
	Files []MergedFile
	// Kept are edited files the new release no longer has: left in place,
	// no longer managed, as an upgrade does.
	Kept []string
	// Labels are the names in the conflict markers written.
	Labels textdiff.Labels
}

// Conflicted returns the files left with conflict markers, relative to
// the scope root.
func (r MergeResult) Conflicted() []string {
	var out []string
	for _, f := range r.Files {
		if f.Action == MergeConflicted {
			out = append(out, f.Rel)
		}
	}
	return out
}

// ErrMergeForce refuses --merge with --force: one keeps the edits, the
// other replaces them.
var ErrMergeForce = errors.New("--merge keeps your edits and --force replaces them; choose one")

// MergeLabels are the names Merge writes in conflict markers.
func MergeLabels(installed, next semver.Version) textdiff.Labels {
	return textdiff.Labels{
		Ours:   "yours (edited " + installed.String() + ")",
		Base:   "installed " + installed.String(),
		Theirs: "release " + next.String(),
	}
}

// Merge installs next over base, the installed version, keeping the
// person's edits: each edited file is merged three ways (base rendered
// again, the file on disk, next rendered), per tool, and conflicts are
// marked in it. The lock then records next with the hashes of its own
// files, so the merged files count as the person's edits on next. Files
// loadout did not write, links and another package's files still refuse,
// as Install does.
//
// Rendering is deterministic, so base renders to exactly what loadout
// wrote; each base file must hash to the lock's record to be used, and a
// file whose base cannot be found is merged whole, never against a guess.
func (in *Installer) Merge(ctx context.Context, base, next *pack.Pack, opts Options) (MergeResult, error) {
	if base.ID() != next.ID() {
		return MergeResult{}, fmt.Errorf("merge %s onto %s: not the same package", base.ID(), next.ID())
	}
	if opts.Force {
		return MergeResult{}, ErrMergeForce
	}
	if err := checkScope(opts.Scope); err != nil {
		return MergeResult{}, err
	}
	if opts.Policy != "" && !opts.Policy.Valid() {
		return MergeResult{}, fmt.Errorf("unknown policy %q: want auto, notify or pin", opts.Policy)
	}
	renderers, err := in.selectRenderers(next.Manifest().Targets, opts.Targets)
	if err != nil {
		return MergeResult{}, err
	}
	rendered, err := render(renderers, next, opts.Scope, stampOf(next), opts.Models)
	if err != nil {
		return MergeResult{}, err
	}
	if info, err := statDir(opts.Scope.Root); err != nil || !info {
		return MergeResult{}, fmt.Errorf("scope root %s is not a folder", opts.Scope.Root)
	}
	m := merger{
		in: in, files: tree{root: opts.Scope.Root}, base: base, next: next, opts: opts, renderers: renderers, rendered: rendered,
		labels: MergeLabels(base.Version(), next.Version()),
	}
	var res MergeResult
	err = in.locks.Update(ctx, func(l *lock.Lock) error {
		var err error
		res, err = m.run(ctx, l)
		return err
	})
	if err != nil {
		return MergeResult{}, err
	}
	return res, nil
}

func stampOf(p *pack.Pack) target.Stamp {
	return target.Stamp{ID: p.ID(), Version: p.Version(), Digest: p.Digest()}
}

// merger is one run of Merge.
type merger struct {
	in         *Installer
	files      tree
	base, next *pack.Pack
	opts       Options
	renderers  []Renderer
	rendered   []renderedFile
	labels     textdiff.Labels
}

// planned is one file to write and what it is.
type planned struct {
	rf   renderedFile
	data []byte
	file MergedFile
}

func (m merger) run(ctx context.Context, l *lock.Lock) (MergeResult, error) {
	id := m.next.ID()
	prev, ok := l.Find(id, m.opts.Scope)
	if !ok {
		return MergeResult{}, fmt.Errorf("%s is not installed in %s scope, so there are no edits to keep; install it instead: %w", id, m.opts.Scope.Kind, ErrNotInstalled)
	}
	if prev.Version != m.base.Version() {
		return MergeResult{}, fmt.Errorf("%s %s is installed, not %s; merge from the installed version", id, prev.Version, m.base.Version())
	}
	policy := m.opts.Policy
	if policy == "" {
		policy = prev.Policy
	}
	write, shadows := splitShadowed(l, m.files.root, id, m.opts.Depth, m.rendered)
	entry := lock.Entry{
		ID: id, Version: m.next.Version(), Digest: m.next.Digest(), Source: m.opts.Source, Remote: m.opts.Remote,
		Policy: policy, Scope: m.opts.Scope, Depth: m.opts.Depth, Files: records(write), Shadowed: shadows,
		Models: m.opts.Models.Digest(),
	}
	dropped := append(unitLeftovers(l, m.files.root, id, entry.Files), stale(prev.Files, entry)...)
	conflicts, err := findConflicts(m.files, l, id, m.opts.Depth, write, dropped)
	if err != nil {
		return MergeResult{}, err
	}
	// Only the person's edits are merged; anything else in the way
	// refuses, as it would refuse an install.
	if blocking := slices.DeleteFunc(conflicts, func(c Conflict) bool { return c.Reason == ReasonModified }); len(blocking) > 0 {
		return MergeResult{}, &ConflictError{Conflicts: blocking}
	}
	bases, err := m.bases(prev)
	if err != nil {
		return MergeResult{}, err
	}
	plan, err := m.plan(prev, write, bases)
	if err != nil {
		return MergeResult{}, err
	}

	res := MergeResult{Entry: entry, Labels: m.labels}
	for _, f := range dropped {
		if m.files.hash(f.Path) != f.SHA256 && m.files.exists(f.Path) {
			res.Kept = append(res.Kept, m.files.abs(f.Path))
		}
		m.in.removeFile(m.files, f)
	}
	tookOver := takeOver(l, m.files.root, id, entry.Files)
	for _, p := range plan {
		if err := ctx.Err(); err != nil {
			return MergeResult{}, err
		}
		if p.data != nil {
			if err := m.files.write(p.rf.file.Path, p.data); err != nil {
				return MergeResult{}, fmt.Errorf("write %s: %w", m.files.abs(p.rf.file.Path), err)
			}
		}
		res.Files = append(res.Files, p.file)
	}
	l.Put(entry)
	m.in.log.Info("merged", "id", id, "from", prev.Version, "to", entry.Version, "scope", entry.Scope,
		"files", len(res.Files), "conflicted", len(res.Conflicted()), "kept", len(res.Kept), "took_over", len(tookOver))
	return res, nil
}

// bases renders the installed version again and keeps each file that
// hashes to what the lock recorded. The lock keeps only the digest of the
// model table, so the base renders with the current table and, for an
// entry written with none, with the empty one.
func (m merger) bases(prev lock.Entry) (map[string][]byte, error) {
	tables := []tier.Table{m.opts.Models}
	if prev.Models == "" && m.opts.Models.Digest() != "" {
		tables = append(tables, tier.Table{})
	}
	out := map[string][]byte{}
	for _, t := range tables {
		rendered, err := render(m.renderers, m.base, m.opts.Scope, stampOf(m.base), t)
		if err != nil {
			return nil, err
		}
		for _, rf := range rendered {
			if _, done := out[rf.file.Path]; done {
				continue
			}
			if rec, ok := recordOf(prev, rf.file.Path); ok && hashBytes(rf.file.Data) == rec.SHA256 {
				out[rf.file.Path] = rf.file.Data
			}
		}
	}
	return out, nil
}

// plan decides each file of the release, a table of cases: not edited
// takes the release (a deleted file comes back); edited and unchanged in
// the release keeps yours; both changed merges line by line; a file too
// long to merge, or without a known base, holds both versions whole.
func (m merger) plan(prev lock.Entry, write []renderedFile, bases map[string][]byte) ([]planned, error) {
	out := make([]planned, 0, len(write))
	for _, rf := range write {
		p := planned{rf: rf, file: MergedFile{Path: m.files.abs(rf.file.Path), Rel: rf.file.Path, Target: rf.target}}
		rec, inPrev := recordOf(prev, rf.file.Path)
		disk, err := m.files.read(rf.file.Path)
		edited := inPrev && err == nil && hashBytes(disk) != rec.SHA256
		base, known := bases[rf.file.Path]
		switch {
		case !edited:
			p.data, p.file.Action = rf.file.Data, MergeTook
		case known && bytes.Equal(base, rf.file.Data):
			p.file.Action = MergeKept
		case known:
			text, n, err := textdiff.Merge3(string(base), string(disk), string(rf.file.Data), m.labels)
			switch {
			case errors.Is(err, textdiff.ErrTooLarge):
				p.data, n, p.file.Whole = textdiff.Whole(base, disk, rf.file.Data, m.labels), 1, true
			case err != nil:
				return nil, fmt.Errorf("merge %s: %w", p.file.Path, err)
			default:
				p.data = []byte(text)
			}
			p.file.Action, p.file.Conflicts = MergeMerged, n
			if n > 0 {
				p.file.Action = MergeConflicted
			}
		default:
			p.data, p.file.Whole = textdiff.Whole(nil, disk, rf.file.Data, m.labels), true
			p.file.Action, p.file.Conflicts = MergeConflicted, 1
		}
		out = append(out, p)
	}
	return out, nil
}

func recordOf(e lock.Entry, path string) (lock.FileRecord, bool) {
	i := slices.IndexFunc(e.Files, func(f lock.FileRecord) bool { return f.Path == path })
	if i < 0 {
		return lock.FileRecord{}, false
	}
	return e.Files[i], true
}

// Marked returns the paths of e, among paths, that still hold conflict
// markers with labels l: what a merge left for the person to resolve.
func (in *Installer) Marked(ctx context.Context, e lock.Entry, paths []string, l textdiff.Labels) ([]string, error) {
	files := tree{root: e.Scope.Root}
	var out []string
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := files.read(p)
		if err != nil {
			continue // gone, or a link: nothing of the merge to resolve there
		}
		if textdiff.Marked(data, l) {
			out = append(out, p)
		}
	}
	return out, nil
}
