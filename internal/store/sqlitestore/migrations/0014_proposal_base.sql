-- What each proposal was made from (its base), and the links a later
-- approval over a newer release or a replacement after a rebase records.
-- Empty means none, so old rows stay valid.
ALTER TABLE proposals ADD COLUMN base_version  TEXT NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN approved_over TEXT NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN replaces      TEXT NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN replaced_by   TEXT NOT NULL DEFAULT '';

-- Who published a release. Empty means nobody is known: every direct
-- publish before this migration.
ALTER TABLE releases ADD COLUMN published_by_token_id TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN published_by_label    TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN published_by_user_id  TEXT NOT NULL DEFAULT '';

-- A release approved from a proposal was published by whoever approved it,
-- and the proposal still knows who that was.
UPDATE releases SET
    published_by_token_id = (SELECT p.decided_by_token_id FROM proposals p WHERE p.id = releases.proposal_id),
    published_by_label    = (SELECT p.decided_by_label    FROM proposals p WHERE p.id = releases.proposal_id),
    published_by_user_id  = (SELECT p.decided_by_user_id  FROM proposals p WHERE p.id = releases.proposal_id)
WHERE proposal_id != '' AND EXISTS (SELECT 1 FROM proposals p WHERE p.id = releases.proposal_id);

-- Every proposal so far was made when the latest release was the newest
-- one published before it; releases only grow, so that is its base. Times
-- are fixed-width UTC text, so comparing them as text is exact.
UPDATE proposals SET base_version = COALESCE((
    SELECT r.version FROM releases r
    WHERE r.team = proposals.team AND r.name = proposals.name
      AND r.published_at <= proposals.created_at
    ORDER BY r.seq DESC LIMIT 1), '');
