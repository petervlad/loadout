-- User accounts on top of namespace tokens. Tokens keep working as before;
-- a token issued by `loadout login` also records the user it belongs to.
ALTER TABLE tokens ADD COLUMN user_id TEXT NOT NULL DEFAULT '';

CREATE INDEX tokens_user ON tokens (user_id) WHERE user_id <> '';

-- A namespace can require every version to go through propose and approve.
ALTER TABLE namespaces ADD COLUMN require_review INTEGER NOT NULL DEFAULT 0;

-- Proposals remember the users behind the tokens, so four eyes compare
-- people, not only tokens.
ALTER TABLE proposals ADD COLUMN author_user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE proposals ADD COLUMN decided_by_user_id TEXT NOT NULL DEFAULT '';

-- Users: email is stored trimmed and lowercased. password_hash is an
-- argon2id PHC string, NULL for a user without a password (Google only, or
-- added by an admin before signing up). email_verified_at is NULL until the
-- address is confirmed. is_admin makes the user an admin of the registry.
CREATE TABLE users (
    id                TEXT    PRIMARY KEY,
    email             TEXT    NOT NULL UNIQUE,
    name              TEXT    NOT NULL DEFAULT '',
    password_hash     TEXT,
    google_subject    TEXT    UNIQUE,
    email_verified_at TEXT,
    is_admin          INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT    NOT NULL
);

-- Memberships: which users belong to a namespace, and with which role.
-- owner is only given on project namespaces (checked by the service).
CREATE TABLE memberships (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    namespace  TEXT NOT NULL REFERENCES namespaces (name),
    role       TEXT NOT NULL CHECK (role IN ('member', 'maintainer', 'owner')),
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, namespace)
);

CREATE INDEX memberships_namespace ON memberships (namespace);

-- Web sessions: only the SHA-256 of the session id is stored.
CREATE TABLE sessions (
    hash       BLOB PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

-- Challenges: one emailed code (and the magic link sent with it). Code and
-- link are stored hashed. password_hash is the password a signup sets once
-- the address is verified, so an unverified signup never changes a user.
CREATE TABLE challenges (
    id            TEXT    PRIMARY KEY,
    user_id       TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose       TEXT    NOT NULL CHECK (purpose IN ('verify-signup', 'login')),
    code_hash     BLOB    NOT NULL,
    link_hash     BLOB    NOT NULL UNIQUE,
    password_hash TEXT,
    attempts      INTEGER NOT NULL DEFAULT 0,
    used          INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL,
    expires_at    TEXT    NOT NULL
);

CREATE INDEX challenges_expires ON challenges (expires_at);
