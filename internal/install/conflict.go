package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
)

// Reason says why an existing file blocks an install.
type Reason string

// Why a file blocks an install.
const (
	ReasonUnmanaged    Reason = "not written by loadout"
	ReasonModified     Reason = "edited since loadout wrote it"
	ReasonOtherPackage Reason = "belongs to another package of the same layer"
	ReasonSymlink      Reason = "is a symlink, not written by loadout"
	// ReasonLinkedFolder blocks even with --force: writing would go
	// through the link into whatever it points at, such as a repository.
	ReasonLinkedFolder Reason = "is a symlink to a folder; loadout never writes through it (remove the link, not what it points to, then install again)"
)

// Blocking reports whether a file in the way for this reason stops an
// install even with --force.
func (r Reason) Blocking() bool {
	return r == ReasonOtherPackage || r == ReasonLinkedFolder
}

// blocking reports whether a conflict stops an install even with --force.
func (c Conflict) blocking() bool { return c.Reason.Blocking() }

// Conflict is one file an install would overwrite.
type Conflict struct {
	Path   string
	Reason Reason
	// Owner is set for ReasonOtherPackage.
	Owner pack.ID
}

// ConflictError lists every file that blocks an install.
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	lines := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		lines[i] = fmt.Sprintf("  %s: %s", c.Path, c.Reason)
		if c.Reason == ReasonOtherPackage {
			lines[i] += " " + c.Owner.String()
		}
	}
	hint := "use --force to overwrite"
	if slices.ContainsFunc(e.Conflicts, Conflict.blocking) {
		hint = "--force cannot overwrite all of them"
	}
	return fmt.Sprintf("%d file(s) would be overwritten, %s:\n%s", len(e.Conflicts), hint, strings.Join(lines, "\n"))
}

// findConflicts checks every target path before anything is written. A file
// owned by a package of a farther layer (higher depth than depth) may be
// taken over, but only as that package wrote it. dropped are the files the
// install removes first, which may stand where a folder is needed.
func findConflicts(files tree, l *lock.Lock, id pack.ID, depth int, rendered []renderedFile, dropped []lock.FileRecord) ([]Conflict, error) {
	var conflicts []Conflict
	linked := map[string]bool{}
	for _, rf := range rendered {
		if link, ok := files.linkedFolder(rf.file.Path); ok {
			if !linked[link] {
				linked[link] = true
				conflicts = append(conflicts, Conflict{Path: files.abs(link), Reason: ReasonLinkedFolder})
			}
			continue
		}
		abs := files.abs(rf.file.Path)
		existing, err := files.read(rf.file.Path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if errors.Is(err, errSymlink) {
			conflicts = append(conflicts, Conflict{Path: abs, Reason: ReasonSymlink})
			continue
		}
		if err != nil && slices.ContainsFunc(dropped, func(d lock.FileRecord) bool { return inTheWay(d.Path, rf.file.Path) }) {
			continue // a dropped file stands where a folder goes, or in a folder where a file goes
		}
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", rf.file.Path, err)
		}
		if bytes.Equal(existing, rf.file.Data) {
			continue // already exactly what we would write
		}

		owner, owned := l.Owner(files.root, rf.file.Path)
		switch {
		case !owned:
			conflicts = append(conflicts, Conflict{Path: abs, Reason: ReasonUnmanaged})
		case owner.ID != id && owner.Depth <= depth:
			conflicts = append(conflicts, Conflict{Path: abs, Reason: ReasonOtherPackage, Owner: owner.ID})
		case hashBytes(existing) != recordedHash(owner, rf.file.Path):
			conflicts = append(conflicts, Conflict{Path: abs, Reason: ReasonModified})
		}
	}
	return conflicts, nil
}

// inTheWay reports whether the file a blocks writing b: one is a folder of
// the other.
func inTheWay(a, b string) bool {
	return strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

func recordedHash(e lock.Entry, path string) string {
	for _, f := range e.Files {
		if f.Path == path {
			return f.SHA256
		}
	}
	return ""
}
