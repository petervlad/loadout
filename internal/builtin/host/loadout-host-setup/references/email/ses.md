# Amazon SES

Verified how: not run end to end. Written from Amazon's SES documentation and the AWS CLI reference on 2026-10-07 and checked against loadout's SMTP code (STARTTLS only). Nobody has sent a loadout sign-in mail through SES with these exact steps. The AWS CLI commands match the reference pages; the console screens are described from the documentation and may have moved.

Sources:
- https://docs.aws.amazon.com/ses/latest/dg/smtp-connect.html (endpoints and ports)
- https://docs.aws.amazon.com/ses/latest/dg/smtp-credentials.html (creating SMTP credentials in the console)
- https://docs.aws.amazon.com/ses/latest/dg/creating-identities.html (domain identity and Easy DKIM records)
- https://docs.aws.amazon.com/ses/latest/dg/mail-from.html (custom MAIL FROM, SPF)
- https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html (the sandbox)
- https://aws.amazon.com/ses/pricing/

## Account

An AWS account and the `aws` tool logged in by the person (see the provision skill's AWS file). SES works per region, and so do identities, the sandbox and SMTP credentials: pick one region near the registry (for example `eu-west-1`) and use it everywhere below with `--region`. SMTP credentials are not the AWS access keys: they are made in the SES console, and the password is a value derived for SMTP.

## Sender domain and DNS

The sender (`LOADOUT_MAIL_FROM`) is an address on a domain the person controls, for example `login@example.com`. Create a domain identity, which starts Easy DKIM:

```sh
aws sesv2 create-email-identity --email-identity example.com --region REGION
```

The answer lists three DKIM tokens and a `SigningHostedZone`. The person adds three CNAME records (public data, you may print them and ask the person to add them, or add them yourself after a yes if the DNS is in Route 53):

| Type | Name | Value |
| --- | --- | --- |
| CNAME | `TOKEN1._domainkey.example.com` | `TOKEN1.SIGNINGHOSTEDZONE` |
| CNAME | `TOKEN2._domainkey.example.com` | `TOKEN2.SIGNINGHOSTEDZONE` |
| CNAME | `TOKEN3._domainkey.example.com` | `TOKEN3.SIGNINGHOSTEDZONE` |

The value's last part depends on the region (for example `dkim.us-west-2.amazonses.com`), so take it from the answer's `SigningHostedZone`, not from memory. Check until DKIM is verified:

```sh
aws sesv2 get-email-identity --email-identity example.com --region REGION --query '{sending:VerifiedForSendingStatus,dkim:DkimAttributes.Status}'
dig +short CNAME TOKEN1._domainkey.example.com
```

**SPF and DMARC.** By default SES sends with its own return-path domain, so SPF passes for Amazon's domain, and DMARC passes through the aligned DKIM signature. To make SPF line up with your domain too, set a custom MAIL FROM subdomain (`aws sesv2 put-email-identity-mail-from-attributes --email-identity example.com --mail-from-domain mail.example.com --behavior-on-mx-failure USE_DEFAULT_VALUE --region REGION`) and add what the documentation lists: an MX record `mail.example.com` pointing at `feedback-smtp.REGION.amazonses.com` with priority 10, and a TXT record on `mail.example.com` with `"v=spf1 include:amazonses.com ~all"`. Add a DMARC record to start with: TXT on `_dmarc.example.com` with `v=DMARC1; p=none; rua=mailto:postmaster@example.com`; tighten it after the mail has been seen to pass.

## SMTP credentials

The person makes them, because the secret appears on their screen once. In the SES console choose SMTP settings, then Create SMTP credentials (it opens IAM and creates an IAM user for sending). The user name shown (an access key id) is `LOADOUT_SMTP_USER`; the SMTP password, shown once with "Show", is `LOADOUT_SMTP_PASSWORD`. They copy both straight into the settings file with their editor, or download the CSV, copy from it, and delete it. Creating them needs IAM permissions the console explains; if the person's AWS user lacks them, a more privileged person does this step. Never ask for them in chat.

## Values for loadout.env

The person writes these in `~/loadout.env` with their editor (the user and password come from the step above):

```sh
LOADOUT_PUBLIC_URL=https://registry.example.com
LOADOUT_SMTP_HOST=email-smtp.REGION.amazonaws.com
LOADOUT_SMTP_PORT=587
LOADOUT_SMTP_USER=
LOADOUT_SMTP_PASSWORD=
LOADOUT_MAIL_FROM="loadout <login@example.com>"
```

SES offers STARTTLS on 25, 587 and 2587, and implicit TLS on 465 and 2465; loadout speaks STARTTLS only, so use 587, or 2587 where 587 is blocked (DigitalOcean). AWS blocks outgoing port 25 from EC2 by default, so do not use 25. Then check, deploy and test as the setup skill says (`loadout serve --check --env-file ~/loadout.env`, then `loadout mail test --to`).

## Limits and sandbox

A new SES account starts in the sandbox, per region. In the sandbox you can send only to verified addresses and domains, at most 200 messages per 24 hours and 1 per second. For a registry that means sign-up mail reaches only addresses the person has verified as identities (`aws sesv2 create-email-identity --email-identity alice@example.com --region REGION` and a click on the link in the mail that address receives). Ask for production access in the SES console (Account dashboard, Request production access) with a plain description: transactional sign-in codes for a team's internal tool, no marketing, how addresses are collected (people type their own), how bounces are handled. Until AWS answers (its documentation says how it reviews requests), tell the person who can and cannot sign up. A mail refusal naming an unverified recipient or "Email address is not verified" is the sandbox.

## Cost

SES bills per message (the pricing page, read 2026-10-07, lists per 1,000 messages by region and path; it is cents for a registry's volume) plus data. There is no monthly minimum for SMTP sending. The IAM user and DNS cost nothing, and Route 53 hosted zones, if used, cost a small monthly fee. Tell the person the figure from the current pricing page.
