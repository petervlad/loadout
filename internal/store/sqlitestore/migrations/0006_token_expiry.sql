-- Tokens from `loadout login` expire; NULL means the token never does.
-- Login tokens issued before this step get the full lifetime from now.
ALTER TABLE tokens ADD COLUMN expires_at TEXT;

UPDATE tokens
SET expires_at = strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now', '+90 days')
WHERE user_id <> '';
