-- Namespaces: the first segment of a package id. A namespace may have a
-- parent whose packages its tokens can read (a project above its teams).
CREATE TABLE namespaces (
    name       TEXT PRIMARY KEY,
    parent     TEXT REFERENCES namespaces (name),
    kind       TEXT NOT NULL CHECK (kind IN ('team', 'project')),
    created_at TEXT NOT NULL
);

-- Every team that already has tokens or releases becomes a root team, so
-- existing registries keep working unchanged.
INSERT INTO namespaces (name, parent, kind, created_at)
SELECT team, NULL, 'team', strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000000Z'
FROM (SELECT team FROM tokens UNION SELECT team FROM releases);
