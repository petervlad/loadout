#!/bin/sh
# Render docs/GUIDE.md into the static page served at /docs, and
# docs/CHANGES.md into the What's new page at /docs/whats-new.
# Needs pandoc 3. Run from anywhere: docs/guide/build.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
site="$root/deploy/site/docs"
mkdir -p "$site/whats-new"
pandoc "$root/docs/GUIDE.md" \
	--from gfm --to html5 \
	--standalone --template "$here/page.html" \
	--lua-filter "$here/table.lua" \
	--lua-filter "$here/sections.lua" \
	--toc --toc-depth 2 \
	--columns 10000 \
	--syntax-highlighting none \
	--metadata pagetitle="loadout docs" \
	--metadata source="docs/GUIDE.md" \
	--metadata description-meta="How to install, write, share and review AI agents, skills and knowledge bases with loadout." \
	--output - |
	# The contents appear twice (sidebar and phone menu); ids must be unique.
	sed -E 's/ id="toc-[^"]*"//' >"$site/index.html"

# changes.lua stops the build on a malformed file; write to a temporary file
# first so a failed run leaves the committed page as it was.
pandoc "$root/docs/CHANGES.md" \
	--from gfm --to html5 \
	--standalone --template "$here/page.html" \
	--lua-filter "$here/changes.lua" \
	--toc --toc-depth 2 \
	--columns 10000 \
	--syntax-highlighting none \
	--metadata pagetitle="What's new in loadout" \
	--metadata changes=true \
	--metadata source="docs/CHANGES.md" \
	--metadata description-meta="What each loadout update added and fixed, newest first." \
	--output "$site/whats-new/index.tmp"
sed -E 's/ id="toc-[^"]*"//' "$site/whats-new/index.tmp" >"$site/whats-new/index.html"
rm "$site/whats-new/index.tmp"
cp "$here/docs.js" "$site/docs.js"
