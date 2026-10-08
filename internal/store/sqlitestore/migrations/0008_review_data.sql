-- A comparison report can carry its result as JSON next to the Markdown,
-- so the web pages show the answers side by side. Empty means text only.
-- Reviews also remember the user behind the token, like proposals do.
ALTER TABLE proposal_reviews ADD COLUMN data BLOB NOT NULL DEFAULT x'';
ALTER TABLE proposal_reviews ADD COLUMN author_user_id TEXT NOT NULL DEFAULT '';
