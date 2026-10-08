package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

var _ registry.NamespaceStore = (*Store)(nil)

const namespaceColumns = "name, parent, kind, created_at, require_review, wiki, course"

// CreateNamespace implements registry.NamespaceStore.
func (s *Store) CreateNamespace(ctx context.Context, ns registry.Namespace) error {
	var parent sql.NullString
	if ns.Parent != "" {
		parent = sql.NullString{String: ns.Parent, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO namespaces (name, parent, kind, created_at, require_review, wiki, course)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ns.Name, parent, string(ns.Kind), formatTime(ns.CreatedAt), ns.RequireReview, ns.Wiki, courseText(ns.Course))
	if isUniqueViolation(err) {
		return fmt.Errorf("create namespace %s: %w", ns.Name, registry.ErrExists)
	}
	if err != nil {
		return fmt.Errorf("create namespace %s: %w", ns.Name, err)
	}
	return nil
}

// Namespace implements registry.NamespaceStore.
func (s *Store) Namespace(ctx context.Context, name string) (registry.Namespace, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+namespaceColumns+` FROM namespaces WHERE name = ?`, name)
	ns, err := s.scanNamespace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Namespace{}, fmt.Errorf("namespace %s: %w", name, registry.ErrNotFound)
	}
	if err != nil {
		return registry.Namespace{}, fmt.Errorf("namespace %s: %w", name, err)
	}
	return ns, nil
}

// Namespaces implements registry.NamespaceStore.
func (s *Store) Namespaces(ctx context.Context) ([]registry.Namespace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+namespaceColumns+` FROM namespaces ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	defer rows.Close()
	var out []registry.Namespace
	for rows.Next() {
		ns, err := s.scanNamespace(rows)
		if err != nil {
			return nil, fmt.Errorf("list namespaces: %w", err)
		}
		out = append(out, ns)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	return out, nil
}

// SetRequireReview implements registry.NamespaceStore.
func (s *Store) SetRequireReview(ctx context.Context, name string, on bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE namespaces SET require_review = ? WHERE name = ?`, on, name)
	return affectedOne(res, err, "namespace "+name+": require review")
}

// SetWiki implements registry.NamespaceStore.
func (s *Store) SetWiki(ctx context.Context, name string, on bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE namespaces SET wiki = ? WHERE name = ?`, on, name)
	return affectedOne(res, err, "namespace "+name+": wiki")
}

// SetCourse implements registry.NamespaceStore.
func (s *Store) SetCourse(ctx context.Context, name string, course pack.ID) error {
	res, err := s.db.ExecContext(ctx, `UPDATE namespaces SET course = ? WHERE name = ?`, courseText(course), name)
	return affectedOne(res, err, "namespace "+name+": course")
}

// courseText is a course as stored: "team/name", or "" for none.
func courseText(id pack.ID) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

// DeleteNamespace implements registry.NamespaceStore: in one transaction it
// checks that nothing lives in the namespace, then removes its memberships,
// tokens, model tiers, wiki revisions and the namespace. A wiki does not
// keep a namespace from being deleted.
func (s *Store) DeleteNamespace(ctx context.Context, name string) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete namespace %s: %w", name, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	var exists, used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM namespaces WHERE name = ?)`, name).Scan(&exists); err != nil {
		return fmt.Errorf("delete namespace %s: %w", name, err)
	}
	if !exists {
		return fmt.Errorf("delete namespace %s: %w", name, registry.ErrNotFound)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM namespaces WHERE parent = ?)
		    OR EXISTS (SELECT 1 FROM releases WHERE team = ?)
		    OR EXISTS (SELECT 1 FROM proposals WHERE team = ?)`,
		name, name, name).Scan(&used); err != nil {
		return fmt.Errorf("delete namespace %s: %w", name, err)
	}
	if used {
		return fmt.Errorf("delete namespace %s: %w", name, registry.ErrNotEmpty)
	}
	for _, q := range []string{
		`DELETE FROM memberships WHERE namespace = ?`,
		`DELETE FROM tokens WHERE team = ?`,
		`DELETE FROM model_tiers WHERE namespace = ?`,
		`DELETE FROM wiki_revisions WHERE namespace = ?`,
		`DELETE FROM namespaces WHERE name = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, name); err != nil {
			return fmt.Errorf("delete namespace %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete namespace %s: %w", name, err)
	}
	return nil
}

func (s *Store) scanNamespace(sc scanner) (registry.Namespace, error) {
	var (
		ns            registry.Namespace
		parent        sql.NullString
		kind, created string
		course        string
	)
	if err := sc.Scan(&ns.Name, &parent, &kind, &created, &ns.RequireReview, &ns.Wiki, &course); err != nil {
		return registry.Namespace{}, err
	}
	at, err := parseTime(created)
	if err != nil {
		return registry.Namespace{}, fmt.Errorf("stored namespace %s: %w", ns.Name, err)
	}
	ns.Parent = parent.String
	ns.Kind = registry.NamespaceKind(kind)
	ns.CreatedAt = at
	if course != "" {
		// A value that does not parse (a row edited by hand) reads as no
		// course: it must not break every page of the namespace.
		if id, err := pack.ParseID(course); err == nil {
			ns.Course = id
		} else {
			s.log.Warn("stored namespace course is not a package id; ignoring it", "namespace", ns.Name, "err", err)
		}
	}
	return ns, nil
}
