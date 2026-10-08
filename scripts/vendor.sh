#!/bin/bash
# Downloads the diagram libraries the preview frames load, checks each
# against the hash pinned here (and a second source), and writes it
# gzipped into internal/web/static/vendor with its license and a line in
# VERSIONS. The registry embeds the result; nothing is fetched at run time.
#
# To update: read the library's release notes and security advisories,
# change the version, run with UPDATE=1 to print the new hashes, check them
# against a second source, pin them here and run again.
set -euo pipefail

MERMAID=12.0.0
# dist.integrity of mermaid@$MERMAID on registry.npmjs.org.
MERMAID_INTEGRITY=sha512-/wQXC9iBxoGV8p3erbvaXs9h77VyLDBH6GdayVjj3hEcSQhFU4N1WUhUppotCEqlIxI2pRMwjwBSwTB1MfZBgQ==

DRAWIO=31.5.3
# sha256 of js/viewer-static.min.js at tag v$DRAWIO of jgraph/drawio; the
# run checks jsdelivr's copy against it and against GitHub's own.
DRAWIO_SHA256=41f8360963bb485db74517ae7ca8ca01e563b587607a82238f901750b14e26d0

root=$(cd "$(dirname "$0")/.." && pwd)
out=$root/internal/web/static/vendor
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
update=${UPDATE:-}

fail() { echo "vendor.sh: $*" >&2; exit 1; }

# check NAME GOT WANT: a pinned hash must match, unless UPDATE=1 prints it.
check() {
  if [ -n "$update" ]; then
    echo "$1: $2"
  elif [ "$2" != "$3" ]; then
    fail "$1 is $2, want $3"
  fi
}

sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }

# put SRC DEST: the file gzipped without a name or time, so the same input
# always gives the same bytes.
put() {
  mkdir -p "$(dirname "$2")"
  gzip -9 -n -c "$1" >"$2"
}

rm -rf "$out"
mkdir -p "$out"
versions=$out/VERSIONS
{
  echo "# Written by scripts/vendor.sh; do not edit. One line per file:"
  echo "# library version file sha256-of-the-uncompressed-file license source"
} >"$versions"

# Mermaid: the npm tarball. Its integrity must match the pin and what the
# npm registry publishes today.
tgz=$tmp/mermaid.tgz
url=https://registry.npmjs.org/mermaid/-/mermaid-$MERMAID.tgz
curl -fsSL -o "$tgz" "$url"
got=sha512-$(openssl dgst -sha512 -binary "$tgz" | base64 | tr -d '\n')
check "mermaid $MERMAID integrity" "$got" "$MERMAID_INTEGRITY"
published=$(curl -fsSL "https://registry.npmjs.org/mermaid/$MERMAID" | python3 -c 'import json, sys; print(json.load(sys.stdin)["dist"]["integrity"])')
[ "$published" = "$got" ] || fail "mermaid $MERMAID: the npm registry publishes $published, the tarball is $got"
tar -xzf "$tgz" -C "$tmp" package/dist/mermaid.min.js package/LICENSE package/package.json
dir=$out/mermaid-$MERMAID
put "$tmp/package/dist/mermaid.min.js" "$dir/mermaid.min.js.gz"
cp "$tmp/package/LICENSE" "$dir/LICENSE"
echo "mermaid $MERMAID mermaid-$MERMAID/mermaid.min.js $(sha256 "$tmp/package/dist/mermaid.min.js") MIT $url" >>"$versions"
# What the bundle includes: Mermaid's dependencies, with the license each
# declares on npm at the newest version its range allows.
python3 - "$tmp/package/package.json" >"$dir/THIRD-PARTY.md" <<'PY'
import json, sys, urllib.request
deps = json.load(open(sys.argv[1]))["dependencies"]
print("# Included in mermaid.min.js\n")
print("Mermaid is MIT licensed (LICENSE). Its bundle includes these packages; the license is the one each declares on npm.\n")
print("| Package | Version range | License |")
print("| --- | --- | --- |")
for name in sorted(deps):
    if name.startswith("@types/"):
        continue  # type definitions, not in the bundle
    with urllib.request.urlopen("https://registry.npmjs.org/" + name.replace("/", "%2F") + "/latest") as r:
        lic = json.load(r).get("license", "see the package")
    if isinstance(lic, dict):
        lic = lic.get("type", "see the package")
    print(f"| {name} | {deps[name]} | {lic} |")
PY

# The draw.io viewer: one file from the release tag, through jsdelivr. Its
# hash must match the pin and GitHub's copy of the same tag.
js=$tmp/viewer-static.min.js
url=https://cdn.jsdelivr.net/gh/jgraph/drawio@v$DRAWIO/src/main/webapp/js/viewer-static.min.js
curl -fsSL -o "$js" "$url"
check "draw.io $DRAWIO sha256" "$(sha256 "$js")" "$DRAWIO_SHA256"
github=$(curl -fsSL "https://raw.githubusercontent.com/jgraph/drawio/v$DRAWIO/src/main/webapp/js/viewer-static.min.js" | shasum -a 256 | cut -d' ' -f1)
[ "$github" = "$(sha256 "$js")" ] || fail "draw.io $DRAWIO: GitHub's copy is $github, jsdelivr's is $(sha256 "$js")"
dir=$out/drawio-$DRAWIO
put "$js" "$dir/viewer-static.min.js.gz"
curl -fsSL -o "$dir/LICENSE" "https://cdn.jsdelivr.net/gh/jgraph/drawio@v$DRAWIO/LICENSE"
echo "drawio $DRAWIO drawio-$DRAWIO/viewer-static.min.js $(sha256 "$js") Apache-2.0 $url" >>"$versions"
# What the bundle includes, from the license comments it keeps.
{
  echo "# Included in viewer-static.min.js"
  echo
  echo "The draw.io viewer and mxGraph are Apache-2.0 licensed (LICENSE), by JGraph Ltd. The bundle keeps these license comments of what it includes:"
  echo
  python3 - "$js" <<'PY'
import re, sys
s = open(sys.argv[1], encoding="utf-8", errors="replace").read()
seen = set()
for m in re.finditer(r"/\*!\s*([^*]{0,200}?(?:[Ll]icen[cs]e|Copyright)[^*]{0,160})\*/", s):
    text = " ".join(m.group(1).split())
    if text not in seen:
        seen.add(text)
        print("- " + text)
PY
  echo "- spin.js 2.0.0 (MIT), named in a plain comment"
} >"$dir/THIRD-PARTY.md"

echo "vendored into $out:"
cat "$versions"
