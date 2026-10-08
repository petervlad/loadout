#!/bin/sh
# Prepare a Debian or Ubuntu machine to run the loadout registry: packages,
# SSH hardening, firewall, Caddy from its apt repository, the loadout user
# and its folders. It is idempotent, so running it twice changes nothing the
# second time. deploy.sh runs it on the host (as root, through sudo) when
# LOADOUT_PREPARE=1 is set, which is for the first deploy only:
#
#   sudo sh prepare.sh ADMIN_USER
#
# ADMIN_USER is the account you ssh in as. It must already exist and hold your
# ssh key: cloud-init.yaml creates it on a cloud VM, or create it by hand on
# any other machine (see README.md). The script then allows only that user to
# ssh in, with a key, and refuses to start if it cannot see a key for it, so
# it cannot lock you out.
set -eu

die() {
	printf 'prepare: %s\n' "$*" >&2
	exit 1
}

[ "$(id -u 2>/dev/null)" = 0 ] || die "run as root: sudo sh prepare.sh ADMIN_USER"
[ $# -eq 1 ] || die "usage: prepare.sh ADMIN_USER"
admin=$1
case "$admin" in
*[!a-z0-9_-]* | [!a-z_]*) die "ADMIN_USER is not a plain user name: $admin" ;;
esac
id "$admin" >/dev/null 2>&1 || die "the user $admin does not exist; create it with your ssh key first (see README.md)"
command -v apt-get >/dev/null 2>&1 || die "this script needs apt (Debian or Ubuntu); on another system do the same steps by hand (see README.md)"

home=$(getent passwd "$admin" | cut -d: -f6)
[ -s "$home/.ssh/authorized_keys" ] || die "$admin has no ssh key in $home/.ssh/authorized_keys; add yours before turning passwords off"

export DEBIAN_FRONTEND=noninteractive
# A fresh cloud image may still be installing updates; wait for it, and for
# the package lock, instead of failing.
if command -v cloud-init >/dev/null 2>&1; then cloud-init status --wait >/dev/null 2>&1 || true; fi
apt="apt-get -o DPkg::Lock::Timeout=300"

printf '==> Packages\n'
$apt update -qq
$apt install -y -qq curl gpg sqlite3 ufw unattended-upgrades

printf '==> Automatic security updates\n'
cat >/etc/apt/apt.conf.d/20auto-upgrades <<'CONF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
CONF
cat >/etc/apt/apt.conf.d/52unattended-upgrades-local <<'CONF'
Unattended-Upgrade::Remove-Unused-Dependencies "true";
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:30";
CONF
systemctl enable --now unattended-upgrades >/dev/null 2>&1 || true

printf '==> SSH: key only, no root, %s only\n' "$admin"
install -d -m 0755 /etc/ssh/sshd_config.d
cat >/etc/ssh/sshd_config.d/10-hardening.conf <<CONF
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
AllowUsers $admin
CONF
# A bad configuration must not reach the running daemon.
sshd -t || {
	rm -f /etc/ssh/sshd_config.d/10-hardening.conf
	die "sshd refused the hardening file, so it was removed and ssh is unchanged; check the ssh configuration on this machine"
}
systemctl restart ssh 2>/dev/null || systemctl restart sshd

printf '==> Firewall: ssh, http, https (tcp and quic)\n'
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw allow 22/tcp >/dev/null
ufw allow 80/tcp >/dev/null
ufw allow 443/tcp >/dev/null
ufw allow 443/udp >/dev/null
ufw --force enable >/dev/null

printf '==> Caddy\n'
if ! command -v caddy >/dev/null 2>&1; then
	curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
	curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt -o /etc/apt/sources.list.d/caddy-stable.list
	chmod 0644 /usr/share/keyrings/caddy-stable-archive-keyring.gpg /etc/apt/sources.list.d/caddy-stable.list
	$apt update -qq
	$apt install -y -qq caddy
fi

printf '==> The loadout user and folders\n'
# The data folder is also made by systemd (StateDirectory); the backup folder
# must exist for the backup unit.
id loadout >/dev/null 2>&1 || useradd --system --home-dir /var/lib/loadout --shell /usr/sbin/nologin loadout
install -d -m 0750 -o loadout -g loadout /var/lib/loadout /var/backups/loadout
install -d -m 0755 -o root -g root /usr/local/lib/loadout

printf '==> Prepared. Deploy loadout next (deploy.sh).\n'
