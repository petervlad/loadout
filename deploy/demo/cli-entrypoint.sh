#!/bin/bash
# Starts the demo shell: waits for the registry, signs alice in with the demo
# password as a browser would, approves `loadout login` for her, and opens
# bash in the sample project. The CLI container shares the registry's network,
# so the registry is localhost here, the address it believes it has.
set -euo pipefail

url=${LOADOUT_DEMO_URL:-http://localhost:8080}
email=alice@example.com
password=demo-loadout-2026
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for _ in $(seq 60); do
	curl -fs "$url/v1/health" >/dev/null 2>&1 && break
	sleep 1
done
curl -fs "$url/v1/health" >/dev/null || { echo "the registry did not answer at $url" >&2; exit 1; }

# Both tools are there for loadout to find: it installs into whichever it sees.
mkdir -p "$HOME/.claude" "$HOME/.copilot"

# csrf URL: the form's token, and the cookie that goes with it, kept in the jar.
csrf() {
	curl -fs -b "$tmp/jar" -c "$tmp/jar" "$1" | sed -n 's/.*name="csrf" value="\([^"]*\)".*/\1/p' | head -1
}
post() {
	curl -fs -o /dev/null -b "$tmp/jar" -c "$tmp/jar" "$@"
}

token=$(csrf "$url/auth/login")
post --data-urlencode "csrf=$token" --data-urlencode "email=$email" --data-urlencode "password=$password" --data-urlencode next=/ "$url/auth/login"

loadout login --remote "$url" </dev/null >"$tmp/login.out" 2>&1 &
login=$!
code=
for _ in $(seq 100); do
	code=$(sed -n 's/.*enter the code \([A-Z]\{4\}-[A-Z]\{4\}\).*/\1/p' "$tmp/login.out" | head -1)
	[ -n "$code" ] && break
	sleep 0.1
done
[ -n "$code" ] || { echo "loadout login gave no code:" >&2; cat "$tmp/login.out" >&2; exit 1; }
token=$(csrf "$url/auth/device?code=$code")
post --data-urlencode "csrf=$token" --data-urlencode "code=$code" --data-urlencode "namespace=*" --data-urlencode action=approve "$url/auth/device"
wait "$login" || { echo "loadout login failed:" >&2; cat "$tmp/login.out" >&2; exit 1; }
# For `loadout memory import`, which saves through a memory token.
loadout memory token create >/dev/null

cat <<BANNER

  loadout demo shell. You are alice@example.com, signed in to $url
  with the sample project in $PWD (a git repo for Claude Code and Copilot).

    loadout status
    loadout install lime/planner --scope project
    loadout sync
    loadout propose --help
    loadout new pipeline lime/my-loop --uses acme/dev-loop@1
    loadout memory import ../personal-memory --team lime

  Open $url in a browser to see the other side. Exit leaves the demo running.

BANNER
exec "$@"
