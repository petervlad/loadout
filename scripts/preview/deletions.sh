#!/bin/bash
# Adds requests to delete packages to the preview (after seed.sh and
# course.sh, with serve.sh running), one in every state the pages show:
#   del-preview1  open, by bogdan, for lime/code-review (alice may approve; bogdan may withdraw)
#   del-preview2  open, by olga, an owner, for lime/release-notes (olga may approve as owner)
#   del-preview3  open and stale, by carla, for lime/planner (a release came out after it)
#   del-preview4  rejected, del-preview5 withdrawn, del-preview6 approved by alice,
#   del-preview7  approved by olga without review, del-preview8 gone (deleted another way)
# The closed ones name packages that are no longer there: a request outlives its package.
# Re-run seed.sh to start over.
set -euo pipefail
S=${LOADOUT_PREVIEW_DIR:-${TMPDIR:-/tmp}/loadout-preview}
DB=$S/data/registry.db
id() { sqlite3 "$DB" "SELECT id FROM users WHERE email = '$1'"; }
ago() { date -u -v-"$1" +%Y-%m-%dT%H:%M:%S.000000000Z; }
BOGDAN=$(id bogdan@example.com); ALICE=$(id alice@example.com); CARLA=$(id carla@example.com)
OLGA=$(id olga@example.com); DAN=$(id dan@example.com)
[ -n "$OLGA" ] || { echo "run course.sh first: olga owns acme" >&2; exit 1; }
sqlite3 "$DB" <<SQL
DELETE FROM deletions WHERE id LIKE 'del-preview%';
INSERT INTO deletions (id, team, name, reason, newest, author_token_id, author_label, author_user_id, status, created_at) VALUES
 ('del-preview1', 'lime', 'code-review', 'Replaced by the review step in lime/planner. Nobody has installed it since spring, and the prompt still quotes a ticket system we left.', '1.8.2', 'web:$BOGDAN', 'bogdan@example.com on the web', '$BOGDAN', 'open', '$(ago 2d)'),
 ('del-preview2', 'lime', 'release-notes', 'Mine, and the new notes agent in acme does the same.', '0.9.0', 'web:$OLGA', 'olga@example.com on the web', '$OLGA', 'open', '$(ago 5H)'),
 ('del-preview3', 'lime', 'planner', 'Two planners is one too many.', '2.2.0', 'web:$CARLA', 'carla@example.com on the web', '$CARLA', 'open', '$(ago 9d)');
INSERT INTO deletions (id, team, name, reason, newest, author_token_id, author_label, author_user_id, status, created_at,
                       decided_at, decided_by_token_id, decided_by_label, decided_by_user_id, decision_note, self_approved, removed) VALUES
 ('del-preview4', 'lime', 'old-notes', 'Looks unused.', '0.3.0', 'web:$DAN', 'dan@example.com on the web', '$DAN', 'rejected', '$(ago 12d)',
  '$(ago 11d)', 'web:$ALICE', 'alice@example.com on the web', '$ALICE', 'The demo still installs it. Ask again after the demo.', 0, ''),
 ('del-preview5', 'lime', 'draft-agent', 'Never went anywhere.', '', 'web:$BOGDAN', 'bogdan@example.com on the web', '$BOGDAN', 'withdrawn', '$(ago 20d)',
  '$(ago 20d)', 'web:$BOGDAN', 'bogdan@example.com on the web', '$BOGDAN', 'Carla is still using it.', 0, ''),
 ('del-preview6', 'lime', 'scratch', 'A test package from the first week.', '0.2.0', 'web:$BOGDAN', 'bogdan@example.com on the web', '$BOGDAN', 'approved', '$(ago 30d)',
  '$(ago 29d)', 'web:$ALICE', 'alice@example.com on the web', '$ALICE', 'Agreed.', 0, '{"releases":["0.1.0","0.2.0"],"kind":"agent","proposals":1,"reviews":0,"prompts":2}'),
 ('del-preview7', 'lime', 'legacy', 'The team moved to acme/python-standards.', '1.0.0', 'web:$OLGA', 'olga@example.com on the web', '$OLGA', 'approved', '$(ago 40d)',
  '$(ago 40d)', 'web:$OLGA', 'olga@example.com on the web', '$OLGA', 'Nobody else is around this week and it blocks the migration.', 1, '{"releases":["1.0.0"],"kind":"agent","proposals":0,"reviews":0,"prompts":0}'),
 ('del-preview8', 'lime', 'temp', 'Not needed.', '0.1.0', 'web:$BOGDAN', 'bogdan@example.com on the web', '$BOGDAN', 'gone', '$(ago 50d)',
  '$(ago 49d)', '', '', '', '', 0, '');
SQL
echo "requests added: open /t/lime/reviews, /t/lime/deletions/del-preview1 .. 8, /t/lime/p/lime/code-review/delete-request"
