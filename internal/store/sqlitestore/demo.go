package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// demoApplicationID is written into the database header by `loadout demo
// seed` ("ldmo"). Nothing else sets it, so demo mode can tell a folder the
// seed made from one that holds a real registry, with no table of its own.
const demoApplicationID = 0x6c646d6f

// MarkDemo records that this database was made by the demo seed.
func (s *Store) MarkDemo(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d", demoApplicationID)); err != nil {
		return fmt.Errorf("mark demo database: %w", err)
	}
	return nil
}

// DemoMarked reports whether the database at path carries the demo marker.
// It opens the file read-only and never creates or migrates it, so a missing
// file is simply not marked.
func DemoMarked(ctx context.Context, path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("look for the demo marker in %s: %w", path, err)
	}
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("look for the demo marker in %s: %w", abs, err)
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: q.Encode()}
	db, err := sql.Open(driverName, u.String())
	if err != nil {
		return false, fmt.Errorf("look for the demo marker in %s: %w", abs, err)
	}
	defer db.Close()
	var id int64
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); err != nil {
		return false, fmt.Errorf("look for the demo marker in %s: %w", abs, err)
	}
	return id == demoApplicationID, nil
}
