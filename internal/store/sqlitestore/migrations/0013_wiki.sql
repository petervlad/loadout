-- A wiki page per namespace, off by default so nothing changes for the
-- namespaces that exist. Every save is a revision, numbered per namespace.
ALTER TABLE namespaces ADD COLUMN wiki INTEGER NOT NULL DEFAULT 0;

CREATE TABLE wiki_revisions (
    namespace      TEXT    NOT NULL REFERENCES namespaces (name),
    seq            INTEGER NOT NULL CHECK (seq > 0),
    format         TEXT    NOT NULL DEFAULT 'md' CHECK (format IN ('md', 'mdx')),
    body           TEXT    NOT NULL,
    size           INTEGER NOT NULL,
    author_user_id TEXT    NOT NULL,
    author_label   TEXT    NOT NULL,
    note           TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    NOT NULL,
    PRIMARY KEY (namespace, seq)
);
