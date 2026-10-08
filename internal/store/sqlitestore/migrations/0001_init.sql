-- Releases: one row per published package version. seq only grows and
-- orders releases across the whole registry.
CREATE TABLE releases (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    team         TEXT    NOT NULL,
    name         TEXT    NOT NULL,
    version      TEXT    NOT NULL,
    major        INTEGER NOT NULL,
    minor        INTEGER NOT NULL,
    patch        INTEGER NOT NULL,
    digest       TEXT    NOT NULL,
    description  TEXT    NOT NULL,
    size         INTEGER NOT NULL,
    published_at TEXT    NOT NULL,
    bundle       BLOB    NOT NULL,
    UNIQUE (team, name, major, minor, patch)
);

-- The UNIQUE constraint above already indexes (team, name, ...) lookups.
CREATE INDEX releases_team_seq ON releases (team, seq);

-- Tokens: only the hash of the secret is stored.
CREATE TABLE tokens (
    id         TEXT PRIMARY KEY,
    hash       BLOB NOT NULL UNIQUE,
    team       TEXT NOT NULL,
    scope      TEXT NOT NULL,
    label      TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX tokens_team_created ON tokens (team, created_at);
