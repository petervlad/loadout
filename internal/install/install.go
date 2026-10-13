// Package install puts packages onto disk for each AI tool and reports on
// what is installed.
//
// Install renders every file first, then checks every target path, and only
// writes when nothing would be lost. A file loadout did not write, or one
// someone edited since, is never overwritten without Options.Force.
package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

// Renderer writes packages in one AI tool's format.
type Renderer = target.Renderer

// LockStore reads the install lock and changes it under an exclusive lock.
type LockStore interface {
	Load() (*lock.Lock, error)
	Update(ctx context.Context, fn func(*lock.Lock) error) error
}

var _ LockStore = (*lock.Store)(nil)

// ErrUnknownTarget is returned for a --target no adapter handles.
var ErrUnknownTarget = errors.New("unknown target")

// Installer installs packages and reports their status.
type Installer struct {
	renderers []Renderer
	locks     LockStore
	log       *slog.Logger
}

// New returns an Installer that writes with the given renderers, in order.
func New(locks LockStore, log *slog.Logger, renderers ...Renderer) *Installer {
	return &Installer{renderers: slices.Clone(renderers), locks: locks, log: log}
}

// Targets returns the names of the targets this installer can write.
func (in *Installer) Targets() []string {
	names := make([]string, len(in.renderers))
	for i, r := range in.renderers {
		names[i] = r.Name()
	}
	return names
}

// Renderer returns the renderer of target name.
func (in *Installer) Renderer(name string) (Renderer, bool) {
	i := slices.IndexFunc(in.renderers, func(r Renderer) bool { return r.Name() == name })
	if i < 0 {
		return nil, false
	}
	return in.renderers[i], true
}

// Options control one install.
type Options struct {
	Scope target.Scope
	// Targets limits the install to these tools. Empty means the package's
	// own targets, or every available target if the package names none.
	Targets []string
	// Source records where the package came from, for status and updates.
	Source string
	// Remote names the registry it came from; empty for a folder.
	Remote string
	// Policy sets the update policy. Empty keeps the current one, or uses
	// lock.DefaultPolicy for a first install.
	Policy lock.Policy
	// Force overwrites files loadout did not write or that were edited. It
	// never takes a file over from another package of the same layer.
	Force bool
	// Adopt moves what stands in the way (files and links an older setup
	// wrote, and same-named copies in ~/.agents that Copilot also reads)
	// into ~/.loadout/backup instead of failing. Nothing is deleted. Files
	// loadout wrote and you edited still need Force.
	Adopt bool
	// Depth is how far the package's namespace is from the installing
	// token's own: 0 for its own namespace and for folders, 1 for the
	// parent, and so on. When another package already owns a file this
	// package renders, the closer one (lower depth) keeps it: a farther
	// package's file is taken over, a closer package's file shadows this
	// one. At the same depth it is a conflict.
	Depth int
	// Models is the installing team's resolved model table; the empty
	// table renders the built-in defaults.
	Models tier.Table
	// Requires holds the release of each package p.Needs() names, at its
	// major, resolved by the caller (the template of a pipeline binding).
	Requires []*pack.Pack
	// Agents holds the definitions of the agents p.Needs() names, so a
	// conductor can be rendered for what they can do. A missing one is not
	// an error here.
	Agents []agent.Definition
}

// Result describes a finished install.
type Result struct {
	Entry lock.Entry
	// Unchanged is true when the same version was already installed intact
	// and no file was written.
	Unchanged bool
	// TookOver lists packages from farther layers whose files this install
	// replaced; their lock entries now record those files as shadowed.
	TookOver []pack.ID
	// Warnings are things the person should know about what was written:
	// a tool that cannot do what the package asks, or an agent the package
	// needs that is not installed in the scope.
	Warnings []string
}

// ShadowedBy returns the closer packages that own files this install did
// not write, in first-seen order. When Entry.FullyShadowed, nothing was
// written at all.
func (r Result) ShadowedBy() []pack.ID {
	var out []pack.ID
	for _, s := range r.Entry.Shadowed {
		if !slices.Contains(out, s.By) {
			out = append(out, s.By)
		}
	}
	return out
}

// renderedFile is one file to write, for the targets that read it, in the
// unit (agent file or skill folder) it belongs to.
type renderedFile struct {
	target string
	unit   string
	file   target.File
}

// Install writes p into opts.Scope for each selected target and records it
// in the lock.
func (in *Installer) Install(ctx context.Context, p *pack.Pack, opts Options) (Result, error) {
	in.log.Debug("install requested", "id", p.ID(), "version", p.Version(), "scope", opts.Scope, "targets", opts.Targets, "source", opts.Source)
	if err := checkScope(opts.Scope); err != nil {
		return Result{}, err
	}
	if opts.Policy != "" && !opts.Policy.Valid() {
		return Result{}, fmt.Errorf("unknown policy %q: want auto, notify or pin", opts.Policy)
	}
	if ok, why := p.Standalone(); !ok {
		return Result{}, fmt.Errorf("%s: %s: %w", p.ID(), why, target.ErrNotStandalone)
	}
	renderers, err := in.selectRenderers(p.Manifest().Targets, opts.Targets)
	if err != nil {
		return Result{}, err
	}

	stamp := target.Stamp{ID: p.ID(), Version: p.Version(), Digest: p.Digest()}
	rendered, warnings, err := render(renderers, p, opts.Scope, stamp, opts)
	if err != nil {
		return Result{}, err
	}

	if info, err := os.Stat(opts.Scope.Root); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("scope root %s is not a folder", opts.Scope.Root)
	}
	files := tree{root: opts.Scope.Root}

	// Everything from reading the lock to saving it happens under the lock,
	// so concurrent loadout commands never lose each other's installs.
	var res Result
	err = in.locks.Update(ctx, func(l *lock.Lock) error {
		var err error
		res, err = in.installLocked(ctx, l, files, p, opts, rendered)
		res.Warnings = slices.Concat(warnings, missingAgents(l, p, opts.Scope))
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func (in *Installer) installLocked(ctx context.Context, l *lock.Lock, files tree, p *pack.Pack, opts Options, rendered []renderedFile) (Result, error) {
	prev, hadPrev := l.Find(p.ID(), opts.Scope)
	policy := opts.Policy
	if policy == "" {
		policy = lock.DefaultPolicy
		if hadPrev {
			policy = prev.Policy
		}
	}
	write, shadows := splitShadowed(l, files.root, p.ID(), opts.Depth, rendered)
	entry := lock.Entry{
		ID:       p.ID(),
		Version:  p.Version(),
		Digest:   p.Digest(),
		Source:   opts.Source,
		Remote:   opts.Remote,
		Policy:   policy,
		Scope:    opts.Scope,
		Depth:    opts.Depth,
		Files:    records(write),
		Shadowed: shadows,
		Models:   opts.Models.Digest(),
		Requires: requirements(opts.Requires),
	}

	// Same files with the same content: a new model table that does not
	// touch this package (a skill, an agent without a hint) only updates
	// the lock entry.
	if hadPrev && prev.Digest == p.Digest() && sameFiles(prev.Files, entry.Files) && slices.Equal(prev.Shadowed, entry.Shadowed) && intact(files, prev.Files) {
		in.log.Info("already installed", "id", entry.ID, "version", entry.Version, "scope", entry.Scope, "policy", entry.Policy)
		if prev.Policy != entry.Policy || prev.Source != entry.Source || prev.Remote != entry.Remote || prev.Depth != entry.Depth || prev.Models != entry.Models || !slices.Equal(prev.Requires, entry.Requires) {
			l.Put(entry)
		}
		return Result{Entry: entry, Unchanged: true}, nil
	}

	dropped := unitLeftovers(l, files.root, p.ID(), entry.Files)
	if hadPrev {
		dropped = append(dropped, stale(prev.Files, entry)...)
	}
	conflicts, err := findConflicts(files, l, p.ID(), opts.Depth, write, dropped)
	if err != nil {
		return Result{}, err
	}
	if opts.Adopt {
		if conflicts, err = in.adopt(files, write, conflicts); err != nil {
			return Result{}, err
		}
	}
	if len(conflicts) > 0 && (!opts.Force || slices.ContainsFunc(conflicts, Conflict.blocking)) {
		return Result{}, &ConflictError{Conflicts: conflicts}
	}

	// Removing first makes room when a file becomes a folder or back.
	for _, f := range dropped {
		in.removeFile(files, f)
	}
	tookOver := takeOver(l, files.root, p.ID(), entry.Files)
	for _, rf := range write {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if err := files.write(rf.file.Path, rf.file.Data); err != nil {
			return Result{}, fmt.Errorf("write %s: %w", files.abs(rf.file.Path), err)
		}
		in.log.Debug("wrote file", "id", p.ID(), "target", rf.target, "path", rf.file.Path)
	}
	l.Put(entry)
	for _, s := range entry.Shadowed {
		in.log.Info("file shadowed by a closer layer", "id", entry.ID, "path", s.Path, "by", s.By)
	}
	for _, id := range tookOver {
		in.log.Info("took files over from a farther layer", "id", entry.ID, "from", id)
	}
	in.log.Info("installed", "id", entry.ID, "version", entry.Version, "digest", entry.Digest.Short(),
		"scope", entry.Scope, "policy", entry.Policy, "depth", entry.Depth, "files", len(entry.Files),
		"shadowed", len(entry.Shadowed), "forced", opts.Force && len(conflicts) > 0)
	return Result{Entry: entry, TookOver: tookOver}, nil
}

// render renders p for every renderer. A file two tools read at the same
// path (a skill in a repository) is written once, for both.
func render(renderers []Renderer, p *pack.Pack, scope target.Scope, stamp target.Stamp, opts Options) ([]renderedFile, []string, error) {
	var out []renderedFile
	var warnings []string
	index := map[string]int{}
	for _, r := range renderers {
		u, err := target.Render(r, p, scope, stamp, target.RenderOptions{Models: opts.Models, Requires: opts.Requires, Agents: opts.Agents})
		if err != nil {
			return nil, nil, fmt.Errorf("render %s for %s: %w", p.ID(), r.Name(), err)
		}
		warnings = append(warnings, u.Warnings...)
		for _, f := range u.Files {
			if i, dup := index[f.Path]; dup {
				if !bytes.Equal(out[i].file.Data, f.Data) {
					return nil, nil, fmt.Errorf("render %s: %s and %s write different content to %s", p.ID(), out[i].target, r.Name(), f.Path)
				}
				out[i].target += "," + r.Name()
				continue
			}
			index[f.Path] = len(out)
			out = append(out, renderedFile{target: r.Name(), unit: u.Root, file: f})
		}
	}
	return out, warnings, nil
}

// requirements records the releases a package was rendered from.
func requirements(required []*pack.Pack) []lock.Requirement {
	var out []lock.Requirement
	for _, r := range required {
		out = append(out, lock.Requirement{ID: r.ID(), Version: r.Version(), Digest: r.Digest()})
	}
	return out
}

// missingAgents warns about each agent p needs that no entry of that name
// records in scope. It reads the lock only, so it knows no kind.
func missingAgents(l *lock.Lock, p *pack.Pack, scope target.Scope) []string {
	var out []string
	for _, name := range p.Needs().Agents {
		if !slices.ContainsFunc(l.Entries(), func(e lock.Entry) bool { return e.Scope == scope && e.ID.Name() == name }) {
			out = append(out, fmt.Sprintf("%s needs the agent %s, which is not installed here. Install it with loadout install, or loadout install --all.", p.ID(), name))
		}
	}
	return out
}

// splitShadowed separates the files this install writes from those a
// package of a closer layer (lower depth) already owns. It decides per
// unit: if a closer package owns any file of a skill folder, the whole
// folder is theirs, so a skill is never a mix of two packages.
func splitShadowed(l *lock.Lock, root string, id pack.ID, depth int, rendered []renderedFile) ([]renderedFile, []lock.Shadow) {
	shadowedBy := map[string]pack.ID{}
	for _, rf := range rendered {
		owner, owned := l.Owner(root, rf.file.Path)
		if owned && owner.ID != id && owner.Depth < depth {
			if _, seen := shadowedBy[rf.unit]; !seen {
				shadowedBy[rf.unit] = owner.ID
			}
		}
	}
	var write []renderedFile
	var shadows []lock.Shadow
	for _, rf := range rendered {
		if by, ok := shadowedBy[rf.unit]; ok {
			sh := lock.Shadow{Target: rf.target, Path: rf.file.Path, By: by}
			if rf.unit != rf.file.Path {
				sh.Unit = rf.unit
			}
			shadows = append(shadows, sh)
			continue
		}
		write = append(write, rf)
	}
	return write, shadows
}

// takeOver moves the written paths, and every file in a skill folder this
// install writes, out of every other entry under root into that entry's
// shadowed list, and returns the packages it took them from.
// findConflicts has already made sure those are farther layers.
func takeOver(l *lock.Lock, root string, id pack.ID, written []lock.FileRecord) []pack.ID {
	var from []pack.ID
	for _, e := range l.Entries() {
		if e.Scope.Root != root || e.ID == id {
			continue
		}
		kept := e.Files[:0]
		for _, f := range e.Files {
			if replaces(written, f.Path) || inUnit(written, f.Path) {
				e.Shadowed = append(e.Shadowed, lock.Shadow{Target: f.Target, Path: f.Path, By: id, Unit: f.Unit})
				continue
			}
			kept = append(kept, f)
		}
		if len(kept) == len(e.Files) {
			continue
		}
		e.Files = kept
		l.Put(e)
		from = append(from, e.ID)
	}
	return from
}

// unitLeftovers returns the files other packages under root have in the
// skill folders this install writes, at paths it does not write. They are
// removed, unless edited, so a folder holds only one package's skill.
func unitLeftovers(l *lock.Lock, root string, id pack.ID, written []lock.FileRecord) []lock.FileRecord {
	var out []lock.FileRecord
	for _, e := range l.Entries() {
		if e.Scope.Root != root || e.ID == id {
			continue
		}
		for _, f := range e.Files {
			if !replaces(written, f.Path) && inUnit(written, f.Path) {
				out = append(out, f)
			}
		}
	}
	return out
}

func replaces(written []lock.FileRecord, p string) bool {
	return slices.ContainsFunc(written, func(w lock.FileRecord) bool { return w.Path == p })
}

func inUnit(written []lock.FileRecord, p string) bool {
	return slices.ContainsFunc(written, func(w lock.FileRecord) bool { return w.Unit != "" && strings.HasPrefix(p, w.Unit+"/") })
}

// selectRenderers resolves which targets to write, in installer order.
func (in *Installer) selectRenderers(supported, requested []string) ([]Renderer, error) {
	names := requested
	if len(names) == 0 {
		names = supported
	}
	if len(names) == 0 {
		return slices.Clone(in.renderers), nil
	}
	var out []Renderer
	for _, r := range in.renderers {
		if slices.Contains(names, r.Name()) {
			out = append(out, r)
		}
	}
	for _, name := range names {
		if !slices.ContainsFunc(out, func(r Renderer) bool { return r.Name() == name }) {
			return nil, fmt.Errorf("%w %q: available targets are %s", ErrUnknownTarget, name, strings.Join(in.Targets(), ", "))
		}
		if len(supported) > 0 && !slices.Contains(supported, name) {
			return nil, fmt.Errorf("package does not support target %q (it supports %s)", name, strings.Join(supported, ", "))
		}
	}
	return out, nil
}

// stale returns the files an earlier version wrote that the new one does
// not, and that no other package now owns.
func stale(old []lock.FileRecord, current lock.Entry) []lock.FileRecord {
	var out []lock.FileRecord
	for _, f := range old {
		if slices.ContainsFunc(current.Files, func(c lock.FileRecord) bool { return c.Path == f.Path }) ||
			slices.ContainsFunc(current.Shadowed, func(s lock.Shadow) bool { return s.Path == f.Path }) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// removeFile deletes a file loadout wrote unless someone edited it, then
// the folders of its skill that are left empty.
func (in *Installer) removeFile(files tree, f lock.FileRecord) {
	if files.hash(f.Path) != f.SHA256 {
		in.log.Warn("kept a file loadout no longer manages because it was edited", "path", f.Path)
		return
	}
	if err := files.remove(f.Path); err != nil {
		in.log.Warn("could not remove old file", "path", f.Path, "err", err)
		return
	}
	files.prune(f.Path, f.Unit)
}

func checkScope(s target.Scope) error {
	if !s.Kind.Valid() {
		return fmt.Errorf("unknown scope %q: want user or project", s.Kind)
	}
	if !filepath.IsAbs(s.Root) {
		return fmt.Errorf("scope root %q is not an absolute path", s.Root)
	}
	return nil
}

func records(rendered []renderedFile) []lock.FileRecord {
	out := make([]lock.FileRecord, len(rendered))
	for i, rf := range rendered {
		out[i] = lock.FileRecord{Target: rf.target, Path: rf.file.Path, SHA256: hashBytes(rf.file.Data)}
		if rf.unit != rf.file.Path {
			out[i].Unit = rf.unit
		}
	}
	return out
}

// sameFiles reports whether two installs wrote the same files with the
// same content.
func sameFiles(a, b []lock.FileRecord) bool {
	return slices.EqualFunc(a, b, func(x, y lock.FileRecord) bool {
		return x.Target == y.Target && x.Path == y.Path && x.SHA256 == y.SHA256
	})
}

func intact(files tree, recs []lock.FileRecord) bool {
	for _, f := range recs {
		if files.hash(f.Path) != f.SHA256 {
			return false
		}
	}
	return true
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Rerenders reports whether installing p with opts again would change a
// file the install of p in opts.Scope wrote: sync asks it when the model
// table changed, before telling anyone about it. Files a closer layer owns
// are not counted. It is false when p is not installed there.
func (in *Installer) Rerenders(_ context.Context, p *pack.Pack, opts Options) (bool, error) {
	renderers, err := in.selectRenderers(p.Manifest().Targets, opts.Targets)
	if err != nil {
		return false, err
	}
	stamp := target.Stamp{ID: p.ID(), Version: p.Version(), Digest: p.Digest()}
	rendered, _, err := render(renderers, p, opts.Scope, stamp, opts)
	if err != nil {
		return false, err
	}
	l, err := in.locks.Load()
	if err != nil {
		return false, err
	}
	prev, ok := l.Find(p.ID(), opts.Scope)
	if !ok {
		return false, nil
	}
	for _, rec := range records(rendered) {
		i := slices.IndexFunc(prev.Files, func(f lock.FileRecord) bool { return f.Path == rec.Path })
		if i >= 0 && prev.Files[i].SHA256 != rec.SHA256 {
			return true, nil
		}
	}
	return false, nil
}

// ErrNotInstalled is returned when an operation names a package that is not
// in the lock.
var ErrNotInstalled = errors.New("not installed")

// SetPolicy changes the update policy of the installs of id, in every scope
// or only in scopes of kind (when kind is not empty). It returns how many
// installs changed.
func (in *Installer) SetPolicy(ctx context.Context, id pack.ID, kind target.ScopeKind, policy lock.Policy) (int, error) {
	if !policy.Valid() {
		return 0, fmt.Errorf("unknown policy %q: want auto, notify or pin", policy)
	}
	n := 0
	err := in.locks.Update(ctx, func(l *lock.Lock) error {
		for _, e := range l.Entries() {
			if e.ID != id || (kind != "" && e.Scope.Kind != kind) {
				continue
			}
			e.Policy = policy
			l.Put(e)
			n++
		}
		if n == 0 {
			return fmt.Errorf("%s: %w", id, ErrNotInstalled)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	in.log.Info("policy changed", "id", id, "policy", policy, "installs", n)
	return n, nil
}

// Removed describes one uninstalled scope.
type Removed struct {
	Scope target.Scope
	// Kept lists files that were edited and therefore left in place.
	Kept []string
	// Unshadowed lists packages of farther layers that this one shadowed.
	// Their version of the files is not on disk; installing them again
	// restores it.
	Unshadowed []pack.ID
}

// Uninstall removes id from every scope, or only scopes of kind. Files that
// were edited since loadout wrote them are kept unless force is set.
func (in *Installer) Uninstall(ctx context.Context, id pack.ID, kind target.ScopeKind, force bool) ([]Removed, error) {
	var out []Removed
	err := in.locks.Update(ctx, func(l *lock.Lock) error {
		for _, e := range l.Entries() {
			if e.ID != id || (kind != "" && e.Scope.Kind != kind) {
				continue
			}
			files := tree{root: e.Scope.Root}
			r := Removed{Scope: e.Scope}
			for _, f := range e.Files {
				if !force && files.hash(f.Path) != f.SHA256 {
					if _, err := files.read(f.Path); err == nil || errors.Is(err, errSymlink) {
						r.Kept = append(r.Kept, files.abs(f.Path))
						continue
					}
				}
				if err := files.remove(f.Path); err != nil {
					return fmt.Errorf("remove %s: %w", files.abs(f.Path), err)
				}
				files.prune(f.Path, f.Unit)
			}
			l.Remove(e.ID, e.Scope)
			r.Unshadowed = shadowedBy(l, e)
			out = append(out, r)
		}
		if len(out) == 0 {
			return fmt.Errorf("%s: %w", id, ErrNotInstalled)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	in.log.Info("uninstalled", "id", id, "scopes", len(out))
	return out, nil
}

// shadowedBy returns the packages in e's scope with files shadowed by e.
func shadowedBy(l *lock.Lock, e lock.Entry) []pack.ID {
	var out []pack.ID
	for _, o := range l.Entries() {
		if o.Scope.Root == e.Scope.Root && slices.ContainsFunc(o.Shadowed, func(s lock.Shadow) bool { return s.By == e.ID }) {
			out = append(out, o.ID)
		}
	}
	return out
}

// Entries returns every install recorded in the lock.
func (in *Installer) Entries(_ context.Context) ([]lock.Entry, error) {
	l, err := in.locks.Load()
	if err != nil {
		return nil, err
	}
	return l.Entries(), nil
}
