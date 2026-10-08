package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// HasAccounts reports whether the database at path holds any account. It
// opens the file read-only and never creates, migrates or writes anything,
// so `serve --check` can ask on a live registry or a folder that has no
// database yet (then there are no accounts).
func HasAccounts(ctx context.Context, path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("look for accounts in %s: %w", path, err)
	}
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("look for accounts in %s: %w", abs, err)
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: q.Encode()}
	db, err := sql.Open(driverName, u.String())
	if err != nil {
		return false, fmt.Errorf("look for accounts in %s: %w", abs, err)
	}
	defer db.Close()
	var n int
	err = db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&n)
	switch {
	case err == nil:
		return n > 0, nil
	case strings.Contains(err.Error(), "no such table"):
		return false, nil // a database from before accounts, or a fresh file
	}
	return false, fmt.Errorf("look for accounts in %s: %w", abs, err)
}
