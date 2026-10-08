package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

// promptColumns are the columns scanPrompt reads, in order.
const promptColumns = `proposal_id, seq, body, author_token_id, author_label, author_user_id, created_at,
	edited_at, removed_at, removed_by_token_id, removed_by_label, removed_by_user_id`

// AddPrompt implements registry.ProposalStore. The transaction holds the
// write lock from BEGIN (see dsn), so concurrent adds cannot pass the
// limits together.
func (s *Store) AddPrompt(ctx context.Context, id string, p registry.Prompt, limit, maxRows int) (_ registry.Prompt, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.Prompt{}, fmt.Errorf("add prompt to %s: %w", id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if _, err := openProposal(ctx, tx, id); err != nil {
		return registry.Prompt{}, fmt.Errorf("add prompt to %s: %w", id, err)
	}
	var rows, active int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE removed_at = '') FROM proposal_prompts WHERE proposal_id = ?`, id).
		Scan(&rows, &active); err != nil {
		return registry.Prompt{}, fmt.Errorf("add prompt to %s: %w", id, err)
	}
	switch {
	case active >= limit:
		return registry.Prompt{}, fmt.Errorf("%w: proposal %s already has %d prompts; remove one first", registry.ErrInvalid, id, limit)
	case rows >= maxRows:
		return registry.Prompt{}, fmt.Errorf("%w: proposal %s has had %d prompts, the most it can have", registry.ErrInvalid, id, maxRows)
	}
	// Rows are never deleted, so the next number is one past the count.
	p.Seq = rows + 1
	p.EditedAt, p.RemovedAt, p.RemovedBy = time.Time{}, time.Time{}, registry.Actor{}
	if err := insertPrompt(ctx, tx, id, p); err != nil {
		return registry.Prompt{}, fmt.Errorf("add prompt to %s: %w", id, err)
	}
	if err := commitPrompts(ctx, tx, id); err != nil {
		return registry.Prompt{}, fmt.Errorf("add prompt to %s: %w", id, err)
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}

// EditPrompt implements registry.ProposalStore.
func (s *Store) EditPrompt(ctx context.Context, id string, seq int, text string, at time.Time) (_ registry.Prompt, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.Prompt{}, fmt.Errorf("edit prompt %d of %s: %w", seq, id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	p, err := activePrompt(ctx, tx, id, seq)
	if err != nil {
		return registry.Prompt{}, fmt.Errorf("edit prompt %d of %s: %w", seq, id, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE proposal_prompts SET body = ?, edited_at = ? WHERE proposal_id = ? AND seq = ?`,
		text, formatTime(at), id, seq); err != nil {
		return registry.Prompt{}, fmt.Errorf("edit prompt %d of %s: %w", seq, id, err)
	}
	p.Text, p.EditedAt = text, at.UTC()
	if err := commitPrompts(ctx, tx, id); err != nil {
		return registry.Prompt{}, fmt.Errorf("edit prompt %d of %s: %w", seq, id, err)
	}
	return p, nil
}

// RemovePrompt implements registry.ProposalStore.
func (s *Store) RemovePrompt(ctx context.Context, id string, seq int, by registry.Actor, at time.Time) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("remove prompt %d of %s: %w", seq, id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if _, err := activePrompt(ctx, tx, id, seq); err != nil {
		return fmt.Errorf("remove prompt %d of %s: %w", seq, id, err)
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM proposal_prompts WHERE proposal_id = ? AND removed_at = ''`, id).Scan(&active); err != nil {
		return fmt.Errorf("remove prompt %d of %s: %w", seq, id, err)
	}
	if active <= 1 {
		return fmt.Errorf("%w: a proposal keeps at least one prompt", registry.ErrInvalid)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE proposal_prompts SET removed_at = ?, removed_by_token_id = ?, removed_by_label = ?, removed_by_user_id = ?
		WHERE proposal_id = ? AND seq = ?`,
		formatTime(at), by.TokenID, by.Label, by.UserID, id, seq); err != nil {
		return fmt.Errorf("remove prompt %d of %s: %w", seq, id, err)
	}
	if err := commitPrompts(ctx, tx, id); err != nil {
		return fmt.Errorf("remove prompt %d of %s: %w", seq, id, err)
	}
	return nil
}

// activePrompt reads prompt seq inside tx and checks that its proposal is
// open and the prompt is not removed.
func activePrompt(ctx context.Context, tx *sql.Tx, id string, seq int) (registry.Prompt, error) {
	if _, err := openProposal(ctx, tx, id); err != nil {
		return registry.Prompt{}, err
	}
	_, p, err := scanPrompt(tx.QueryRowContext(ctx, `SELECT `+promptColumns+` FROM proposal_prompts WHERE proposal_id = ? AND seq = ?`, id, seq))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return registry.Prompt{}, fmt.Errorf("%w: proposal %s has no prompt %d", registry.ErrNotFound, id, seq)
	case err != nil:
		return registry.Prompt{}, err
	case !p.RemovedAt.IsZero():
		return registry.Prompt{}, fmt.Errorf("%w: prompt %d of proposal %s was removed", registry.ErrNotFound, seq, id)
	}
	return p, nil
}

func insertPrompt(ctx context.Context, tx *sql.Tx, id string, p registry.Prompt) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO proposal_prompts (proposal_id, seq, body, author_token_id, author_label, author_user_id, created_at, edited_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, p.Seq, p.Text, p.Author.TokenID, p.Author.Label, p.Author.UserID, formatTime(p.CreatedAt), optionalTime(p.EditedAt))
	if err != nil {
		return fmt.Errorf("insert prompt %d: %w", p.Seq, err)
	}
	return nil
}

// commitPrompts copies the active texts into the old prompts column, which
// an older loadout reads after a rollback, and commits tx.
func commitPrompts(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE proposals SET prompts = (
			SELECT json_group_array(body ORDER BY seq) FROM proposal_prompts WHERE proposal_id = ? AND removed_at = ''
		) WHERE id = ?`, id, id); err != nil {
		return fmt.Errorf("update old prompts column: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// loadPrompts fills Prompts and RemovedPrompts of props in one query.
func (s *Store) loadPrompts(ctx context.Context, props []*registry.Proposal) error {
	if len(props) == 0 {
		return nil
	}
	byID, list, err := idList(props)
	if err != nil {
		return fmt.Errorf("prompts: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+promptColumns+` FROM proposal_prompts
		WHERE proposal_id IN (SELECT value FROM json_each(?)) ORDER BY proposal_id, seq`, list)
	if err != nil {
		return fmt.Errorf("prompts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		id, pr, err := scanPrompt(rows)
		if err != nil {
			return fmt.Errorf("prompts: %w", err)
		}
		p := byID[id]
		if pr.RemovedAt.IsZero() {
			p.Prompts = append(p.Prompts, pr)
		} else {
			p.RemovedPrompts = append(p.RemovedPrompts, pr)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("prompts: %w", err)
	}
	return nil
}

// idList indexes props by id and lists their ids as one JSON array, so a
// query over many proposals keeps one text whatever their number.
func idList(props []*registry.Proposal) (map[string]*registry.Proposal, string, error) {
	byID := make(map[string]*registry.Proposal, len(props))
	ids := make([]string, 0, len(props))
	for _, p := range props {
		byID[p.ID] = p
		ids = append(ids, p.ID)
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, "", err
	}
	return byID, string(list), nil
}

// scanPrompt reads the promptColumns and returns the proposal id with the
// prompt.
func scanPrompt(sc scanner) (string, registry.Prompt, error) {
	var (
		id                       string
		p                        registry.Prompt
		created, edited, removed string
	)
	if err := sc.Scan(&id, &p.Seq, &p.Text, &p.Author.TokenID, &p.Author.Label, &p.Author.UserID, &created,
		&edited, &removed, &p.RemovedBy.TokenID, &p.RemovedBy.Label, &p.RemovedBy.UserID); err != nil {
		return "", registry.Prompt{}, err
	}
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return "", registry.Prompt{}, fmt.Errorf("prompt %d of %s: %w", p.Seq, id, err)
	}
	for _, t := range []struct {
		raw string
		dst *time.Time
	}{{edited, &p.EditedAt}, {removed, &p.RemovedAt}} {
		if t.raw == "" {
			continue
		}
		if *t.dst, err = parseTime(t.raw); err != nil {
			return "", registry.Prompt{}, fmt.Errorf("prompt %d of %s: %w", p.Seq, id, err)
		}
	}
	return id, p, nil
}

// optionalTime formats t, or "" for the zero time, as columns that are
// empty until something happens store it.
func optionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatTime(t)
}
