package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
)

// State is the health of an installed file.
type State string

// File states, from healthy to worst. Shadowed is healthy: a closer
// layer's package owns the file on purpose.
const (
	StateOK       State = "ok"
	StateShadowed State = "shadowed"
	StateModified State = "modified"
	StateMissing  State = "missing"
)

// FileStatus is the state of one installed file.
type FileStatus struct {
	Target string
	Path   string // absolute
	State  State
	// Unit is the skill folder the file belongs to (absolute); empty for an
	// agent file.
	Unit string
	// ShadowedBy is the closer package that owns the file, for StateShadowed.
	ShadowedBy pack.ID
}

// Status is the state of one installed package.
type Status struct {
	Entry lock.Entry
	Files []FileStatus
}

// State returns the worst state of the package's files.
func (s Status) State() State {
	worst := StateOK
	for _, f := range s.Files {
		switch f.State {
		case StateMissing:
			return StateMissing
		case StateModified:
			worst = StateModified
		}
	}
	return worst
}

// Status reports every installed package and whether its files are intact.
func (in *Installer) Status(ctx context.Context) ([]Status, error) {
	l, err := in.locks.Load()
	if err != nil {
		return nil, err
	}
	entries := l.Entries()
	out := make([]Status, 0, len(entries))
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		st, err := entryStatus(l, e)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func entryStatus(l *lock.Lock, e lock.Entry) (Status, error) {
	st := Status{Entry: e, Files: make([]FileStatus, len(e.Files), len(e.Files)+len(e.Shadowed))}
	files := tree{root: e.Scope.Root}
	for i, f := range e.Files {
		file := FileStatus{Target: f.Target, Path: files.abs(f.Path), State: StateMissing}
		if f.Unit != "" {
			file.Unit = files.abs(f.Unit)
		}
		_, err := files.read(f.Path)
		switch h := files.hash(f.Path); {
		case h == f.SHA256:
			file.State = StateOK
		case err == nil || errors.Is(err, errSymlink):
			file.State = StateModified
		case !errors.Is(err, fs.ErrNotExist):
			return Status{}, fmt.Errorf("check %s: %w", file.Path, err)
		}
		st.Files[i] = file
	}
	// A shadowed file is healthy while its owner still has it; once that
	// package is gone, this entry's version of the file is missing until it
	// is installed again.
	for _, sh := range e.Shadowed {
		file := FileStatus{Target: sh.Target, Path: files.abs(sh.Path), State: StateMissing}
		if sh.Unit != "" {
			file.Unit = files.abs(sh.Unit)
		}
		if owner, ok := l.Owner(e.Scope.Root, sh.Path); ok && owner.ID != e.ID {
			file.State, file.ShadowedBy = StateShadowed, owner.ID
		} else if by, ok := l.Find(sh.By, e.Scope); ok && len(by.Files) > 0 {
			// A skill is shadowed as a whole folder, so the closer skill
			// need not have a file at every path of this one.
			file.State, file.ShadowedBy = StateShadowed, by.ID
		}
		st.Files = append(st.Files, file)
	}
	return st, nil
}

// Edited returns the files of e that exist and differ from what loadout
// wrote, as slash paths relative to the scope root. A missing file is not
// an edit (an install puts it back); a symlink in a file's place is. With
// only not empty, just those paths are checked, which keeps a knowledge
// base of thousands of files cheap to look at again. Shadowed files are
// not e's and never count.
func (in *Installer) Edited(ctx context.Context, e lock.Entry, only []string) ([]string, error) {
	var want map[string]bool
	if len(only) > 0 {
		want = make(map[string]bool, len(only))
		for _, p := range only {
			want[p] = true
		}
	}
	files := tree{root: e.Scope.Root}
	var out []string
	for _, f := range e.Files {
		if want != nil && !want[f.Path] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := files.read(f.Path)
		switch {
		case errors.Is(err, errSymlink):
			out = append(out, f.Path)
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("check %s: %w", files.abs(f.Path), err)
		case hashBytes(data) != f.SHA256:
			out = append(out, f.Path)
		}
	}
	return out, nil
}
