# The first admin

An admin can do everything on the registry: create projects and teams, make owners, delete namespaces. Only the person who runs the host grants the first one, on the host, with the registry's data folder `/var/lib/loadout` and the service user. Ask the person which address it is (their own, one they can read mail at in email modes) and confirm before running anything.

## Email codes (with or without Google)

```sh
ssh admin@IP sudo -u loadout loadout admin grant --email you@example.com --data /var/lib/loadout
```

This creates the account if it does not exist, without a password and not yet verified. The person then signs up with that address on `https://NAME/auth/signup`: they choose a password (12 to 128 characters, not a common one, not the email), the registry mails a 6-digit code and a link, and the account is theirs once they confirm one of them. Nobody else can take it over before that, because the pending password sits on the code, not on the account.

If the mail does not come, do not work around it: run the mail test from the email step (`loadout mail test --to`), fix the provider side, and sign up again. With Google configured, "Continue with Google" signs in the same address with no mailed code.

There is no way into an email-mode registry without working mail or Google, because a sign-in mails a code. If the provider cannot be made to work, say so and offer simple login (see the setup skill's first step) rather than inventing a bypass.

## Simple login, first start

A new simple-login registry already has an admin: on its first start `serve` creates `admin@loadout.local` with a generated password printed once to the service's log, or the password the person put in `LOADOUT_ADMIN_PASSWORD`. Tell the person to read the block themselves (`ssh admin@IP sudo journalctl -u loadout | grep -A3 "first admin created"`), sign in at `https://NAME/auth/login`, and change the password at once, because the journal keeps it: `ssh -t admin@IP sudo -u loadout loadout user password --email admin@loadout.local --data /var/lib/loadout`. Do not read the block for them. It only appears while the registry has no account, and never in email modes (that address cannot receive a code), where the commands below or `admin grant` apply. Then make their own admin with the commands below and, if they like, remove the first one with `loadout user delete --email admin@loadout.local`.

## Simple login, an admin of your own

```sh
ssh -t admin@IP sudo -u loadout loadout user password --email you@example.com --data /var/lib/loadout
ssh admin@IP sudo -u loadout loadout admin grant --email you@example.com --data /var/lib/loadout
```

The first command asks for the password twice, without echo: `-t` gives it a terminal, and the person types it. Give them the command to run in their own terminal; do not run it for them, do not pipe a password in, and do not ask them to tell you the password. It must be 12 to 128 characters and not a common one. The second command makes the account an admin. A person signs in on the website with that email and password, with no code.

## Check

```sh
ssh admin@IP sudo -u loadout loadout admin list --data /var/lib/loadout
```

The address is listed as verified. Then, on the person's computer:

```sh
loadout login --remote https://NAME
```

It opens the browser, the person signs in, and the command says they are signed in. After that, `loadout namespace list` works as them. If login is refused with a certificate error on a LAN-only registry, the laptop does not trust the registry's certificate yet: see the home server file in the provision skill.

## More people later

Each person is added to a team or project with `loadout member add` (the `loadout-admin` skill covers it), and needs no account first. With email codes they sign up themselves with that address; with simple login the operator sets their password the same way as above and hands it over out of band.
