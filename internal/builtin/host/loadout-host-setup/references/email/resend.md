# Resend

Verified how: not run end to end. Written from Resend's documentation on 2026-10-07 and checked against loadout's SMTP code (STARTTLS only). Nobody has sent a loadout sign-in mail through Resend with these exact steps. The SMTP host, ports, user name and quotas below were read from Resend's pages that day; its dashboard screens are described from the documentation and may differ.

Sources:
- https://resend.com/docs/send-with-smtp (host, ports, user, password)
- https://resend.com/docs/dashboard/domains/introduction (verifying a domain, subdomains, DMARC)
- https://resend.com/docs/knowledge-base/resend-sending-limits (free quotas)
- https://resend.com/pricing

## Account

A Resend account, made by the person at resend.com. No cloud CLI is needed. Resend recommends sending from a subdomain to keep the reputation apart, for example `updates.example.com`, with `LOADOUT_MAIL_FROM` an address on it.

## Sender domain and DNS

In the dashboard the person adds the domain (or subdomain) and picks a region. Resend then shows the DNS records to add: in its documentation an SPF TXT record and an MX record for the return path, a DKIM TXT record, and optionally a DMARC record. The records are public data: the person may paste them to you so you can check them with `dig`, but the exact names and values come from the dashboard, not from this file. After adding them, the person presses Verify in the dashboard. Check:

```sh
dig +short TXT resend._domainkey.updates.example.com
dig +short MX send.updates.example.com
```

(the names are the dashboard's; these are the usual shapes). Add a DMARC record if Resend suggests one: TXT on `_dmarc.updates.example.com` (or the root) with `v=DMARC1; p=none; rua=mailto:postmaster@example.com`, tightened later.

## SMTP credentials

The user name is always `resend`. The password is an API key: in the dashboard, API Keys, create one with "Sending access" limited to the domain, and copy it once. The person types or pastes it into the settings file with their editor and nowhere else. Never ask for it in chat; if it was pasted there, they delete the key and make another.

## Values for loadout.env

```sh
LOADOUT_PUBLIC_URL=https://registry.example.com
LOADOUT_SMTP_HOST=smtp.resend.com
LOADOUT_SMTP_PORT=587
LOADOUT_SMTP_USER=resend
LOADOUT_SMTP_PASSWORD=
LOADOUT_MAIL_FROM="loadout <login@updates.example.com>"
```

Resend lists ports 25, 465, 587, 2465 and 2587. STARTTLS is on 25, 587 and 2587; 465 and 2465 are implicit TLS, which loadout does not speak. Use 587, or 2587 where 587 is blocked (DigitalOcean); avoid 25. Check, deploy and test as the setup skill says.

## Limits and sandbox

The pages read describe no sandbox, but expect a refusal until the sender domain shows as verified in the dashboard. The free plan's quotas, read from Resend's page on 2026-10-07: 100 transactional emails per day and 3,000 per month, counting sent and received mail, and each recipient on a message counts. A registry's sign-in codes stay well inside that for a team; if sign-ups happen in bursts, remember each code and each sign-in mail is one message.

## Cost

Resend has a free plan (its pricing page says free forever, with the quotas above) and paid plans with monthly quotas and pay-as-you-go overage. A team registry fits the free plan. Read the pricing page for current paid prices before recommending a paid plan.
