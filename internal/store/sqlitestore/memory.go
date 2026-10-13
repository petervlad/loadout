package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// MemoryStore is team memory on its own SQLite file, memory.db. It is a
// memory.Store and a memory.TokenStore. The switch is memory.SwitchStore, whose
// Get clashes with Store.Get, so it is a view: Switches. Safe for concurrent
// use.
type MemoryStore struct {
	db *sql.DB
}

var (
	_ memory.Store       = (*MemoryStore)(nil)
	_ memory.TokenStore  = (*MemoryStore)(nil)
	_ memory.SwitchStore = switches{}
)

// OpenMemory opens (creating if needed) memory.db at path and migrates it with
// its own migrations and user_version. secure_delete zeroes freed pages, so a
// purged entry leaves no copy in the file. Files and folders get the modes
// Open gives them. A nil log discards log output.
func OpenMemory(ctx context.Context, path string, log *slog.Logger) (*MemoryStore, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	db, err := openDB(ctx, path, log, memoryMigrationFiles, "memorymigrations", "secure_delete(ON)")
	if err != nil {
		return nil, err
	}
	return &MemoryStore{db: db}, nil
}

// Close closes the database.
func (s *MemoryStore) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close memory store: %w", err)
	}
	return nil
}

// Switches returns the per-namespace switch kept in the same file.
func (s *MemoryStore) Switches() memory.SwitchStore { return switches{s.db} }

// sqlRunner is satisfied by *sql.DB and *sql.Tx.
type sqlRunner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const entryColumns = `e.id, e.namespace, e.type, e.name, e.description, e.body, e.status,
	e.author_user_id, e.author_label, e.created_at, e.updated_at, e.confirmed_at, e.version, e.uses,
	COALESCE((SELECT CASE WHEN v.by_user_id <> e.author_user_id THEN v.by_label ELSE '' END
		FROM entry_versions v WHERE v.entry_id = e.id AND v.action <> 'confirm'
		ORDER BY v.version DESC LIMIT 1), '')`

func scanEntry(r scanner) (memory.Entry, error) {
	var (
		e                           memory.Entry
		typ, status                 string
		created, updated, confirmed string
	)
	if err := r.Scan(&e.ID, &e.Namespace, &typ, &e.Name, &e.Description, &e.Body, &status,
		&e.AuthorID, &e.AuthorLabel, &created, &updated, &confirmed, &e.Version, &e.Uses, &e.EditedBy); err != nil {
		return memory.Entry{}, err
	}
	e.Type, e.Status = memory.Type(typ), memory.Status(status)
	var err error
	if e.CreatedAt, err = parseTime(created); err != nil {
		return memory.Entry{}, err
	}
	if e.UpdatedAt, err = parseTime(updated); err != nil {
		return memory.Entry{}, err
	}
	if e.ConfirmedAt, err = parseTime(confirmed); err != nil {
		return memory.Entry{}, err
	}
	return e, nil
}

// entries runs a query that selects entryColumns and loads the links of what
// it found.
func entries(ctx context.Context, q sqlRunner, query string, args ...any) ([]memory.Entry, error) {
	found, err := queryEntries(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	return found, attachLinks(ctx, q, found)
}

// queryEntries scans the rows of a query that selects entryColumns.
func queryEntries(ctx context.Context, q sqlRunner, query string, args ...any) ([]memory.Entry, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []memory.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// attachLinks sets Links on each entry, in the order they were saved.
func attachLinks(ctx context.Context, q sqlRunner, es []memory.Entry) error {
	if len(es) == 0 {
		return nil
	}
	at := make(map[int64]int, len(es))
	for i, e := range es {
		at[e.ID] = i
	}
	rows, err := q.QueryContext(ctx,
		`SELECT from_id, to_id FROM entry_links WHERE from_id IN (`+placeholders(len(es))+`) ORDER BY rowid`,
		ids(es)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var from, to int64
		if err := rows.Scan(&from, &to); err != nil {
			return err
		}
		es[at[from]].Links = append(es[at[from]].Links, to)
	}
	return rows.Err()
}

func ids(es []memory.Entry) []any {
	out := make([]any, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func strArgs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func getEntry(ctx context.Context, q sqlRunner, id int64) (memory.Entry, error) {
	es, err := entries(ctx, q, `SELECT `+entryColumns+` FROM entries e WHERE e.id = ?`, id)
	if err != nil {
		return memory.Entry{}, fmt.Errorf("memory %d: %w", id, err)
	}
	if len(es) == 0 {
		return memory.Entry{}, fmt.Errorf("memory %d: %w", id, registry.ErrNotFound)
	}
	return es[0], nil
}

// inTx runs fn in a write transaction. Transactions begin immediately (see
// dsn), so two writers never deadlock on a lock upgrade.
func (s *MemoryStore) inTx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func checkLinksExist(ctx context.Context, tx *sql.Tx, links []int64) error {
	for _, id := range links {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM entries WHERE id = ?`, id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: a linked memory does not exist", registry.ErrInvalid)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func setLinks(ctx context.Context, tx *sql.Tx, id int64, links []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM entry_links WHERE from_id = ?`, id); err != nil {
		return err
	}
	for _, to := range links {
		if _, err := tx.ExecContext(ctx, `INSERT INTO entry_links (from_id, to_id) VALUES (?, ?)`, id, to); err != nil {
			return err
		}
	}
	return nil
}

// snapshot writes the entry as it now stands as its latest version.
func snapshot(ctx context.Context, tx *sql.Tx, id int64, action memory.Action, by memory.Caller, note string, at time.Time) (memory.Entry, error) {
	e, err := getEntry(ctx, tx, id)
	if err != nil {
		return memory.Entry{}, err
	}
	links, err := json.Marshal(append([]int64{}, e.Links...))
	if err != nil {
		return memory.Entry{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO entry_versions (entry_id, version, action, namespace, type, name, description, body, links, by_user_id, by_label, note, at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Version, string(action), e.Namespace, string(e.Type), e.Name, e.Description, e.Body, string(links),
		by.UserID, by.Label, note, formatTime(at))
	if err != nil {
		return memory.Entry{}, err
	}
	// Read again so the entry carries the editor this version just set.
	return getEntry(ctx, tx, id)
}

// Insert implements memory.Store.
func (s *MemoryStore) Insert(ctx context.Context, d memory.Draft, by memory.Caller, at time.Time, opts ...memory.WriteOption) (e memory.Entry, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := checkWriteLimit(ctx, tx, by.UserID, opts); err != nil {
			return err
		}
		links := d.Links()
		if err := checkLinksExist(ctx, tx, links); err != nil {
			return err
		}
		ts := formatTime(at)
		res, err := tx.ExecContext(ctx, `
			INSERT INTO entries (namespace, type, name, description, body, author_user_id, author_label, created_at, updated_at, confirmed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.Namespace(), string(d.Type()), d.Name(), d.Description(), d.Body(), by.UserID, by.Label, ts, ts, ts)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := setLinks(ctx, tx, id, links); err != nil {
			return err
		}
		e, err = snapshot(ctx, tx, id, memory.ActionSave, by, "", at)
		return err
	})
	if err != nil {
		return memory.Entry{}, fmt.Errorf("insert memory: %w", err)
	}
	return e, nil
}

// Get implements memory.Store. It does not count a use.
func (s *MemoryStore) Get(ctx context.Context, id int64) (memory.Entry, error) {
	return getEntry(ctx, s.db, id)
}

// GetMany implements memory.Store.
func (s *MemoryStore) GetMany(ctx context.Context, want []int64) ([]memory.Entry, error) {
	if len(want) == 0 {
		return nil, nil
	}
	args := make([]any, len(want))
	for i, id := range want {
		args[i] = id
	}
	found, err := entries(ctx, s.db, `SELECT `+entryColumns+` FROM entries e WHERE e.id IN (`+placeholders(len(want))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("get memories: %w", err)
	}
	byID := make(map[int64]memory.Entry, len(found))
	for _, e := range found {
		byID[e.ID] = e
	}
	var out []memory.Entry
	for _, id := range want {
		if e, ok := byID[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// CountUse implements memory.Store. An id given twice counts twice.
func (s *MemoryStore) CountUse(ctx context.Context, want ...int64) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for _, id := range want {
			if _, err := tx.ExecContext(ctx, `UPDATE entries SET uses = uses + 1 WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("count memory uses: %w", err)
	}
	return nil
}

// History implements memory.Store.
func (s *MemoryStore) History(ctx context.Context, id int64) ([]memory.Version, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM entries WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memory %d: %w", id, registry.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("history of memory %d: %w", id, err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT entry_id, version, action, namespace, type, name, description, body, links, by_user_id, by_label, note, at
		FROM entry_versions WHERE entry_id = ? ORDER BY version`, id)
	if err != nil {
		return nil, fmt.Errorf("history of memory %d: %w", id, err)
	}
	defer rows.Close()
	var out []memory.Version
	for rows.Next() {
		var (
			v                      memory.Version
			action, typ, links, at string
		)
		if err := rows.Scan(&v.EntryID, &v.Number, &action, &v.Namespace, &typ, &v.Name, &v.Description, &v.Body,
			&links, &v.By.UserID, &v.By.Label, &v.Note, &at); err != nil {
			return nil, fmt.Errorf("history of memory %d: %w", id, err)
		}
		v.Action, v.Type = memory.Action(action), memory.Type(typ)
		if err := json.Unmarshal([]byte(links), &v.Links); err != nil {
			return nil, fmt.Errorf("history of memory %d: links: %w", id, err)
		}
		if v.At, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("history of memory %d: %w", id, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history of memory %d: %w", id, err)
	}
	return out, nil
}

// Update implements memory.Store.
func (s *MemoryStore) Update(ctx context.Context, id int64, base int, r memory.Revision, by memory.Caller, at time.Time, opts ...memory.WriteOption) (e memory.Entry, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := checkWriteLimit(ctx, tx, by.UserID, opts); err != nil {
			return err
		}
		cur, err := getEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Version != base {
			return fmt.Errorf("memory %d: %w", id, memory.ErrStale)
		}
		if cur.Status != memory.StatusActive {
			return fmt.Errorf("%w: memory %d is retracted", registry.ErrInvalid, id)
		}
		links := r.Links()
		if err := checkLinksExist(ctx, tx, links); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE entries SET type = ?, name = ?, description = ?, body = ?, confirmed_at = ?, updated_at = ?, version = version + 1
			WHERE id = ?`,
			string(r.Type()), r.Name(), r.Description(), r.Body(), formatTime(r.ConfirmedAt()), formatTime(at), id); err != nil {
			return err
		}
		if err := setLinks(ctx, tx, id, links); err != nil {
			return err
		}
		e, err = snapshot(ctx, tx, id, r.Action(), by, r.Note(), at)
		return err
	})
	if err != nil {
		return memory.Entry{}, fmt.Errorf("update memory %d: %w", id, err)
	}
	return e, nil
}

// Retract implements memory.Store.
func (s *MemoryStore) Retract(ctx context.Context, id int64, reason string, by memory.Caller, at time.Time) (e memory.Entry, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := getEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status == memory.StatusRetracted {
			e = cur
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE entries SET status = 'retracted', updated_at = ?, version = version + 1 WHERE id = ?`,
			formatTime(at), id); err != nil {
			return err
		}
		e, err = snapshot(ctx, tx, id, memory.ActionRetract, by, reason, at)
		return err
	})
	if err != nil {
		return memory.Entry{}, fmt.Errorf("retract memory %d: %w", id, err)
	}
	return e, nil
}

// Move implements memory.Store.
func (s *MemoryStore) Move(ctx context.Context, id int64, to string, by memory.Caller, at time.Time) (e memory.Entry, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := getEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status != memory.StatusActive || cur.Namespace == to || strings.TrimSpace(to) == "" {
			return fmt.Errorf("%w: memory %d cannot be moved there", registry.ErrInvalid, id)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE entries SET namespace = ?, updated_at = ?, version = version + 1 WHERE id = ?`,
			to, formatTime(at), id); err != nil {
			return err
		}
		e, err = snapshot(ctx, tx, id, memory.ActionMove, by, "", at)
		return err
	})
	if err != nil {
		return memory.Entry{}, fmt.Errorf("move memory %d: %w", id, err)
	}
	return e, nil
}

// Purge implements memory.Store. After the delete it merges the full-text
// index, so the terms of the entry leave their segments, and truncates the
// write-ahead log. With secure_delete that leaves no copy of the text in
// memory.db or its WAL.
func (s *MemoryStore) Purge(ctx context.Context, id int64, at time.Time) (p memory.Purged, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		e, err := getEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		p = memory.Purged{EntryID: id, Namespace: e.Namespace, CreatedAt: e.CreatedAt, At: at.UTC()}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entry_versions WHERE entry_id = ?`, id).Scan(&p.Versions); err != nil {
			return err
		}
		// Versions and links go with the entry (ON DELETE CASCADE).
		if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE id = ?`, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO purges (entry_id, namespace, versions, at) VALUES (?, ?, ?, ?)`,
			id, p.Namespace, p.Versions, formatTime(at))
		return err
	})
	if err != nil {
		return memory.Purged{}, fmt.Errorf("purge memory %d: %w", id, err)
	}
	if err := s.Scrub(ctx); err != nil {
		return p, fmt.Errorf("purge memory %d: the entry is gone but %w", id, err)
	}
	return p, nil
}

// RemoveNamespace implements memory.Store. Like Purge it merges the full-text
// index and truncates the log afterwards, so the text leaves the files.
func (s *MemoryStore) RemoveNamespace(ctx context.Context, namespace string) (r memory.Removed, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		const mine = `(SELECT id FROM entries WHERE namespace = ?)`
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE namespace = ?`, namespace).Scan(&r.Entries); err != nil {
			return err
		}
		if r.Entries == 0 {
			return nil
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entry_versions WHERE entry_id IN `+mine, namespace).Scan(&r.Versions); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM entry_links WHERE from_id IN `+mine+` OR to_id IN `+mine, namespace, namespace).Scan(&r.Links); err != nil {
			return err
		}
		// Versions and links go with the entries (ON DELETE CASCADE).
		_, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE namespace = ?`, namespace)
		return err
	})
	if err != nil {
		return memory.Removed{}, fmt.Errorf("remove memory of %s: %w", namespace, err)
	}
	if r.Entries == 0 {
		return r, nil
	}
	if err := s.Scrub(ctx); err != nil {
		return r, fmt.Errorf("remove memory of %s: the entries are gone but %w", namespace, err)
	}
	return r, nil
}

// Scrub implements memory.Store: it merges the full-text index and truncates
// the WAL. It is safe to run again when a purge could not finish it.
func (s *MemoryStore) Scrub(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO entries_fts (entries_fts) VALUES ('optimize')`); err != nil {
		return fmt.Errorf("optimize the search index: %w", err)
	}
	var busy, logFrames, done int
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &done); err != nil {
		return fmt.Errorf("truncate the write-ahead log: %w", err)
	}
	if busy != 0 {
		return errors.New("the write-ahead log could not be truncated because the database is busy. Run the purge command again")
	}
	return nil
}

// Purges implements memory.Store. The purges table keeps no creation date, so
// CreatedAt is zero here: Purge returns it once, for the backup listing.
func (s *MemoryStore) Purges(ctx context.Context) ([]memory.Purged, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT entry_id, namespace, versions, at FROM purges ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list purges: %w", err)
	}
	defer rows.Close()
	var out []memory.Purged
	for rows.Next() {
		var (
			p  memory.Purged
			at string
		)
		if err := rows.Scan(&p.EntryID, &p.Namespace, &p.Versions, &at); err != nil {
			return nil, fmt.Errorf("list purges: %w", err)
		}
		if p.At, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("list purges: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list purges: %w", err)
	}
	return out, nil
}

// matchQuery quotes each word, so user text never reaches full-text syntax.
// Words are letters and digits only (memory.Words), so a quote cannot occur.
func matchQuery(words []string, op string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + w + `"`
	}
	return strings.Join(quoted, op)
}

// ranked runs a full-text query over some namespaces: active entries only,
// best bm25 first (name weighs 10, description 5, body 1), then newest, then
// the higher id.
func (s *MemoryStore) ranked(ctx context.Context, match string, namespaces []string, typ memory.Type, limit int) ([]memory.Entry, error) {
	q := `SELECT ` + entryColumns + `
		FROM entries_fts JOIN entries e ON e.id = entries_fts.rowid
		WHERE entries_fts MATCH ? AND e.status = 'active' AND e.namespace IN (` + placeholders(len(namespaces)) + `)`
	args := append([]any{match}, strArgs(namespaces)...)
	if typ != "" {
		q += ` AND e.type = ?`
		args = append(args, string(typ))
	}
	q += ` ORDER BY bm25(entries_fts, 10.0, 5.0, 1.0), e.updated_at DESC, e.id DESC LIMIT ?`
	if limit <= 0 {
		limit = -1
	}
	return entries(ctx, s.db, q, append(args, limit)...)
}

// Search implements memory.Store.
func (s *MemoryStore) Search(ctx context.Context, q memory.Query) ([]memory.Entry, error) {
	words := memory.Words(q.Text)
	if len(words) == 0 || len(q.Namespaces) == 0 {
		return nil, nil
	}
	got, err := s.ranked(ctx, matchQuery(words, " "), q.Namespaces, q.Type, q.Limit)
	if err == nil && len(got) == 0 && len(words) > 1 {
		got, err = s.ranked(ctx, matchQuery(words, " OR "), q.Namespaces, q.Type, q.Limit)
	}
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	return got, nil
}

// Candidates implements memory.Store.
func (s *MemoryStore) Candidates(ctx context.Context, namespaces []string, words []string, limit int) ([]memory.Entry, error) {
	var clean []string
	for _, w := range words {
		for _, t := range memory.Words(w) {
			if !slices.Contains(clean, t) {
				clean = append(clean, t)
			}
		}
	}
	if len(clean) == 0 || len(namespaces) == 0 {
		return nil, nil
	}
	got, err := s.ranked(ctx, matchQuery(clean, " OR "), namespaces, "", limit)
	if err != nil {
		return nil, fmt.Errorf("memory candidates: %w", err)
	}
	return got, nil
}

// Recent implements memory.Store.
func (s *MemoryStore) Recent(ctx context.Context, namespaces []string, since time.Time, limit int) ([]memory.Entry, error) {
	if len(namespaces) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = -1
	}
	got, err := entries(ctx, s.db, `SELECT `+entryColumns+` FROM entries e
		WHERE e.status = 'active' AND e.namespace IN (`+placeholders(len(namespaces))+`) AND e.updated_at >= ?
		ORDER BY e.updated_at DESC, e.id DESC LIMIT ?`,
		append(strArgs(namespaces), formatTime(since), limit)...)
	if err != nil {
		return nil, fmt.Errorf("recent memories: %w", err)
	}
	return got, nil
}

// Index implements memory.Store.
func (s *MemoryStore) Index(ctx context.Context, namespaces []string, now time.Time, limit int) ([]memory.Entry, int, error) {
	if len(namespaces) == 0 {
		return nil, 0, nil
	}
	in := ` e.status = 'active' AND e.namespace IN (` + placeholders(len(namespaces)) + `)`
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries e WHERE`+in, strArgs(namespaces)...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count memories: %w", err)
	}
	if limit <= 0 {
		limit = -1
	}
	cutoff := formatTime(now.Add(-memory.IndexFresh))
	// Fresh entries first, newest first. The rest by uses, then newest.
	args := append(strArgs(namespaces), cutoff, cutoff, limit)
	got, err := entries(ctx, s.db, `SELECT `+entryColumns+` FROM entries e WHERE`+in+`
		ORDER BY (e.updated_at >= ?) DESC, CASE WHEN e.updated_at >= ? THEN 0 ELSE e.uses END DESC,
		         e.updated_at DESC, e.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("index memories: %w", err)
	}
	return got, total, nil
}

// WritesSince implements memory.Store.
func (s *MemoryStore) WritesSince(ctx context.Context, userID string, since time.Time) (memory.Writes, error) {
	w, err := countWrites(ctx, s.db, userID, since)
	if err != nil {
		return memory.Writes{}, fmt.Errorf("count memory writes: %w", err)
	}
	return w, nil
}

// checkWriteLimit refuses a write inside its transaction when the author has
// the most the option allows. Transactions begin immediately, so two writes
// at once are counted one after the other.
func checkWriteLimit(ctx context.Context, tx *sql.Tx, userID string, opts []memory.WriteOption) error {
	l, ok := memory.LimitOf(opts)
	if !ok {
		return nil
	}
	w, err := countWrites(ctx, tx, userID, l.Since)
	if err != nil {
		return fmt.Errorf("count memory writes: %w", err)
	}
	if w.Count >= l.Max {
		return &memory.WriteLimitError{Oldest: w.Oldest}
	}
	return nil
}

// countWrites counts the save and update versions of a person after since. A
// retract, a move and a confirm are not writes.
func countWrites(ctx context.Context, q sqlRunner, userID string, since time.Time) (memory.Writes, error) {
	var (
		n      int
		oldest sql.NullString
	)
	err := q.QueryRowContext(ctx, `SELECT COUNT(*), MIN(at) FROM entry_versions
		WHERE by_user_id = ? AND at > ? AND action IN ('save', 'update')`,
		userID, formatTime(since)).Scan(&n, &oldest)
	if err != nil {
		return memory.Writes{}, err
	}
	w := memory.Writes{Count: n}
	if oldest.Valid {
		if w.Oldest, err = parseTime(oldest.String); err != nil {
			return memory.Writes{}, err
		}
	}
	return w, nil
}

// Create implements memory.TokenStore.
func (s *MemoryStore) Create(ctx context.Context, t memory.Token, hash []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_tokens (id, hash, user_id, label, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, hash, t.UserID, t.Label, formatTime(t.CreatedAt), formatTime(t.ExpiresAt))
	if isUniqueViolation(err) {
		return fmt.Errorf("create memory token %s: %w", t.ID, registry.ErrExists)
	}
	if err != nil {
		return fmt.Errorf("create memory token %s: %w", t.ID, err)
	}
	return nil
}

const mtColumns = `id, user_id, label, created_at, expires_at`

func scanMemoryToken(r scanner) (memory.Token, error) {
	var (
		t               memory.Token
		created, expire string
	)
	if err := r.Scan(&t.ID, &t.UserID, &t.Label, &created, &expire); err != nil {
		return memory.Token{}, err
	}
	var err error
	if t.CreatedAt, err = parseTime(created); err != nil {
		return memory.Token{}, err
	}
	if t.ExpiresAt, err = parseTime(expire); err != nil {
		return memory.Token{}, err
	}
	return t, nil
}

// ByHash implements memory.TokenStore.
func (s *MemoryStore) ByHash(ctx context.Context, hash []byte) (memory.Token, error) {
	t, err := scanMemoryToken(s.db.QueryRowContext(ctx, `SELECT `+mtColumns+` FROM memory_tokens WHERE hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return memory.Token{}, fmt.Errorf("memory token by hash: %w", registry.ErrNotFound)
	}
	if err != nil {
		return memory.Token{}, fmt.Errorf("memory token by hash: %w", err)
	}
	return t, nil
}

// List implements memory.TokenStore.
func (s *MemoryStore) List(ctx context.Context, userID string) ([]memory.Token, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mtColumns+` FROM memory_tokens WHERE user_id = ? ORDER BY created_at DESC, rowid DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list memory tokens: %w", err)
	}
	defer rows.Close()
	var out []memory.Token
	for rows.Next() {
		t, err := scanMemoryToken(rows)
		if err != nil {
			return nil, fmt.Errorf("list memory tokens: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list memory tokens: %w", err)
	}
	return out, nil
}

// Revoke implements memory.TokenStore.
func (s *MemoryStore) Revoke(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memory_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("revoke memory token %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("memory token %s: %w", id, registry.ErrNotFound)
	}
	return nil
}

// RevokeAll implements memory.TokenStore.
func (s *MemoryStore) RevokeAll(ctx context.Context, userID string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memory_tokens WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke memory tokens: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// switches is memory.SwitchStore on the same database.
type switches struct{ db *sql.DB }

// Get implements memory.SwitchStore.
func (w switches) Get(ctx context.Context, namespace string) (memory.Space, error) {
	sp := memory.Space{Namespace: namespace}
	var on int
	var at string
	err := w.db.QueryRowContext(ctx, `SELECT enabled, changed_by, changed_at FROM memory_spaces WHERE namespace = ?`, namespace).
		Scan(&on, &sp.ChangedBy, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return sp, nil
	}
	if err != nil {
		return memory.Space{}, fmt.Errorf("memory switch of %s: %w", namespace, err)
	}
	sp.Enabled = on != 0
	if sp.ChangedAt, err = parseTime(at); err != nil {
		return memory.Space{}, err
	}
	return sp, nil
}

// Set implements memory.SwitchStore.
func (w switches) Set(ctx context.Context, namespace string, on bool, by string, at time.Time) error {
	v := 0
	if on {
		v = 1
	}
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO memory_spaces (namespace, enabled, changed_by, changed_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (namespace) DO UPDATE SET enabled = excluded.enabled, changed_by = excluded.changed_by, changed_at = excluded.changed_at`,
		namespace, v, by, formatTime(at))
	if err != nil {
		return fmt.Errorf("set memory switch of %s: %w", namespace, err)
	}
	return nil
}

// Remove implements memory.SwitchStore.
func (w switches) Remove(ctx context.Context, namespace string) error {
	if _, err := w.db.ExecContext(ctx, `DELETE FROM memory_spaces WHERE namespace = ?`, namespace); err != nil {
		return fmt.Errorf("remove memory switch of %s: %w", namespace, err)
	}
	return nil
}

// Enabled implements memory.SwitchStore.
func (w switches) Enabled(ctx context.Context) ([]string, error) {
	rows, err := w.db.QueryContext(ctx, `SELECT namespace FROM memory_spaces WHERE enabled = 1 ORDER BY namespace`)
	if err != nil {
		return nil, fmt.Errorf("list memory switches: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			return nil, fmt.Errorf("list memory switches: %w", err)
		}
		out = append(out, ns)
	}
	return out, rows.Err()
}
