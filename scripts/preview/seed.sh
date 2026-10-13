#!/bin/bash
# Seeds a throwaway registry for the UI screenshots. Everything lives under
# $LOADOUT_PREVIEW_DIR; HOME is isolated so the real ~/.loadout is never touched.
set -euo pipefail
S=${LOADOUT_PREVIEW_DIR:-${TMPDIR:-/tmp}/loadout-preview}
mkdir -p "$S"
# Build the binary from this checkout, so the preview shows the code as it is.
(cd "$(dirname "$0")/../.." && go build -o "$S/loadout" ./cmd/loadout)
L=$S/loadout
DATA=$S/data
export HOME=$S/home
# LOADOUT_PREVIEW_PORT lets two previews run side by side.
PORT=${LOADOUT_PREVIEW_PORT:-7431}
URL=http://localhost:$PORT
if curl -fs "$URL/v1/health" >/dev/null 2>&1; then
  echo "something already serves $URL; stop it or set LOADOUT_PREVIEW_PORT" >&2
  exit 1
fi
rm -rf "$DATA" "$HOME" "$S/pk"
mkdir -p "$DATA" "$HOME" "$S/pk"

$L namespace create acme --kind project --data "$DATA"
$L namespace create lime --parent acme --kind team --data "$DATA"
$L namespace create koala --parent acme --kind team --data "$DATA"

$L member add --namespace lime --email alice@acme.example --role maintainer --data "$DATA"
$L member add --namespace lime --email bogdan@acme.example --role member --data "$DATA"
$L member add --namespace lime --email carla@acme.example --role maintainer --data "$DATA"
$L member add --namespace lime --email dan@acme.example --role member --data "$DATA"
$L member add --namespace koala --email alice@acme.example --role member --data "$DATA"
# An admin, for the delete page (olga, an owner of acme, comes with course.sh).
$L admin grant --email ada@acme.example --data "$DATA"

LIME=$($L token create --team lime --publish --label seed-lime --data "$DATA" | grep -o 'ldt_[a-z0-9]*')
ACME=$($L token create --team acme --publish --label seed-acme --data "$DATA" | grep -o 'ldt_[a-z0-9]*')
$L token create --team lime --read --label zuul-release-job --data "$DATA" >/dev/null

# Start the server in the background for publishing.
LOADOUT_PUBLIC_URL=$URL LOADOUT_DEV=1 $L serve --addr 127.0.0.1:$PORT --data "$DATA" >"$S/serve.log" 2>&1 &
SERVER=$!
trap 'kill $SERVER 2>/dev/null || true' EXIT
for _ in $(seq 50); do curl -fs "$URL/v1/health" >/dev/null && break; sleep 0.1; done

echo "$LIME" | $L remote add lime "$URL" --token-stdin >/dev/null
echo "$ACME" | $L remote add acme "$URL" --token-stdin >/dev/null

pkg() { # team name version description remote body
  local dir="$S/pk/$1-$2"
  mkdir -p "$dir"
  printf 'name: %s/%s\nkind: agent\nversion: %s\ndescription: %s\n' "$1" "$2" "$3" "$4" >"$dir/loadout.yaml"
  printf -- '---\nname: %s\ndescription: %s\nmodel: balanced\ntools: [read, search]\n---\n%s\n' "$2" "$4" "$6" >"$dir/agent.md"
  $L publish "$dir" --remote "$5" >/dev/null
}

PLANNER_OLD='You are the team planner. Turn a ticket into a plan.

- Read the ticket and the code it touches.
- Keep plans short and general.
- List the risks.'
pkg lime planner 2.2.0 "Turns a ticket into an ordered plan with files, risks and checks" lime "$PLANNER_OLD"
pkg lime planner 2.3.0 "Turns a ticket into an ordered plan with files, risks and checks" lime "$PLANNER_OLD
- Ask before guessing."
pkg lime code-review 1.8.2 "Adversarial review of a branch against its ticket" lime "Review the branch against the ticket. <script>alert(1)</script> is text."
pkg lime release-notes 0.9.0 "Drafts release notes from merged pull requests" lime "Draft release notes."
pkg acme python-standards 4.1.0 "House rules for Python across every acme team" acme "Follow the house rules."
pkg acme docs-writer 1.0.3 "Short user-facing docs with diagrams and links" acme "Write short docs."

# The candidate: planner 2.4.0.
dir="$S/pk/lime-planner"
printf 'name: lime/planner\nkind: agent\nversion: 2.4.0\ndescription: Turns a ticket into an ordered plan with files, risks and checks\n' >"$dir/loadout.yaml"
printf -- '---\nname: planner\ndescription: Turns a ticket into an ordered plan with files, risks and checks\nmodel: balanced\ntools: [read, search]\n---\n%s\n' 'You are the team planner. Turn a ticket into a plan.

- Read the ticket and the code it touches.
- Name every file you would touch.
- End with one concrete test.
- List the risks.
- Ask before guessing.' >"$dir/agent.md"
$L propose "$dir" --remote lime --note "Plans now name the exact files and end with a test step." \
  --prompt "Plan TRK-1234: add a retry to the upload job" \
  --prompt "Plan a database migration for the releases table" \
  --prompt "Plan retries for every upload job, with files, risks and a test" >/dev/null
ID=$(sqlite3 "$DATA/registry.db" "SELECT id FROM proposals LIMIT 1")

# A long answer (about 400 words, with a list, a table and a code block)
# and a medium one (about 120), so the page shows an answer cut to its
# first words next to one shown whole.
LONG='## Plan: retries for every upload job

Today only `upload/job.py` retries, and only on timeouts. The nightly bundle, the docs upload and the symbol upload each fail on the first 503 and page whoever is on call. This plan moves the retry into one helper and uses it in all four jobs, so the rules are the same everywhere and a flaky upstream stops waking people up.

### Files

1. `upload/retry.py`: new module with `retry(fn, tries, backoff)`, moved out of `job.py` and extended with jitter.
2. `upload/job.py`: call the shared helper instead of the local loop.
3. `nightly/bundle.py`, `docs/publish.py` and `symbols/push.py`: wrap the single network call in `retry()`.
4. `upload/config.py`: add `UPLOAD_RETRIES` (default 3) and `UPLOAD_BACKOFF` (default 2 s).
5. `tests/test_retry.py`: new tests for the helper.

### What gets retried

| Response | Retry? | Why |
|---|---|---|
| Timeout | yes | the upstream is slow, not wrong |
| 502, 503, 504 | yes | the load balancer lost a backend |
| 429 | yes, after `Retry-After` | we are being asked to wait |
| 400 to 428 | no | the request itself is wrong |
| 500 | no | usually a bug on their side |

### The helper

```python
def retry(fn, tries=3, backoff=2.0):
    for attempt in range(1, tries + 1):
        try:
            return fn()
        except Retryable as err:
            if attempt == tries:
                raise
            log.warning("attempt %d failed: %s", attempt, err)
            time.sleep(backoff * 2 ** (attempt - 1) * random.uniform(0.8, 1.2))
```

### Order of work

1. Add the helper and its tests, unused.
2. Switch `job.py` over and check the existing tests still pass.
3. Switch the other three jobs one pull request at a time, so a problem in one does not block the rest.
4. Watch the on-call pages for a week and lower the default to 2 tries if nothing is being retried.

### Risks

- A stuck upstream now takes up to 14 s longer to fail, which pushes the nightly bundle past its 02:00 slot on a bad night. Acceptable: it runs again at 03:00.
- Retrying a POST that did succeed would upload twice. All four endpoints are idempotent on the bundle digest, so a second upload is a no-op; I checked each one.
- Jitter makes the tests timing-dependent. The tests inject a fake `sleep`, so they stay fast and exact.

### Test

A fake server that returns 503 twice and then 200: the job succeeds on the third try and logs two warnings. A second case returns 400 once: the job fails at once and logs one error, without retrying.'
MEDIUM='Here is a plan for adding retries to the upload jobs.

1. Find each place where an upload happens and wrap it in a retry loop.
2. Use exponential backoff, starting at a second or two, with a sensible limit on the number of tries.
3. Retry on timeouts and server errors, but not on client errors, since those will not fix themselves.
4. Log each failed attempt so on-call can see what happened.
5. Consider making the limit configurable per job.

Risks: retries could hide a real outage, and retrying an upload that did succeed could upload twice, so check that the endpoints are idempotent first.

Test it with a fake server that fails a couple of times before succeeding. Let me know if you want me to go deeper on any step.'
json() { python3 -c 'import json, sys; print(json.dumps(sys.argv[1]))' "$1"; }

cat >"$S/report.json" <<EOF
{"tool":"claude","tool_version":"2.1.0","model":"sonnet","proposal_id":"$ID","package":"lime/planner",
 "current":{"version":"2.3.0","digest":"sha256:aa","source":"installed"},"candidate":{"version":"2.4.0","digest":"sha256:bb","source":"proposal","proposal_id":"$ID"},"runs":2,
 "mode":"full","context":[{"id":"lime/code-review","version":"1.8.2","kind":"agent"},{"id":"lime/release-notes","version":"0.9.0","kind":"agent"},
  {"id":"acme/docs-writer","version":"1.0.3","kind":"agent"},{"id":"acme/python-standards","version":"4.1.0","kind":"agent"}],
 "generated_at":"2026-09-28T11:05:00Z","prompts":[
 {"prompt":"Plan TRK-1234: add a retry to the upload job","runs":[
  {"current":{"answer":{"text":"Here is a plan for adding a retry to the upload job.\n\n1. Find where the upload happens and wrap it in a retry.\n2. Use exponential backoff with a sensible limit.\n3. Log each failed attempt.\n4. Consider making the limit configurable.\n\nRisks: retries could hide a real outage. Let me know if you want me to go deeper on any step.","duration_ns":14200000000,"input_tokens":3900,"output_tokens":1100}},
   "candidate":{"answer":{"text":"Plan for TRK-1234\n\n1. upload/job.py: wrap push_bundle() in the existing retry() helper, 3 tries, 2 s then 8 s.\n2. upload/config.py: add UPLOAD_RETRIES, default 3.\n3. Retry only on timeouts and 5xx; fail at once on 4xx.\n4. Log attempt number and cause at WARN; the final failure at ERROR.\n\nRisk: a stuck upstream now takes up to 10 s longer to fail.\nTest: a fake server that returns 503 twice, then 200. <script>alert(1)</script>","duration_ns":11800000000,"input_tokens":3700,"output_tokens":900}}},
  {"current":{"answer":{"text":"Wrap the upload in a retry loop.","duration_ns":9000000000,"input_tokens":3800,"output_tokens":400}},
   "candidate":{"error":"claude exited with status 1: rate limited"}}]},
 {"prompt":"Plan a database migration for the releases table","runs":[
  {"current":{"answer":{"text":"Add the column, backfill, then add the index.","duration_ns":8100000000,"input_tokens":3100,"output_tokens":300}},
   "candidate":{"answer":{"text":"1. migrations/0006_releases.sql: add the column as nullable.\n2. Backfill in batches of 1000.\n3. Add the index concurrently.\n\nTest: run the migration against a copy of prod.","duration_ns":9400000000,"input_tokens":3200,"output_tokens":420}}}]},
 {"prompt":"Plan retries for every upload job, with files, risks and a test","runs":[
  {"current":{"answer":{"text":$(json "$MEDIUM"),"duration_ns":10300000000,"input_tokens":3600,"output_tokens":350}},
   "candidate":{"answer":{"text":$(json "$LONG"),"duration_ns":24600000000,"input_tokens":3900,"output_tokens":1400}}}]}]}
EOF
printf '# Review of lime/planner 2.4.0\n\nSee the JSON.\n' >"$S/report.md"
curl -fs -H "Authorization: Bearer $LIME" -F "markdown=@$S/report.md;type=text/markdown" -F "report=@$S/report.json;type=application/json" "$URL/v1/proposals/$ID/reviews" >/dev/null

# Prompts teammates added after the report (Carla, Alice), and one Dan
# added and removed; their authors are set below.
prompt() { curl -fs -H "Authorization: Bearer $LIME" -H "Content-Type: application/json" -d "{\"text\":$(json "$1")}" "$URL/v1/proposals/$ID/prompts" >/dev/null; }
prompt 'Plan the rollback if the retry makes the nightly bundle miss its 02:00 slot.
Name the job and the flag you would flip.'
prompt 'Plan TRK-1301, whose title is "<script>alert(1)</script> in the upload log"'
prompt 'Plan it in Python 2'

# People: names, sign-in methods, the proposal's author and reviewer, and
# sessions with known secrets for the screenshots.
DB="$DATA/registry.db"
id() { sqlite3 "$DB" "SELECT id FROM users WHERE email = '$1'"; }
NOW=$(date -u +%Y-%m-%dT%H:%M:%S.000000000Z)
LATER=$(date -u -v+20d +%Y-%m-%dT%H:%M:%S.000000000Z)
PROPOSED=$(date -u -v-3H +%Y-%m-%dT%H:%M:%S.000000000Z)
sqlite3 "$DB" <<EOF
UPDATE users SET name = 'Alice Martin', google_subject = 'g-alice', email_verified_at = '$NOW' WHERE email = 'alice@acme.example';
UPDATE users SET name = 'Bogdan Ionescu', password_hash = 'x', email_verified_at = '$NOW' WHERE email = 'bogdan@acme.example';
UPDATE users SET name = 'Carla Ruiz', google_subject = 'g-carla', email_verified_at = '$NOW' WHERE email = 'carla@acme.example';
UPDATE memberships SET created_at = '2026-06-03T10:00:00.000000000Z' WHERE user_id = '$(id alice@acme.example)';
UPDATE memberships SET created_at = '2026-07-10T10:00:00.000000000Z' WHERE user_id = '$(id bogdan@acme.example)';
UPDATE memberships SET created_at = '2026-08-21T10:00:00.000000000Z' WHERE user_id = '$(id carla@acme.example)';
UPDATE proposals SET author_user_id = '$(id bogdan@acme.example)', author_label = 'bogdan@acme.example via loadout login', created_at = '$PROPOSED';
UPDATE proposal_prompts SET author_user_id = '$(id bogdan@acme.example)', author_label = 'bogdan@acme.example via loadout login', created_at = '$PROPOSED' WHERE proposal_id = '$ID' AND seq <= 3;
UPDATE proposal_prompts SET author_user_id = '$(id carla@acme.example)', author_label = 'carla@acme.example via loadout login', created_at = '$(date -u -v-90M +%Y-%m-%dT%H:%M:%S.000000000Z)' WHERE proposal_id = '$ID' AND seq = 4;
UPDATE proposal_prompts SET author_user_id = '$(id alice@acme.example)', author_label = 'alice@acme.example via loadout login', created_at = '$(date -u -v-40M +%Y-%m-%dT%H:%M:%S.000000000Z)' WHERE proposal_id = '$ID' AND seq = 5;
UPDATE proposal_prompts SET author_user_id = '$(id dan@acme.example)', author_label = 'dan@acme.example via loadout login', created_at = '$(date -u -v-30M +%Y-%m-%dT%H:%M:%S.000000000Z)',
  removed_at = '$(date -u -v-20M +%Y-%m-%dT%H:%M:%S.000000000Z)', removed_by_user_id = '$(id dan@acme.example)', removed_by_label = 'dan@acme.example via loadout login' WHERE proposal_id = '$ID' AND seq = 6;
UPDATE proposal_reviews SET author_user_id = '$(id carla@acme.example)', author_label = 'carla@acme.example via loadout login', created_at = '$(date -u -v-2H +%Y-%m-%dT%H:%M:%S.000000000Z)';
UPDATE releases SET published_at = '$(date -u -v-2d +%Y-%m-%dT%H:%M:%S.000000000Z)' WHERE name = 'planner' AND version = '2.3.0';
UPDATE releases SET published_at = '$(date -u -v-30d +%Y-%m-%dT%H:%M:%S.000000000Z)' WHERE name = 'planner' AND version = '2.2.0';
UPDATE releases SET published_at = '$(date -u -v-1d +%Y-%m-%dT%H:%M:%S.000000000Z)' WHERE name = 'code-review';
UPDATE releases SET published_at = '$(date -u -v-21d +%Y-%m-%dT%H:%M:%S.000000000Z)' WHERE name = 'release-notes';
UPDATE releases SET published_at = '$(date -u -v-40d +%Y-%m-%dT%H:%M:%S.000000000Z)' WHERE team = 'acme';
INSERT INTO sessions (hash, user_id, created_at, expires_at) VALUES
  (X'$(printf 'alice-session' | shasum -a 256 | cut -d' ' -f1)', '$(id alice@acme.example)', '$NOW', '$LATER'),
  (X'$(printf 'bogdan-session' | shasum -a 256 | cut -d' ' -f1)', '$(id bogdan@acme.example)', '$NOW', '$LATER'),
  (X'$(printf 'ada-session' | shasum -a 256 | cut -d' ' -f1)', '$(id ada@acme.example)', '$NOW', '$LATER');
EOF
echo "proposal $ID"
echo "$ID" >"$S/proposal-id"
