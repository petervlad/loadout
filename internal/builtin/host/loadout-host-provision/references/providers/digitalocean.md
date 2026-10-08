# DigitalOcean

Shape: [VM](../shapes/vm.md). Tool: `doctl`.

Verified how: not run end to end. Written from DigitalOcean's documentation and the `doctl` reference and checked against loadout's deploy files, on 2026-10-07. Nobody has created a Droplet with these exact commands, so if a command or flag differs from what `doctl ... --help` shows, trust the help and the documentation (below) and say what you changed.

Sources:
- https://docs.digitalocean.com/support/why-is-smtp-blocked/ (blocked mail ports, last verified by DigitalOcean 13 Jul 2026)
- https://docs.digitalocean.com/reference/doctl/reference/compute/droplet/create/
- https://docs.digitalocean.com/reference/doctl/reference/compute/firewall/create/
- https://docs.digitalocean.com/reference/doctl/reference/compute/ssh-key/import/
- https://www.digitalocean.com/pricing/droplets
- https://docs.digitalocean.com/products/backups/details/pricing/ and https://docs.digitalocean.com/products/snapshots/details/pricing/

## Before you start

- A DigitalOcean account and `doctl` logged in. The person runs `doctl auth init` themselves (it asks for an API token at a prompt; the token stays in doctl's own config). You check with `doctl account get`. Never put a token on a command line or read doctl's config.
- The person's public key (a `.pub` file).
- **Mail needs a different port.** DigitalOcean blocks outgoing SMTP ports 25, 465 and 587 on all Droplets by default, including through a Reserved IP (https://docs.digitalocean.com/support/why-is-smtp-blocked/, checked 2026-10-07). loadout's sign-in mail uses SMTP with STARTTLS on `LOADOUT_SMTP_PORT` (default 587), so on DigitalOcean the person must pick a provider that offers STARTTLS on another port and set that port: 2525 (Postmark, Mailgun and others), 2587 (Resend, Amazon SES). The `loadout-host-setup` skill's email files list the port for each. Tell the person this before they choose a mail provider. A registry with simple login or token-only access sends no mail and is not affected.

## Create the machine

```sh
doctl compute ssh-key import loadout-admin --public-key-file ~/.ssh/id_ed25519.pub
doctl compute size list
doctl compute region list
doctl compute image list-distribution --public
```

Show the person the size and the price. `s-1vcpu-1gb` (1 GiB, 25 GiB disk) is enough; `s-2vcpu-2gb` is comfortable. `--user-data-file` takes cloud-init YAML, so pass the [VM shape](../shapes/vm.md)'s cloud-init file with the key replaced; it creates `admin`.

```sh
doctl compute droplet create loadout --size s-1vcpu-1gb --image ubuntu-24-04-x64 --region fra1 --ssh-keys KEY_ID_OR_FINGERPRINT --user-data-file cloud-init.yaml --enable-ipv6 --wait
doctl compute droplet get loadout --format ID,PublicIPv4,PublicIPv6,Status
```

`KEY_ID_OR_FINGERPRINT` is printed by the `ssh-key import`. Record the Droplet id, key and region, then `ssh admin@IP cloud-init status --wait`.

## Firewall

A cloud firewall in front of the Droplet. Give it the outbound rules too, so it does not cut the machine off (a Droplet needs to reach package servers, Let's Encrypt and your mail provider):

```sh
doctl compute firewall create --name loadout-registry --droplet-ids DROPLET_ID --inbound-rules "protocol:tcp,ports:22,address:0.0.0.0/0,address:::/0 protocol:tcp,ports:80,address:0.0.0.0/0,address:::/0 protocol:tcp,ports:443,address:0.0.0.0/0,address:::/0 protocol:udp,ports:443,address:0.0.0.0/0,address:::/0" --outbound-rules "protocol:tcp,ports:all,address:0.0.0.0/0,address:::/0 protocol:udp,ports:all,address:0.0.0.0/0,address:::/0 protocol:icmp,address:0.0.0.0/0,address:::/0"
```

`prepare.sh` also sets `ufw` inside the machine. The format is "Use a quoted string of space-separated values for multiple rules" (doctl reference); if doctl refuses a rule, show its message.

## Name

[dns.md](../dns.md): an A record to the Droplet's IPv4 (and AAAA to its IPv6), or `<ip-with-dashes>.sslip.io`. If the person's domain is already at DigitalOcean, the `doctl compute domain records create` example in dns.md applies; ask first.

## Deploy

The [VM shape](../shapes/vm.md), step 3, with `LOADOUT_HOST` the Droplet's IPv4. Then, when the person sets up sign-in mail, remember the port note above: `LOADOUT_SMTP_PORT=2525` or `2587`.

## Off-host backups

- DigitalOcean backups: `doctl compute droplet-action enable-backups DROPLET_ID` (weekly adds 20 percent of the Droplet's price per month; check the backups pricing page for daily). Ask before enabling.
- A snapshot before an upgrade with a migration: `doctl compute droplet-action snapshot DROPLET_ID --snapshot-name loadout-before-upgrade --wait`, billed by size (0.06 USD per GB per month on the pricing page checked 2026-10-07).
- Pull the nightly archive to the person's computer as [backups.md](../backups.md) says.

## Cost

From the pricing page checked 2026-10-07: `s-1vcpu-1gb` is 6 USD per month (0.00893 per hour, 25 GiB disk, 1,000 GiB transfer). Weekly backups add 20 percent (1.20 USD). Snapshots 0.06 USD per GB per month. A Droplet is billed until destroyed, including while powered off. Roughly 6 USD per month; tell the person the sum before creating anything.

## Tear down

Only after a yes for exactly this, and only what the notes file lists. Destroying a Droplet does not delete its snapshots or firewall.

```sh
doctl compute droplet delete loadout
doctl compute firewall delete FIREWALL_ID
doctl compute ssh-key delete KEY_ID
doctl compute snapshot list
```

The last command lists snapshots; delete one you made with `doctl compute snapshot delete SNAPSHOT_ID` after the person agrees. Remove the DNS records the person added.
