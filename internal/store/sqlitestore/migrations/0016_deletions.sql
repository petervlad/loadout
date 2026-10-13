-- Requests to delete a package, reviewed like proposals. A row outlives
-- the package: it is the record of who asked, who decided and what went.
-- A new table, so every row from before stays valid; the defaults keep the
-- columns of an open request empty until someone decides.
CREATE TABLE deletions (
    id                  TEXT    PRIMARY KEY,
    team                TEXT    NOT NULL,
    name                TEXT    NOT NULL,
    reason              TEXT    NOT NULL,
    newest              TEXT    NOT NULL DEFAULT '',
    author_token_id     TEXT    NOT NULL,
    author_label        TEXT    NOT NULL,
    author_user_id      TEXT    NOT NULL DEFAULT '',
    status              TEXT    NOT NULL CHECK (status IN ('open', 'approved', 'rejected', 'withdrawn', 'gone')),
    created_at          TEXT    NOT NULL,
    decided_at          TEXT    NOT NULL DEFAULT '',
    decided_by_token_id TEXT    NOT NULL DEFAULT '',
    decided_by_label    TEXT    NOT NULL DEFAULT '',
    decided_by_user_id  TEXT    NOT NULL DEFAULT '',
    decision_note       TEXT    NOT NULL DEFAULT '',
    self_approved       INTEGER NOT NULL DEFAULT 0,
    removed             TEXT    NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX deletions_one_open ON deletions (team, name) WHERE status = 'open';
CREATE INDEX deletions_team_created ON deletions (team, created_at);
