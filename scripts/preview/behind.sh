#!/bin/bash
# Adds proposals that are behind the latest release (needs skills.sh run
# and serve.sh running): the skill proposal 1.1.0, made from 1.0.0, gets
# 1.0.1 (by Carla) and 1.0.2 (publisher unknown, as before releases
# recorded one) released after it; and lime/code-review 1.9.0 is proposed
# from 1.8.2, then 1.9.0 is published directly, which blocks it; and
# lime/release-notes 1.0.0, proposed from 0.9.0, falls behind 0.9.1, is
# rebased and proposed again, which replaces it (a replaced pair).
set -euo pipefail
S=${LOADOUT_PREVIEW_DIR:-${TMPDIR:-/tmp}/loadout-preview}
L=$S/loadout; export HOME=$S/home
P=$S/pk; DB=$S/data/registry.db
rm -rf "$P/rn-release" "$P/cr-propose" "$P/cr-release" "$P/rel-propose" "$P/rel-release"

skill() { # version style-line skill-step
  local d=$P/rn-release
  mkdir -p "$d/references"
  printf 'name: lime/release-notes-skill\nkind: skill\nversion: %s\ndescription: Writes release notes from merged pull requests\n' "$1" >"$d/loadout.yaml"
  cat >"$d/SKILL.md" <<Y
---
name: release-notes-skill
description: Write release notes from the pull requests merged since the last tag. Use when asked for release notes or a changelog.
---
# Release notes

1. List the PRs merged since the last tag.
2. Group them as Features, Fixes and Internal (see references/style.md).
3. One line per PR, with its ticket.
$3
Y
  printf '# Style\n\n%s\n' "$2" >"$d/references/style.md"
  printf '# Template\n\n## Features\n\n## Fixes\n' >"$d/references/template.md"
  $L publish "$d" --remote lime >/dev/null
}
skill 1.0.1 'Plain words. No marketing. Past tense.' '4. Put breaking changes first.'
skill 1.0.2 'Plain words. No marketing. Past tense.' '4. Put breaking changes first, under their own heading.'

agent() { # dir version body
  mkdir -p "$1"
  printf 'name: lime/code-review\nkind: agent\nversion: %s\ndescription: Adversarial review of a branch against its ticket\n' "$2" >"$1/loadout.yaml"
  printf -- '---\nname: code-review\ndescription: Adversarial review of a branch against its ticket\nmodel: balanced\ntools: [read, search]\n---\n%s\n' "$3" >"$1/agent.md"
}
agent "$P/cr-propose" 1.9.0 'Review the branch against the ticket. Name the line of every finding.'
$L propose "$P/cr-propose" --remote lime --note "Findings name their line" --prompt "Review INFOTM-1234" >/dev/null
agent "$P/cr-release" 1.9.0 'Review the branch against the ticket. Start with the riskiest file.'
$L publish "$P/cr-release" --remote lime >/dev/null

notes() { # dir version body [model]
  mkdir -p "$1"
  printf 'name: lime/release-notes\nkind: agent\nversion: %s\ndescription: Drafts release notes from merged pull requests\n' "$2" >"$1/loadout.yaml"
  printf -- '---\nname: release-notes\ndescription: Drafts release notes from merged pull requests\nmodel: %s\ntools: [read, search]\n---\n%s\n' "${4:-balanced}" "$3" >"$1/agent.md"
}
notes "$P/rel-propose" 1.0.0 'Draft release notes.
Group them by ticket.'
$L propose "$P/rel-propose" --remote lime --note "Group notes by ticket" --prompt "Draft notes for 2.4.0" \
  --prompt "Draft notes for a release with only fixes" >/dev/null
notes "$P/rel-release" 0.9.1 'Draft release notes.' fast
$L publish "$P/rel-release" --remote lime >/dev/null
OLD=$(sqlite3 "$DB" "SELECT id FROM proposals WHERE name = 'release-notes'")
$L rebase "$P/rel-propose" --remote lime >/dev/null
$L propose "$P/rel-propose" --remote lime --note "Group notes by ticket, in past tense" >/dev/null

id() { sqlite3 "$DB" "SELECT id FROM users WHERE email = '$1'"; }
ago() { date -u -v"$1" +%Y-%m-%dT%H:%M:%S.000000000Z; }
sqlite3 "$DB" <<Y
UPDATE proposals SET author_user_id = '$(id bogdan@acme.example)', author_label = 'bogdan@acme.example via loadout login', created_at = '$(ago -3d)' WHERE name = 'release-notes-skill';
UPDATE proposals SET author_user_id = '$(id alice@acme.example)', author_label = 'alice@acme.example via loadout login', created_at = '$(ago -5H)' WHERE name = 'code-review';
UPDATE releases SET published_at = '$(ago -4d)' WHERE name = 'release-notes-skill' AND version = '1.0.0';
UPDATE releases SET published_at = '$(ago -2d)', published_by_user_id = '$(id carla@acme.example)', published_by_label = 'carla@acme.example via loadout login' WHERE name = 'release-notes-skill' AND version = '1.0.1';
UPDATE releases SET published_at = '$(ago -2H)', published_by_token_id = '', published_by_label = '', published_by_user_id = '' WHERE name = 'release-notes-skill' AND version = '1.0.2';
UPDATE releases SET published_at = '$(ago -1H)', published_by_user_id = '$(id carla@acme.example)', published_by_label = 'carla@acme.example via loadout login' WHERE name = 'code-review' AND version = '1.9.0';
UPDATE proposals SET author_user_id = '$(id bogdan@acme.example)', author_label = 'bogdan@acme.example via loadout login', created_at = '$(ago -2d)', decided_by_user_id = '$(id bogdan@acme.example)', decided_by_label = 'bogdan@acme.example via loadout login', decided_at = '$(ago -30M)' WHERE id = '$OLD';
UPDATE proposals SET author_user_id = '$(id bogdan@acme.example)', author_label = 'bogdan@acme.example via loadout login', created_at = '$(ago -30M)' WHERE name = 'release-notes' AND id != '$OLD';
UPDATE proposal_prompts SET author_user_id = '$(id bogdan@acme.example)', author_label = 'bogdan@acme.example via loadout login', created_at = '$(ago -2d)' WHERE body = 'Draft notes for 2.4.0';
UPDATE proposal_prompts SET author_user_id = '$(id carla@acme.example)', author_label = 'carla@acme.example via loadout login', created_at = '$(ago -1d)' WHERE body = 'Draft notes for a release with only fixes'; -- Carla added it on the web; it came along
UPDATE releases SET published_at = '$(ago -3H)', published_by_user_id = '$(id alice@acme.example)', published_by_label = 'alice@acme.example via loadout login' WHERE name = 'release-notes' AND version = '0.9.1';
Y
echo "replaced: $OLD"
echo "replacement: $(sqlite3 "$DB" "SELECT id FROM proposals WHERE name = 'release-notes' AND id != '$OLD'")"
echo "behind: $(sqlite3 "$DB" "SELECT id FROM proposals WHERE name = 'release-notes-skill'")"
echo "blocked: $(sqlite3 "$DB" "SELECT id FROM proposals WHERE name = 'code-review'")"
