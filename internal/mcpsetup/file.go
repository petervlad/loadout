package mcpsetup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/petervlad/loadout/internal/atomicfile"
)

// backupSuffix names the copy of a file kept before loadout's first change.
const backupSuffix = ".loadout.bak"

// errChanged means the file changed between loadout's read and its write.
var errChanged = errors.New("the file changed while loadout was changing it (is the tool writing it?); run the command again")

// noLinks refuses a path any part of which is a symbolic link. Dotfiles
// setups link ~/.copilot into a repository, and renaming a new file over
// the link would replace it.
func noLinks(root *os.Root, rel string) error {
	parts := strings.Split(rel, "/")
	for i := range parts {
		p := path.Join(parts[:i+1]...)
		info, err := root.Lstat(filepath.FromSlash(p))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("~/%s is a symbolic link, and loadout never writes through one", p)
		}
	}
	return nil
}

// read returns the file, or nil when there is none.
func read(root *os.Root, rel string) ([]byte, error) {
	data, err := root.ReadFile(filepath.FromSlash(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ~/%s: %w", rel, err)
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

// write replaces the file atomically with data, keeping its permissions,
// or removes it for nil. Folders are made only below the tool's own,
// which exists.
func write(root *os.Root, rel string, data []byte) error {
	name := filepath.FromSlash(rel)
	if data == nil {
		if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	perm := fs.FileMode(0o644)
	if info, err := root.Stat(name); err == nil {
		perm = info.Mode().Perm()
	}
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return atomicfile.WriteIn(root, name, data, perm)
}

// editFile applies fn to the file rel below the home folder and writes the
// result, returning the bytes before and after (nil for no file). A file
// that changed meanwhile is read and changed once more before giving up.
// holdsMore says whether bytes hold anything of the person's, which a
// backup is kept of before the first change.
func editFile(e env, rel string, holdsMore func([]byte) bool, fn func(cur []byte) ([]byte, error)) (before, after []byte, err error) {
	for attempt := 0; ; attempt++ {
		before, after, err = editOnce(e, rel, holdsMore, fn)
		if !errors.Is(err, errChanged) || attempt > 0 {
			return before, after, err
		}
	}
}

func editOnce(e env, rel string, holdsMore func([]byte) bool, fn func(cur []byte) ([]byte, error)) (before, after []byte, err error) {
	root, err := os.OpenRoot(e.home)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	if err := noLinks(root, rel); err != nil {
		return nil, nil, err
	}
	if before, err = read(root, rel); err != nil {
		return nil, nil, err
	}
	if after, err = fn(before); err != nil {
		return before, nil, err
	}
	if bytes.Equal(before, after) && (before == nil) == (after == nil) {
		return before, after, nil
	}
	if err := backup(e, root, rel, before, holdsMore); err != nil {
		return before, after, err
	}
	// The tool may write its file at any moment; read it again just
	// before replacing it, so nothing it wrote meanwhile is lost.
	again, err := read(root, rel)
	if err != nil {
		return before, after, err
	}
	if !bytes.Equal(again, before) || (again == nil) != (before == nil) {
		return before, after, errChanged
	}
	if err := write(root, rel, after); err != nil {
		return before, after, fmt.Errorf("write ~/%s: %w", rel, err)
	}
	return before, after, nil
}

// backup keeps a copy of a file that holds anything of the person's,
// before loadout's first change to it, and never overwrites that copy.
func backup(e env, root *os.Root, rel string, before []byte, holdsMore func([]byte) bool) error {
	if before == nil || !holdsMore(before) {
		return nil
	}
	name := filepath.FromSlash(rel) + backupSuffix
	if _, err := root.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		return err // already kept, or cannot tell: then do not write
	}
	if err := atomicfile.WriteIn(root, name, before, 0o600); err != nil {
		return fmt.Errorf("back up ~/%s: %w", rel, err)
	}
	e.log.Info("kept a copy before changing a tool's file", "file", "~/"+rel+backupSuffix)
	return nil
}
