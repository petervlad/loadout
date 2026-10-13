package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
)

var _ registry.DeletionStore = (*Store)(nil)

// deletionColumns are the columns scanDeletion reads, in order.
const deletionColumns = `id, team, name, reason, newest, author_token_id, author_label, author_user_id,
	status, created_at, decided_at, decided_by_token_id, decided_by_label, decided_by_user_id,
	decision_note, self_approved, removed`

// CreateDeletion implements registry.DeletionStore. The partial unique
// index on open requests is what a second request for one package hits.
func (s *Store) CreateDeletion(ctx context.Context, d registry.Deletion) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deletions (id, team, name, reason, newest, author_token_id, author_label, author_user_id, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'open', ?)`,
		d.ID, d.Package.Team(), d.Package.Name(), d.Reason, d.Newest.String(),
		d.Author.TokenID, d.Author.Label, d.Author.UserID, formatTime(d.CreatedAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("create deletion %s: %w: %s already has an open request", d.ID, registry.ErrExists, d.Package)
	}
	if err != nil {
		return fmt.Errorf("create deletion %s: %w", d.ID, err)
	}
	return nil
}

// Deletion implements registry.DeletionStore.
func (s *Store) Deletion(ctx context.Context, id string) (registry.Deletion, error) {
	d, err := deletionByID(ctx, s.db, id)
	if err != nil {
		return registry.Deletion{}, fmt.Errorf("deletion %s: %w", id, err)
	}
	return d, nil
}

// Deletions implements registry.DeletionStore.
func (s *Store) Deletions(ctx context.Context, f registry.DeletionFilter) ([]registry.Deletion, error) {
	// An empty status matches every row.
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+deletionColumns+` FROM deletions
		WHERE team = ? AND (? = '' OR status = ?)
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`, f.Team, string(f.Status), string(f.Status), f.Limit)
	if err != nil {
		return nil, fmt.Errorf("deletions of %s: %w", f.Team, err)
	}
	defer rows.Close()
	var out []registry.Deletion
	for rows.Next() {
		d, err := scanDeletion(rows)
		if err != nil {
			return nil, fmt.Errorf("deletions of %s: %w", f.Team, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("deletions of %s: %w", f.Team, err)
	}
	return out, nil
}

// DecideDeletion implements registry.DeletionStore.
func (s *Store) DecideDeletion(ctx context.Context, id string, d registry.Decision) (_ registry.Deletion, err error) {
	if d.Status != registry.ProposalRejected && d.Status != registry.ProposalWithdrawn {
		return registry.Deletion{}, fmt.Errorf("decide deletion %s: %w: cannot close a request as %q", id, registry.ErrInvalid, d.Status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.Deletion{}, fmt.Errorf("decide deletion %s: %w", id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if _, err := openDeletion(ctx, tx, id); err != nil {
		return registry.Deletion{}, fmt.Errorf("decide deletion %s: %w", id, err)
	}
	if err := setDeletionDecision(ctx, tx, id, registry.DeletionStatus(d.Status), d, registry.PackageFootprint{}); err != nil {
		return registry.Deletion{}, fmt.Errorf("decide deletion %s: %w", id, err)
	}
	got, err := deletionByID(ctx, tx, id)
	if err != nil {
		return registry.Deletion{}, fmt.Errorf("decide deletion %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return registry.Deletion{}, fmt.Errorf("decide deletion %s: commit: %w", id, err)
	}
	return got, nil
}

// ApproveDeletion implements registry.DeletionStore. The transaction holds
// SQLite's write lock from BEGIN (see dsn), so the release check, the delete
// and the decision cannot interleave with a publish.
func (s *Store) ApproveDeletion(ctx context.Context, id string, d registry.Decision) (_ registry.Deletion, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: %w", id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	del, err := openDeletion(ctx, tx, id)
	if err != nil {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: %w", id, err)
	}
	latest, err := latestRelease(ctx, tx, del.Package)
	if err != nil {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: %w", id, err)
	}
	if !latest.Equal(d.Latest) {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: %w: the newest release of %s is %q, not %q", id, registry.ErrStale, del.Package, latest, d.Latest)
	}
	// This also closes the request itself as gone; the decision below
	// overwrites that.
	fp, err := deletePackageTx(ctx, tx, del.Package, d.At)
	if err != nil {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: %w", id, err)
	}
	fp.Deletions = nil // the request itself was the only one open
	if err := setDeletionDecision(ctx, tx, id, registry.DeletionApproved, d, fp); err != nil {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: %w", id, err)
	}
	got, err := deletionByID(ctx, tx, id)
	if err != nil {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return registry.Deletion{}, fmt.Errorf("approve deletion %s: commit: %w", id, err)
	}
	return got, nil
}

// RecordDeletion implements registry.DeletionStore.
func (s *Store) RecordDeletion(ctx context.Context, d registry.Deletion) (_ registry.PackageFootprint, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("record deletion of %s: %w", d.Package, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	newest, err := latestRelease(ctx, tx, d.Package)
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("record deletion of %s: %w", d.Package, err)
	}
	fp, err := deletePackageTx(ctx, tx, d.Package, d.CreatedAt)
	if err != nil {
		return registry.PackageFootprint{}, err
	}
	removed, err := encodeRemoved(fp)
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("record deletion of %s: %w", d.Package, err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO deletions (id, team, name, reason, newest, author_token_id, author_label, author_user_id, status, created_at,
		                       decided_at, decided_by_token_id, decided_by_label, decided_by_user_id, decision_note, self_approved, removed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'approved', ?, ?, ?, ?, ?, ?, 1, ?)`,
		d.ID, d.Package.Team(), d.Package.Name(), d.Reason, newest.String(),
		d.Author.TokenID, d.Author.Label, d.Author.UserID, formatTime(d.CreatedAt),
		formatTime(d.CreatedAt), d.Author.TokenID, d.Author.Label, d.Author.UserID, d.DecisionNote, removed)
	if err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("record deletion of %s: %w", d.Package, err)
	}
	if err := tx.Commit(); err != nil {
		return registry.PackageFootprint{}, fmt.Errorf("record deletion of %s: commit: %w", d.Package, err)
	}
	return fp, nil
}

func setDeletionDecision(ctx context.Context, tx *sql.Tx, id string, status registry.DeletionStatus, d registry.Decision, removed registry.PackageFootprint) error {
	enc := ""
	if status == registry.DeletionApproved {
		var err error
		if enc, err = encodeRemoved(removed); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE deletions
		SET status = ?, decided_at = ?, decided_by_token_id = ?, decided_by_label = ?, decided_by_user_id = ?,
		    decision_note = ?, self_approved = ?, removed = ?
		WHERE id = ?`,
		string(status), formatTime(d.At), d.By.TokenID, d.By.Label, d.By.UserID, d.Note, d.SelfApproved, enc, id)
	return err
}

func deletionByID(ctx context.Context, q queryer, id string) (registry.Deletion, error) {
	d, err := scanDeletion(q.QueryRowContext(ctx, `SELECT `+deletionColumns+` FROM deletions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Deletion{}, registry.ErrNotFound
	}
	return d, err
}

// openDeletion reads a request inside tx and checks that it is open.
func openDeletion(ctx context.Context, tx *sql.Tx, id string) (registry.Deletion, error) {
	d, err := deletionByID(ctx, tx, id)
	if err != nil {
		return registry.Deletion{}, err
	}
	if d.Status != registry.DeletionOpen {
		return registry.Deletion{}, fmt.Errorf("%w: it is %s", registry.ErrProposalClosed, d.Status)
	}
	return d, nil
}

func scanDeletion(sc scanner) (registry.Deletion, error) {
	var (
		d                           registry.Deletion
		team, name, newest, status  string
		created, decided, removedJS string
	)
	err := sc.Scan(&d.ID, &team, &name, &d.Reason, &newest, &d.Author.TokenID, &d.Author.Label, &d.Author.UserID,
		&status, &created, &decided, &d.DecidedBy.TokenID, &d.DecidedBy.Label, &d.DecidedBy.UserID,
		&d.DecisionNote, &d.SelfApproved, &removedJS)
	if err != nil {
		return registry.Deletion{}, err
	}
	if d.Package, err = pack.NewID(team, name); err != nil {
		return registry.Deletion{}, fmt.Errorf("stored deletion %s: %w", d.ID, err)
	}
	if d.Newest, err = registry.ParseBase(newest); err != nil {
		return registry.Deletion{}, fmt.Errorf("stored deletion %s: %w", d.ID, err)
	}
	d.Status = registry.DeletionStatus(status)
	if !d.Status.Valid() {
		return registry.Deletion{}, fmt.Errorf("stored deletion %s: unknown status %q", d.ID, status)
	}
	if d.CreatedAt, err = parseTime(created); err != nil {
		return registry.Deletion{}, fmt.Errorf("stored deletion %s: %w", d.ID, err)
	}
	if decided != "" {
		if d.DecidedAt, err = parseTime(decided); err != nil {
			return registry.Deletion{}, fmt.Errorf("stored deletion %s: %w", d.ID, err)
		}
	}
	if d.Removed, err = decodeRemoved(d.Package, removedJS); err != nil {
		return registry.Deletion{}, fmt.Errorf("stored deletion %s: %w", d.ID, err)
	}
	return d, nil
}

// removedJSON is how a footprint is kept in the deletions table. The
// record must stay readable when the footprint type grows, so it has its
// own shape, with every field optional.
type removedJSON struct {
	Releases  []string `json:"releases,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	Proposals int      `json:"proposals,omitempty"`
	Open      []string `json:"open,omitempty"`
	Reviews   int      `json:"reviews,omitempty"`
	Prompts   int      `json:"prompts,omitempty"`
	Courses   []string `json:"courses,omitempty"`
	Deletions []string `json:"deletions,omitempty"`
}

func encodeRemoved(fp registry.PackageFootprint) (string, error) {
	r := removedJSON{
		Kind: string(fp.Kind), Proposals: fp.Proposals, Open: fp.Open, Reviews: fp.Reviews,
		Prompts: fp.Prompts, Courses: fp.Courses, Deletions: fp.Deletions,
	}
	for _, v := range fp.Releases {
		r.Releases = append(r.Releases, v.String())
	}
	b, err := json.Marshal(r)
	return string(b), err
}

func decodeRemoved(id pack.ID, s string) (registry.PackageFootprint, error) {
	if s == "" {
		return registry.PackageFootprint{}, nil // not approved: nothing went
	}
	fp := registry.PackageFootprint{ID: id}
	var r removedJSON
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return fp, fmt.Errorf("removed: %w", err)
	}
	for _, v := range r.Releases {
		ver, err := semver.Parse(v)
		if err != nil {
			return fp, fmt.Errorf("removed: %w", err)
		}
		fp.Releases = append(fp.Releases, ver)
	}
	fp.Kind, fp.Proposals, fp.Open, fp.Reviews, fp.Prompts = pack.Kind(r.Kind), r.Proposals, r.Open, r.Reviews, r.Prompts
	fp.Courses, fp.Deletions = r.Courses, r.Deletions
	return fp, nil
}
