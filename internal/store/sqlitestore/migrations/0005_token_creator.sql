-- A machine token made by a user through their account (for CI or a shared
-- machine) records who made it, so four eyes treat it as that user. NULL for
-- login tokens and for tokens made on the registry host.
ALTER TABLE tokens ADD COLUMN created_by_user_id TEXT;
