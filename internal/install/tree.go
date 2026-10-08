package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/petervlad/loadout/internal/atomicfile"
)

// errSymlink is returned when an installed file's path holds a symlink, which
// loadout never writes.
var errSymlink = errors.New("is a symlink")

// tree reads and writes installed files under one scope root.
//
// Folders on the way may be symlinks: many people keep ~/.claude or
// ~/.claude/agents in a dotfiles repo. Each operation therefore resolves the
// one folder that holds the file and confines itself to it with os.Root, so a
// file name can never reach outside that folder.
type tree struct {
	root string
}

// abs returns the absolute path of a slash-separated relative path.
func (t tree) abs(rel string) string { return filepath.Join(t.root, filepath.FromSlash(rel)) }

// open returns the resolved folder holding rel, and rel's base name.
func (t tree) open(rel string, create bool) (*os.Root, string, error) {
	p := t.abs(rel)
	dir, base := filepath.Dir(p), filepath.Base(p)
	if create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, "", err
	}
	r, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, "", err
	}
	return r, base, nil
}

// read returns the file's content. A symlink in the file's own place is
// reported as errSymlink, not followed.
func (t tree) read(rel string) ([]byte, error) {
	r, base, err := t.open(rel, false)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	info, err := r.Lstat(base)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s: %w", t.abs(rel), errSymlink)
	}
	return r.ReadFile(base)
}

// write replaces the file atomically: a temp file in the same folder, then a
// rename, so a tool never reads a half-written agent. A symlink in the file's
// place is replaced, never followed.
func (t tree) write(rel string, data []byte) error {
	if link, ok := t.linkedFolder(rel); ok {
		return fmt.Errorf("%s: %w", t.abs(link), errLinkedFolder)
	}
	r, base, err := t.open(rel, true)
	if err != nil {
		return err
	}
	defer r.Close()
	return atomicfile.WriteIn(r, base, data, 0o644)
}

// remove deletes the file; a missing file or folder is not an error. It
// never deletes through a symlinked folder.
func (t tree) remove(rel string) error {
	if link, ok := t.linkedFolder(rel); ok {
		return fmt.Errorf("%s: %w", t.abs(link), errLinkedFolder)
	}
	r, base, err := t.open(rel, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer r.Close()
	if err := r.Remove(base); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// hash returns the hex sha256 of the file, or "" if it cannot be read.
func (t tree) hash(rel string) string {
	data, err := t.read(rel)
	if err != nil {
		return ""
	}
	return hashBytes(data)
}

// prune removes the folders between the file rel and its unit (the skill
// folder, inclusive) that are now empty. Only folders inside the unit are
// touched, never the tool's own folders above it.
func (t tree) prune(rel, unit string) {
	if unit == "" || !strings.HasPrefix(rel, unit+"/") {
		return
	}
	if _, ok := t.linkedFolder(rel); ok {
		return
	}
	for dir := path.Dir(rel); ; dir = path.Dir(dir) {
		// os.Remove only removes an empty folder.
		if err := os.Remove(t.abs(dir)); err != nil {
			return
		}
		if dir == unit {
			return
		}
	}
}

// errLinkedFolder is returned for a write through a symlinked folder.
var errLinkedFolder = errors.New("is a symlink to a folder; loadout never writes through it")

// linkedFolder returns the first folder on rel's way, below the tool's own
// folders (the first two segments, such as .claude/skills, which a dotfiles
// setup may link), that is a symlink. Writing through it would change
// whatever it points at, for example a skill in someone's repository.
func (t tree) linkedFolder(rel string) (string, bool) {
	segs := strings.Split(rel, "/")
	for i := 3; i < len(segs); i++ {
		dir := strings.Join(segs[:i], "/")
		info, err := os.Lstat(t.abs(dir))
		if err != nil {
			return "", false
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return dir, true
		}
	}
	return "", false
}

// exists reports whether something, a link included, is at rel.
func (t tree) exists(rel string) bool {
	_, err := os.Lstat(t.abs(rel))
	return err == nil
}

// statDir reports whether p is a folder.
func statDir(p string) (bool, error) {
	info, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
