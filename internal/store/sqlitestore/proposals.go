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

var _ registry.ProposalStore = (*Store)(nil)

// proposalColumns are the columns scanProposal reads, in order. Prompts
// come from proposal_prompts; the old prompts column is only written.
const proposalColumns = `id, team, name, version, digest, description, size,
	author_token_id, author_label, author_user_id, note, status, created_at,
	decided_at, decided_by_token_id, decided_by_label, decided_by_user_id, decision_note, kind, self_approved,
	base_version, approved_over, replaces, replaced_by`

// CreateProposal implements registry.ProposalStore. It also writes the
// prompt texts into the old prompts column, so an older loadout reads them
// after a rollback.
func (s *Store) CreateProposal(ctx context.Context, p registry.Proposal, bundle []byte) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create proposal %s: %w", p.ID, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	p.Replaces = "" // only ReplaceProposal links proposals
	if err := insertProposal(ctx, tx, p, bundle); err != nil {
		return fmt.Errorf("create proposal %s: %w", p.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create proposal %s: commit: %w", p.ID, err)
	}
	return nil
}

// ReplaceProposal implements registry.ProposalStore. The transaction holds
// the write lock from BEGIN (see dsn), so of a replace and an approval, or
// two replaces, of one proposal only the first finds it open.
func (s *Store) ReplaceProposal(ctx context.Context, old string, p registry.Proposal, bundle []byte, d registry.Decision) (err error) {
	if d.Status != registry.ProposalWithdrawn {
		return fmt.Errorf("replace proposal %s: %w: the old one closes as withdrawn, not %q", old, registry.ErrInvalid, d.Status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace proposal %s: %w", old, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if _, err := openProposal(ctx, tx, old); err != nil {
		return fmt.Errorf("replace proposal %s: %w", old, err)
	}
	// Closed first, so the new one may keep the old one's version.
	if err := setDecision(ctx, tx, old, d); err != nil {
		return fmt.Errorf("replace proposal %s: %w", old, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE proposals SET replaced_by = ? WHERE id = ?`, p.ID, old); err != nil {
		return fmt.Errorf("replace proposal %s: %w", old, err)
	}
	p.Replaces = old
	if err := insertProposal(ctx, tx, p, bundle); err != nil {
		return fmt.Errorf("replace proposal %s with %s: %w", old, p.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace proposal %s: commit: %w", old, err)
	}
	return nil
}

// insertProposal stores an open proposal, its Base and Replaces, and its
// prompts numbered from 1 in order, each with its author and times.
func insertProposal(ctx context.Context, tx *sql.Tx, p registry.Proposal, bundle []byte) error {
	major, minor, patch, err := versionParts(p.Version)
	if err != nil {
		return err
	}
	prompts, err := json.Marshal(p.PromptTexts())
	if err != nil {
		return err
	}
	if bundle == nil {
		bundle = []byte{} // the column is NOT NULL; nil would bind as NULL
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO proposals
			(id, team, name, version, major, minor, patch, digest, description, size,
			 author_token_id, author_label, author_user_id, note, prompts, status, created_at, bundle, kind, base_version, replaces)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Package.Team(), p.Package.Name(), p.Version.String(), major, minor, patch,
		string(p.Digest), p.Description, p.Size, p.Author.TokenID, p.Author.Label, p.Author.UserID,
		p.Note, string(prompts), string(p.Status), formatTime(p.CreatedAt), bundle, string(p.Kind), p.Base.String(), p.Replaces)
	if isUniqueViolation(err) {
		// The primary key is random, so the partial index on open
		// proposals is what a real conflict hits.
		return fmt.Errorf("%w: %s@%s already has an open proposal", registry.ErrVersionConflict, p.Package, p.Version)
	}
	if err != nil {
		return err
	}
	for i, pr := range p.Prompts {
		pr.Seq = i + 1
		if err := insertPrompt(ctx, tx, p.ID, pr); err != nil {
			return err
		}
	}
	return nil
}

// Proposal implements registry.ProposalStore.
func (s *Store) Proposal(ctx context.Context, id string) (registry.Proposal, error) {
	p, err := proposalByID(ctx, s.db, id)
	if err != nil {
		return registry.Proposal{}, fmt.Errorf("proposal %s: %w", id, err)
	}
	if err := s.loadDetails(ctx, []*registry.Proposal{&p}); err != nil {
		return registry.Proposal{}, fmt.Errorf("proposal %s: %w", id, err)
	}
	return p, nil
}

// ProposalBundle implements registry.ProposalStore.
func (s *Store) ProposalBundle(ctx context.Context, id string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT bundle FROM proposals WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("proposal bundle %s: %w", id, registry.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("proposal bundle %s: %w", id, err)
	}
	return data, nil
}

// Proposals implements registry.ProposalStore. Prompts and review
// summaries come in one more query each, whatever the length of the list.
func (s *Store) Proposals(ctx context.Context, f registry.ProposalFilter) ([]registry.Proposal, error) {
	name := ""
	if !f.Package.IsZero() {
		name = f.Package.Name()
	}
	// An empty name or status matches every row.
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+proposalColumns+` FROM proposals
		WHERE team = ? AND (? = '' OR name = ?) AND (? = '' OR status = ?)
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`,
		f.Team, name, name, string(f.Status), string(f.Status), f.Limit)
	if err != nil {
		return nil, fmt.Errorf("proposals of %s: %w", f.Team, err)
	}
	defer rows.Close()
	var out []registry.Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("proposals of %s: %w", f.Team, err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proposals of %s: %w", f.Team, err)
	}
	ptrs := make([]*registry.Proposal, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	if err := s.loadDetails(ctx, ptrs); err != nil {
		return nil, fmt.Errorf("proposals of %s: %w", f.Team, err)
	}
	return out, nil
}

// AddReview implements registry.ProposalStore.
func (s *Store) AddReview(ctx context.Context, id string, r registry.Review, text, data []byte, limit int) (_ registry.Review, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.Review{}, fmt.Errorf("add review to %s: %w", id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if _, err := openProposal(ctx, tx, id); err != nil {
		return registry.Review{}, fmt.Errorf("add review to %s: %w", id, err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM proposal_reviews WHERE proposal_id = ?`, id).Scan(&n); err != nil {
		return registry.Review{}, fmt.Errorf("add review to %s: %w", id, err)
	}
	if n >= limit {
		return registry.Review{}, fmt.Errorf("add review to %s: %w: it already has %d reviews", id, registry.ErrInvalid, n)
	}
	r.Seq = n + 1
	if text == nil {
		text = []byte{}
	}
	if data == nil {
		data = []byte{}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO proposal_reviews (proposal_id, seq, author_token_id, author_label, author_user_id, created_at, body, data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, r.Seq, r.Author.TokenID, r.Author.Label, r.Author.UserID, formatTime(r.CreatedAt), text, data); err != nil {
		return registry.Review{}, fmt.Errorf("add review to %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return registry.Review{}, fmt.Errorf("add review to %s: commit: %w", id, err)
	}
	return r, nil
}

// ReviewText implements registry.ProposalStore.
func (s *Store) ReviewText(ctx context.Context, id string, seq int) (registry.Review, []byte, error) {
	return s.reviewColumn(ctx, id, seq, reviewBodyQuery)
}

// ReviewData implements registry.ProposalStore.
func (s *Store) ReviewData(ctx context.Context, id string, seq int) (registry.Review, []byte, error) {
	r, data, err := s.reviewColumn(ctx, id, seq, reviewDataQuery)
	if err == nil && len(data) == 0 {
		return registry.Review{}, nil, fmt.Errorf("review data %d of %s: %w", seq, id, registry.ErrNotFound)
	}
	return r, data, err
}

// The two queries of reviewColumn: one review with its text, or with its data.
const (
	reviewBodyQuery = `SELECT seq, author_token_id, author_label, author_user_id, created_at, length(body), length(data), body
		FROM proposal_reviews WHERE proposal_id = ? AND seq = ?`
	reviewDataQuery = `SELECT seq, author_token_id, author_label, author_user_id, created_at, length(body), length(data), data
		FROM proposal_reviews WHERE proposal_id = ? AND seq = ?`
)

// reviewColumn returns one review and the column that query selects last.
func (s *Store) reviewColumn(ctx context.Context, id string, seq int, query string) (registry.Review, []byte, error) {
	var (
		r       registry.Review
		created string
		out     []byte
	)
	err := s.db.QueryRowContext(ctx, query, id, seq).
		Scan(&r.Seq, &r.Author.TokenID, &r.Author.Label, &r.Author.UserID, &created, &r.Size, &r.DataSize, &out)
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Review{}, nil, fmt.Errorf("review %d of %s: %w", seq, id, registry.ErrNotFound)
	}
	if err != nil {
		return registry.Review{}, nil, fmt.Errorf("review %d of %s: %w", seq, id, err)
	}
	if r.CreatedAt, err = parseTime(created); err != nil {
		return registry.Review{}, nil, fmt.Errorf("review %d of %s: %w", seq, id, err)
	}
	return r, out, nil
}

// DecideProposal implements registry.ProposalStore.
func (s *Store) DecideProposal(ctx context.Context, id string, d registry.Decision) (_ registry.Proposal, err error) {
	if d.Status != registry.ProposalRejected && d.Status != registry.ProposalWithdrawn {
		return registry.Proposal{}, fmt.Errorf("decide proposal %s: %w: cannot close a proposal as %q", id, registry.ErrInvalid, d.Status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.Proposal{}, fmt.Errorf("decide proposal %s: %w", id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	p, err := openProposal(ctx, tx, id)
	if err != nil {
		return registry.Proposal{}, fmt.Errorf("decide proposal %s: %w", id, err)
	}
	if err := setDecision(ctx, tx, id, d); err != nil {
		return registry.Proposal{}, fmt.Errorf("decide proposal %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return registry.Proposal{}, fmt.Errorf("decide proposal %s: commit: %w", id, err)
	}
	return s.withDecision(ctx, p, d)
}

// ApproveProposal implements registry.ProposalStore. The transaction holds
// SQLite's write lock from BEGIN (see dsn), so the version check and the
// insert cannot interleave with a concurrent publish.
func (s *Store) ApproveProposal(ctx context.Context, id string, d registry.Decision) (_ registry.Proposal, _ registry.Release, err error) {
	d.Status = registry.ProposalApproved
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: %w", id, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	p, err := openProposal(ctx, tx, id)
	if err != nil {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: %w", id, err)
	}
	if err := checkAboveLatest(ctx, tx, p.Package, p.Version); err != nil {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: %w", id, err)
	}
	latest, err := latestRelease(ctx, tx, p.Package)
	if err != nil {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: %w", id, err)
	}
	if !latest.Equal(d.Latest) {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: %w: the latest release of %s is %q, not %q", id, registry.ErrStale, p.Package, latest, d.Latest)
	}

	major, minor, patch, err := versionParts(p.Version)
	if err != nil {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: %w", id, err)
	}
	var seq int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO releases
			(team, name, version, major, minor, patch, digest, description, size, published_at, bundle, kind, proposal_id, self_approved,
			 published_by_token_id, published_by_label, published_by_user_id)
		SELECT team, name, version, major, minor, patch, digest, description, size, ?, bundle, kind, id, ?, ?, ?, ?
		FROM proposals WHERE id = ?
		RETURNING seq`,
		formatTime(d.At), d.SelfApproved, d.By.TokenID, d.By.Label, d.By.UserID, id).Scan(&seq)
	if isUniqueViolation(err) {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: %w", id, registry.ErrVersionConflict)
	}
	if err != nil {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: insert release %d.%d.%d: %w", id, major, minor, patch, err)
	}
	if err := setDecision(ctx, tx, id, d); err != nil {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("approve proposal %s: commit: %w", id, err)
	}

	rel := registry.Release{
		ID:           p.Package,
		Version:      p.Version,
		Digest:       p.Digest,
		Description:  p.Description,
		Kind:         p.Kind,
		Size:         p.Size,
		PublishedAt:  d.At.UTC(),
		Seq:          seq,
		Proposal:     p.ID,
		SelfApproved: d.SelfApproved,
		PublishedBy:  d.By,
	}
	p, err = s.withDecision(ctx, p, d)
	if err != nil {
		return registry.Proposal{}, registry.Release{}, err
	}
	return p, rel, nil
}

// withDecision returns p as closed by d, with its reviews loaded.
func (s *Store) withDecision(ctx context.Context, p registry.Proposal, d registry.Decision) (registry.Proposal, error) {
	p.Status = d.Status
	p.DecidedAt = d.At.UTC()
	p.DecidedBy = d.By
	p.DecisionNote = d.Note
	p.SelfApproved = d.SelfApproved
	p.ApprovedOver = d.Over
	if err := s.loadDetails(ctx, []*registry.Proposal{&p}); err != nil {
		return registry.Proposal{}, fmt.Errorf("proposal %s: %w", p.ID, err)
	}
	return p, nil
}

func setDecision(ctx context.Context, tx *sql.Tx, id string, d registry.Decision) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE proposals
		SET status = ?, decided_at = ?, decided_by_token_id = ?, decided_by_label = ?, decided_by_user_id = ?, decision_note = ?, self_approved = ?,
		    approved_over = ?
		WHERE id = ?`,
		string(d.Status), formatTime(d.At), d.By.TokenID, d.By.Label, d.By.UserID, d.Note, d.SelfApproved, d.Over.String(), id)
	return err
}

func proposalByID(ctx context.Context, q queryer, id string) (registry.Proposal, error) {
	p, err := scanProposal(q.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM proposals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Proposal{}, registry.ErrNotFound
	}
	return p, err
}

// openProposal reads a proposal inside tx and checks that it is open.
func openProposal(ctx context.Context, tx *sql.Tx, id string) (registry.Proposal, error) {
	p, err := proposalByID(ctx, tx, id)
	if err != nil {
		return registry.Proposal{}, err
	}
	if p.Status != registry.ProposalOpen {
		return registry.Proposal{}, fmt.Errorf("%w: it is %s", registry.ErrProposalClosed, p.Status)
	}
	return p, nil
}

// loadDetails fills the prompts and review summaries of props.
func (s *Store) loadDetails(ctx context.Context, props []*registry.Proposal) error {
	if err := s.loadPrompts(ctx, props); err != nil {
		return err
	}
	return s.loadReviews(ctx, props)
}

// loadReviews fills Reviews of props, without their text, in one query.
func (s *Store) loadReviews(ctx context.Context, props []*registry.Proposal) error {
	if len(props) == 0 {
		return nil
	}
	byID, list, err := idList(props)
	if err != nil {
		return fmt.Errorf("reviews: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT proposal_id, seq, author_token_id, author_label, author_user_id, created_at, length(body), length(data)
		FROM proposal_reviews WHERE proposal_id IN (SELECT value FROM json_each(?)) ORDER BY proposal_id, seq`, list)
	if err != nil {
		return fmt.Errorf("reviews: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, created string
			r           registry.Review
		)
		if err := rows.Scan(&id, &r.Seq, &r.Author.TokenID, &r.Author.Label, &r.Author.UserID, &created, &r.Size, &r.DataSize); err != nil {
			return fmt.Errorf("reviews: %w", err)
		}
		if r.CreatedAt, err = parseTime(created); err != nil {
			return fmt.Errorf("reviews: %w", err)
		}
		byID[id].Reviews = append(byID[id].Reviews, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reviews: %w", err)
	}
	return nil
}

func scanProposal(sc scanner) (registry.Proposal, error) {
	var (
		p                           registry.Proposal
		team, name, version, digest string
		status                      string
		created, decided            string
		kind                        string
		size                        int64
		base, over                  string
	)
	err := sc.Scan(&p.ID, &team, &name, &version, &digest, &p.Description, &size,
		&p.Author.TokenID, &p.Author.Label, &p.Author.UserID, &p.Note, &status, &created,
		&decided, &p.DecidedBy.TokenID, &p.DecidedBy.Label, &p.DecidedBy.UserID, &p.DecisionNote, &kind, &p.SelfApproved,
		&base, &over, &p.Replaces, &p.ReplacedBy)
	p.Kind = pack.Kind(kind)
	if err != nil {
		return registry.Proposal{}, err
	}
	if p.Package, err = pack.NewID(team, name); err != nil {
		return registry.Proposal{}, fmt.Errorf("stored proposal %s: %w", p.ID, err)
	}
	if p.Version, err = semver.Parse(version); err != nil {
		return registry.Proposal{}, fmt.Errorf("stored proposal %s: %w", p.ID, err)
	}
	p.Status = registry.ProposalStatus(status)
	if !p.Status.Valid() {
		return registry.Proposal{}, fmt.Errorf("stored proposal %s: unknown status %q", p.ID, status)
	}
	if p.CreatedAt, err = parseTime(created); err != nil {
		return registry.Proposal{}, fmt.Errorf("stored proposal %s: %w", p.ID, err)
	}
	if decided != "" {
		if p.DecidedAt, err = parseTime(decided); err != nil {
			return registry.Proposal{}, fmt.Errorf("stored proposal %s: %w", p.ID, err)
		}
	}
	if p.Base, err = registry.ParseBase(base); err != nil {
		return registry.Proposal{}, fmt.Errorf("stored proposal %s: %w", p.ID, err)
	}
	if p.ApprovedOver, err = registry.ParseBase(over); err != nil {
		return registry.Proposal{}, fmt.Errorf("stored proposal %s: approved over: %w", p.ID, err)
	}
	p.Digest = pack.Digest(digest)
	p.Size = int(size)
	return p, nil
}
