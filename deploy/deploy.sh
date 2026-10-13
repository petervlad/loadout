#!/bin/sh
# Install loadout, with its systemd units and Caddy config, on a Linux host
# you can ssh to. The loadout binary comes from one of three places:
#
#   - this checkout, built with go (the default);
#   - LOADOUT_BINARY=FILE, a linux binary you already have;
#   - LOADOUT_VERSION=X.Y.Z, downloaded from the project's GitHub release and
#     checked against the release's checksums.txt (SHA-256) before it is used.
#
# First deploy of a fresh Debian or Ubuntu machine (see README.md):
#
#   LOADOUT_PREPARE=1 LOADOUT_VERSION=0.2.0 LOADOUT_HOST=203.0.113.7 \
#     LOADOUT_DOMAIN=203-0-113-7.sslip.io sh deploy.sh
#
# Later deploys leave LOADOUT_PREPARE out. This script needs only a POSIX
# shell, ssh, scp and curl on your machine, plus go in the default mode.
#
# Environment:
#   LOADOUT_HOST      SSH host name or IP (required)
#   LOADOUT_DOMAIN    public name Caddy serves and gets a certificate for (required)
#   LOADOUT_SSH_USER  admin user with passwordless sudo (default: admin)
#   LOADOUT_ARCH      amd64 or arm64 (default: amd64)
#   LOADOUT_PREPARE   1 to run prepare.sh on the host first (packages, ssh
#                     hardening, firewall, Caddy, the loadout user). First
#                     deploy only; never run otherwise.
#   LOADOUT_BINARY    path of a linux loadout binary to install
#   LOADOUT_VERSION   release to download and install, for example 0.2.0
#   LOADOUT_RELEASE_URL
#                     where releases are downloaded from, for a mirror
#                     (default: the project's GitHub releases)
#   LOADOUT_ENV_FILE  local file with the registry's settings, started from
#                     loadout.env.example; installed as
#                     /etc/loadout/loadout.env, root only. Unset: the file on
#                     the host, if any, is left as it is. Either file is
#                     checked with `loadout serve --check` before the service
#                     restarts, and a mistake stops the deploy.
#   LOADOUT_SITE_DIR, LOADOUT_SITE_DOMAIN
#                     optional, both or neither: a folder of static pages and
#                     the name Caddy serves them under (site.caddy). Without
#                     them no site is installed, and one a past deploy
#                     installed is taken out of Caddy's configuration.
#   LOADOUT_DOWNLOADS 1 to also publish the CLI for macOS and Linux (arm64 and
#                     amd64) and an install.sh at https://LOADOUT_DOMAIN/install.sh,
#                     so people on the team need no Go and no clone. Built
#                     from this checkout, so it cannot be combined with
#                     LOADOUT_BINARY or LOADOUT_VERSION. Without it, what an
#                     earlier deploy published is taken down.
set -eu

die() {
	printf 'deploy: %s\n' "$*" >&2
	exit 1
}

HOST="${LOADOUT_HOST:-}"
DOMAIN="${LOADOUT_DOMAIN:-}"
SSH_USER="${LOADOUT_SSH_USER:-admin}"
ARCH="${LOADOUT_ARCH:-amd64}"
PREPARE="${LOADOUT_PREPARE:-}"
BINARY="${LOADOUT_BINARY:-}"
VERSION_WANTED="${LOADOUT_VERSION:-}"
RELEASE_URL="${LOADOUT_RELEASE_URL:-https://github.com/petervlad/loadout/releases/download}"
ENV_FILE="${LOADOUT_ENV_FILE:-}"
SITE_DIR="${LOADOUT_SITE_DIR:-}"
SITE_DOMAIN="${LOADOUT_SITE_DOMAIN:-}"
DOWNLOADS="${LOADOUT_DOWNLOADS:-}"

[ -n "$HOST" ] || die "set LOADOUT_HOST"
[ -n "$DOMAIN" ] || die "set LOADOUT_DOMAIN (e.g. 203-0-113-7.sslip.io)"
case "$ARCH" in
amd64 | arm64) ;;
*) die "LOADOUT_ARCH must be amd64 or arm64, got $ARCH" ;;
esac
# These end up in a systemd drop-in or a remote shell: keep them plain.
case "$DOMAIN" in
*[!A-Za-z0-9.-]* | .* | *.) die "LOADOUT_DOMAIN is not a valid host name: $DOMAIN" ;;
esac
case "$SSH_USER" in
'' | *[!A-Za-z0-9_.-]*) die "LOADOUT_SSH_USER is not a plain user name: $SSH_USER" ;;
esac
case "$PREPARE" in
'' | 0 | 1) ;;
*) die "LOADOUT_PREPARE is 1 to prepare the host, or unset" ;;
esac
[ "$PREPARE" != 0 ] || PREPARE=""
{ [ -n "$SITE_DIR" ] && [ -n "$SITE_DOMAIN" ]; } || { [ -z "$SITE_DIR" ] && [ -z "$SITE_DOMAIN" ]; } ||
	die "set both LOADOUT_SITE_DIR and LOADOUT_SITE_DOMAIN, or neither"
case "$SITE_DOMAIN" in
*[!A-Za-z0-9.-]* | .* | *.) die "LOADOUT_SITE_DOMAIN is not a valid host name: $SITE_DOMAIN" ;;
esac
[ -z "$SITE_DIR" ] || [ -d "$SITE_DIR" ] || die "LOADOUT_SITE_DIR is not a folder: $SITE_DIR"
[ -z "$ENV_FILE" ] || [ -f "$ENV_FILE" ] || die "LOADOUT_ENV_FILE does not exist: $ENV_FILE"
case "$DOWNLOADS" in
'' | 0 | 1) ;;
*) die "LOADOUT_DOWNLOADS is 1 to publish the CLI, or unset" ;;
esac
[ "$DOWNLOADS" != 0 ] || DOWNLOADS=""
[ -z "$DOWNLOADS" ] || { [ -z "$BINARY" ] && [ -z "$VERSION_WANTED" ]; } ||
	die "LOADOUT_DOWNLOADS builds the CLI from this checkout: leave LOADOUT_BINARY and LOADOUT_VERSION out"
[ -z "$BINARY" ] || [ -z "$VERSION_WANTED" ] || die "set LOADOUT_BINARY or LOADOUT_VERSION, not both"
command -v curl >/dev/null 2>&1 || die "curl is not installed"

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
target="$SSH_USER@$HOST"

stage=$(mktemp -d)
work=$(mktemp -d)
trap 'rm -rf "$stage" "$work"' EXIT INT TERM

# sha256 prints the SHA-256 of a file, with whichever tool this machine has.
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "neither sha256sum nor shasum is installed, so the download cannot be verified"
	fi
}

# fetch_release downloads release $1 for linux/$ARCH into $work, verifies it
# against the release's checksums.txt and leaves the binary at $stage/loadout.
fetch_release() {
	v=$1
	archive="loadout_${v}_linux_${ARCH}.tar.gz"
	base="$RELEASE_URL/v$v"
	printf '==> Downloading loadout %s for linux/%s\n' "$v" "$ARCH"
	curl -fsSL -o "$work/$archive" "$base/$archive" || die "cannot download $base/$archive; is $v a release?"
	curl -fsSL -o "$work/checksums.txt" "$base/checksums.txt" || die "cannot download $base/checksums.txt"
	want=$(awk -v f="$archive" '$2 == f { print $1 }' "$work/checksums.txt")
	[ -n "$want" ] || die "checksums.txt has no entry for $archive; refusing to install it"
	got=$(sha256 "$work/$archive")
	[ "$got" = "$want" ] || die "the checksum of $archive does not match checksums.txt (expected $want, got $got); refusing to install it"
	printf '==> Checksum ok (SHA-256 %s)\n' "$got"
	mkdir "$work/unpacked"
	tar -xzf "$work/$archive" -C "$work/unpacked" loadout || die "$archive holds no loadout binary"
	cp "$work/unpacked/loadout" "$stage/loadout"
}

# build_checkout builds this checkout for linux/$ARCH. The registry tells
# loadout CLIs about the release it was built from, only when it is exactly a
# release tag: deploy from the tag (v0.1.0) to advertise it. A commit after
# a tag, a dirty tree or no tag advertises nothing, so CLIs stay quiet.
build_checkout() {
	[ -f "$root/go.mod" ] && [ -d "$root/cmd/loadout" ] ||
		die "this is not a loadout checkout, so there is nothing to build: set LOADOUT_VERSION=X.Y.Z to download a release, or LOADOUT_BINARY=FILE to install a binary"
	command -v go >/dev/null 2>&1 || die "go is not installed; set LOADOUT_VERSION=X.Y.Z to download a release instead"
	version=$(git -C "$root" describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
	# Exactly vMAJOR.MINOR.PATCH, as loadout reads a release (no leading
	# zeros; 0.0.0 is no release).
	advertises=""
	if printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' &&
		[ "$version" != v0.0.0 ]; then
		advertises="${version#v}"
	fi
	printf '==> Building loadout %s for linux/%s\n' "$version" "$ARCH"
	if [ -n "$advertises" ]; then
		printf '==> The registry will tell older loadout CLIs that %s is out\n' "$advertises"
	else
		printf '==> The registry will advertise no CLI release: HEAD is not a clean release tag\n'
	fi
	(
		cd "$root"
		CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath \
			-ldflags "-s -w -X main.version=$version" -o "$stage/loadout" ./cmd/loadout
	)
}

# check_env_locally runs the local loadout's check over the env file, so a
# typo stops here, before anything is uploaded. It is skipped, with a line
# saying so, when there is no loadout here or it predates `serve --check`;
# the host checks again with the binary it is about to run. A local loadout
# older than the one deployed may not know a new variable: update it.
check_env_locally() {
	[ -n "$ENV_FILE" ] || return 0
	if ! command -v loadout >/dev/null 2>&1; then
		printf '==> No loadout on this machine to check %s; the host checks it before restarting\n' "$ENV_FILE"
		return 0
	fi
	if ! loadout serve --help 2>&1 | grep -q -- '--check'; then
		printf '==> The loadout on this machine is too old for serve --check; the host checks %s before restarting\n' "$ENV_FILE"
		return 0
	fi
	printf '==> Checking %s with the loadout on this machine\n' "$ENV_FILE"
	loadout serve --check --env-file "$ENV_FILE" || die "the settings file is not right (see above); fix it and run again"
}

# build_downloads makes $stage/dl.tar: install.sh for this registry and, in
# dl/, one archive of the CLI per system with the registry's flags plus the
# install method, checksums.txt and VERSION. The names carry no version
# because every deploy replaces them. It builds in .dl, a hidden folder of
# the stage, so the scp glob below uploads only the tar.
build_downloads() {
	command -v go >/dev/null 2>&1 || die "go is not installed, and LOADOUT_DOWNLOADS builds the CLI here"
	command -v tar >/dev/null 2>&1 || die "tar is not installed"
	printf '==> Building the loadout CLI for darwin and linux\n'
	dl="$stage/.dl/dl"
	mkdir -p "$dl" "$stage/.dl/bin"
	for os_arch in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
		os="${os_arch%/*}"
		arch="${os_arch#*/}"
		(
			cd "$root"
			CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
				-ldflags "-s -w -X main.version=$version -X main.installMethod=registry" \
				-o "$stage/.dl/bin/loadout" ./cmd/loadout
		)
		COPYFILE_DISABLE=1 tar --no-xattrs -C "$stage/.dl/bin" -czf "$dl/loadout_${os}_${arch}.tar.gz" loadout
		rm -f "$stage/.dl/bin/loadout"
	done
	rm -rf "$stage/.dl/bin"
	(
		cd "$dl"
		for f in loadout_*.tar.gz; do
			printf '%s  %s\n' "$(sha256 "$f")" "$f"
		done >checksums.txt
	)
	printf '%s\n' "$version" >"$dl/VERSION"
	# The address is the one people fetch the script from, so each registry's
	# copy knows its own. DOMAIN was checked to be host name characters
	# above, which are safe in sed.
	sed "s|__LOADOUT_BASE_URL__|https://$DOMAIN|g" "$here/install.sh" >"$stage/.dl/install.sh"
	COPYFILE_DISABLE=1 tar --no-xattrs -C "$stage/.dl" -cf "$stage/dl.tar" .
}

version=""
if [ -n "$BINARY" ]; then
	[ -f "$BINARY" ] || die "LOADOUT_BINARY does not exist: $BINARY"
	# A Mac or Windows binary would install fine and never start.
	[ "$(head -c 4 "$BINARY" | od -An -tx1 | tr -d ' \n')" = 7f454c46 ] ||
		die "LOADOUT_BINARY is not a linux binary (it does not start with the ELF header): $BINARY"
	printf '==> Installing the binary %s\n' "$BINARY"
	cp "$BINARY" "$stage/loadout"
	version="the given binary"
elif [ -n "$VERSION_WANTED" ]; then
	v="${VERSION_WANTED#v}"
	printf '%s\n' "$v" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
		die "LOADOUT_VERSION is a release such as 0.2.0, got $VERSION_WANTED"
	fetch_release "$v"
	version="v$v"
else
	build_checkout
fi
check_env_locally

cp "$here/loadout.service" "$here/loadout-backup.service" "$here/loadout-backup.timer" \
	"$here/loadout-backup.sh" "$here/Caddyfile" "$stage/"
# The env file holds secrets: copied without printing it, into the 0700
# stage folder, and installed root-only on the host.
if [ -n "$ENV_FILE" ]; then
	(umask 077 && cp "$ENV_FILE" "$stage/loadout.env")
fi
[ -z "$PREPARE" ] || cp "$here/prepare.sh" "$stage/"
# The CLI downloads, only when asked for; see downloads.routes.
if [ -n "$DOWNLOADS" ]; then
	build_downloads
	cp "$here/downloads.routes" "$stage/"
fi
# Static pages, only when asked for; see site.caddy.
if [ -n "$SITE_DIR" ]; then
	cp "$here/site.caddy" "$stage/"
	COPYFILE_DISABLE=1 tar --no-xattrs -C "$SITE_DIR" -cf "$stage/site.tar" .
fi

printf '==> Uploading to %s\n' "$target"
remote=$(ssh "$target" 'mktemp -d') || die "cannot ssh to $target"
# The upload holds loadout.env: never leave it behind on the host.
scp -q "$stage"/* "$target:$remote/" || {
	# shellcheck disable=SC2029 # $remote is meant to expand here.
	ssh "$target" "rm -rf '$remote'" || true
	die "upload to $target failed"
}

printf '==> Installing\n'
# The values below are plain (checked above or made by mktemp), so they can
# be written into the script; the script itself is quoted and expands
# nothing here.
{
	printf "remote='%s'\ndomain='%s'\nsite_domain='%s'\nadmin='%s'\nprepare='%s'\n" \
		"$remote" "$DOMAIN" "$SITE_DOMAIN" "$SSH_USER" "$PREPARE"
	cat <<'INSTALL'
set -eu
# The upload holds loadout.env: remove it however the install ends.
trap "cd / && rm -rf '$remote'" EXIT
cd "$remote"
if [ -n "$prepare" ]; then
	sh prepare.sh "$admin"
fi
id loadout >/dev/null 2>&1 || useradd --system --home-dir /var/lib/loadout --shell /usr/sbin/nologin loadout
install -d -m 0755 -o root -g root /usr/local/lib/loadout
install -d -m 0750 -o loadout -g loadout /var/lib/loadout /var/backups/loadout
command -v caddy >/dev/null 2>&1 || { echo "Caddy is not installed on this host: deploy once with LOADOUT_PREPARE=1, or install Caddy yourself" >&2; exit 1; }

# Check the settings with the binary that will run them, before it replaces
# the running one: a typo or a half-set pair stops the deploy here and the
# service keeps running as it was.
install -m 0755 -o root -g root loadout /usr/local/bin/loadout.new
envfile=""
if [ -f loadout.env ]; then
	envfile=loadout.env
elif [ -f /etc/loadout/loadout.env ]; then
	envfile=/etc/loadout/loadout.env
fi
if [ -n "$envfile" ]; then
	if /usr/local/bin/loadout.new serve --help 2>&1 | grep -q -- '--check'; then
		echo "checking the settings on the host"
		# HOME is the upload folder, so the check leaves nothing behind.
		HOME="$remote" /usr/local/bin/loadout.new serve --check --env-file "$envfile" || {
			rm -f /usr/local/bin/loadout.new
			echo "The settings are not right (see above). The running service was not touched." >&2
			exit 1
		}
	else
		echo "this loadout is too old for serve --check; the settings were not checked"
	fi
fi
mv -f /usr/local/bin/loadout.new /usr/local/bin/loadout

install -m 0755 -o root -g root loadout-backup.sh /usr/local/lib/loadout/loadout-backup.sh
# host-facts:begin
# The service's sandbox hides /proc/cpuinfo, so the CPU model is written here
# once per deploy for the server page. A system without the line gets an empty file.
{ grep -m1 '^model name' /proc/cpuinfo || true; } | sed 's/^model name[[:space:]]*:[[:space:]]*/cpu_model=/' | tr -cd '[:print:]\n' > /usr/local/lib/loadout/host-facts.new
chmod 0644 /usr/local/lib/loadout/host-facts.new
mv -f /usr/local/lib/loadout/host-facts.new /usr/local/lib/loadout/host-facts
# host-facts:end
install -m 0644 -o root -g root loadout.service loadout-backup.service loadout-backup.timer /etc/systemd/system/
if [ -f loadout.env ]; then
	install -d -m 0755 -o root -g root /etc/loadout
	install -m 0600 -o root -g root loadout.env /etc/loadout/loadout.env
	rm -f loadout.env
	echo "installed /etc/loadout/loadout.env"
fi

install -d -m 0755 /etc/systemd/system/caddy.service.d
printf '[Service]\nEnvironment=LOADOUT_DOMAIN=%s\nEnvironment=LOADOUT_SITE_DOMAIN=%s\n' "$domain" "$site_domain" > /etc/systemd/system/caddy.service.d/loadout.conf
# Check the configuration as it will be (the Caddyfile, the files already in
# loadout.d and the new site.caddy) before anything replaces the live one.
install -d -m 0755 -o root -g root /etc/caddy/loadout.d
rm -rf check.d
install -d check.d
cp /etc/caddy/loadout.d/*.caddy check.d/ 2>/dev/null || true
rm -f check.d/site.caddy check.d/downloads.routes
[ ! -f site.caddy ] || cp site.caddy check.d/
[ ! -f downloads.routes ] || cp downloads.routes check.d/
sed "s#/etc/caddy/loadout.d#$remote/check.d#" Caddyfile > Caddyfile.check
LOADOUT_DOMAIN="$domain" LOADOUT_SITE_DOMAIN="$site_domain" caddy adapt --adapter caddyfile --config Caddyfile.check >/dev/null
install -m 0644 -o root -g root Caddyfile /etc/caddy/Caddyfile
if [ -f site.caddy ]; then
	install -m 0644 -o root -g root site.caddy /etc/caddy/loadout.d/site.caddy
	# Replace the static site in one step so Caddy never serves half of it.
	rm -rf /var/www/loadout-site.new
	install -d -m 0755 -o root -g root /var/www /var/www/loadout-site.new
	tar -C /var/www/loadout-site.new --no-same-owner --no-same-permissions -xf site.tar
	chown -R root:root /var/www/loadout-site.new
	chmod -R u=rwX,go=rX /var/www/loadout-site.new
	rm -rf /var/www/loadout-site.old
	[ ! -d /var/www/loadout-site ] || mv /var/www/loadout-site /var/www/loadout-site.old
	mv /var/www/loadout-site.new /var/www/loadout-site
	rm -rf /var/www/loadout-site.old
else
	# Its domain is no longer set, so its block could not start.
	rm -f /etc/caddy/loadout.d/site.caddy
fi

if [ -f downloads.routes ]; then
	install -m 0644 -o root -g root downloads.routes /etc/caddy/loadout.d/downloads.routes
	# Replace the download folder in one step like the site, so nobody gets
	# half of an update.
	rm -rf /var/www/loadout-dl.new
	install -d -m 0755 -o root -g root /var/www /var/www/loadout-dl.new
	tar -C /var/www/loadout-dl.new --no-same-owner --no-same-permissions -xf dl.tar
	chown -R root:root /var/www/loadout-dl.new
	find /var/www/loadout-dl.new -type d -exec chmod 0755 {} +
	find /var/www/loadout-dl.new -type f -exec chmod 0644 {} +
	rm -rf /var/www/loadout-dl.old
	[ ! -d /var/www/loadout-dl ] || mv /var/www/loadout-dl /var/www/loadout-dl.old
	mv /var/www/loadout-dl.new /var/www/loadout-dl
	rm -rf /var/www/loadout-dl.old
	echo "installed /var/www/loadout-dl"
else
	rm -rf /etc/caddy/loadout.d/downloads.routes /var/www/loadout-dl
fi

systemctl daemon-reload
systemctl enable --quiet loadout.service loadout-backup.timer
systemctl restart loadout.service
systemctl start loadout-backup.timer
systemctl restart caddy.service
sleep 1
systemctl is-active --quiet loadout.service || { journalctl -u loadout --no-pager -n 30; exit 1; }
/usr/local/bin/loadout version
INSTALL
} | ssh "$target" sudo sh -s

printf '==> Checking https://%s/v1/health\n' "$DOMAIN"
# The first run waits for Caddy to get its certificate.
curl -fsS --retry 20 --retry-delay 3 --retry-all-errors "https://$DOMAIN/v1/health" ||
	die "health check failed; see: ssh $target journalctl -u loadout -u caddy -n 50"
printf '\n==> Deployed %s to https://%s\n' "$version" "$DOMAIN"
