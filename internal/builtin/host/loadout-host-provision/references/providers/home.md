# A home or office server, or a local VM

Shape: [VM](../shapes/vm.md). Tool: none; the machine itself. For a local VM on a Mac, Windows or Linux computer, Multipass is one way; any hypervisor works.

Verified how: not run end to end. Written from loadout's deploy files and from Caddy's and Multipass's documentation on 2026-10-07. Nobody has deployed a registry to a home machine or a Multipass VM with these exact steps. The Multipass flags below come from memory, because its documentation site did not answer when this was written: run `multipass help launch` and trust it over this file. The part most likely to need adjusting is trusting Caddy's local certificate on each laptop.

Sources:
- https://caddyserver.com/docs/automatic-https (local and internal names get certificates from Caddy's own authority)
- https://caddyserver.com/docs/running (where the root certificate is)
- https://documentation.ubuntu.com/multipass/ (Multipass; the command reference moved and was not readable on 2026-10-07)
- `deploy/README.md` in loadout: "Behind a tunnel or another proxy"

## Before you start

- A machine that stays on: a spare computer, a mini PC, a Raspberry Pi 4 or later with a 64-bit Debian or Ubuntu (Arm: `LOADOUT_ARCH=arm64`), or a VM. It needs Debian or Ubuntu, a fixed address on the network (a DHCP reservation in the router, or a static address), and an ssh login with the person's key. A laptop that sleeps is a poor registry.
- Decide who reaches it, because that decides the name and the certificate. Ask the person:
  1. **Only people on the same network** (an office or a home): a LAN name and Caddy's own certificate authority. No domain needed, no Let's Encrypt. Each laptop must trust Caddy's root certificate once.
  2. **People anywhere, through a port forward**: needs a public domain and a router that forwards 80 and 443 (tcp) and 443 (udp) to the machine, and an address that does not change (or a dynamic DNS name). Caddy then gets a Let's Encrypt certificate like on a cloud VM. Say plainly that this exposes the home connection to the internet and that some internet providers block port 80 or 443 at home.
  3. **People anywhere, through a tunnel** (Cloudflare Tunnel, Tailscale Funnel or similar): no open ports at home. The tunnel's provider is another account and another credential the person sets up themselves; follow that provider's documentation and see "Behind a tunnel or another proxy" in loadout's `deploy/README.md` for the Caddy snippet that trusts the tunnel and the header it sets.
- The sign-in web pages need HTTPS (session cookies are `Secure`, except on `http://localhost`), so a plain `http://` address does not work for them.

## Create the machine

**A computer with Debian or Ubuntu installed:** create the `admin` user by hand as the [VM shape](../shapes/vm.md) says.

**A local VM with Multipass:** copy `assets/cloud-init.yaml` to a scratch folder, replace the key line with the person's public key, and:

```sh
multipass launch 24.04 --name loadout --cpus 1 --memory 1G --disk 10G --cloud-init cloud-init.yaml
multipass info loadout
```

`multipass info` shows the VM's address. A default Multipass network is private to the computer running it, so other laptops cannot reach the VM until it has a bridged network (see Multipass's documentation for `--network`), and a VM is for trying things, not for a team. Record the VM's name.

## Firewall

`prepare.sh` sets `ufw` inside the machine (22, 80, 443 tcp, 443 udp). Check the router for the port-forward case only: the forwards to the machine's fixed address. In the LAN-only case nothing is forwarded, which is the point.

## Name

- **LAN only.** Use a name Caddy treats as internal: one ending in `.home.arpa` or `.internal` (for example `loadout.home.arpa`), or the machine's address itself (for example `192.168.1.20`). Caddy serves such names over HTTPS with certificates from its own authority, with no ACME and no DNS check. For a name, each laptop needs it to resolve: a record in the router's local DNS, or a line in each laptop's hosts file (`192.168.1.20 loadout.home.arpa`); say which the person prefers. `LOADOUT_DOMAIN` is that name.
- **Port forward.** A public name that points at the home address: [dns.md](../dns.md).
- **Tunnel.** The name is the tunnel's, set up in its provider's console.

## Deploy

The [VM shape](../shapes/vm.md), step 3, with `LOADOUT_HOST` the machine's address on the network and `LOADOUT_DOMAIN` the name above.

LAN-only note: the script's last step calls `https://NAME/v1/health` from the person's computer, which does not trust Caddy's authority yet, so on the first deploy that check fails after retrying, even though the install worked. Then trust the root certificate and check again:

1. Copy it from the machine: `ssh admin@IP sudo cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt > caddy-root.crt` (a public certificate, not a secret).
2. The person adds it to their computer's trust store themselves, which asks for their own admin password: on a Mac `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain caddy-root.crt`; on Linux copy it to `/usr/local/share/ca-certificates/` and run `sudo update-ca-certificates`; on Windows `certutil -addstore -f "ROOT" caddy-root.crt` in an administrator prompt. Browsers with their own store (Firefox) need it imported there too. Every teammate repeats this once, so give them the file and the command for their system.
3. `curl -fsS https://NAME/v1/health`.

Without the trust step the `loadout login` and the web pages show certificate errors.

## Off-host backups

A home machine has no provider to snapshot it. The pulled archive is the backup: [backups.md](../backups.md), to another computer or disk that is not in the same box. Do the restore test. For a VM, `multipass snapshot` and copies of the disk are the VM's own layer.

## Cost

No provider fee. The machine's electricity (a mini PC at 10 to 15 W is about a euro or a dollar a month), the domain if the person buys one, and the internet connection they already pay for. Nothing to confirm with a provider; tell the person that.

## Tear down

Only after a yes for exactly this. Stop and remove the service on the machine (`ssh admin@IP sudo systemctl disable --now loadout caddy loadout-backup.timer`), and delete the data only after the person has a pulled copy they have restored once. Remove the router's port forwards, the local DNS or hosts entries, the tunnel, and the root certificate from each laptop that trusted it. For a Multipass VM: `multipass delete loadout --purge`.
