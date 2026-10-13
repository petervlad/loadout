package memory

import (
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"
)

// backupStamp is the layout of the stamp in loadout-<stamp>.tar.gz, as
// deploy/loadout-backup.sh writes it (UTC).
const backupStamp = "20060102T150405Z"

// BackupsHolding lists the names of the backups in dir made from..to
// (inclusive), oldest first. A backup made after an entry was saved holds it,
// so from is the entry's creation and to is now. Files that do not look like
// loadout-<stamp>.tar.gz are ignored. dir is opened as a root, so a link
// inside it never leads out.
func BackupsHolding(dir string, from, to time.Time) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open the backup folder %s: %w", dir, err)
	}
	defer root.Close()
	files, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("read the backup folder %s: %w", dir, err)
	}
	var out []string
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		at, ok := backupTime(f.Name())
		if ok && !at.Before(from.UTC().Truncate(time.Second)) && !at.After(to) {
			out = append(out, f.Name())
		}
	}
	slices.Sort(out) // the stamp sorts as time
	return out, nil
}

// backupTime reads the stamp of a backup file name.
func backupTime(name string) (time.Time, bool) {
	stamp, ok := strings.CutPrefix(name, "loadout-")
	if !ok {
		return time.Time{}, false
	}
	stamp, ok = strings.CutSuffix(stamp, ".tar.gz")
	if !ok {
		return time.Time{}, false
	}
	at, err := time.Parse(backupStamp, stamp)
	return at, err == nil
}
