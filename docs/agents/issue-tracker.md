# Issue tracker: local files

loadout keeps its work in the repo, not in GitHub Issues.

## Conventions

- **Spec**: one file per feature at `docs/plans/<feature-slug>.md`, committed. This is the same place the `loadout-planner` agent writes its plans, so a spec and a plan for one feature are one file.
- **Tickets**: one file per ticket at `.scratch/<feature-slug>/issues/<NN>-<slug>.md`, numbered from `01` in dependency order. `.scratch/` is ignored by git: tickets are working notes, the plan is the record.
- **Status**: the first lines of a ticket hold `Status: <triage label>` (see `docs/agents/triage-labels.md`), `Blocked by: 02, 03` when it has blockers, and `Claimed by: <name>` once someone works on it.
- **Comments**: append to a `## Comments` section at the bottom of the file.
- **Close**: set `Status: done` and add one line on what was done (commit hash or "won't do, because ...").

## When a skill says "publish to the issue tracker"

A spec goes to `docs/plans/<feature-slug>.md`; a ticket goes to `.scratch/<feature-slug>/issues/`.

## When a skill says "fetch the relevant ticket"

Read the file. A bare number means a ticket in the feature folder being discussed.

## Wayfinding operations

Used by `/wayfinder`.

- **Map**: `.scratch/<effort>/map.md` (Notes / Decisions so far / Fog).
- **Child ticket**: `.scratch/<effort>/issues/NN-<slug>.md` with the question in the body and a `Type:` line (`research`, `prototype`, `grilling` or `task`).
- **Frontier**: open, unblocked and unclaimed tickets in that folder; the lowest number wins.
- **Claim**: add `Claimed by:` as the session's first write.
- **Resolve**: write the answer in the ticket, set `Status: done`, then add a one-line pointer to the map's Decisions so far.
