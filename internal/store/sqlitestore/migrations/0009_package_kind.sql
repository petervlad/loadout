-- What a package contains: agent, skill or kb. Every release and proposal
-- made before skills existed is an agent.
ALTER TABLE releases ADD COLUMN kind TEXT NOT NULL DEFAULT 'agent';
ALTER TABLE proposals ADD COLUMN kind TEXT NOT NULL DEFAULT 'agent';
