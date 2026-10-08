package hook

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
	"github.com/petervlad/loadout/internal/target"
)

// backupSuffix names the copy of a file kept before loadout's first change.
const backupSuffix = ".loadout.bak"

// errChanged means the file changed between loadout's read and its write.
var errChanged = errors.New("the file changed while loadout was changing it (is the tool writing it?); run the command again")

// change applies fn to t's hook file and writes the result, returning the
// bytes before and after (nil for no file). A file that changed meanwhile
// is read and changed once more before giving up.
func (s *Service) change(t Tool, fn func(cur []byte) ([]byte, error)) (before, after []byte, err error) {
	for attempt := 0; ; attempt++ {
		before, after, err = s.changeOnce(t, fn)
		if !errors.Is(err, errChanged) || attempt > 0 {
			return before, after, err
		}
	}
}

func (s *Service) changeOnce(t Tool, fn func(cur []byte) ([]byte, error)) (before, after []byte, err error) {
	root, err := os.OpenRoot(s.home)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	rel := t.HookFile()
	if err := noLinks(root, rel); err != nil {
		return nil, nil, err
	}
	before, err = read(root, rel)
	if err != nil {
		return nil, nil, err
	}
	after, err = fn(before)
	if err != nil {
		return before, nil, err
	}
	if bytes.Equal(before, after) && (before == nil) == (after == nil) {
		return before, after, nil
	}
	if err := s.backup(root, t, rel, before); err != nil {
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
func (s *Service) backup(root *os.Root, t Tool, rel string, before []byte) error {
	if before == nil {
		return nil
	}
	if rest, err := t.WithoutHook(before); err != nil || rest == nil {
		return nil //nolint:nilerr // only loadout's hook in it: nothing of the person's to keep
	}
	name := filepath.FromSlash(rel) + backupSuffix
	if _, err := root.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		return err // already kept, or cannot tell: then do not write
	}
	if err := atomicfile.WriteIn(root, name, before, 0o600); err != nil {
		return fmt.Errorf("back up ~/%s: %w", rel, err)
	}
	s.log.Info("kept a copy before changing a hook file", "file", "~/"+rel+backupSuffix)
	return nil
}

// noLinks refuses a path any part of which is a symbolic link. Dotfiles
// setups link ~/.claude or settings.json into a repository, and renaming a
// new file over the link would replace it.
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

// command is what a tool's hook runs: bare loadout when the PATH finds
// this very binary, else its absolute path, since a tool started from the
// Dock may not see the shell's PATH. The guards keep a missing or older
// loadout silent rather than an error in every session.
func (s *Service) command(tool string) target.HookCommand {
	args := " notices --hook " + tool
	bin := s.binary()
	if bin == "" {
		return target.HookCommand{
			POSIX:      "loadout" + args + " 2>/dev/null || true",
			PowerShell: "if (Get-Command loadout -ErrorAction SilentlyContinue) { loadout" + args + " 2>$null }; exit 0",
			Windows:    s.windows,
		}
	}
	ps := "'" + strings.ReplaceAll(bin, "'", "''") + "'"
	return target.HookCommand{
		POSIX:      "'" + strings.ReplaceAll(bin, "'", `'\''`) + "'" + args + " 2>/dev/null || true",
		PowerShell: "if (Test-Path -LiteralPath " + ps + ") { & " + ps + args + " 2>$null }; exit 0",
		Windows:    s.windows,
	}
}

// binary returns the running loadout's path, or "" when the PATH finds it
// as loadout.
func (s *Service) binary() string {
	exe, err := s.executable()
	if err != nil {
		return ""
	}
	exe = resolved(exe)
	if found, err := s.lookPath("loadout"); err == nil && resolved(found) == exe {
		return ""
	}
	return exe
}

// resolved is p with its symbolic links followed, or p when that fails.
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
