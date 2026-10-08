-- Prompts become a list people edit on a proposal: anyone who reads it adds
-- prompts, and only the person who added one edits or removes it. A removed
-- prompt keeps its row and its number, so a stale form never removes a
-- newer prompt that took the same number.
CREATE TABLE proposal_prompts (
    proposal_id         TEXT    NOT NULL REFERENCES proposals (id),
    seq                 INTEGER NOT NULL,
    body                TEXT    NOT NULL,
    author_token_id     TEXT    NOT NULL,
    author_label        TEXT    NOT NULL,
    author_user_id      TEXT    NOT NULL,
    created_at          TEXT    NOT NULL,
    edited_at           TEXT    NOT NULL DEFAULT '',
    removed_at          TEXT    NOT NULL DEFAULT '',
    removed_by_token_id TEXT    NOT NULL DEFAULT '',
    removed_by_label    TEXT    NOT NULL DEFAULT '',
    removed_by_user_id  TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (proposal_id, seq)
);

-- Every prompt stored so far was the author's, given when proposing. The
-- proposals.prompts column stays and is still kept up to date with the
-- active texts, so an older loadout after a rollback reads them.
INSERT INTO proposal_prompts (proposal_id, seq, body, author_token_id, author_label, author_user_id, created_at)
SELECT p.id, CAST(j.key AS INTEGER) + 1, j.value, p.author_token_id, p.author_label, p.author_user_id, p.created_at
FROM proposals AS p, json_each(p.prompts) AS j;
