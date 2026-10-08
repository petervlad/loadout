#!/bin/bash
# Adds a course to the preview (after seed.sh, with serve.sh running): olga
# owns acme; lime's wiki has a long page and the course lime/lime-course
# (seven lessons, a Mermaid diagram, three quizzes and one broken quiz);
# acme's course acme/onboarding shows on koala, which has none of its
# own; koala's course names a package that does not exist.
set -euo pipefail
S=${LOADOUT_PREVIEW_DIR:-${TMPDIR:-/tmp}/loadout-preview}
L=$S/loadout; DATA=$S/data; DB=$DATA/registry.db; export HOME=$S/home
P=$S/pk; rm -rf "$P/lime-course" "$P/onboarding"
$L member add --namespace acme --email olga@acme.example --role owner --data "$DATA" >/dev/null
$L namespace create koala --parent acme --kind team --data "$DATA" >/dev/null
for ns in acme lime koala koala; do $L namespace set $ns --wiki on --data "$DATA" >/dev/null; done

quiz() { # question right wrong why-right why-wrong
  printf '```quiz\n%s\n\n- [ ] %s\n  %s\n- [x] %s\n  %s\n```\n' "$1" "$3" "$5" "$2" "$4"
}
cd "$P"
$L new kb lime/lime-course >/dev/null
rm lime-course/README.md; mkdir -p lime-course/reference
cat > lime-course/loadout.yaml <<Y
name: lime/lime-course
kind: kb
version: 1.4.0
description: The lime dev loop in seven short lessons, from your first install to a release.
Y
printf '# Set up loadout\n\nInstall the CLI and sign in with `loadout login`. Your AI tool then gets the team'"'"'s agents and skills.\n\nSee the [command reference](reference/commands.md).\n\n%s' "$(quiz 'Where do the team'"'"'s agents land after loadout login?' 'In your AI tool, for every project' 'In the repository you are in' 'Right: loadout installs into the tool, not into a repository.' 'Agents live with the tool; a repository only holds your code.')" > lime-course/01-setup.md
printf '# Plan a ticket\n\nStart every ticket with the planner agent. It names the files and ends with a test.\n\n## What a good plan has\n\n- the files it touches\n- the risks\n- one concrete test\n' > lime-course/02-plan.md
cat > lime-course/03-propose.md <<'Y'
# Propose a version

When you change an agent, propose it instead of publishing. A teammate reviews it on the web page.

```mermaid
flowchart LR
  edit[Edit agent.md] --> propose[loadout propose] --> review[Review on the web] --> release[Release]
```

## When your proposal is behind

Someone released a newer version while yours waited. Rebase it.

```quiz
Your proposal is behind the latest release. What do you run first?

- [ ] loadout publish --base latest
  Not quite. That skips the merge, and a teammate may lose the release's changes.
- [x] loadout rebase
  It merges your folder onto the latest release on your machine; then you propose again.
- [ ] Withdraw it and start over
  You can, but rebase keeps your work and the prompts.
```

Back to [setting up](01-setup.md), or on to [review](04-review.md).
Y
printf '# Review a proposal\n\nOpen the proposal, read the diff, run the prompts and approve or reject with a note.\n\n%s' "$(quiz 'Who may approve a proposal in lime?' 'A maintainer other than the author' 'Anyone who can read lime' 'Four eyes: a second maintainer approves.' 'Members read and propose; maintainers approve.')" > lime-course/04-review.md
cat > lime-course/05-release.md <<'Y'
# Release

An approved proposal is a release. Everyone's next `loadout sync` installs it.

```quiz
This quiz forgot to indent its explanation.

- [x] loadout sync
The explanation is not indented, so the quiz cannot be read.
- [ ] loadout publish
```
Y
printf '# Roll back\n\nA bad release? Publish the previous version again with a higher number.\n' > lime-course/06-rollback.md
printf '# Where to ask\n\nAsk in the lime channel, or write it in the wiki above.\n' > lime-course/07-help.md
printf '# Command reference\n\n| Command | What it does |\n| --- | --- |\n| loadout login | sign in |\n| loadout sync | install what changed |\n' > lime-course/reference/commands.md
$L publish lime-course --remote lime >/dev/null

$L new kb acme/onboarding >/dev/null
rm onboarding/README.md
cat > onboarding/loadout.yaml <<Y
name: acme/onboarding
kind: kb
version: 2.0.0
description: What every acme team member needs in the first week.
Y
printf '# Welcome to acme\n\nRead this first.\n' > onboarding/01-welcome.md
printf '# Your first week\n\n%s' "$(quiz 'Who owns a team'"'"'s settings?' 'Its owners and the owners of acme' 'Every member' 'Owners of the project own every team in it.' 'Members read; owners change settings.')" > onboarding/02-first-week.md
$L publish onboarding --remote acme >/dev/null

$L namespace set lime --course lime/lime-course --data "$DATA" >/dev/null
$L namespace set acme --course acme/onboarding --data "$DATA" >/dev/null

# A long wiki page above the box, saved as alice.
WIKI='# Lime

Everything the lime team should know. The course below walks a new teammate through the dev loop.

## Setup

Install the CLI, sign in with loadout login and run loadout sync.

## Who to ask

Alice for releases, Carla for reviews.

## Deploys

Merge to main; the gate job deploys.

## On call

The rota is in the team calendar.'
NOW=$(date -u +%Y-%m-%dT%H:%M:%S.000000000Z)
ALICE=$(sqlite3 "$DB" "SELECT id FROM users WHERE email = 'alice@acme.example'")
python3 - "$DB" "$WIKI" "$ALICE" "$NOW" <<'PY'
import sqlite3, sys
db, text, alice, now = sys.argv[1:]
c = sqlite3.connect(db)
c.execute("INSERT INTO wiki_revisions (namespace, seq, format, body, size, author_user_id, author_label, note, created_at) VALUES ('lime', 1, 'md', ?, ?, ?, 'alice@acme.example', 'First page', ?)", (text, len(text.encode()), alice, now))
c.execute("UPDATE namespaces SET course = 'koala/gone' WHERE name = 'koala'")
c.commit()
PY
OLGA=$(sqlite3 "$DB" "SELECT id FROM users WHERE email = 'olga@acme.example'")
LATER=$(date -u -v+20d +%Y-%m-%dT%H:%M:%S.000000000Z)
sqlite3 "$DB" "UPDATE users SET name = 'Olga Berg', email_verified_at = '$NOW' WHERE id = '$OLGA';
INSERT INTO sessions (hash, user_id, created_at, expires_at) VALUES (X'$(printf 'olga-session' | shasum -a 256 | cut -d' ' -f1)', '$OLGA', '$NOW', '$LATER');"
echo "course seeded"
