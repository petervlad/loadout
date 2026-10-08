# Postmark

Verified how: not run end to end. Written from Postmark's documentation on 2026-10-07 and checked against loadout's SMTP code (STARTTLS only). Nobody has sent a loadout sign-in mail through Postmark with these exact steps. The SMTP details and prices were read from Postmark's pages that day. Postmark's account review process was not read (its help page did not load), so what it asks for and how long it takes is unconfirmed: read Postmark's current account approval page with the person.

Sources:
- https://postmarkapp.com/developer/user-guide/send-email-with-smtp (host, ports, tokens, STARTTLS)
- https://postmarkapp.com/pricing

## Account

A Postmark account made by the person, with a **Server** (Postmark's name for a sending project) and its default transactional message stream. Postmark reviews new accounts, and until a review passes sending may be limited (to which recipients is in its account approval page, which was not read). Say so before the person relies on it for sign-up, and tell them to ask Postmark for approval early.

## Sender domain and DNS

Under Sender Signatures, add the domain (better than one address, because the sign-in mail comes from `login@example.com` or similar). Postmark shows a DKIM TXT record and a Return-Path CNAME to add; both come from the dashboard, and are public data the person may paste to you so you can check them with `dig`. The person presses Verify afterwards. Add a DMARC TXT record on `_dmarc.example.com`, starting with `v=DMARC1; p=none; rua=mailto:postmaster@example.com`. The Return-Path domain is what makes SPF line up with the sender domain, so keep it.

## SMTP credentials

Postmark offers two. Pick one and let the person copy it into the settings file with their editor; never ask for it in chat:

- The Server API token (Server, API Tokens), which acts as both the user name and the password. The person types the same token on both lines.
- An SMTP token (Server, the transactional stream, Settings): an Access Key as the user name and a Secret Key as the password. The secret is shown once.

SMTP access is enabled per stream in the same settings page.

## Values for loadout.env

```sh
LOADOUT_PUBLIC_URL=https://registry.example.com
LOADOUT_SMTP_HOST=smtp.postmarkapp.com
LOADOUT_SMTP_PORT=587
LOADOUT_SMTP_USER=
LOADOUT_SMTP_PASSWORD=
LOADOUT_MAIL_FROM="loadout <login@example.com>"
```

Postmark's SMTP ports are 25, 2525 and 587, with TLS through STARTTLS. Use 587, or 2525 where 587 is blocked (DigitalOcean). Without a stream header Postmark uses the default transactional stream, which is what sign-in mail should use. Do not use `smtp-broadcasts.postmarkapp.com`, which is for broadcast mail. Check, deploy and test as the setup skill says.

## Limits and sandbox

Account review (above) decides who you can send to. Postmark's SMTP endpoint accepts the message and reports bounces afterwards, so a bad recipient shows up in Postmark's activity page, not as an error from `loadout mail test`. If the test is refused, the usual causes are the wrong token (the Server token, not the Account token, as both user and password), SMTP access not enabled on the stream, or a sender address that is not under a verified signature.

## Cost

From Postmark's pricing page, read 2026-10-07: a free plan of 100 emails per month for testing, and a paid plan from about 15 USD per month for 10,000 emails, with extra emails billed per 1,000. Ask the person to confirm the figure on the current page. A registry needs few messages, but the free plan's 100 per month is thin for a team.
