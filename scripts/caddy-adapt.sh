#!/bin/sh
# Check that deploy/Caddyfile adapts, without and with the optional site block
# and the download routes.
#
#   sh scripts/caddy-adapt.sh
#
# Needs caddy on PATH. The Caddyfile imports /etc/caddy/loadout.d/*.caddy and *.routes; the
# copy under test points that import at a temporary folder instead.
set -eu

command -v caddy >/dev/null 2>&1 || { echo "caddy-adapt: caddy is not installed." >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/loadout.d"
sed "s#/etc/caddy/loadout.d/#$tmp/loadout.d/#" "$root/deploy/Caddyfile" >"$tmp/Caddyfile"

export LOADOUT_DOMAIN=registry.example.com
export LOADOUT_SITE_DOMAIN=docs.example.com

caddy adapt --config "$tmp/Caddyfile" --adapter caddyfile >/dev/null
echo "caddy-adapt: without the site block: ok."

cp "$root/deploy/site.caddy" "$tmp/loadout.d/site.caddy"
caddy adapt --config "$tmp/Caddyfile" --adapter caddyfile >/dev/null
echo "caddy-adapt: with the site block: ok."

cp "$root/deploy/downloads.routes" "$tmp/loadout.d/downloads.routes"
caddy adapt --config "$tmp/Caddyfile" --adapter caddyfile >/dev/null
echo "caddy-adapt: with the download routes: ok."
