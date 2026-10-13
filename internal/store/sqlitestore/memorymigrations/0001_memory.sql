-- Team memory lives in its own file, so a memory bug or migration cannot
-- damage packages. It holds the switch, the memory tokens, the entries with
-- their history and links, the full-text index and the purge records.
CREATE TABLE memory_spaces (
    namespace  TEXT PRIMARY KEY,
    enabled    INTEGER NOT NULL DEFAULT 0,
    changed_by TEXT    NOT NULL,
    changed_at TEXT    NOT NULL
);

CREATE TABLE memory_tokens (
    id         TEXT PRIMARY KEY,
    hash       BLOB NOT NULL UNIQUE,
    user_id    TEXT NOT NULL,
    label      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE TABLE entries (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace      TEXT    NOT NULL,
    type           TEXT    NOT NULL CHECK (type IN ('feedback', 'project', 'reference')),
    name           TEXT    NOT NULL,
    description    TEXT    NOT NULL,
    body           TEXT    NOT NULL CHECK (length(CAST(body AS BLOB)) <= 4096),
    status         TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retracted')),
    author_user_id TEXT    NOT NULL,
    author_label   TEXT    NOT NULL,
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL,
    confirmed_at   TEXT    NOT NULL,
    version        INTEGER NOT NULL DEFAULT 1,
    uses           INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE entry_versions (
    entry_id    INTEGER NOT NULL REFERENCES entries (id) ON DELETE CASCADE,
    version     INTEGER NOT NULL,
    action      TEXT    NOT NULL CHECK (action IN ('save', 'update', 'confirm', 'retract', 'move')),
    namespace   TEXT    NOT NULL,
    type        TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL,
    body        TEXT    NOT NULL,
    links       TEXT    NOT NULL DEFAULT '[]',
    by_user_id  TEXT    NOT NULL,
    by_label    TEXT    NOT NULL,
    note        TEXT    NOT NULL DEFAULT '',
    at          TEXT    NOT NULL,
    PRIMARY KEY (entry_id, version)
);

CREATE TABLE entry_links (
    from_id INTEGER NOT NULL REFERENCES entries (id) ON DELETE CASCADE,
    to_id   INTEGER NOT NULL REFERENCES entries (id) ON DELETE CASCADE,
    PRIMARY KEY (from_id, to_id)
);

CREATE TABLE purges (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id  INTEGER NOT NULL,
    namespace TEXT    NOT NULL,
    versions  INTEGER NOT NULL,
    at        TEXT    NOT NULL
);

-- External content: the index holds terms only, the text stays in entries.
CREATE VIRTUAL TABLE entries_fts USING fts5(
    name, description, body,
    content = 'entries', content_rowid = 'id',
    tokenize = 'porter unicode61 remove_diacritics 2'
);

CREATE INDEX entries_by_namespace ON entries (namespace, status, updated_at);
CREATE INDEX entry_versions_by_user ON entry_versions (by_user_id, at);
CREATE INDEX memory_tokens_by_user ON memory_tokens (user_id);

-- The documented way to keep an external-content index in step: a change
-- removes the old terms with the 'delete' command, then adds the new ones.
CREATE TRIGGER entries_fts_insert AFTER INSERT ON entries BEGIN
    INSERT INTO entries_fts (rowid, name, description, body)
    VALUES (new.id, new.name, new.description, new.body);
END;

CREATE TRIGGER entries_fts_delete AFTER DELETE ON entries BEGIN
    INSERT INTO entries_fts (entries_fts, rowid, name, description, body)
    VALUES ('delete', old.id, old.name, old.description, old.body);
END;

CREATE TRIGGER entries_fts_update AFTER UPDATE OF name, description, body ON entries BEGIN
    INSERT INTO entries_fts (entries_fts, rowid, name, description, body)
    VALUES ('delete', old.id, old.name, old.description, old.body);
    INSERT INTO entries_fts (rowid, name, description, body)
    VALUES (new.id, new.name, new.description, new.body);
END;
