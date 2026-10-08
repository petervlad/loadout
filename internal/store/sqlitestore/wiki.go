package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/wiki"
)

var _ registry.WikiStore = (*Store)(nil)

const wikiInfoColumns = "namespace, seq, format, size, author_user_id, author_label, note, created_at"

// WikiRevision implements registry.WikiStore.
func (s *Store) WikiRevision(ctx context.Context, namespace string, seq int) (wiki.Revision, error) {
	q := `SELECT ` + wikiInfoColumns + `, body FROM wiki_revisions WHERE namespace = ? AND seq = ?`
	args := []any{namespace, seq}
	if seq == 0 {
		q = `SELECT ` + wikiInfoColumns + `, body FROM wiki_revisions WHERE namespace = ? ORDER BY seq DESC LIMIT 1`
		args = args[:1]
	}
	var body string
	info, err := scanWikiInfo(s.db.QueryRowContext(ctx, q, args...), &body)
	if errors.Is(err, sql.ErrNoRows) {
		return wiki.Revision{}, fmt.Errorf("wiki of %s: revision %d: %w", namespace, seq, registry.ErrNotFound)
	}
	if err != nil {
		return wiki.Revision{}, fmt.Errorf("wiki of %s: revision %d: %w", namespace, seq, err)
	}
	doc, err := wiki.NewDoc(info.Format, body)
	if err != nil {
		return wiki.Revision{}, fmt.Errorf("stored wiki of %s: revision %d: %w", namespace, info.Seq, err)
	}
	return wiki.Revision{Info: info, Doc: doc}, nil
}

// WikiRevisions implements registry.WikiStore. It never reads the bodies.
func (s *Store) WikiRevisions(ctx context.Context, namespace string) ([]wiki.Info, error) {
	if _, err := s.Namespace(ctx, namespace); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+wikiInfoColumns+` FROM wiki_revisions WHERE namespace = ? ORDER BY seq DESC`, namespace)
	if err != nil {
		return nil, fmt.Errorf("wiki of %s: history: %w", namespace, err)
	}
	defer rows.Close()
	var out []wiki.Info
	for rows.Next() {
		info, err := scanWikiInfo(rows)
		if err != nil {
			return nil, fmt.Errorf("wiki of %s: history: %w", namespace, err)
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wiki of %s: history: %w", namespace, err)
	}
	return out, nil
}

// PutWikiRevision implements registry.WikiStore. The transaction takes the
// write lock at BEGIN (see dsn), so the check of the newest revision and
// the insert cannot interleave with another save; the primary key refuses
// a second revision with the same number all the same.
func (s *Store) PutWikiRevision(ctx context.Context, rev wiki.Revision, base, keep int) (_ wiki.Info, err error) {
	ns := rev.Namespace
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wiki.Info{}, fmt.Errorf("save wiki of %s: %w", ns, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM namespaces WHERE name = ?)`, ns).Scan(&exists); err != nil {
		return wiki.Info{}, fmt.Errorf("save wiki of %s: %w", ns, err)
	}
	if !exists {
		return wiki.Info{}, fmt.Errorf("save wiki of %s: %w", ns, registry.ErrNotFound)
	}
	var newest int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM wiki_revisions WHERE namespace = ?`, ns).Scan(&newest); err != nil {
		return wiki.Info{}, fmt.Errorf("save wiki of %s: %w", ns, err)
	}
	if newest != base {
		return wiki.Info{}, fmt.Errorf("save wiki of %s on revision %d: revision %d exists: %w", ns, base, newest, registry.ErrStale)
	}
	info := rev.Info
	info.Seq, info.Format, info.Size = base+1, rev.Doc.Format(), rev.Doc.Size()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO wiki_revisions (namespace, seq, format, body, size, author_user_id, author_label, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ns, info.Seq, string(info.Format), rev.Doc.Text(), info.Size, info.AuthorUserID, info.Author, info.Note, formatTime(info.CreatedAt))
	if isUniqueViolation(err) {
		return wiki.Info{}, fmt.Errorf("save wiki of %s on revision %d: %w", ns, base, registry.ErrStale)
	}
	if err != nil {
		return wiki.Info{}, fmt.Errorf("save wiki of %s: %w", ns, err)
	}
	if keep > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM wiki_revisions WHERE namespace = ? AND seq <= ?`, ns, info.Seq-keep); err != nil {
			return wiki.Info{}, fmt.Errorf("save wiki of %s: prune: %w", ns, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return wiki.Info{}, fmt.Errorf("save wiki of %s: %w", ns, err)
	}
	info.CreatedAt = info.CreatedAt.UTC()
	return info, nil
}

// scanWikiInfo reads wikiInfoColumns, then into more.
func scanWikiInfo(sc scanner, more ...any) (wiki.Info, error) {
	var (
		info            wiki.Info
		format, created string
	)
	dest := append([]any{&info.Namespace, &info.Seq, &format, &info.Size, &info.AuthorUserID, &info.Author, &info.Note, &created}, more...)
	if err := sc.Scan(dest...); err != nil {
		return wiki.Info{}, err
	}
	at, err := parseTime(created)
	if err != nil {
		return wiki.Info{}, err
	}
	info.Format, info.CreatedAt = wiki.Format(format), at
	return info, nil
}
