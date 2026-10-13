# Hetzner Cloud

Shape: [VM](../shapes/vm.md). Tool: `hcloud`.

Verified how: not run end to end. Written from Hetzner's documentation and the `hcloud` reference, and checked against loadout's deploy files, on 2026-10-07. Nobody has created a server with these exact commands, so if a command or a flag name differs from what `hcloud` prints, trust `hcloud help` and Hetzner's documentation (below) and say what you changed.

Sources:
- https://github.com/hetznercloud/cli (command reference under `docs/reference/manual/`)
- https://docs.hetzner.com/cloud/servers/faq/ (blocked mail ports)
- https://docs.hetzner.com/cloud/servers/backups-snapshots/overview/
- https://docs.hetzner.com/general/infrastructure-and-availability/price-adjustment (prices)
- https://docs.hetzner.com/cloud/servers/overview/ (the IPv4 address costs extra)

## Before you start

- A Hetzner Cloud account with a project, and `hcloud` installed with a context for that project. The person creates the context themselves: `hcloud context create NAME` asks for a project API token (Read & Write) in the terminal, and the token stays in `hcloud`'s own config. You check with `hcloud context list`. Never put a token on a command line.
- The person's public key: `~/.ssh/id_ed25519.pub`, or another `.pub` file they name.
- Hetzner blocks outgoing ports 25 and 465 on new accounts (an account can ask for them after a month and a first paid invoice). Port 587 works, so sign-in mail over SMTP with STARTTLS on 587 needs nothing (checked 2026-10-07 on the Hetzner FAQ above).

## Create the machine

Pick a size. Show the person the choices and the price before you create anything:

```sh
hcloud server-type list
hcloud server-type describe cx23
```

A 2 vCPU, 4 GB x86 type (`cx23` at the time of writing) is more than enough. `cax11` is the Arm equivalent (then deploy with `LOADOUT_ARCH=arm64`). The cheapest types are sometimes unavailable at a location, so use the locations `hcloud server-type describe` lists as available. `--location` replaces the removed `--datacenter` flag.

Upload the key, then create the server with the cloud-init file from the [VM shape](../shapes/vm.md) (its key line replaced). Always pass `--ssh-key`: without one Hetzner emails a root password.

```sh
hcloud ssh-key create --name loadout-admin --public-key-from-file ~/.ssh/id_ed25519.pub
hcloud server create --name loadout --type cx23 --image ubuntu-24.04 --location fsn1 --ssh-key loadout-admin --firewall loadout-registry --user-data-from-file cloud-init.yaml
hcloud server ip loadout
```

Create the firewall first (next section), so `--firewall` finds it. `hcloud image list --type system` lists images if `ubuntu-24.04` has been retired. Any Debian or Ubuntu LTS works. Record the server, key and firewall names, then `ssh admin@IP cloud-init status --wait`.

## Firewall

Hetzner's cloud firewall is outside the machine. `prepare.sh` also sets `ufw` inside it. Allow ssh, http, https and QUIC from anywhere (ssh is key only, to narrow it to the person's address, give `--source-ips` their address, knowing a changed address locks them out until they edit the rule):

```sh
hcloud firewall create --name loadout-registry
hcloud firewall add-rule loadout-registry --direction in --protocol tcp --port 22 --source-ips 0.0.0.0/0 --source-ips ::/0
hcloud firewall add-rule loadout-registry --direction in --protocol tcp --port 80 --source-ips 0.0.0.0/0 --source-ips ::/0
hcloud firewall add-rule loadout-registry --direction in --protocol tcp --port 443 --source-ips 0.0.0.0/0 --source-ips ::/0
hcloud firewall add-rule loadout-registry --direction in --protocol udp --port 443 --source-ips 0.0.0.0/0 --source-ips ::/0
```

If you created the server before the firewall, attach it: `hcloud firewall apply-to-resource loadout-registry --type server --server loadout`.

## Name

[dns.md](../dns.md): an A record to the address `hcloud server ip loadout` printed (and AAAA to the address `hcloud server ip --ipv6 loadout` prints, if the person wants IPv6), or `<ip-with-dashes>.sslip.io`.

## Deploy

Follow the [VM shape](../shapes/vm.md), step 3, with `LOADOUT_HOST` the server's IPv4 address. Then the checks in step 4.

## Off-host backups

Two layers, see [backups.md](../backups.md):

- Hetzner backups: `hcloud server enable-backup loadout` (daily, seven slots, priced as a share of the server's price, the Hetzner pricing page shows it). Ask before enabling.
- A snapshot before an upgrade with a migration: `hcloud server create-image --type snapshot --description "before upgrade" loadout`. Snapshots stay until deleted and are billed by size.
- Pull the nightly archive to the person's computer as backups.md says.

Backups and snapshots copy the server's disk and nothing else, which is all the registry needs.

## Cost

Checked 2026-10-07 against the Hetzner price-adjustment page (prices for new orders from 15 June 2026), in euros without VAT: `cx23` 5.49 per month (hourly 0.0088), `cax11` 5.99, `cx33` 8.49, each without the IPv4 address, which adds 0.50 per month. A server is billed by the hour up to its monthly price, until deleted. `hcloud server-type describe` shows the current price for the location. Traffic (20 TB included in the EU) is far above what a registry uses. Backups add a percentage of the server's price. Snapshots are billed by size. Tell the person the sum before you create anything: roughly 6 euros per month.

## Tear down

Only after a yes for exactly this, and only what the notes file lists. A server's disk, its backups and its primary IPv4 address go with it. Snapshots and ssh keys and firewalls do not.

```sh
hcloud server delete loadout
hcloud firewall delete loadout-registry
hcloud ssh-key delete loadout-admin
hcloud image list --type snapshot
hcloud primary-ip list
```

The last two list what may be left (delete a snapshot or an unattached primary IP only if the notes say you made it, and the person agrees). Remove the DNS records the person added.
