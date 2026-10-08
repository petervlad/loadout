---
name: loadout-host-provision
description: Create and harden the machine for a loadout registry on the user's own provider, give it a name and HTTPS, install loadout with systemd and Caddy, and set up off-host backups. Use when the user wants a server, VM, container app or home machine to run their own loadout registry, or asks to deploy, move or tear down one. Covers Hetzner, AWS Lightsail and EC2, DigitalOcean, Fly.io, any VPS with ssh, and a home server or local VM.
---
# Provisioning a machine for a loadout registry

You make the machine a loadout registry runs on, with the person's own cloud account and the cloud's own command line tool (`hcloud`, `aws`, `doctl`, `fly`), then install loadout on it. loadout never calls a cloud for anyone, never runs a model and never holds an AI key. Everything below runs on the person's computer, as them, with their logins. When the machine is up, hand over to the `loadout-host-setup` skill for the first admin and sign-in mail.

## Rules for you, the agent

- Ask before every step that costs money, changes DNS or deletes anything, naming the resource, the provider and the price. Never tear anything down without a yes for that teardown.
- Never ask for, read, print or paste a secret. Cloud tokens stay in the provider tool's own store: the person logs in themselves (`hcloud context create`, `aws configure` or `aws sso login`, `doctl auth init`, `fly auth login`; most of them prompt for a token or key, which the person types at that prompt, not you). Never pass a token on a command line, never print an environment variable that may hold one, and never open the provider tool's credential files. Only read the person's SSH public key (`~/.ssh/*.pub`), never a private key.
- Never open `loadout.env`, `/etc/loadout/loadout.env` or `~/.loadout/credentials.yaml`. Learn what is set with `loadout serve --check`, and prove mail with `loadout mail test`.
- Never `set -x` around a command that loads an environment file.
- Run commands on the host as the service user with `--data /var/lib/loadout`. The person types passwords into `loadout user password` themselves, in their own terminal.
- loadout never calls a model and holds no AI key: do not look for one or suggest one.
- Provider tools, prices and policies change. Every provider file says what it was checked against and when. When a command fails or its output differs from the file, read the provider's current documentation (the file lists its sources) and tell the person what you found; do not improvise around it.
- Record each resource the moment it exists (name, id, region, what it costs) in a notes file the person chooses, for example `~/loadout-host/notes.md`, with no secrets in it. Teardown deletes only what is in that file.

## Flow

1. **Ask**: where it runs (a provider in the table below, or "I have a machine"), the name (their own domain, or `<ip-with-dashes>.sslip.io` for no domain), and the architecture (`amd64` unless they pick an Arm machine, then `LOADOUT_ARCH=arm64`). Read the matching provider file and its shape file ([shapes/vm.md](references/shapes/vm.md) or [shapes/container.md](references/shapes/container.md)).
2. **Check the tools**: the provider tool is installed and logged in, without reading its credentials: `hcloud context list`, `aws sts get-caller-identity`, `doctl account get`, `fly auth whoami`. If not, tell the person the one command to run themselves. Check `ssh -V`, `curl --version`, and that a public key exists (`ls ~/.ssh/*.pub`); if there is none, give them `ssh-keygen -t ed25519` to run.
3. **Cost**: show what the provider file's Cost section says for the size you will pick, and ask.
4. **Create the machine** with the person's public key, as the provider file says. Open ports 22, 80 and 443 (tcp) and 443 (udp). Record what you created.
5. **Name**: set DNS and wait until `dig +short NAME` returns the address, see [dns.md](references/dns.md). Do this before the first deploy: Let's Encrypt limits failed attempts.
6. **Deploy**: `loadout version` prints the word loadout and the release this CLI is, such as 0.2.0; the registry installs that one. From this skill's folder:

   ```sh
   LOADOUT_PREPARE=1 LOADOUT_VERSION=X.Y.Z LOADOUT_HOST=IP LOADOUT_DOMAIN=NAME LOADOUT_SSH_USER=admin sh assets/deploy.sh
   ```

   `deploy.sh` verifies the download against the release's `checksums.txt`, runs `prepare.sh` on the host (packages, ssh hardening, firewall, Caddy), installs the units and Caddy file, and ends by calling `https://NAME/v1/health`. A dev build has no release: ask which release to install, or use `LOADOUT_BINARY=FILE` with a Linux binary the person has. The loadout files are text and installed without the executable bit, so always start the script with `sh`.
7. **Check**: `curl -fsS https://NAME/v1/health`, and `ssh admin@IP systemctl is-active loadout caddy`.
8. **Backups**: turn on off-host backups and run one restore test, see [backups.md](references/backups.md).
9. **Hand over**: write down for the person what was created, what it costs per month and how to tear it down (the provider file's Tear down section, with the real names). Then continue with the `loadout-host-setup` skill. Re-running step 6 without `LOADOUT_PREPARE` upgrades the registry.

A container platform (Fly.io) has no step 5 to 7 as written: its provider file and [shapes/container.md](references/shapes/container.md) replace them.

## Providers

One file each, all with the same headings: Before you start, Create the machine, Firewall, Name, Deploy, Off-host backups, Cost, Tear down. A provider file says only what differs; the shape file says the rest. For a provider not listed, read the shape file and [vps.md](references/providers/vps.md) and map each heading to the provider's console.

| Provider | Shape | File | You need | Rough cost per month | Notes |
| --- | --- | --- | --- | --- | --- |
| Hetzner Cloud | VM | `references/providers/hetzner.md` | an account, `hcloud` logged in | about 6 EUR | Ports 25 and 465 blocked for new accounts, 587 works |
| AWS Lightsail or EC2 | VM | `references/providers/aws.md` | an account, `aws` configured | about 7 USD (Lightsail) | Port 25 blocked, 587 works; Lightsail first, EC2 for an existing VPC |
| DigitalOcean | VM | `references/providers/digitalocean.md` | an account, `doctl` logged in | about 6 USD | Ports 25, 465 and 587 blocked: mail needs an alternative port such as 2525 |
| Fly.io | container | `references/providers/fly.md` | an account with a card, `fly` logged in | about 4 to 5 USD | One machine and one volume, never scale out |
| Any VPS with ssh | VM | `references/providers/vps.md` | the VPS and its root or admin ssh access | 5 to 10 USD | Vultr, Linode, OVH, Scaleway and others; also the file for an unknown provider |
| Home server or local VM | VM | `references/providers/home.md` | a Debian or Ubuntu machine or VM | none beyond power | LAN only, a port forward, or a tunnel |

## Shared files

| File | What it says |
| --- | --- |
| `references/shapes/vm.md` | A Linux VM: the admin user, first ssh, prepare, deploy, checks |
| `references/shapes/container.md` | A container platform: image, volume, one instance, proxy settings, host commands |
| `references/dns.md` | A and AAAA records, sslip.io, checking with dig before TLS |
| `references/backups.md` | Provider snapshots, pulling the nightly archive, a restore test |

## Assets

The deploy files, copied from the loadout repository's `deploy/` folder when this skill is built, so they match this loadout. Use them from this skill's folder.

| File | What it is |
| --- | --- |
| `assets/deploy.sh` | Installs the binary, units, settings and Caddy files on the host and checks that it came up |
| `assets/prepare.sh` | Packages, ssh hardening, firewall, Caddy, the `loadout` user. Idempotent; `deploy.sh` runs it with `LOADOUT_PREPARE=1` |
| `assets/cloud-init.yaml` | Optional VM user data: the admin user with the person's key, no root login, no ssh passwords |
| `assets/Caddyfile` | `/etc/caddy/Caddyfile` for the registry, with an import of `/etc/caddy/loadout.d/*.caddy` |
| `assets/site.caddy` | A static site block, installed only when `LOADOUT_SITE_DIR` and `LOADOUT_SITE_DOMAIN` are set; not needed here |
| `assets/loadout.service` | The hardened systemd unit |
| `assets/loadout-backup.service` | The nightly backup job |
| `assets/loadout-backup.timer` | Runs the backup job at about 03:15 |
| `assets/loadout-backup.sh` | Writes `/var/backups/loadout/loadout-TIME.tar.gz`, 14 days kept on the same disk |
| `assets/loadout.env.example` | Every setting, commented out; `loadout-host-setup` fills it in |
| `assets/container/Dockerfile` | A static release binary on a distroless base, `/data` volume |
| `assets/container/fly.toml.example` | A Fly.io configuration: one machine, one volume, health check |

`deploy.sh` needs a POSIX shell, `ssh`, `scp` and `curl` on the person's computer, and nothing else when it installs a release.
