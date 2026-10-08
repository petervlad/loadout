package rebase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/textdiff"
)

// Change is what a rebase did to one file of the folder.
type Change int

// The changes a rebase makes, one per file that differs anywhere.
const (
	Yours       Change = iota + 1 // changed only in the folder: kept as it is
	FromRelease                   // changed only in the release: taken from it
	Deleted                       // deleted in the release, unchanged in the folder
	Merged                        // changed on both sides, merged cleanly
	Conflict                      // changed on both sides in the same place
	YouDeleted                    // deleted in the folder, unchanged in the release: stays deleted
)

// File is what happened to one file.
type File struct {
	Path   string
	Change Change
	// Conflicts counts the conflicts written into the file; a file one
	// side deleted and the other changed counts one, with no markers.
	Conflicts int
	// Note says more about a conflict without markers.
	Note string
	// Undecided marks a file one side deleted and the other changed: it
	// has no markers, and the author decides whether it stays.
	Undecided bool
}

// Result is what Rebase did.
type Result struct {
	// UpToDate means the folder was not behind; nothing was written.
	UpToDate bool
	Base     string // what the folder was made from, "" for none
	Onto     string // the release merged onto
	// Version is the folder's version now; Bumped says it was raised
	// above Onto because the folder's own was not above it.
	Version semver.Version
	Bumped  bool
	// Files lists every file that differs anywhere, by path.
	Files []File
	// Conflicts are the paths left with conflicts; Undecided those of them
	// one side deleted and the other changed.
	Conflicts []string
	Undecided []string
}

// Rebase merges the folder's changes since o.Base onto o.Latest and
// writes the result into dir. p is the folder as loaded; nil means there
// is none yet (or it is empty), and the proposal o.Start is the
// author's side. Nothing is written until every file has merged.
func (s *Service) Rebase(ctx context.Context, dir string, p *pack.Pack, o Origin) (Result, error) {
	if !o.Behind() {
		return Result{UpToDate: true, Base: o.Base, Onto: o.Latest}, nil
	}
	// A new record would replace the one that knows what the last rebase
	// left unresolved, and its markers would then pass propose and publish.
	if err := s.finished(dir, p); err != nil {
		return Result{}, err
	}
	sides, err := s.fetchSides(ctx, p, o)
	if err != nil {
		return Result{}, err
	}
	version, bumped := resultVersion(sides.ours.Version(), sides.theirs.Version())
	b, ours, theirs, err := sides.withVersion(version)
	if err != nil {
		return Result{}, err
	}
	l := labels(o)
	merged, files := mergeFiles(b, ours, theirs, l)
	res := Result{Base: o.Base, Onto: o.Latest, Version: version, Bumped: bumped, Files: files}
	for _, f := range files {
		if f.Change == Conflict {
			res.Conflicts = append(res.Conflicts, f.Path)
		}
		if f.Undecided {
			res.Undecided = append(res.Undecided, f.Path)
		}
	}
	if err := check(merged, len(res.Conflicts) == 0); err != nil {
		return Result{}, err
	}
	var current map[string][]byte
	if p != nil {
		current = p.Files()
	}
	if err := write(dir, current, merged); err != nil {
		return Result{}, err
	}
	rec := Record{
		Remote: s.remote, Package: o.Package.String(), Base: o.Base, Onto: o.Latest, Replaces: o.Replaces,
		Labels: l, Conflicts: res.Conflicts, Undecided: res.Undecided, At: s.now().UTC(),
	}
	if err := s.records.Put(dir, rec); err != nil {
		return Result{}, fmt.Errorf("the folder is rebased, but %w", err)
	}
	return res, nil
}

// sides are the three versions a rebase merges; base is nil before the
// first release.
type sides struct{ base, ours, theirs *pack.Pack }

func (s *Service) fetchSides(ctx context.Context, p *pack.Pack, o Origin) (sides, error) {
	var out sides
	var err error
	if o.Base != "" {
		if out.base, err = s.reg.Fetch(ctx, o.Package, o.Base); err != nil {
			return sides{}, fmt.Errorf("fetch the base %s: %w", o.Base, err)
		}
	}
	if out.theirs, err = s.reg.Fetch(ctx, o.Package, o.Latest); err != nil {
		return sides{}, fmt.Errorf("fetch the latest release %s: %w", o.Latest, err)
	}
	out.ours = p
	if p == nil {
		if out.ours, err = s.proposal(ctx, o.Start); err != nil {
			return sides{}, err
		}
	}
	for _, other := range []*pack.Pack{out.base, out.theirs} {
		switch {
		case other == nil:
		case other.ID() != out.ours.ID():
			return sides{}, fmt.Errorf("the folder holds %s, not %s", out.ours.ID(), other.ID())
		case other.Kind() != out.ours.Kind():
			return sides{}, fmt.Errorf("%s is %s in %s and %s in your folder; the kind changed, so update the folder by hand",
				o.Package, other.Kind().Describe(), other.Version(), out.ours.Kind().Describe())
		}
	}
	return out, nil
}

func (s *Service) proposal(ctx context.Context, id string) (*pack.Pack, error) {
	if id == "" {
		return nil, errors.New("there is no folder to rebase; give the folder, or --proposal ID")
	}
	prop, err := s.reg.Proposal(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("proposal %s: %w", id, err)
	}
	p, err := s.reg.FetchProposal(ctx, prop)
	if err != nil {
		return nil, fmt.Errorf("fetch proposal %s: %w", id, err)
	}
	return p, nil
}

// withVersion returns the three sides with one version in their
// manifests, since it always differs and is not a change to merge.
func (s sides) withVersion(v semver.Version) (base, ours, theirs map[string][]byte, err error) {
	set := func(p *pack.Pack) (map[string][]byte, error) {
		if p == nil {
			return nil, nil
		}
		q, err := pack.WithVersion(p, v)
		if err != nil {
			return nil, err
		}
		return q.Files(), nil
	}
	if base, err = set(s.base); err != nil {
		return nil, nil, nil, err
	}
	if ours, err = set(s.ours); err != nil {
		return nil, nil, nil, err
	}
	if theirs, err = set(s.theirs); err != nil {
		return nil, nil, nil, err
	}
	return base, ours, theirs, nil
}

// resultVersion keeps the folder's version when it is above the release
// merged onto, else takes the release's next patch.
func resultVersion(ours, onto semver.Version) (semver.Version, bool) {
	if ours.Compare(onto) > 0 {
		return ours, false
	}
	return onto.Bump(semver.Patch), true
}

func labels(o Origin) Labels {
	l := Labels{Base: "base " + o.Base, Ours: "yours", Theirs: "release " + o.Latest}
	if o.Base == "" {
		l.Base = "base (none)"
	}
	switch {
	case o.Replaces != "":
		l.Ours = "yours (proposal " + o.Replaces + ")"
	case o.Start != "":
		l.Ours = "proposal " + o.Start
	}
	return l
}

// side is one version of a file; ok is false where it does not exist.
type side struct {
	data []byte
	ok   bool
}

func (a side) same(b side) bool { return a.ok == b.ok && bytes.Equal(a.data, b.data) }

func sideOf(files map[string][]byte, p string) side {
	data, ok := files[p]
	return side{data: data, ok: ok}
}

// mergeFiles merges every path of the three sides and returns the result
// and what happened to each path that differs anywhere.
func mergeFiles(base, ours, theirs map[string][]byte, l Labels) (map[string][]byte, []File) {
	paths := map[string]bool{}
	for _, m := range []map[string][]byte{base, ours, theirs} {
		for p := range m {
			paths[p] = true
		}
	}
	out := map[string][]byte{}
	var files []File
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		got, f := mergePath(sideOf(base, p), sideOf(ours, p), sideOf(theirs, p), l)
		if got.ok {
			out[p] = got.data
		}
		if f.Change != 0 {
			f.Path = p
			files = append(files, f)
		}
	}
	return out, files
}

// mergePath merges one file, a table of cases: a side that did not change
// takes the other; the same change on both sides applies once; changes on
// both are merged line by line, conflicts marked; a file one side deleted
// and the other changed is kept, as a conflict.
func mergePath(b, o, t side, l Labels) (side, File) {
	switch {
	case o.same(t):
		return o, File{}
	case o.same(b):
		if !t.ok {
			return t, File{Change: Deleted}
		}
		return t, File{Change: FromRelease}
	case t.same(b) && !o.ok:
		return o, File{Change: YouDeleted}
	case t.same(b):
		return o, File{Change: Yours}
	case !o.ok:
		return t, File{Change: Conflict, Conflicts: 1, Undecided: true, Note: "you deleted it and the release changed it; it is back as released"}
	case !t.ok:
		return o, File{Change: Conflict, Conflicts: 1, Undecided: true, Note: "the release deleted it and you changed it; it is kept as yours"}
	}
	text, n, err := textdiff.Merge3(string(b.data), string(o.data), string(t.data), textdiff.Labels(l))
	if errors.Is(err, textdiff.ErrTooLarge) {
		return side{data: textdiff.Whole(b.data, o.data, t.data, textdiff.Labels(l)), ok: true}, File{Change: Conflict, Conflicts: 1, Note: "too long to merge line by line, so both versions are in the file whole"}
	}
	if n > 0 {
		return side{data: []byte(text), ok: true}, File{Change: Conflict, Conflicts: n}
	}
	return side{data: []byte(text), ok: true}, File{Change: Merged}
}

// check makes sure every path is safe to write and, when nothing
// conflicts, that the result is a valid package.
func check(merged map[string][]byte, clean bool) error {
	for p := range merged {
		if err := pack.CheckPath(p); err != nil {
			return fmt.Errorf("merged files: %w", err)
		}
	}
	if !clean {
		return nil
	}
	if _, err := pack.FromFiles(merged); err != nil {
		return fmt.Errorf("the merged files are not a valid package, so nothing was written: %w", err)
	}
	return nil
}

// write makes dir hold merged: it writes each file that differs from
// current through os.Root, atomically, and deletes package files merged
// away. Hidden files are never package files, so they are never touched.
// A failure names the files already written.
func write(dir string, current, merged map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer root.Close()
	var done []string
	fail := func(err error) error {
		if len(done) == 0 {
			return fmt.Errorf("nothing was written: %w", err)
		}
		return fmt.Errorf("after writing %s: %w; the other files are as they were", strings.Join(done, ", "), err)
	}
	for _, p := range slices.Sorted(maps.Keys(merged)) {
		if old, ok := current[p]; ok && bytes.Equal(old, merged[p]) {
			continue
		}
		if d := path.Dir(p); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return fail(fmt.Errorf("create %s: %w", d, err))
			}
		}
		if err := atomicfile.WriteIn(root, p, merged[p], 0o644); err != nil {
			return fail(fmt.Errorf("write %s: %w", p, err))
		}
		done = append(done, p)
	}
	for _, p := range slices.Sorted(maps.Keys(current)) {
		if _, ok := merged[p]; ok {
			continue
		}
		if err := root.Remove(p); err != nil {
			return fail(fmt.Errorf("delete %s: %w", p, err))
		}
		done = append(done, p+" (deleted)")
	}
	return nil
}

// Pending is what a rebase left for the author to finish.
type Pending struct {
	// Markers are the conflicted files that still hold loadout's markers.
	Markers []string
	// Undecided are the files one side deleted and the other changed that
	// are still there and not marked resolved.
	Undecided []string
}

// None reports whether nothing is left to finish.
func (p Pending) None() bool { return len(p.Markers) == 0 && len(p.Undecided) == 0 }

// Unresolved returns what dir's rebase left unfinished, so a folder is not
// proposed or published half resolved. A file deleted on one side and
// changed on the other is decided once the author deletes it or marks it
// resolved.
func (s *Service) Unresolved(dir string) (Pending, error) {
	rec, ok, err := s.recordAny(dir)
	if err != nil || !ok {
		return Pending{}, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Pending{}, fmt.Errorf("open %s: %w", dir, err)
	}
	defer root.Close()
	var out Pending
	for _, p := range rec.Conflicts {
		data, err := root.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Pending{}, fmt.Errorf("read %s: %w", p, err)
		}
		if slices.Contains(rec.Undecided, p) {
			out.Undecided = append(out.Undecided, p)
			continue
		}
		if textdiff.Marked(data, textdiff.Labels(rec.Labels)) {
			out.Markers = append(out.Markers, p)
		}
	}
	return out, nil
}

// finished refuses to rebase a folder whose last rebase left conflicts
// that are not resolved yet. A folder that is not there yet has none.
func (s *Service) finished(dir string, p *pack.Pack) error {
	if p == nil {
		return nil
	}
	pending, err := s.Unresolved(dir)
	if err != nil || pending.None() {
		return err
	}
	var left []string
	if len(pending.Markers) > 0 {
		left = append(left, "conflict markers in "+strings.Join(pending.Markers, ", "))
	}
	if len(pending.Undecided) > 0 {
		left = append(left, "undecided "+strings.Join(pending.Undecided, ", ")+" (delete, or keep with --resolved)")
	}
	return fmt.Errorf("the last loadout rebase of this folder is not finished: %s. Finish it first, then rebase again", strings.Join(left, "; "))
}

// MarkResolved keeps files that one side deleted and the other changed,
// as the author decided. A file that is not undecided is an error, so a
// typo does not pass silently.
func (s *Service) MarkResolved(dir string, files []string) error {
	rec, ok, err := s.recordAny(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		f = filepath.ToSlash(filepath.Clean(f))
		i := slices.Index(rec.Undecided, f)
		if !ok || i < 0 {
			return fmt.Errorf("%s has no undecided conflict from loadout rebase; only a file deleted on one side and changed on the other is marked resolved", f)
		}
		rec.Undecided = slices.Delete(rec.Undecided, i, i+1)
	}
	return s.records.Put(dir, rec)
}

// Done forgets dir's rebase record, once the registry accepted what the
// folder became.
func (s *Service) Done(dir string) error {
	if s.records == nil {
		return nil
	}
	return s.records.Delete(dir)
}

// recordAny returns dir's rebase record for this registry, whatever its package.
func (s *Service) recordAny(dir string) (Record, bool, error) {
	if s.records == nil {
		return Record{}, false, nil
	}
	rec, ok, err := s.records.Get(dir)
	if err != nil || !ok || rec.Remote != s.remote {
		return Record{}, false, err
	}
	return rec, true, nil
}
