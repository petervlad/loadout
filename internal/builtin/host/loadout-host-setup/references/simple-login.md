# Simple login, said plainly

`LOADOUT_LOGIN=simple` is for a registry that has no mail provider and where the operator is happy to hand out passwords. Tell the person all of this before they choose it, and ask again if they still want it.

## What it is

The sign-in page takes an email and a password, and a right password starts the session at once: no code, no mail. `loadout login` works as usual on top of it.

## What it does not have

- **No self sign-up.** Nobody can create an account. The operator creates each one.
- **No reset by mail.** A person who forgets their password asks the operator.
- **No Google.** The Google settings are ignored.
- **No mail at all.** SMTP settings are ignored and no mail queue starts.

## How passwords are set

The operator, on the host, for every person (the person types it in their own terminal, never in the chat, and the operator hands it over out of band):

```sh
ssh -t admin@IP sudo -u loadout loadout user password --email alice@example.com --data /var/lib/loadout
```

It creates the account when it does not exist, marks the address verified, and ends the user's other sessions. Running it again replaces the password. The password is 12 to 128 characters and not a common one. Apart from the first admin below, accounts exist only because the operator made them.

## The first admin

The first time the registry starts with no account at all, `serve` creates one admin, `admin@loadout.local`, verified, so the person can sign in at once without shell commands. Its password is `LOADOUT_ADMIN_PASSWORD` from the settings file if the person set it (12 to 128 characters, not a common one, or the service refuses to start and says so), otherwise 24 random characters printed once to the service's log in a marked block: `sudo journalctl -u loadout | grep -A3 "first admin created"`. The person reads it themselves; do not read it for them and never write it into a file or the chat. The systemd journal keeps that block, so the person signs in and changes the password on the host with `ssh -t admin@IP sudo -u loadout loadout user password --email admin@loadout.local --data /var/lib/loadout`. It is created only while the accounts table is empty: an existing registry is never touched and deleting the admin later does not recreate it. `loadout serve --check` says `first admin: will be created (empty database)` or `first admin: exists`.

## Turning it on

In the settings file, with `LOADOUT_PUBLIC_URL`:

```sh
LOADOUT_LOGIN=simple
```

The person adds that line with their own editor, you run `loadout serve --check --env-file ~/loadout.env` (its `login` line says simple, its `email` line says off), and deploy the file as the setup skill's step 3 says. `journalctl -u loadout` shows "simple login is on".

## Switching to email later

Remove the `LOADOUT_LOGIN=simple` line, add the mail settings (one of the email files), check and deploy. The accounts keep their passwords. From then on people can sign up and reset by mail, and a sign-in mails a code.
