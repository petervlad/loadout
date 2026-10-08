#!/bin/sh
# Install the latest loadout release.
#
#   curl -fsSL https://raw.githubusercontent.com/petervlad/loadout/main/scripts/install.sh | sh
#
# Environment:
#   LOADOUT_VERSION      release to install, e.g. v0.3.0 (default: latest)
#   LOADOUT_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#   LOADOUT_REPO         GitHub owner/repo (default: petervlad/loadout)
# curl honours HTTPS_PROXY / NO_PROXY on its own.
set -eu

REPO="${LOADOUT_REPO:-petervlad/loadout}"
INSTALL_DIR="${LOADOUT_INSTALL_DIR:-$HOME/.local/bin}"

die() {
	printf 'loadout install: %s\n' "$*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

need curl
need tar
need uname
need mktemp

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
MINGW* | MSYS* | CYGWIN*) die "on Windows, download the .zip from https://github.com/$REPO/releases" ;;
*) die "unsupported OS: $(uname -s)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	die "sha256sum or shasum is required to verify the download"
fi

tag="${LOADOUT_VERSION:-}"
if [ -z "$tag" ]; then
	# /releases/latest redirects to /releases/tag/<tag>.
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
		die "could not reach github.com to find the latest release"
	tag="${url##*/}"
	case "$tag" in
	v*) ;;
	*) die "no published release found for $REPO" ;;
	esac
fi
case "$tag" in
v*) ;;
*) tag="v$tag" ;;
esac
version="${tag#v}"

archive="loadout_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'Downloading loadout %s (%s/%s)\n' "$tag" "$os" "$arch"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || die "download failed: $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "download failed: $base/checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$archive is not listed in checksums.txt"
got=$(sha256 "$tmp/$archive")
[ "$want" = "$got" ] || die "checksum mismatch for $archive (want $want, got $got)"
printf 'Checksum OK\n'

tar -xzf "$tmp/$archive" -C "$tmp" loadout || die "could not extract $archive"
mkdir -p "$INSTALL_DIR" || die "cannot create $INSTALL_DIR"
[ -w "$INSTALL_DIR" ] || die "$INSTALL_DIR is not writable; set LOADOUT_INSTALL_DIR"
# Install via a temp name + rename so a running loadout is never half-written.
cp "$tmp/loadout" "$INSTALL_DIR/.loadout.new"
chmod 0755 "$INSTALL_DIR/.loadout.new"
mv -f "$INSTALL_DIR/.loadout.new" "$INSTALL_DIR/loadout"

printf 'Installed %s\n' "$INSTALL_DIR/loadout"
case ":$PATH:" in
*":$INSTALL_DIR:"*) ;;
*) printf '\n%s is not on your PATH. Add this to your shell profile:\n' "$INSTALL_DIR"
	printf "  export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR" ;;
esac
printf '\nNext steps:\n  loadout version\n  loadout new agent <team>/<name>\n  loadout --help\n'
