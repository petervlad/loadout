-- Wrong codes per user and purpose, kept for a day: enough of them lock code
-- entry for the user, so asking for new codes does not reset the count.
CREATE TABLE code_failures (
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose TEXT NOT NULL,
    at      TEXT NOT NULL
);

CREATE INDEX code_failures_user ON code_failures (user_id, purpose, at);
CREATE INDEX code_failures_at ON code_failures (at);
