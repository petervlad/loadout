# Any other SMTP provider

For a provider without its own file here (Mailgun, SendGrid, Brevo, Mailjet, Google Workspace, your own relay): anything that offers SMTP submission with STARTTLS works, because that is all loadout speaks.

Verified how: not run end to end. The requirements below come from loadout's own SMTP code and the checks in `loadout serve --check` and `loadout mail test`, read on 2026-10-07. Mailgun's host, ports and credentials were read from its documentation; Google's from its SMTP setup page. The other providers are named without their details because those were not read: take them from the provider's SMTP settings page.

Sources:
- https://documentation.mailgun.com/docs/mailgun/user-manual/sending-messages/send-smtp (Mailgun host, ports, per-domain credentials)
- https://support.google.com/a/answer/176600 (Google Workspace SMTP options)
- `internal/mail/smtp.go` in loadout (STARTTLS required before authentication)

## Account

An account at the provider, made by the person, with a sending domain added. What loadout needs from it:

| Setting | Value |
| --- | --- |
| Host | the provider's SMTP submission host |
| Port | the submission port that offers STARTTLS (587 by default); where 587 is blocked, the provider's alternative STARTTLS port, often 2525 or 2587 |
| User and password | SMTP credentials, or an API key used as the password |
| Sender | an address on a domain the provider has verified |
| Security | STARTTLS offered on that port; loadout refuses to send a password over a connection that does not offer it, and it does not speak implicit TLS (usually port 465) |

## Sender domain and DNS

Add the domain at the provider and add the records it shows: an SPF record (or a custom return-path domain), DKIM records and a DMARC record. The provider's page gives the exact names and values; they are public data the person may paste to you so you can check them with `dig`. Add DMARC as TXT on `_dmarc.example.com` with `v=DMARC1; p=none; rua=mailto:postmaster@example.com` first, tightened once mail passes. Add the records before the first mail, or the first sign-in mails land in spam or are refused.

## SMTP credentials

The person creates them in the provider's dashboard and types them into the settings file with their editor. Never ask for them in chat.

- Mailgun: smtp.mailgun.org, ports 587 or 2525 with STARTTLS (465 is implicit TLS and does not work with loadout, and 25 is often blocked). Credentials are per sending domain: Sending, Domain settings, SMTP credentials.
- Google Workspace (a handful of people): `smtp.gmail.com`, port 587 with TLS through STARTTLS, user the full address, password an app password (the account needs 2-Step Verification). Google's page gives a sending limit of 2,000 messages per day. `smtp-relay.gmail.com` needs a different setup (an allowed address) and has no password, so it does not fit.
- Microsoft 365: SMTP AUTH is often turned off for a tenant, and the person's admin has to allow it; if it is off, use another provider.
- Your own relay: a submission service with STARTTLS and a password login, with SPF, DKIM and DMARC set up by whoever runs it.

## Values for loadout.env

```sh
LOADOUT_PUBLIC_URL=https://registry.example.com
LOADOUT_SMTP_HOST=smtp.example.net
LOADOUT_SMTP_PORT=587
LOADOUT_SMTP_USER=
LOADOUT_SMTP_PASSWORD=
LOADOUT_MAIL_FROM="loadout <login@example.com>"
```

All of host, user, password and sender must be set to turn email on; `loadout serve --check` says `email: smtp via HOST:PORT` when they are. Then deploy and test as the setup skill says.

## Limits and sandbox

Read the provider's sending limits and trial rules: many start in a sandbox, a trial or a review that limits recipients, and say so in their documentation. A refusal from `loadout mail test` names what is wrong: authentication failed (the user or password), a sender not verified, a recipient not allowed (a trial), or "does not offer STARTTLS" (the wrong port). Where a provider takes days to approve an account, tell the person to start that first.

## Cost

Whatever the provider charges; read its pricing page and tell the person. A registry sends one or two messages per sign-in, so the volume is small, and many providers have a free plan that covers it.
