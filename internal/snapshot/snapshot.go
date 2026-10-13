// Package snapshot keeps copies of the registry database in one folder,
// taken before a change nothing undoes.
package snapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Copier writes a consistent copy of a database to a new file.
type Copier interface {
	SnapshotTo(ctx context.Context, path string) error
}

// Dir writes database copies into one folder, named by label and time:
// before-delete-lime-lime-app-dev-20261006T101500Z.db. The label comes first
// so the backup script can prune a kind of copy by its prefix. It is immutable
// after New and safe for concurrent use.
type Dir struct {
	dir string
	db  Copier
	now func() time.Time
}

// New returns a Dir for an existing, absolute folder. A relative path
// would depend on where the server was started, so it is refused.
func New(dir string, db Copier, now func() time.Time) (*Dir, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("the backup folder %q must be an absolute path; set LOADOUT_BACKUP_DIR to one", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("the backup folder is not usable: %w; create it or fix LOADOUT_BACKUP_DIR", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("the backup folder %s is not a folder; fix LOADOUT_BACKUP_DIR", dir)
	}
	if now == nil {
		now = time.Now
	}
	return &Dir{dir: dir, db: db, now: now}, nil
}

// Take writes a copy named after label and returns the file name. Labels
// are letters, digits and hyphens, so a label cannot reach outside the
// folder. A failed copy leaves no partial file behind.
func (d *Dir) Take(ctx context.Context, label string) (string, error) {
	if !validLabel(label) {
		return "", fmt.Errorf("snapshot label %q must be lowercase letters, digits and hyphens", label)
	}
	name := fmt.Sprintf("%s-%s.db", label, d.now().UTC().Format("20060102T150405Z"))
	path := filepath.Join(d.dir, name)
	// Created here, empty and 0600 (the copy holds every token hash), because
	// VACUUM INTO accepts an empty file and would otherwise use the umask.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create the copy %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return "", errors.Join(fmt.Errorf("create the copy %s: %w", name, err), os.Remove(path))
	}
	if err := d.db.SnapshotTo(ctx, path); err != nil {
		return "", errors.Join(err, os.Remove(path))
	}
	return name, nil
}

func validLabel(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
