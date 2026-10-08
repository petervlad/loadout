-- Model tiers: per namespace, which model and reasoning effort each AI tool
-- runs for an agent's hint. Teams inherit their project's values. No rows
-- means the built-in defaults, so nothing existing changes.
CREATE TABLE model_tiers (
    namespace TEXT NOT NULL REFERENCES namespaces (name),
    tool      TEXT NOT NULL,
    hint      TEXT NOT NULL CHECK (hint IN ('fast', 'balanced', 'deep')),
    model     TEXT NOT NULL DEFAULT '',
    effort    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (namespace, tool, hint)
);
