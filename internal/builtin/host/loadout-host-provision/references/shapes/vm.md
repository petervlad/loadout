# The VM shape

A Debian or Ubuntu machine you can ssh to: a cloud VM, a VPS, a home server or a local VM. Caddy ends HTTPS and proxies to `loadout serve` on `127.0.0.1:7420`, systemd runs it, there is no Docker. `prepare.sh` uses `apt`; on another system the person does the same steps by hand (they are plain commands in `assets/prepare.sh`). A small machine is plenty: 1 vCPU and 1 GB of memory, 10 GB of disk. Run one instance only: the database is SQLite and the sign-in limits live in the process.

## 1. The admin user

`deploy.sh` and `prepare.sh` need one user that can ssh in with a key and run `sudo` without a password. Afterwards only that user may ssh in, and only with a key. There are two ways to get it.

**Cloud-init** (most clouds). Copy `assets/cloud-init.yaml` to a scratch folder and replace its `ssh-ed25519 AAAA_REPLACE_WITH_YOUR_PUBLIC_KEY you@laptop` entry with the person's public key line (the contents of the `.pub` file; it is public, you may read and write it). Pass the file as the machine's user data, "Cloud config" (the provider file gives the flag). It creates `admin`, locks the password, turns root login and ssh passwords off. Wait for it: `ssh admin@IP cloud-init status --wait`.

**By hand** (a machine without cloud-init). As root on the machine, with the person's public key in place of the example:

```sh
adduser --disabled-password --gecos "" admin
install -d -m 700 -o admin -g admin /home/admin/.ssh
echo 'ssh-ed25519 AAAA... you@laptop' > /home/admin/.ssh/authorized_keys
chown admin:admin /home/admin/.ssh/authorized_keys && chmod 600 /home/admin/.ssh/authorized_keys
echo 'admin ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/admin && chmod 440 /etc/sudoers.d/admin
```

A cloud image that already has a default user with the person's key and passwordless sudo (`ubuntu` on Lightsail) needs none of this: use that name as `LOADOUT_SSH_USER`.

## 2. First contact

The first `ssh` to a new machine asks whether to trust its host key. `ssh -o StrictHostKeyChecking=accept-new admin@IP true` records it without asking and refuses a changed key later. If the provider's console shows the host key fingerprint, compare it.

## 3. Deploy

From this skill's folder:

```sh
LOADOUT_PREPARE=1 LOADOUT_VERSION=X.Y.Z LOADOUT_HOST=IP LOADOUT_DOMAIN=NAME sh assets/deploy.sh
```

- `LOADOUT_PREPARE=1` runs `prepare.sh` on the host first. Use it on the first deploy only.
- `LOADOUT_SSH_USER` when the user is not `admin`. `LOADOUT_ARCH=arm64` for an Arm machine.
- `LOADOUT_VERSION` downloads that release and refuses a download whose SHA-256 does not match the release's `checksums.txt`. `LOADOUT_BINARY=FILE` installs a Linux binary the person has. `LOADOUT_RELEASE_URL` points at a mirror.
- `LOADOUT_ENV_FILE=FILE` installs the person's settings file as `/etc/loadout/loadout.env`, readable by root only; the `loadout-host-setup` skill does that. Before it restarts anything, the script runs `loadout serve --check` on the host over the file and stops on a mistake, leaving the running service as it was.
- It ends by calling `https://NAME/v1/health`; the first run waits for Caddy's certificate.

`prepare.sh` refuses to start if the admin user has no ssh key, so it cannot lock the person out.

## 4. Check

```sh
curl -fsS https://NAME/v1/health
ssh admin@IP systemctl is-active loadout caddy
ssh admin@IP sudo ufw status
ssh admin@IP systemctl list-timers loadout-backup.timer
ssh admin@IP sudo -u loadout loadout version
```

The first deploy failing at the health check usually means DNS or a closed port 80: `ssh admin@IP journalctl -u caddy -n 30` says what Let's Encrypt answered. Fix the cause, then run the same command again without `LOADOUT_PREPARE`.

## 5. Later

- Upgrade: the same deploy command without `LOADOUT_PREPARE`, with the new `LOADOUT_VERSION`. When the release notes name a database migration, start a backup first: `ssh admin@IP sudo systemctl start loadout-backup.service`.
- Logs: `ssh admin@IP journalctl -u loadout -n 50`.
- The Host panel on the admins' server page reads `LOADOUT_HOST_PROVIDER`, `LOADOUT_HOST_REGION`, `LOADOUT_HOST_SIZE`, `LOADOUT_HOST_CONTACT` and `LOADOUT_HOST_CONSOLE` from the settings file (plain text; `loadout-host-setup` covers it).
