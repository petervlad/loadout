#!/bin/sh
# Print the commits a What's new draft starts from: by default those since
# the newest <!-- through HASH --> marker in docs/CHANGES.md. Writes nothing;
# the drafter (a person or their agent) decides what is
# user-facing. A hint after each commit marks the ones its files show are
# not: tests only, plan, site only.
#
#   sh scripts/changes-since.sh               since the newest marker
#   sh scripts/changes-since.sh --since HASH  since HASH
#   sh scripts/changes-since.sh --all         from the first commit (the backfill)
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

usage() {
	echo "usage: sh scripts/changes-since.sh [--all | --since HASH]" >&2
	exit 2
}

range=""
case "${1:-}" in
"") ;;
--all)
	[ $# -eq 1 ] || usage
	range=HEAD
	;;
--since)
	[ $# -eq 2 ] || usage
	since=$2
	;;
*) usage ;;
esac

if [ -z "$range" ]; then
	if [ -z "${since:-}" ]; then
		since=$(sed -n 's/^<!-- through \([0-9a-f]\{7,40\}\) -->$/\1/p' docs/CHANGES.md | head -n 1)
		if [ -z "$since" ]; then
			echo "docs/CHANGES.md has no <!-- through HASH --> marker; pass --since HASH or --all." >&2
			exit 1
		fi
	fi
	if ! git cat-file -e "$since^{commit}" 2>/dev/null; then
		echo "$since is not a commit in this repository; pass --since HASH with the last deployed commit." >&2
		exit 1
	fi
	if ! git merge-base --is-ancestor "$since" HEAD; then
		echo "$since is not an ancestor of HEAD (was history rewritten?); pick the range by hand with --since HASH." >&2
		exit 1
	fi
	range="$since..HEAD"
fi

# hint names what a commit's files say about it, or nothing.
hint() {
	files=$(git show --no-renames --format= --name-only "$1")
	[ -n "$files" ] || return 0
	if ! echo "$files" | grep -qvE '(_test\.go$|(^|/)testdata/)'; then
		echo "tests only"
	elif ! echo "$files" | grep -qv '^docs/plans/'; then
		echo "plan"
	elif ! echo "$files" | grep -qvE '^(deploy/site/|docs/CHANGES\.md$)'; then
		echo "site only"
	fi
}

commits=$(git log --no-merges --reverse --format=%h "$range")
if [ -z "$commits" ]; then
	echo "No commits since $since." >&2
	exit 0
fi
for c in $commits; do
	h=$(hint "$c")
	echo "== $(git log -1 --date=short --format='%h %ad %s' "$c")${h:+  [$h]}"
	# The body's first paragraph, indented.
	git log -1 --format=%b "$c" | grep -v "^Co-Authored-By:" | awk 'NF == 0 { exit } { print "   " $0 }'
	git show --no-renames --format= --name-only "$c" | sed 's/^/   . /'
	echo
done
