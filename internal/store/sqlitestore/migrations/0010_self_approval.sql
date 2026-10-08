-- An owner may approve their own proposal, which releases it without a
-- second person's review; the proposal and the release it became say so.
-- Every earlier approval had a second person, and earlier releases say
-- nothing about the proposal they came from.
ALTER TABLE proposals ADD COLUMN self_approved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE releases ADD COLUMN proposal_id TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN self_approved INTEGER NOT NULL DEFAULT 0;
