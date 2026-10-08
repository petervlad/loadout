-- Proposals: candidate package versions waiting for review. A proposal is
-- not a release; approving one copies it into releases in one transaction.
CREATE TABLE proposals (
    id                  TEXT    PRIMARY KEY,
    team                TEXT    NOT NULL,
    name                TEXT    NOT NULL,
    version             TEXT    NOT NULL,
    major               INTEGER NOT NULL,
    minor               INTEGER NOT NULL,
    patch               INTEGER NOT NULL,
    digest              TEXT    NOT NULL,
    description         TEXT    NOT NULL,
    size                INTEGER NOT NULL,
    author_token_id     TEXT    NOT NULL,
    author_label        TEXT    NOT NULL,
    note                TEXT    NOT NULL,
    prompts             TEXT    NOT NULL, -- JSON array of strings
    status              TEXT    NOT NULL CHECK (status IN ('open', 'approved', 'rejected', 'withdrawn')),
    created_at          TEXT    NOT NULL,
    decided_at          TEXT    NOT NULL DEFAULT '',
    decided_by_token_id TEXT    NOT NULL DEFAULT '',
    decided_by_label    TEXT    NOT NULL DEFAULT '',
    decision_note       TEXT    NOT NULL DEFAULT '',
    bundle              BLOB    NOT NULL
);

-- At most one open proposal per package version.
CREATE UNIQUE INDEX proposals_one_open ON proposals (team, name, major, minor, patch) WHERE status = 'open';

CREATE INDEX proposals_team_created ON proposals (team, created_at);

-- Review results attached to a proposal, numbered from 1.
CREATE TABLE proposal_reviews (
    proposal_id     TEXT    NOT NULL REFERENCES proposals (id),
    seq             INTEGER NOT NULL,
    author_token_id TEXT    NOT NULL,
    author_label    TEXT    NOT NULL,
    created_at      TEXT    NOT NULL,
    body            BLOB    NOT NULL,
    PRIMARY KEY (proposal_id, seq)
);
