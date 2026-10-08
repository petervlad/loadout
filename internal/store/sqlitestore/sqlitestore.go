// Package sqlitestore is a registry.Store, registry.TokenStore,
// registry.NamespaceStore, registry.ProposalStore, registry.WikiStore and
// account.Store backed by a single SQLite file, for running the registry on a LAN box. It uses the pure Go driver modernc.org/sqlite, so it
// builds with CGO_ENABLED=0.
package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/petervlad/loadout/internal/registry"
)

// driverName is the database/sql name modernc.org/sqlite registers.
const driverName = "sqlite"

// timeFormat stores times as fixed-width UTC text with nanoseconds, so they
// round-trip exactly and sort lexically in time order.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Store is a registry store on one SQLite database. It is safe for
// concurrent use.
type Store struct {
	db  *sql.DB
	log *slog.Logger
}

var (
	_ registry.Store      = (*Store)(nil)
	_ registry.TokenStore = (*Store)(nil)
)

// Open opens (creating if needed) the database at path and migrates it.
// The parent directory is created with mode 0700 and a new database file
// with mode 0600. A nil log discards log output.
func Open(ctx context.Context, path string, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	if err := createFile(abs); err != nil {
		return nil, fmt.Errorf("open store %s: %w", abs, err)
	}
	db, err := sql.Open(driverName, dsn(abs))
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", abs, err)
	}
	// SQLite has one writer at a time. Writers wait for each other through
	// busy_timeout and BEGIN IMMEDIATE (see dsn); WAL lets readers run
	// alongside them. A bounded pool keeps the number of waiting writers sane.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxIdleTime(5 * time.Minute)

	s := &Store{db: db, log: log}
	if err := s.migrate(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("open store %s: %w", abs, err), db.Close())
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

// createFile makes the parent directory (0700) and an empty database file
// (0600) if they do not exist, so SQLite never creates it with the umask
// default. SQLite gives its -wal and -shm files the same mode.
func createFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// dsn builds the modernc.org/sqlite connection string for path. Every new
// connection in the pool runs the pragmas:
//   - busy_timeout: wait up to 5s for the write lock instead of SQLITE_BUSY.
//   - journal_mode(WAL): readers do not block the writer or each other.
//   - synchronous(NORMAL): durable enough under WAL, much faster than FULL.
//   - foreign_keys(ON): enforce references if a later migration adds any.
//
// _txlock=immediate makes every transaction take the write lock at BEGIN,
// so a transaction never has to upgrade a read lock, which is the case
// busy_timeout cannot help with.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Set("_txlock", "immediate")
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}
	return u.String()
}

// isUniqueViolation reports whether err is a SQLite UNIQUE or PRIMARY KEY
// constraint failure. The driver enables extended result codes.
func isUniqueViolation(err error) bool {
	se, ok := errors.AsType[*sqlite.Error](err)
	if !ok {
		return false
	}
	return se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time %q: %w", s, err)
	}
	return t.UTC(), nil
}
