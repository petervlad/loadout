# Fly.io

Shape: [container](../shapes/container.md). Tool: `fly` (also installed as `flyctl`).

Verified how: not run end to end. Written from Fly.io's documentation and the `fly` command reference and checked against loadout's `deploy/container` files, on 2026-10-07. Nobody has deployed this registry on Fly with these exact commands. Two parts are known to be unconfirmed: that `fly ssh console` can run `loadout` in an image with no shell (the Dockerfile's final image is distroless), and which address range Fly's proxy connects from (Fly's documentation did not state one when this was written). If either fails, say so and use the fallbacks below.

Sources:
- https://fly.io/docs/about/pricing/
- https://fly.io/docs/reference/configuration/ (`[[vm]]`, `[mounts]`, `[http_service]`)
- https://fly.io/docs/flyctl/launch/, https://fly.io/docs/flyctl/deploy/, https://fly.io/docs/flyctl/volumes-create/, https://fly.io/docs/flyctl/secrets-import/, https://fly.io/docs/flyctl/ssh-console/
- https://fly.io/docs/volumes/snapshots/
- https://fly.io/docs/networking/request-headers/ (`Fly-Client-IP`)
- https://fly.io/docs/networking/custom-domain/

## Before you start

- A Fly.io account with a payment method (there is no free tier), and `fly` logged in: the person runs `fly auth login` (it opens a browser). You check with `fly auth whoami`. Never use `--access-token` or read Fly's config.
- Docker is not needed: `fly deploy` builds the image on Fly's builders from `Dockerfile`.
- Mail: Fly's rules for outgoing SMTP ports were not checked for this file. Port 587 with STARTTLS is the default; prove it with `loadout mail test` after deploying, and if it times out read Fly's current documentation and use the mail provider's alternative port.
- One machine, one volume, always. `fly deploy` creates a spare machine by default; the commands below turn that off.

## Create the machine

In a scratch folder, copy `assets/container/Dockerfile` as `Dockerfile` and `assets/container/fly.toml.example` as `fly.toml`. Edit `fly.toml`: `app` (a unique name; this makes the address `NAME.fly.dev`), `primary_region` (ask; `fly platform regions` lists them), `LOADOUT_VERSION` (the release, without the `v`, from `loadout version`), `LOADOUT_PUBLIC_URL` (`https://NAME.fly.dev`, or the person's own domain once it works), and the SMTP host and sender in `[env]`. Remove `[env]` lines for mail if the registry will not send mail. The file already pins `build-target = "root-volume"`, because Fly mounts volumes root-owned, 512 MB of memory, and one machine.

```sh
fly launch --no-deploy --copy-config --config fly.toml --name NAME
fly volumes create loadout_data --size 1 --region REGION --app NAME
```

The volume is created in the same region as `primary_region`. Record the app name, region and volume. The volume's name must match `[mounts] source` in `fly.toml`.

Secrets, if the registry sends mail or uses Google: the person writes `NAME=VALUE` lines to a file with their own editor (`LOADOUT_SMTP_USER=...`, `LOADOUT_SMTP_PASSWORD=...`; you never open the file) and then you run:

```sh
fly secrets import --app NAME --stage < secrets.txt
```

Then the person deletes the file. `--stage` sets the secrets without deploying yet.

## Firewall

Fly's proxy ends TLS and accepts what `fly.toml` names: `internal_port = 8080` with `force_https = true`. There is nothing to open and no ssh to the machine.

loadout must also know the proxy, or every visitor shares one sign-in limit. Fly sets `Fly-Client-IP` to the client's address. Set `LOADOUT_CLIENT_IP_HEADER = "Fly-Client-IP"` and `LOADOUT_TRUSTED_PROXIES` in `fly.toml`'s `[env]` to the range Fly documents for its proxy: read Fly's current documentation for it, and never use `0.0.0.0/0` (loadout refuses it). If the documentation does not name a range, deploy first without the two lines, then find the address the proxy connects from while the person loads `https://NAME.fly.dev/` in a browser: `fly ssh console --app NAME --pty -C "/usr/local/bin/loadout version"` proves the console works, and a connection list from the machine (`ss -tn`, if the console has it) shows the peer address. Use the narrowest prefix that covers what Fly says, tell the person the address and the reasoning, and confirm with `loadout serve --check`: its `proxies` line must show the header and range.

## Name

Without a domain the app is `https://NAME.fly.dev`, with a certificate from Fly: nothing to do. With the person's own domain: [dns.md](../dns.md) for the records, then `fly certs add registry.example.com --app NAME`, which prints the A and AAAA records to set (allocate addresses with `fly ips allocate-v4` and `fly ips allocate-v6` if the app has none; a dedicated IPv4 costs 2 USD per month, a shared one is free). Update `LOADOUT_PUBLIC_URL` to the new name and deploy again.

## Deploy

```sh
fly deploy --ha=false --app NAME
fly status --app NAME
curl -fsS https://NAME.fly.dev/v1/health
```

`--ha=false` keeps it to one machine. `fly scale count 1 --app NAME` makes sure.

Host commands run in the machine's console, as root, with the data folder `/data`. They have no shell, so give the program path. Anything that asks for a password needs a terminal (`--pty`), which the person runs in their own terminal:

```sh
fly ssh console --app NAME --pty -C "/usr/local/bin/loadout serve --check"
fly ssh console --app NAME --pty -C "/usr/local/bin/loadout admin grant --email you@example.com --data /data"
fly ssh console --app NAME --pty -C "/usr/local/bin/loadout user password --email you@example.com --data /data"
```

If `fly ssh console -C` cannot start a command in this image, say so. The other way in is a Dockerfile built on an image with a shell, which is a change to loadout's container files and not something to improvise: tell the person and stop.

## Off-host backups

Fly takes daily snapshots of every volume and keeps them 5 days; `fly volumes update VOLUME_ID --snapshot-retention 14` (or the `[mounts]` setting `snapshot_retention`) keeps them longer. A snapshot restores into a new volume, which is a new machine's data: `fly volumes create loadout_data --snapshot-id SNAPSHOT_ID --region REGION --app NAME` (see https://fly.io/docs/volumes/snapshots/). Take one before an upgrade with a migration: `fly volumes snapshots create VOLUME_ID`. Snapshots stay at Fly, so they protect against a lost volume or a bad upgrade but not against losing the Fly account: say so plainly. loadout has no command yet that writes a consistent copy from inside the container, and copying the live database file out can give a broken copy, so do not suggest it. Do a restore test: create a volume from the latest snapshot with the command above under a test name, check `fly volumes list` shows it, then delete the test volume (ask first).

## Cost

From Fly's pricing page, checked 2026-10-07 (US dollars): a `shared-cpu-1x` machine with 512 MB is 3.69 per month when it runs all month, 1 GB is 6.70; a volume is 0.15 per GB per month (1 GB: 0.15); volume snapshots 0.08 per GB per month with the first 10 GB free; shared IPv4 and IPv6 addresses are free, a dedicated IPv4 is 2 per month; the first 10 certificates are free; outbound transfer is billed (0.02 per GB in North America and Europe) and a registry uses very little. About 4 to 5 USD per month. Prices differ by region; the page lists them.

## Tear down

Only after a yes for exactly this, and only the app the notes file lists. Destroying the app deletes its machine and volumes, and their snapshots with them: copy anything the person needs first.

```sh
fly apps destroy NAME
```

Remove the DNS records the person added, and any certificate (`fly certs remove HOSTNAME --app NAME` before destroying, if the person keeps the app).
