package sqlitestore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
)

// releaseColumns are the columns scanRelease reads, in order.
const releaseColumns = "seq, team, name, version, digest, description, size, published_at, kind, proposal_id, self_approved, " +
	"published_by_token_id, published_by_label, published_by_user_id"

// PutRelease implements registry.Store. Like ApproveProposal it runs in one
// transaction that holds SQLite's write lock from BEGIN (see dsn), so two
// publishes cannot both pass the version check.
func (s *Store) PutRelease(ctx context.Context, rel registry.Release, bundle []byte) (_ registry.Release, err error) {
	major, minor, patch, err := versionParts(rel.Version)
	if err != nil {
		return registry.Release{}, fmt.Errorf("put release %s@%s: %w", rel.ID, rel.Version, err)
	}
	if bundle == nil {
		bundle = []byte{} // the column is NOT NULL; nil would bind as NULL
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.Release{}, fmt.Errorf("put release %s@%s: %w", rel.ID, rel.Version, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if err := checkAboveLatest(ctx, tx, rel.ID, rel.Version); err != nil {
		return registry.Release{}, fmt.Errorf("put release %s@%s: %w", rel.ID, rel.Version, err)
	}
	var seq int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO releases
			(team, name, version, major, minor, patch, digest, description, size, published_at, bundle, kind,
			 published_by_token_id, published_by_label, published_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING seq`,
		rel.ID.Team(), rel.ID.Name(), rel.Version.String(), major, minor, patch,
		string(rel.Digest), rel.Description, rel.Size, formatTime(rel.PublishedAt), bundle, string(rel.Kind),
		rel.PublishedBy.TokenID, rel.PublishedBy.Label, rel.PublishedBy.UserID,
	).Scan(&seq)
	if isUniqueViolation(err) {
		return registry.Release{}, fmt.Errorf("put release %s@%s: %w", rel.ID, rel.Version, registry.ErrVersionConflict)
	}
	if err != nil {
		return registry.Release{}, fmt.Errorf("put release %s@%s: %w", rel.ID, rel.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return registry.Release{}, fmt.Errorf("put release %s@%s: commit: %w", rel.ID, rel.Version, err)
	}
	rel.Seq = seq
	return rel, nil
}

// checkAboveLatest returns ErrVersionConflict unless v is higher than every
// release of id. Run it in the transaction that inserts the release.
func checkAboveLatest(ctx context.Context, q queryer, id pack.ID, v semver.Version) error {
	latest, err := latestRelease(ctx, q, id)
	if err != nil {
		return err
	}
	if lv, ok := latest.Version(); ok && v.Compare(lv) <= 0 {
		return fmt.Errorf("%w: %s@%s is not higher than the latest release %s", registry.ErrVersionConflict, id, v, lv)
	}
	return nil
}

// latestRelease returns the latest release of id, none without one.
func latestRelease(ctx context.Context, q queryer, id pack.ID) (registry.Base, error) {
	var major, minor, patch int
	err := q.QueryRowContext(ctx, `
		SELECT major, minor, patch FROM releases
		WHERE team = ? AND name = ?
		ORDER BY major DESC, minor DESC, patch DESC
		LIMIT 1`, id.Team(), id.Name()).Scan(&major, &minor, &patch)
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Base{}, nil
	}
	if err != nil {
		return registry.Base{}, fmt.Errorf("latest release of %s: %w", id, err)
	}
	return registry.BaseAt(semver.New(major, minor, patch)), nil
}

// Release implements registry.Store.
func (s *Store) Release(ctx context.Context, id pack.ID, v semver.Version) (registry.Release, []byte, error) {
	major, minor, patch, err := versionParts(v)
	if err != nil {
		return registry.Release{}, nil, fmt.Errorf("release %s@%s: %w", id, v, err)
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+releaseColumns+`, bundle FROM releases
		WHERE team = ? AND name = ? AND major = ? AND minor = ? AND patch = ?`,
		id.Team(), id.Name(), major, minor, patch)
	var bundle []byte
	rel, err := scanRelease(row, &bundle)
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Release{}, nil, fmt.Errorf("release %s@%s: %w", id, v, registry.ErrNotFound)
	}
	if err != nil {
		return registry.Release{}, nil, fmt.Errorf("release %s@%s: %w", id, v, err)
	}
	return rel, bytes.Clone(bundle), nil
}

// Releases implements registry.Store.
func (s *Store) Releases(ctx context.Context, id pack.ID) ([]registry.Release, error) {
	rels, err := s.queryReleases(ctx, `
		SELECT `+releaseColumns+` FROM releases
		WHERE team = ? AND name = ?
		ORDER BY major, minor, patch`,
		id.Team(), id.Name())
	if err != nil {
		return nil, fmt.Errorf("releases %s: %w", id, err)
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("releases %s: %w", id, registry.ErrNotFound)
	}
	return rels, nil
}

// Packages implements registry.Store.
func (s *Store) Packages(ctx context.Context, team string) ([]registry.Release, error) {
	rels, err := s.queryReleases(ctx, `
		SELECT `+releaseColumns+` FROM releases AS r
		WHERE r.team = ? AND NOT EXISTS (
			SELECT 1 FROM releases AS n
			WHERE n.team = r.team AND n.name = r.name
			  AND (n.major, n.minor, n.patch) > (r.major, r.minor, r.patch)
		)
		ORDER BY r.name`,
		team)
	if err != nil {
		return nil, fmt.Errorf("packages of %s: %w", team, err)
	}
	return rels, nil
}

// Changes implements registry.Store.
func (s *Store) Changes(ctx context.Context, team string, since int64, limit int) ([]registry.Release, error) {
	rels, err := s.queryReleases(ctx, `
		SELECT `+releaseColumns+` FROM releases
		WHERE team = ? AND seq > ?
		ORDER BY seq
		LIMIT ?`,
		team, since, limit)
	if err != nil {
		return nil, fmt.Errorf("changes of %s since %d: %w", team, since, err)
	}
	return rels, nil
}

func (s *Store) queryReleases(ctx context.Context, query string, args ...any) ([]registry.Release, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []registry.Release
	for rows.Next() {
		rel, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanRelease reads the releaseColumns, then any extra destinations.
func scanRelease(sc scanner, extra ...any) (registry.Release, error) {
	var (
		rel                          registry.Release
		team, name, version          string
		digest, description, pubTime string
		kind                         string
		size                         int64
	)
	dest := append([]any{
		&rel.Seq, &team, &name, &version, &digest, &description, &size, &pubTime, &kind, &rel.Proposal, &rel.SelfApproved,
		&rel.PublishedBy.TokenID, &rel.PublishedBy.Label, &rel.PublishedBy.UserID,
	}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return registry.Release{}, err
	}
	id, err := pack.NewID(team, name)
	if err != nil {
		return registry.Release{}, fmt.Errorf("stored release %d: %w", rel.Seq, err)
	}
	v, err := semver.Parse(version)
	if err != nil {
		return registry.Release{}, fmt.Errorf("stored release %d: %w", rel.Seq, err)
	}
	published, err := parseTime(pubTime)
	if err != nil {
		return registry.Release{}, fmt.Errorf("stored release %d: %w", rel.Seq, err)
	}
	rel.ID = id
	rel.Version = v
	rel.Digest = pack.Digest(digest)
	rel.Description = description
	rel.Kind = pack.Kind(kind)
	rel.Size = int(size)
	rel.PublishedAt = published
	return rel, nil
}

// versionParts splits v into the integer columns used for numeric ordering.
// semver.Version keeps its fields private, so this goes through String.
func versionParts(v semver.Version) (major, minor, patch int, err error) {
	parts := strings.Split(v.String(), ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("unexpected version format %q", v)
	}
	var nums [3]int
	for i, p := range parts {
		if nums[i], err = strconv.Atoi(p); err != nil {
			return 0, 0, 0, fmt.Errorf("unexpected version format %q: %w", v, err)
		}
	}
	return nums[0], nums[1], nums[2], nil
}
