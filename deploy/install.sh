#!/bin/sh
# Install the loadout CLI from the registry this script was fetched from.
#
#   curl -fsSL https://REGISTRY/install.sh | sh
#
# Running it again is the update. It needs curl and tar, and no sudo: the CLI
# goes to ~/.local/bin unless LOADOUT_BIN_DIR says otherwise.
#
# deploy.sh puts the address of the registry that serves this file in the
# next line; LOADOUT_BASE_URL in the environment wins (the tests use it).
set -eu

BASE="${LOADOUT_BASE_URL:-__LOADOUT_BASE_URL__}"

say() { printf '%s\n' "$*"; }
fail() {
	printf 'install: %s\n' "$*" >&2
	exit 1
}

# The placeholder is not spelled out here: deploy.sh replaces every copy of
# it, so a comparison with the whole word would compare the address with
# itself. A copy that was never staged still ends in these characters.
case "$BASE" in
"" | *LOADOUT_BASE_URL__) fail "this copy of install.sh does not know its registry. Fetch it from your registry, for example: curl -fsSL https://REGISTRY/install.sh | sh" ;;
esac
BASE="${BASE%/}"

[ -n "${HOME:-}" ] || fail "HOME is not set, so there is no place to put loadout. Set HOME and run this again."
command -v curl >/dev/null 2>&1 || fail "curl is not installed. Install curl and run this again."
command -v tar >/dev/null 2>&1 || fail "tar is not installed. Install tar and run this again."

os_name=$(uname -s)
arch_name=$(uname -m)
case "$os_name" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "this computer reports the system '$os_name', and loadout is built for Darwin (macOS) and Linux only." ;;
esac
case "$arch_name" in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=amd64 ;;
*) fail "this computer reports the architecture '$arch_name', and loadout is built for arm64, aarch64, amd64 and x86_64 only." ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
	fail "neither sha256sum nor shasum is installed, so the download cannot be checked. Install one of them and run this again."
fi

bin_dir="${LOADOUT_BIN_DIR:-$HOME/.local/bin}"
archive="loadout_${os}_${arch}.tar.gz"

tmp=$(mktemp -d) || fail "cannot make a temporary folder."
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' INT TERM

# get NAME DEST: one file from the registry's download folder. curl checks
# the registry's certificate against the system's trust store.
get() {
	curl -fsSL --connect-timeout 15 -o "$2" "$BASE/dl/$1"
}

get "$archive" "$tmp/$archive" ||
	fail "cannot download $BASE/dl/$archive. Check the address, that you can reach the registry and that it serves this system ($os, $arch), then run this again."
get checksums.txt "$tmp/checksums.txt" ||
	fail "cannot download $BASE/dl/checksums.txt, so the download cannot be checked. Run this again, or ask the registry owner."

# checksums.txt is what sha256sum and shasum -a 256 write: HASH, two spaces
# (or a space and a star), the file name.
want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt has no line for $archive, so nothing was installed. Ask the registry owner to deploy again."
got=$(sha256 "$tmp/$archive")
[ "$got" = "$want" ] || fail "the checksum of $archive does not match checksums.txt (got $got, want $want), so nothing was installed. Run this again; if it keeps failing, tell the registry owner."

mkdir "$tmp/x"
tar -xzf "$tmp/$archive" -C "$tmp/x" || fail "cannot unpack $archive, so nothing was installed."
[ -f "$tmp/x/loadout" ] || fail "$archive has no loadout file in it, so nothing was installed."

mkdir -p "$bin_dir" || fail "cannot make $bin_dir. Set LOADOUT_BIN_DIR to a folder you can write to."
# Copy beside the old one and rename over it: a loadout that is running
# keeps working, and a half-copied file is never the one on PATH.
cp "$tmp/x/loadout" "$bin_dir/.loadout.new.$$" || fail "cannot write to $bin_dir. Set LOADOUT_BIN_DIR to a folder you can write to."
chmod 0755 "$bin_dir/.loadout.new.$$"
mv -f "$bin_dir/.loadout.new.$$" "$bin_dir/loadout" || {
	rm -f "$bin_dir/.loadout.new.$$"
	fail "cannot replace $bin_dir/loadout."
}

say "Installed $bin_dir/loadout."
"$bin_dir/loadout" version 2>&1 | head -n 1 || true

case ":$PATH:" in
*":$bin_dir:"*) ;;
*)
	say ""
	say "$bin_dir is not on your PATH. Add it, then open a new terminal:"
	say "  echo 'export PATH=\"$bin_dir:\$PATH\"' >> ~/.zshrc"
	;;
esac

say ""
say "Next, sign in:"
say "  loadout login --remote $BASE"
