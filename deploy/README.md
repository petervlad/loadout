# Run the registry on your own host

`loadout serve` is one static binary with a SQLite database. Two ways to run it:

- **A Linux host you can ssh to** (a cloud VM, a VPS, a home or office server): Caddy terminates HTTPS and proxies to `loadout serve` on `127.0.0.1:7420`, systemd runs it, no Docker. This is the rest of this page. `prepare.sh` and `deploy.sh` do the work on Debian and Ubuntu; on another system, do the same steps by hand (they are plain commands in `prepare.sh`).
- **A container platform** (Fly.io and similar): see [Containers](#containers).

Either way, run **one instance only**: the database is SQLite and the sign-in limits live in the process.

| File | What it is |
| --- | --- |
| `cloud-init.yaml` | Optional VM user data: the admin user with your ssh key, no root login, no ssh passwords |
| `prepare.sh` | Packages, ssh hardening, firewall, Caddy, the `loadout` user and folders. Idempotent. Run once by `deploy.sh` with `LOADOUT_PREPARE=1` |
| `deploy.sh` | Installs the binary, units, settings and Caddy files on the host and checks that it came up |
| `loadout.env.example` | Every setting, commented out, to copy and fill in |
| `loadout.service` | `/etc/systemd/system/`, the hardened server unit |
| `Caddyfile` | `/etc/caddy/Caddyfile`: the registry's block, and an import of `/etc/caddy/loadout.d/*.caddy` |
| `site.caddy` | `/etc/caddy/loadout.d/site.caddy`, only when you ask for a static site (below) |
| `loadout-backup.sh`, `.service`, `.timer` | Nightly backup to `/var/backups/loadout`, 14 days kept |
| `container/` | A `Dockerfile` and a `fly.toml.example` |

On your own machine `deploy.sh` needs a POSIX shell, `ssh`, `scp` and `curl`, and `go` only when it builds from a checkout.

## 1. Get a machine

Any Debian or Ubuntu machine you can ssh to, reachable on ports 22, 80 and 443 (tcp, and 443 udp). A small one is plenty: 1 vCPU and 1 GB of memory. Providers differ in how you create it (Hetzner, AWS Lightsail, DigitalOcean, Vultr and others all work; a spare computer at home works too, see the note on names below).

**A cloud VM with cloud-init.** Put your ssh public key into `cloud-init.yaml` (replace `AAAA_REPLACE_WITH_YOUR_PUBLIC_KEY`) and paste the file as the server's user data ("Cloud config"). For example with Hetzner's CLI: `hcloud server create --name loadout --type <type> --image ubuntu-24.04 --user-data-from-file deploy/cloud-init.yaml --ssh-key <key>`. Wait for it: `ssh admin@<ip> cloud-init status --wait`.

**Any other machine.** Create the admin user once, as root, with your key:

```sh
adduser --disabled-password --gecos "" admin
install -d -m 700 -o admin -g admin /home/admin/.ssh
echo 'ssh-ed25519 AAAA... you@laptop' > /home/admin/.ssh/authorized_keys
chown admin:admin /home/admin/.ssh/authorized_keys && chmod 600 /home/admin/.ssh/authorized_keys
echo 'admin ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/admin && chmod 440 /etc/sudoers.d/admin
```

`deploy.sh` runs `sudo` without a password and `prepare.sh` then allows only this user to ssh in, with a key. It refuses to start if it finds no key for the user, so it cannot lock you out. Use `LOADOUT_SSH_USER` if the user is not called `admin`.

## 2. Pick a name

- Own domain: create an `A` record (and `AAAA` if you use IPv6) pointing at the machine, and wait until `dig +short registry.example.com` shows it before the first deploy (Let's Encrypt limits failed attempts).
- No domain: use `<ip-with-dashes>.sslip.io`, e.g. `203-0-113-7.sslip.io` for `203.0.113.7`. It resolves to that IP and Caddy gets a real Let's Encrypt certificate for it.
- A machine on a private network cannot get a public certificate from Caddy; put a tunnel in front (see "Behind a tunnel or another proxy") or use Caddy's `tls internal` and install its root certificate on each laptop. The web pages need HTTPS, because session cookies are `Secure` except on `http://localhost`.

## 3. Write the settings

All of loadout's login settings are environment variables. Copy the template, fill it in with your editor (the passwords stay in your file, nobody else needs to see them), and check it:

```sh
install -m 600 deploy/loadout.env.example loadout.env
$EDITOR loadout.env
loadout serve --check --env-file loadout.env
```

`serve --check` reads the file the way systemd does (it never runs it), builds the mail and Google settings the way `serve` would without using the network, prints one line per part and never a password, and exits 1 when a variable is wrong or one that loadout does not read (a typo such as `LOADOUT_SMTP_PASS` would otherwise silently turn email off). The settings are optional: with none, the registry serves the token API and teammates join with invites.

## 4. Deploy

The first deploy of a prepared-by-hand or cloud-init machine adds `LOADOUT_PREPARE=1`, which runs `prepare.sh` on the host first. The binary comes from one of three places:

```sh
# a release, downloaded and checked against its checksums.txt (SHA-256); needs no checkout and no Go
LOADOUT_PREPARE=1 LOADOUT_VERSION=0.2.0 LOADOUT_ENV_FILE=loadout.env \
LOADOUT_HOST=203.0.113.7 LOADOUT_DOMAIN=203-0-113-7.sslip.io sh deploy.sh

# a linux binary you already have
LOADOUT_BINARY=./loadout-linux-amd64 ...

# this checkout, built with go (the default)
LOADOUT_HOST=... LOADOUT_DOMAIN=... deploy/deploy.sh
```

`LOADOUT_VERSION` refuses a download whose checksum does not match, and `LOADOUT_RELEASE_URL` points it at a mirror. Without a checkout and without either variable, `deploy.sh` stops and names them. Other options: `LOADOUT_SSH_USER` (default `admin`), `LOADOUT_ARCH=arm64` for Arm machines. Re-run the command (without `LOADOUT_PREPARE`) to upgrade; the settings file on the host is kept unless you pass `LOADOUT_ENV_FILE` again. It ends by calling `https://$LOADOUT_DOMAIN/v1/health`.

Before it restarts anything, `deploy.sh` checks the settings with `loadout serve --check`, on your machine when it has a `loadout` and on the host with the binary about to be installed, and runs `caddy adapt` on the Caddy configuration it is about to install. A mistake stops the deploy and the running service is not touched.

### Releases: deploy from a tag

The registry tells the loadout CLIs that talk to it which release it was built from, so a person on an older CLI hears that a newer one is out. It does so only when it was built exactly from a clean release tag: building from a checkout, `deploy.sh` stamps `git describe --tags --match 'v[0-9]*' --dirty`, and says before building which release the registry will advertise. A commit after a tag, uncommitted changes, or no tag at all advertises nothing, and the CLIs stay quiet. A release downloaded with `LOADOUT_VERSION` is built from its tag by the project's release workflow.

To build a release yourself, tag, then deploy that tag from a clean checkout:

```sh
git tag -a v0.2.0 -m "loadout 0.2.0"
git worktree add ../loadout-v0.2.0 v0.2.0        # a clean tree, whatever the main checkout holds
LOADOUT_HOST=203.0.113.7 LOADOUT_DOMAIN=registry.example.com ../loadout-v0.2.0/deploy/deploy.sh
git worktree remove ../loadout-v0.2.0
```

Tag when the CLI changed in a way people should take; a fix only the server needs can be deployed without a tag (the registry then advertises nothing until the next tagged deploy, and CLIs keep what they last heard for up to 30 days). Pushing a `v*` tag to GitHub also runs `.github/workflows/release.yml`, which builds the release archives and `checksums.txt`.

### A static site next to the registry (optional)

Caddy can also serve a folder of static pages under a second name, with strict headers. Give `deploy.sh` both the folder and the name; without them no site is installed, and a `site.caddy` left by an earlier deploy is removed:

```sh
LOADOUT_SITE_DIR=./site LOADOUT_SITE_DOMAIN=example.com \
LOADOUT_HOST=203.0.113.7 LOADOUT_DOMAIN=registry.example.com deploy/deploy.sh
```

The pages go to `/var/www/loadout-site` (replaced in one step), and `site.caddy` to `/etc/caddy/loadout.d/`. `deploy.sh` runs `caddy adapt` on the configuration it is about to install, with the files already in `loadout.d`, and stops before replacing anything if Caddy refuses it. Caddy logs a warning that `loadout.d` matched no files when nothing is there; that is harmless.

### Behind a tunnel or another proxy

loadout limits sign-in attempts per client address. By default it reads the address from `X-Real-IP`, which only Caddy on the same machine may set (Caddy sends it from `{client_ip}`). Two cases need a setting:

- A tunnel or load balancer ends on the VM and Caddy sees only the tunnel's address. Put a global block in `/etc/caddy/loadout.d/00-proxies.caddy` so Caddy trusts the tunnel and reads the header it sets (the file is imported before the registry's block, and `deploy.sh` leaves other files in that folder alone):

  ```
  {
  	servers {
  		trusted_proxies static 203.0.113.0/24
  		client_ip_headers CF-Connecting-IP
  	}
  }
  ```

- A proxy reaches `loadout serve` directly, without Caddy (a container platform, see `container/`). Tell loadout in its environment file which addresses it connects from and which header carries the client's address:

  ```sh
  LOADOUT_TRUSTED_PROXIES=203.0.113.0/24,2001:db8::/32
  LOADOUT_CLIENT_IP_HEADER=Fly-Client-IP
  ```

  Use the single-address header your platform documents. `X-Forwarded-For` and `Forwarded` are refused (clients can extend them), and so is a range that covers every address, since then clients would pick their own limit key. `loadout serve --check` prints a `proxies` line.

Check a Caddy change without installing it by running `caddy adapt --adapter caddyfile --config Caddyfile` with `LOADOUT_DOMAIN` set, after pointing the `import` line at a folder of your own.

## 5. Create a publish token

```sh
ssh admin@203.0.113.7
sudo -u loadout loadout token create --team <team> --publish --data /var/lib/loadout
```

The token is shown once. Hand it to the publisher; readers get a token without `--publish`.

## 6. Login

Web and CLI login (`loadout login`, see [docs/DESIGN.md](../docs/DESIGN.md#login)) is configured by the settings in section 3, which the unit reads from `/etc/loadout/loadout.env` (`EnvironmentFile=-...`, so the file is optional; without it the registry behaves as before). systemd reads the file as root before dropping to the `loadout` user, so it stays `root:root 0600`. There are three ways to let people in:

- **Email codes** (the usual one): `LOADOUT_PUBLIC_URL` plus an SMTP provider (`LOADOUT_SMTP_HOST`, `_USER`, `_PASSWORD` and `LOADOUT_MAIL_FROM`). People sign up themselves and get a code by mail. The sender address must be verified at your provider, and its domain should carry the provider's SPF and DKIM records or the mail lands in spam. Send one test message and wait for the mail server's answer (see "Check the configuration" below).
- **Email codes and Google**: the above plus `LOADOUT_GOOGLE_CLIENT_ID` and `_SECRET`; in the Google Cloud console the OAuth client's authorized redirect URI is `https://<domain>/auth/google/callback`.
- **Login without mail**: `LOADOUT_LOGIN=simple` (below).

After a deploy, `journalctl -u loadout -n 20` shows a "login is on" line with what is enabled (never a secret).

Then add people on the host and let them sign in:

```sh
sudo -u loadout loadout admin grant --email you@example.com --data /var/lib/loadout
sudo -u loadout loadout member add --namespace payments --email alice@example.com --role maintainer --data /var/lib/loadout
loadout login --remote https://registry.example.com     # Alice, on her laptop
```

## Server page (optional)

Registry admins see the host, disk, backups and counts on the website under Account, **Server details**. The disk, backup and count parts need nothing. To describe the host, add lines to your settings file and deploy it (or edit `/etc/loadout/loadout.env` on the host, root only, 0600, and restart). Every line is optional plain text; the console must be an `https` link:

```sh
LOADOUT_HOST_PROVIDER=<for example Example Cloud>
LOADOUT_HOST_REGION=<region, project or data centre>
LOADOUT_HOST_SIZE=<for example 2 vCPU, 4 GB RAM, 40 GB disk>
LOADOUT_HOST_CONTACT=<a team or an address>
LOADOUT_HOST_CONSOLE=<https link to the host's console>
```

```sh
sudo systemctl restart loadout
journalctl -u loadout -n 20     # a "server page" line names host_provider
```

On a VM that is not at Hetzner, make sure no `LOADOUT_HETZNER_` lines are left in the file (a copy of the old one has them): with a token set the page also shows the Hetzner server, and the log warns.

Only on a Hetzner VM, for the Hetzner part (server, traffic and an estimated monthly cost), create a token in the Hetzner Console: the server's project, Security, API tokens, Generate API token, permission **Read**. Then add:

```sh
LOADOUT_HETZNER_TOKEN=<the read-only token>
LOADOUT_HETZNER_SERVER=<the server's id or name>
```

Keep them in your `LOADOUT_ENV_FILE` (a deploy that installs a file replaces the one on the host). The token is only sent to `api.hetzner.cloud`, with GET requests.

## 7. Backups

`loadout-backup.timer` runs nightly at about 03:15. It copies each SQLite database with `sqlite3 .backup` (consistent while the server runs), adds the other files from `/var/lib/loadout`, and writes `/var/backups/loadout/loadout-<UTC time>.tar.gz`. Archives older than 14 days are deleted.

```sh
sudo systemctl start loadout-backup.service   # back up now
systemctl list-timers loadout-backup.timer    # next run
journalctl -u loadout-backup -n 20            # last result
```

The backups live on the same disk. Copy them off the machine (your provider's snapshots or backups, or `rsync` from your own machine).

Restore:

```sh
sudo systemctl stop loadout
sudo find /var/lib/loadout -mindepth 1 -delete
sudo tar -C /var/lib/loadout -xzf /var/backups/loadout/loadout-<time>.tar.gz
sudo chown -R loadout:loadout /var/lib/loadout
sudo systemctl start loadout
```

## Operate

```sh
journalctl -u loadout -f                     # server log
sudo tail -f /var/log/caddy/loadout-access.log
systemd-analyze security loadout             # hardening score
```

## Check the configuration

Before restarting after a change to `/etc/loadout/loadout.env`, check it. This reads the file the way systemd does (it never runs it), builds the mail and Google settings the way `loadout serve` would, without the network, and prints one line per part. It never prints a password or token, and it names a variable that is wrong or that loadout does not read and then exits 1 (a typo such as `LOADOUT_SMTP_PASS` would otherwise silently turn email off):

```sh
sudo loadout serve --check --env-file /etc/loadout/loadout.env
```

To prove mail works, send one message and wait for the mail server's answer. It runs with the service's environment, so the file is never read by you or printed:

```sh
sudo systemd-run --pipe --wait --uid=loadout \
  --property=EnvironmentFile=/etc/loadout/loadout.env \
  /usr/local/bin/loadout mail test --to you@example.com
```

`LOADOUT_DOCS_URL` (optional) points the pages' Docs and What's new links at a guide you host yourself, for a registry without internet access.

## Login without mail

For a registry where only email and password sign-in should exist (no Google, no mailed code, no sign-up), set one line in the settings file (and add `LOADOUT_PUBLIC_URL`) and deploy, or add it to `/etc/loadout/loadout.env` and restart:

```sh
LOADOUT_LOGIN=simple
```

**The first admin.** The first time `loadout serve` starts on a registry with no account at all, it creates one admin, `admin@loadout.local` (verified, the same admin role `admin grant` gives), so a stranger can sign in at once with no shell commands:

1. Deploy. Read the one-time password from the service's log: `sudo journalctl -u loadout | grep -A3 'first admin created'`. It is printed once, in a marked block on stderr, and is not written to loadout's log file, the database (only its argon2id hash) or any settings file. Or set `LOADOUT_ADMIN_PASSWORD` in the settings file before the first start: that password is used and never printed (12 to 128 characters, not a common one; otherwise the service refuses to start and says why).
2. Sign in at `https://<domain>/auth/login` as `admin@loadout.local`.
3. Change the password, because the systemd journal keeps the printed block: on the host, `sudo -u loadout loadout user password --email admin@loadout.local --data /var/lib/loadout` (type the new one). The old one stops working and every session of that account ends. The account page shows a reminder while the account exists. Then add your own account (below); if you like, remove this one afterwards with `loadout user delete --email admin@loadout.local`.

It is created only when the accounts table is completely empty. Registries that already have an account are untouched, and deleting the admin later does not bring it back unless every account is gone again. `loadout serve --check` says `first admin: will be created (empty database)` or `first admin: exists`, from a read-only look at the database, and never prints a password. It is simple login only: in email modes the address cannot receive a code, so make the first admin with `loadout admin grant` and sign up instead.

Give everyone else an account on the host, and make more admins if needed:

```sh
sudo -u loadout loadout user password --email you@example.com --data /var/lib/loadout   # type the password, or pipe one line
sudo -u loadout loadout admin grant --email you@example.com --data /var/lib/loadout
```

The password comes from standard input, never from a flag, and must pass the usual checks (12 to 128 characters, not a common one). Running it again replaces the password and ends that user's sessions. People cannot sign up or reset a password themselves: you set every password and hand it over out of band. Switching to email later is one line removed and the mail settings added; the accounts keep their passwords. `journalctl -u loadout -n 20` shows "simple login is on".

## Containers

`container/Dockerfile` builds an image from a release (nothing is compiled): a static binary on a distroless base, not root, `/data` as the volume, `loadout serve --data /data --addr :8080`. It downloads `loadout_X.Y.Z_linux_ARCH.tar.gz` and `checksums.txt` and stops if the SHA-256 does not match.

```sh
docker build --target registry --build-arg LOADOUT_VERSION=0.2.0 -t loadout-registry deploy/container
docker run -p 8080:8080 -v loadout-data:/data \
  -e LOADOUT_PUBLIC_URL=https://registry.example.com loadout-registry
```

TLS is the platform's. Because its proxy is not on the same machine, set `LOADOUT_TRUSTED_PROXIES` and `LOADOUT_CLIENT_IP_HEADER` (see "Behind a tunnel or another proxy"); without them every visitor shares the proxy's address and its sign-in limits. `fly.toml.example` shows Fly.io: one machine, one volume, a health check on `/v1/health`, forced HTTPS, and a `root-volume` build target because Fly mounts volumes root-owned. It names the proxy settings but leaves Fly's address range for you to fill in from Fly's documentation. Passwords and tokens go in the platform's secrets (`fly secrets import` reads `NAME=VALUE` lines from standard input, so they stay out of the shell history), never in the file. Deploy with `fly deploy --ha=false`: by default Fly adds a spare machine, and this registry must run on one. The container shape has no nightly backup unit: snapshot the volume with the platform's tools.

The Dockerfile and `fly.toml.example` were written from the platforms' documentation and have not been run against a live account by the project's tests; try them with a throwaway app first.
