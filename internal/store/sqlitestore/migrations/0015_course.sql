-- The knowledge base a namespace's wiki shows as its course, as
-- "team/name"; empty means none of its own, so every namespace that
-- exists keeps showing what it shows today: nothing.
ALTER TABLE namespaces ADD COLUMN course TEXT NOT NULL DEFAULT '';
