package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
)

// reader is what footprint reads through: *sql.DB, or the *sql.Tx a delete
// runs in.
type reader interface {
	queryer
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// PackageFootprint implements registry.Store.
func (s *Store) PackageFootprint(ctx context.Context, id pack.ID) (registry.PackageFootprint, error) {
	fp, err := footprint(ctx, s.db, id)
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("package footprint %s: %w", id, err)
	}
	return fp, nil
}

// DeletePackage implements registry.Store.
func (s *Store) DeletePackage(ctx context.Context, id pack.ID, at time.Time) (_ registry.PackageFootprint, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("delete package %s: %w", id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	fp, err := deletePackageTx(ctx, tx, id, at)
	if err != nil {
		return registry.PackageFootprint{}, err
	}
	if err := tx.Commit(); err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("delete package %s: commit: %w", id, err)
	}
	return fp, nil
}

// deletePackageTx removes the package inside tx and returns what it
// removed. Reviews and prompts go before their proposals because foreign
// keys are on. seq is AUTOINCREMENT, so a later publish under the same name
// never reuses a deleted seq and clients of Changes never see it step back.
// Open requests to delete it close as gone at at; the closed ones are the
// record and stay.
func deletePackageTx(ctx context.Context, tx *sql.Tx, id pack.ID, at time.Time) (registry.PackageFootprint, error) {
	fp, err := footprint(ctx, tx, id)
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("delete package %s: %w", id, err)
	}
	team, name := id.Team(), id.Name()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM proposal_prompts WHERE proposal_id IN (SELECT id FROM proposals WHERE team = ? AND name = ?)`, []any{team, name}},
		{`DELETE FROM proposal_reviews WHERE proposal_id IN (SELECT id FROM proposals WHERE team = ? AND name = ?)`, []any{team, name}},
		{`DELETE FROM proposals WHERE team = ? AND name = ?`, []any{team, name}},
		{`DELETE FROM releases WHERE team = ? AND name = ?`, []any{team, name}},
		{`UPDATE namespaces SET course = '' WHERE course = ?`, []any{courseText(id)}},
		{`UPDATE deletions SET status = 'gone', decided_at = ? WHERE team = ? AND name = ? AND status = 'open'`, []any{formatTime(at), team, name}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return registry.PackageFootprint{}, fmt.Errorf("delete package %s: %w", id, err)
		}
	}
	return fp, nil
}

// footprint reads what the database keeps of id, or ErrNotFound when there
// is no release and no proposal.
func footprint(ctx context.Context, q reader, id pack.ID) (registry.PackageFootprint, error) {
	fp := registry.PackageFootprint{ID: id}
	team, name := id.Team(), id.Name()

	rows, err := q.QueryContext(ctx, `
		SELECT version, kind FROM releases WHERE team = ? AND name = ? ORDER BY major, minor, patch`, team, name)
	if err != nil {
		return fp, err
	}
	defer rows.Close()
	for rows.Next() {
		var v, kind string
		if err := rows.Scan(&v, &kind); err != nil {
			return fp, err
		}
		ver, err := semver.Parse(v)
		if err != nil {
			return fp, fmt.Errorf("stored version %q: %w", v, err)
		}
		fp.Releases = append(fp.Releases, ver)
		fp.Kind = pack.Kind(kind) // the last row is the newest release
	}
	if err := rows.Err(); err != nil {
		return fp, err
	}

	if fp.Open, err = column(ctx, q, `
		SELECT id FROM proposals WHERE team = ? AND name = ? AND status = 'open' ORDER BY created_at, rowid`, team, name); err != nil {
		return fp, err
	}
	if fp.Courses, err = column(ctx, q, `SELECT name FROM namespaces WHERE course = ? ORDER BY name`, courseText(id)); err != nil {
		return fp, err
	}
	if fp.Deletions, err = column(ctx, q, `
		SELECT id FROM deletions WHERE team = ? AND name = ? AND status = 'open' ORDER BY created_at, rowid`, team, name); err != nil {
		return fp, err
	}
	if err := q.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM proposals WHERE team = ? AND name = ?),
			(SELECT count(*) FROM proposal_reviews WHERE proposal_id IN (SELECT id FROM proposals WHERE team = ? AND name = ?)),
			(SELECT count(*) FROM proposal_prompts WHERE proposal_id IN (SELECT id FROM proposals WHERE team = ? AND name = ?))`,
		team, name, team, name, team, name).Scan(&fp.Proposals, &fp.Reviews, &fp.Prompts); err != nil {
		return fp, err
	}
	if len(fp.Releases) == 0 && fp.Proposals == 0 {
		return registry.PackageFootprint{}, fmt.Errorf("%w: no release or proposal", registry.ErrNotFound)
	}
	return fp, nil
}

// column reads one text column of every row.
func column(ctx context.Context, q reader, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
