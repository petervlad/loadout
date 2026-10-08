#!/bin/sh
# Fail when the tree names something that must not be public.
#
#   LOADOUT_DENYLIST=/path/to/denylist.txt sh scripts/denylist-check.sh [DIR]
#
# The denylist (extended regular expressions, one per line, "#" comments) names
# the companies, teams and hosts of the maintainer's own setup, so it is kept
# outside this repository and passed in. Without LOADOUT_DENYLIST the script
# says so and succeeds, so contributors and CI need nothing. Checks the file
# contents and the file names of everything git tracks or would add (ignored
# files are skipped). Prints where it matched, never the list.
set -eu

list=${LOADOUT_DENYLIST:-}
if [ -z "$list" ]; then
	echo "denylist-check: LOADOUT_DENYLIST is not set; skipped."
	exit 0
fi
[ -f "$list" ] || { echo "denylist-check: no such file: $list" >&2; exit 2; }
dir=${1:-.}
cd "$dir"

# grep -f treats an empty line as a pattern that matches everything.
pat=$(mktemp)
trap 'rm -f "$pat"' EXIT
grep -v -E '^[[:space:]]*(#|$)' "$list" > "$pat" || true
[ -s "$pat" ] || { echo "denylist-check: $list has no patterns." >&2; exit 2; }

found=0
if git grep --untracked -n -I -i -E -f "$pat" -- . >&2; then
	found=1
fi
# File names too: a folder called after a team is as telling as its text.
names=$(git ls-files --cached --others --exclude-standard | grep -i -E -f "$pat" || true)
if [ -n "$names" ]; then
	printf 'file name: %s\n' "$names" >&2
	found=1
fi
if [ "$found" = 1 ]; then
	echo "denylist-check: the tree names something on the denylist (see above)." >&2
	exit 1
fi
echo "denylist-check: clean."
