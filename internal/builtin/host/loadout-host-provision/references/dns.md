# Names, DNS and the wait before TLS

A registry needs a name Caddy can get a certificate for, and the name must point at the machine before the first deploy. Let's Encrypt allows only 5 failed validations per name and account per hour, refilling slowly (https://letsencrypt.org/docs/rate-limits/), so a deploy before DNS is right can lock the person out of a certificate for a while. The web pages need HTTPS: session cookies are `Secure` except on `http://localhost`.

## With a domain

Ask the person which name to use, for example `registry.example.com`, on a domain they control. Create:

| Type | Name | Value | TTL |
| --- | --- | --- | --- |
| A | `registry.example.com` | the machine's IPv4 address, for example `203.0.113.7` | 300 |
| AAAA | `registry.example.com` | its IPv6 address, only if the machine has one and the firewall allows 80 and 443 over IPv6 | 300 |

The person adds the records in their registrar's or DNS host's console, or with a command line tool for the DNS host they already use. You may run that tool only after the person says yes to the exact record. Two examples, for DNS hosted at DigitalOcean and at Route 53:

```sh
doctl compute domain records create example.com --record-type A --record-name registry --record-data 203.0.113.7 --record-ttl 300
aws route53 change-resource-record-sets --hosted-zone-id ZONE_ID --change-batch file://record.json
```

If DNS is behind Cloudflare's proxy, set the record to "DNS only" (no orange cloud) so Caddy's certificate validation reaches the machine.

## Without a domain

Use `<ip-with-dashes>.sslip.io`: `203-0-113-7.sslip.io` resolves to `203.0.113.7`, and Caddy gets a real certificate for it (https://sslip.io). It is a free shared service run by someone else: fine for a team trial, and the person should know the name changes if the address does. An IPv6 address is written with dashes too.

## Wait for DNS

Before the first deploy:

```sh
dig +short registry.example.com
dig +short registry.example.com @1.1.1.1
```

Both must print the machine's address. If `dig` is not installed use `host registry.example.com` or `nslookup registry.example.com`. A record that exists but is not visible yet needs time (the old TTL). Wait and ask again rather than deploying. When the name resolves to something else, say what it resolves to and stop.

## Names for other uses

The sign-in mail sender is a different name with its own DNS records (SPF, DKIM, DMARC): the `loadout-host-setup` skill covers it. Use a subdomain such as `registry.example.com` for the registry and `example.com` or `mail.example.com` for the sender.
