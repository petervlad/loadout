package sqlitestore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strconv"
	"strings"
)

// ErrSchemaTooNew means the database was migrated by a newer loadout than
// this one; opening it could corrupt data the newer schema relies on.
var ErrSchemaTooNew = errors.New("database schema is newer than this loadout supports")

//go:embed migrations/*.sql
var migrationFiles embed.FS

//go:embed memorymigrations/*.sql
var memoryMigrationFiles embed.FS

// migration is one schema step. Version N is the Nth file, and after it is
// applied PRAGMA user_version is N.
type migration struct {
	version int
	name    string
	sql     string
}

// migrations returns the migrations of one folder in order. Files are named
// NNNN_description.sql and must be numbered 1, 2, 3, ... without gaps.
func migrations(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	out := make([]migration, 0, len(entries))
	for i, e := range entries { // ReadDir sorts by name
		num, _, ok := strings.Cut(e.Name(), "_")
		n, err := strconv.Atoi(num)
		if !ok || err != nil || n != i+1 {
			return nil, fmt.Errorf("migration %s: want name %04d_<description>.sql", e.Name(), i+1)
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: n, name: e.Name(), sql: string(b)})
	}
	return out, nil
}

// migrate applies every migration of a folder newer than the database's
// user_version, each in its own transaction. registry.db and memory.db each
// have their own folder and their own user_version.
func migrate(ctx context.Context, db *sql.DB, log *slog.Logger, fsys fs.FS, dir string) error {
	ms, err := migrations(fsys, dir)
	if err != nil {
		return err
	}
	cur, err := userVersion(ctx, db)
	if err != nil {
		return err
	}
	if cur > len(ms) {
		return fmt.Errorf("%w: database is at version %d, this build knows %d", ErrSchemaTooNew, cur, len(ms))
	}
	for _, m := range ms[cur:] {
		if err := apply(ctx, db, log, m); err != nil {
			return err
		}
	}
	return nil
}

// apply runs one migration in a write transaction. It rereads user_version
// under the write lock so two processes opening the same file at once do
// not apply the same step twice.
func apply(ctx context.Context, db *sql.DB, log *slog.Logger, m migration) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %s: begin: %w", m.name, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	cur, err := userVersion(ctx, tx)
	if err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	if cur >= m.version {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	// PRAGMA does not take bound parameters; version is an int we own.
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
		return fmt.Errorf("migration %s: set user_version: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %s: commit: %w", m.name, err)
	}
	log.DebugContext(ctx, "applied migration", "version", m.version, "name", m.name)
	return nil
}

// queryer is satisfied by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func userVersion(ctx context.Context, q queryer) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("read user_version: %w", err)
	}
	return v, nil
}
