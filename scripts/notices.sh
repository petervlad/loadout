#!/bin/sh
# Write THIRD_PARTY_NOTICES.md: the licences of everything loadout ships that
# it did not write.
#
#   sh scripts/notices.sh            write THIRD_PARTY_NOTICES.md
#   sh scripts/notices.sh --check    fail when the file is out of date
#
# Sources, in order: the Go modules the binary links (listed for the three
# release systems, licence files read from the module cache, so run
# `go mod download` first on a fresh machine), the diagram libraries embedded
# under internal/web/static/vendor, and the borrowed skills under
# .claude/skills. It also fails on a skill folder with no LICENSE that is not
# loadout's own, and on the apple-design skill, which has no licence and must
# never be copied here.
set -eu
export LC_ALL=C

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
mode='write'
case "${1:-}" in
"") ;;
--check) mode=check ;;
*) echo "usage: sh scripts/notices.sh [--check]" >&2; exit 2 ;;
esac

die() { echo "notices: $*" >&2; exit 1; }

# Skills written for this repository; the project's own LICENSE covers them.
own_skills=" loadout-fanout loadout-preview "
# Borrowed skills, by the copyright holder in their LICENSE file: repository
# and the commit they were copied at (keep in step with CLAUDE.md).
source_of() {
	case "$1" in
	"Copyright (c) 2026 Matt Pocock") echo "mattpocock/skills d81f3a1" ;;
	"Copyright (c) 2026 RampStack Co.") echo "rampstackco/claude-skills 3d4510a" ;;
	*) return 1 ;;
	esac
}

# kind FILE: the licence's SPDX name, guessed from its text.
kind() {
	if grep -q 'Apache License' "$1"; then echo Apache-2.0
	elif grep -q 'Mozilla Public License' "$1"; then echo MPL-2.0
	elif grep -q 'Permission is hereby granted, free of charge' "$1"; then echo MIT
	elif grep -q 'Permission to use, copy, modify' "$1"; then echo ISC
	elif grep -q 'Neither the name' "$1"; then echo BSD-3-Clause
	elif grep -q 'Redistribution and use in source and binary forms' "$1"; then echo BSD-2-Clause
	else echo "see text"; fi
}

# text FILE: the file as an indented block, so its own markdown cannot leak.
text() { sed 's/^/    /;s/[[:space:]]*$//' "$1"; }

# licence_files DIR: LICENSE, COPYING, NOTICE and PATENTS files, sorted.
licence_files() {
	find "$1" -maxdepth 1 -type f \( -name 'LICENSE*' -o -name 'LICENCE*' -o -name 'COPYING*' -o -name 'NOTICE*' -o -name 'PATENTS*' \) | sort
}

modules() {
	for os in linux darwin windows; do
		CGO_ENABLED=0 GOOS=$os go list -deps \
			-f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/loadout
	done | sort -u
}

emit() {
	cat <<'HEAD'
# Third-party notices

loadout is MIT licensed (see `LICENSE`). It contains or links the software
below, each under its own licence. This file is written by
`scripts/notices.sh`; do not edit it by hand.

## Go modules

The modules the `loadout` binary links, with the licence files each ships.

HEAD
	mods=$(modules) || die "go list failed; run go mod download first"
	printf '| Module | Version | Licence |\n| --- | --- | --- |\n'
	echo "$mods" | while read -r path ver dir; do
		[ -d "$dir" ] || die "$path $ver is not in the module cache; run go mod download"
		main=$(licence_files "$dir" | head -n 1)
		[ -n "$main" ] || die "$path has no licence file"
		printf '| `%s` | %s | %s |\n' "$path" "$ver" "$(kind "$main")"
	done
	echo "$mods" | while read -r path ver dir; do
		printf '\n### %s %s\n' "$path" "$ver"
		licence_files "$dir" | while read -r f; do
			printf '\n`%s`\n\n' "$(basename "$f")"
			text "$f"
		done
	done

	cat <<'MID'

## Embedded diagram libraries

The registry serves these two libraries from its own binary (gzipped under
`internal/web/static/vendor`, pinned by hash in `scripts/vendor.sh`). Each
bundles further packages; the lists are in the `THIRD-PARTY.md` next to it.
MID
	vend=internal/web/static/vendor
	for d in "$vend"/*/; do
		d=${d%/}
		name=$(basename "$d")
		printf '\n### %s\n' "$name"
		for f in "$d"/LICENSE "$d"/THIRD-PARTY.md; do
			[ -f "$f" ] || die "$f is missing"
			printf '\n`%s`\n\n' "$(basename "$f")"
			text "$f"
		done
	done

	cat <<'SK'

## Borrowed skills

Skills copied into `.claude/skills` for developing loadout. They are not part
of the binary or the release archives. Each skill folder keeps its own
`LICENSE`; `loadout-fanout` and `loadout-preview` are loadout's own.
SK
	for d in .claude/skills/*/; do
		d=${d%/}
		name=$(basename "$d")
		case "$own_skills" in *" $name "*) continue ;; esac
		[ -f "$d/LICENSE" ] || die "$d has no LICENSE; add one or do not copy the skill (apple-design has none and must stay out)"
		holder=$(grep -m1 '^Copyright' "$d/LICENSE" || true)
		source_of "$holder" >/dev/null || die "$d/LICENSE names an unknown holder ($holder); add it to scripts/notices.sh"
		echo "$holder|$name"
	done | sort | awk -F'|' '{ if ($1 != last) { if (last != "") print ""; print $1; last = $1 } printf "%s ", $2 }' > "$tmp.skills"
	echo >> "$tmp.skills"
	for holder in "Copyright (c) 2026 Matt Pocock" "Copyright (c) 2026 RampStack Co."; do
		# shellcheck disable=SC2046 # source_of prints two words on purpose
		set -- $(source_of "$holder")
		repo=$1 commit=$2
		names=$(awk -v h="$holder" '$0 == h { on = 1; next } on { print; exit }' "$tmp.skills")
		[ -n "$names" ] || continue
		first=$(echo "$names" | awk '{print $1}')
		for n in $names; do
			cmp -s ".claude/skills/$n/LICENSE" ".claude/skills/$first/LICENSE" || die "LICENSE of $n differs from $first"
		done
		printf '\n### %s (commit %s)\n\n' "$repo" "$commit"
		printf 'https://github.com/%s, MIT. Skills: %s.\n\n' "$repo" "$(echo $names | sed 's/ /, /g')"
		text ".claude/skills/$first/LICENSE"
		echo
	done
	[ ! -e .claude/skills/apple-design ] || die "apple-design has no licence and must not be in the repository"
}

tmp=$(mktemp)
out=$(mktemp)
trap 'rm -f "$tmp" "$tmp.skills" "$out"' EXIT
emit > "$out"
# One trailing newline, no trailing blank lines.
awk 'NF { for (i = 0; i < blank; i++) print ""; blank = 0; print; next } { blank++ }' "$out" > "$tmp"

if [ "$mode" = check ]; then
	cmp -s "$tmp" THIRD_PARTY_NOTICES.md || die "THIRD_PARTY_NOTICES.md is out of date; run: sh scripts/notices.sh"
	echo "notices: up to date."
else
	cp "$tmp" THIRD_PARTY_NOTICES.md
	echo "notices: wrote THIRD_PARTY_NOTICES.md."
fi
