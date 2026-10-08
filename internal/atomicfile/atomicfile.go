// Package atomicfile replaces files so a reader or a crash sees either the old
// content or the new, never a half-written file.
package atomicfile

import (
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Write replaces the file at path with data and gives it exactly perm. The
// parent folder must exist.
func Write(path string, data []byte, perm fs.FileMode) error {
	r, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer r.Close()
	return WriteIn(r, filepath.Base(path), data, perm)
}

// WriteIn replaces the file name inside r, which confines the write to r: a
// temp file with a random suffix in the same folder is written, synced and
// renamed over name. A symlink in name's place is replaced, never followed.
func WriteIn(r *os.Root, name string, data []byte, perm fs.FileMode) (err error) {
	tmp := filepath.Join(filepath.Dir(name), "."+filepath.Base(name)+"."+rand.Text()[:10]+".tmp")
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = r.Remove(tmp) // best effort; err is the one to report
		}
	}()

	if err := write(f, data, perm); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	return r.Rename(tmp, name)
}

// write fills f and flushes it to disk. Chmod undoes the umask so the file
// gets exactly perm.
func write(f *os.File, data []byte, perm fs.FileMode) error {
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}
