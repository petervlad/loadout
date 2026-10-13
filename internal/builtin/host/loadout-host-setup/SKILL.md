---
name: loadout-host-setup
description: Set up a loadout registry step by step, covering the machine (with loadout-host-provision), the settings file, sign-in mail through SMTP or a provider such as Amazon SES, Resend or Postmark, simple login when there is no mail, optional Google sign-in, the first admin, and the first project and team. Use when the user wants to run their own loadout registry, add sign-in mail to one, fix mail that does not arrive, or hand out the first accounts.
---
# Setting up a loadout registry

You walk the person through a working registry: the machine, the settings file, how people sign in, the first admin and the first team. It runs on their computer and their registry host with their own logins. loadout never calls a model, never holds an AI key and calls no cloud or mail provider for anyone. The only network calls are the ones the person's own tools make.

## Rules for you, the agent

- Ask before every step that costs money, changes DNS or deletes anything, naming the resource, the provider and the price. Never tear anything down without a yes for that teardown.
- Never ask for, read, print or paste a secret. Cloud tokens stay in the provider tool's own store (the person logs in). SMTP passwords, API keys and Google secrets are typed by the person into the settings file with their own editor, and account passwords into `loadout user password` in their own terminal. If a secret ends up in the chat, tell the person to rotate it.
- Never open `loadout.env`, `/etc/loadout/loadout.env` or `~/.loadout/credentials.yaml`, and do not copy, search or diff them. Learn what is set with `loadout serve --check` (it prints one line per part and never a secret), and prove mail with `loadout mail test`.
- Never `set -x` around a command that loads the settings file.
- Run host commands as the service user with `--data /var/lib/loadout`, through ssh, as in `ssh admin@IP sudo -u loadout loadout admin list --data /var/lib/loadout`. Add `-t` to ssh when the person must type something.
- loadout never calls a model and holds no AI key: do not look for one or suggest one.
- Ask before each step that changes the registry for people: a new admin, a new project or team, a member. Say the exact email and name.
- Providers change. Every email file says when it was checked against the provider's documentation and what was not run for real. When a command or screen differs, read the provider's current documentation and tell the person.

## Steps

Each step ends with a check you run and show.

1. **Choose how people sign in.** Ask, then write the choice down.

   | Mode | Needs | People sign up themselves | Lost password |
   | --- | --- | --- | --- |
   | Email codes (recommended) | an SMTP provider and a sender domain | yes | a mailed code |
   | Email codes plus Google | the above and a Google OAuth client | yes | a mailed code, or Google |
   | Simple login (`LOADOUT_LOGIN=simple`) | nothing | no | the operator runs `loadout user password` |
   | Tokens only (no `LOADOUT_PUBLIC_URL`) | nothing | no, people join with invites | a new invite |

   Simple login is for a small team with no mail: read [simple-login.md](references/simple-login.md) before the person picks it.

2. **The machine.** Run the `loadout-host-provision` skill (install it with `loadout setup --host-skills` if it is missing). Check: `curl -fsS https://NAME/v1/health`.

3. **The settings file.** The person copies the template and fills it in with their own editor, then you check it, without opening it:

   ```sh
   install -m 600 assets/loadout.env.example ~/loadout.env
   ```

   They set `LOADOUT_PUBLIC_URL` (the registry's `https://` address) and, by mode, the mail lines (step 4), `LOADOUT_LOGIN=simple` or the Google lines (step 5). Then:

   ```sh
   loadout serve --check --env-file ~/loadout.env
   ```

   Read the summary lines (`public URL`, `login`, `email`, `google`, `proxies`, `unknown`) and compare them with the chosen mode. It exits 1 and names the variable when one is wrong or misspelt. Keep `~/loadout.env` on the person's computer and out of any repository: it is what later deploys install. To put it on the host, run the deploy from the `loadout-host-provision` skill's folder (a sibling of this one) again, without `LOADOUT_PREPARE`:

   ```sh
   LOADOUT_ENV_FILE=~/loadout.env LOADOUT_VERSION=X.Y.Z LOADOUT_HOST=IP LOADOUT_DOMAIN=NAME sh ../loadout-host-provision/assets/deploy.sh
   ```

   `deploy.sh` checks the file again on the host with the new binary before it restarts anything. On a container platform the settings go in the platform's configuration and secret store instead: see that provider's file in the provision skill. Check: `ssh admin@IP sudo journalctl -u loadout -n 20` has a "login is on" line (or "simple login is on").

4. **Email** (modes with email codes). Pick the provider:

   | Provider | File | Port and security | Notes |
   | --- | --- | --- | --- |
   | Amazon SES | `references/email/ses.md` | 587 or 2587, STARTTLS | cheapest at volume. Sandbox until production access |
   | Resend | `references/email/resend.md` | 587 or 2587, STARTTLS | simple. Free plan. API key as the password |
   | Postmark | `references/email/postmark.md` | 587 or 2525, STARTTLS | strong on delivery. Account review |
   | Any other SMTP provider | `references/email/smtp.md` | STARTTLS submission | Mailgun and others. Your own relay |

   Follow the provider's file: account, sender domain and DNS records (SPF, DKIM, DMARC) added before the first mail, SMTP credentials the person creates and types into the settings file, the values for it, and the limits. The sender address is on a domain the person controls. On DigitalOcean, which blocks ports 25, 465 and 587, use the provider's alternative port (2525 or 2587). Deploy the file as in step 3, then send one test mail and wait for the mail server's answer. It runs with the service's own environment, so you never see the file:

   ```sh
   ssh admin@IP sudo systemd-run --pipe --wait --uid=loadout --property=EnvironmentFile=/etc/loadout/loadout.env /usr/local/bin/loadout mail test --to you@example.com
   ```

   It prints "sent", or the server's refusal without the password. Check: the mail arrives (ask the person), not in spam, and in the message's headers ("show original") `dkim=pass` and `spf=pass` (and `dmarc=pass`). The person reads them and tells you. A refusal text names the cause: a wrong user or password, an unverified sender, a sandbox limit or a blocked port. The provider's file says what each means.

5. **Google** (optional). [google.md](references/google.md): the OAuth client, the redirect address, two variables. Check: `loadout serve --check` says `google: on`.

6. **The first admin.** [first-admin.md](references/first-admin.md): `loadout admin grant`, then the person signs up (email codes) or gets a password from `loadout user password` (simple login). Check: `loadout admin list` shows the address verified, and `loadout login --remote https://NAME` works on the person's computer.

7. **First project and team.** With the first admin signed in, on the website choose **New** (a project, then a team in it), or on the host:

   ```sh
   ssh admin@IP sudo -u loadout loadout namespace create acme --kind project --data /var/lib/loadout
   ssh admin@IP sudo -u loadout loadout namespace create payments --parent acme --data /var/lib/loadout
   ssh admin@IP sudo -u loadout loadout member add --namespace payments --email alice@example.com --role maintainer --data /var/lib/loadout
   ```

   Ask for the names and emails first. A person added needs no account beforehand: with email codes they sign up with that address, with simple login you give them a password. Check: `loadout namespace list` after the person signs in on their own computer shows the project and team. Then hand over to the built-in `loadout-admin` skill, which runs members, roles, review and tokens for the team.

8. **Operate.** Tell the person: an outside uptime check on `https://NAME/v1/health`. `ssh admin@IP sudo journalctl -u loadout -n 50`. Upgrades by running the deploy again with the new `LOADOUT_VERSION` (back up first when the release notes name a migration: `ssh admin@IP sudo systemctl start loadout-backup.service`), and the Host panel on the admins' server page, which reads `LOADOUT_HOST_PROVIDER`, `LOADOUT_HOST_REGION`, `LOADOUT_HOST_SIZE`, `LOADOUT_HOST_CONTACT` and `LOADOUT_HOST_CONSOLE` from the settings file.

## Files

The email files are in the table in step 4.

| File | What it says |
| --- | --- |
| `references/first-admin.md` | The first admin with email codes and with simple login, and what to check |
| `references/simple-login.md` | Simple login said plainly: what it does not have, how passwords are set, how to switch to email |
| `references/google.md` | Optional Google sign-in: the OAuth client, the redirect address, the two variables |
| `assets/loadout.env.example` | Every setting, commented out, to copy and fill in |
