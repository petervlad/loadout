# Any VPS with ssh

Shape: [VM](../shapes/vm.md). Tool: none required. The provider's console or its own command line tool, which the person runs or lets you run once they say yes.

Verified how: not run end to end, and there is no single provider here to check against. This file is the shared path for Vultr, Linode (Akamai), OVH, Scaleway, Contabo and any other provider that sells a Linux machine with an ssh login. It was written from loadout's own deploy files and README on 2026-10-07 and the steps that are the same everywhere. Provider facts (prices, blocked ports, console menus) are the person's to confirm in the provider's documentation: say so, and read it with them when something is unclear.

Sources:
- `assets/prepare.sh` and `assets/deploy.sh` in this skill (what is installed and which ports are used)
- the provider's own documentation for creating a server, adding an ssh key, a cloud firewall and snapshots
- https://cloudinit.readthedocs.io/en/latest/ (what the cloud-init file does, where a provider supports it)

## Before you start

- An account, and a way to create a server: the provider's console (the person clicks, you tell them exactly what to choose) or its command line tool if the person already uses one (then follow the same rules as the other files: logged in by the person, no token on a command line).
- The person's public key (a `.pub` file), added to the provider's SSH keys list so the server gets it.
- Ask the provider's policy on outgoing mail ports before choosing a mail provider: many cloud and VPS providers block 25, and some also 465 or 587 for new accounts. Look it up in the provider's documentation (search its docs for "SMTP" or "port 25"), and write down what you found with the date. If 587 is blocked, a mail provider's alternative STARTTLS port (2525, 2587) usually works. The `loadout-host-setup` email files list them.

## Create the machine

Choose, in the provider's console:

- Image: Ubuntu LTS (24.04 or newer) or Debian stable. `prepare.sh` needs `apt`.
- Size: 1 vCPU, 1 GB of memory, 10 GB of disk or more. x86 unless the person picks Arm (then `LOADOUT_ARCH=arm64`).
- Region: close to the team.
- SSH key: the person's. Never choose "email me a root password".
- User data or "cloud-init", if the console has the field: paste the [VM shape](../shapes/vm.md)'s cloud-init file with the key replaced. If there is none, create the `admin` user by hand as the shape file says, from the console's web terminal or a first ssh as root.
- IPv4 address: a public one. Note it. An IPv6 address is optional.

Record what you created (provider, server name or id, region, the address, monthly price shown).

## Firewall

Open 22, 80 and 443 over tcp and 443 over udp. If the provider has a cloud firewall, create a rule set with those four and attach it. If not, `prepare.sh` configures `ufw` inside the machine anyway. Many providers leave all ports open by default, which is fine here because `prepare.sh` closes the rest.

## Name

[dns.md](../dns.md): an A record to the server's IPv4 address, or `<ip-with-dashes>.sslip.io`.

## Deploy

The [VM shape](../shapes/vm.md), step 3. If the provider's default user is `root` and there is no way to create `admin` through the console, log in as root once and run the by-hand commands from the shape file. Do not run `deploy.sh` as root.

## Off-host backups

Turn on the provider's backups or snapshots if it has them (ask about the price first), and pull the nightly archive as [backups.md](../backups.md) says. An unknown provider may have no backup product: the pulled archive is then the backup, and the restore test matters more.

## Cost

Typically 4 to 10 USD or EUR per month for 1 GB of memory, plus a few cents for an extra address or backups. Read the price the provider's console shows for the exact plan and region, tell the person the monthly sum and the billing period (hourly or monthly, whether it bills a stopped server), and ask before creating.

## Tear down

Only after a yes for exactly this, and only what the notes file lists. In the provider's console, delete the server (check what happens to its extra addresses, backups and snapshots: they often stay and keep billing), the firewall you created and the ssh key you added if it was only for this. Remove the DNS records the person added.
