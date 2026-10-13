---
name: loadout-admin
description: Run a loadout team or project as a maintainer or owner, covering members and roles, invites, machine tokens for CI, requiring review, the team's wiki, and which model each AI tool runs for the team's agents. Use when the user asks to add, invite, promote, demote or remove someone, make, list or revoke a loadout token, turn review or the wiki on or off, create a team, or change model settings on a loadout registry.
---
# Running a loadout team

These commands change who can do what for a whole team. Most need the maintainer or owner role; the registry refuses what the person's role does not allow. loadout never runs a model and never holds an AI key; model settings are only names it writes into agent files.

## First, look for local rules

Look for an installed skill named `<project>-loadout` (for example `acme-loadout`) or `loadout-local`. It may say who gets which role and how the company names teams. Prefer it for anything company specific.

## Rules for you, the agent

- Every command here is visible to the team. Ask the person before each `loadout member add`, `loadout member invite`, `loadout member set`, `loadout member remove`, `loadout namespace set`, `loadout namespace create`, `loadout token create` and `loadout token revoke`, with the exact email, role and namespace. Read-only commands (`loadout member list`, `loadout token list`, `loadout namespace list`, `loadout namespace tiers`) need no question.
- Never read, print or pass a token. A new machine token is shown once, to the person: let them copy it into their CI secret store themselves; do not capture, echo or paste it. Never open `~/.loadout/credentials.yaml`.
- The person runs `loadout login` themselves.
- Never add `--force` to overwrite a file the person edited without asking.
- If the person asks to try a teammate's package with `loadout review`, say it runs with full access and offer `--safe`.
- A line after a command saying a newer loadout is out, or that a registry needs a newer one, is for the person: tell them once, as the `loadout-setup` skill says, and do not update loadout yourself unless they ask.
- loadout never calls a model and holds no AI key. Do not look for a key or suggest one.

## Roles

| Role | Can do |
| --- | --- |
| member | install, sync, propose |
| maintainer (of a team) | also publish, approve or reject (not their own), add and remove members, machine tokens |
| owner (of a project) | all of that in the project and every team below; create teams; add maintainers; turn review and the wiki on or off and choose the wiki's course; approve their own proposals as owner, with a note |
| admin (whole registry) | everything: create projects, make owners, delete empty namespaces |

Maintainers add members; owners add maintainers; admins add owners. Anyone in a team is also a member of the project above it. A person needs no account first: they sign up with the address that was added, then run `loadout login`.

## Members

After `loadout login` these act on that registry (`--remote NAME` picks another):

```sh
loadout member list --namespace payments
loadout member add --namespace payments --email bob@example.com                  # a member
loadout member add --namespace payments --email alice@example.com --role maintainer
loadout member set --namespace payments --email alice@example.com --role member
loadout member remove --namespace payments --email bob@example.com               # also revokes his login tokens
loadout member invite --namespace acme --email dana@example.com --team payments=maintainer --team mobile=member
```

`member invite` adds a person to a project and some of its teams in one step: all roles are given or none, a role they have is raised and never lowered, and it prints a sign-in link (mailed too when the registry sends mail). The link holds no secret.

## Accounts on a registry with simple login

A registry started with `LOADOUT_LOGIN=simple` has email and password sign-in, no Google and no sign-up page. Accounts are made on the registry host, as the operator, not over the website. The password goes through standard input or a file, never on the command line, and the person should change a shared one:

```sh
echo 'PASSWORD' | loadout user add --data /var/lib/loadout --email NAME@company.com --password-file -
loadout member add --data /var/lib/loadout --namespace PROJECT --email NAME@company.com --role member
loadout member add --data /var/lib/loadout --namespace TEAM --email NAME@company.com --role member
```

Add the person to the project (L0) and to their team (L1). `loadout user add` keeps the password of an account that exists. Ask first, and never print or repeat a password back.

## Deleting a package

The web way first, and for everyone but admins it goes through review: anyone who reads a package asks on its page (**Ask to delete…**, with a reason) or with `loadout propose delete TEAM/NAME --reason "why"`, and a maintainer or owner of the package's team (or project) who is not the person who asked approves it on the request's page or with `loadout review approve del-ID`. An owner may approve their own request with `--as-owner --note "why"`. The record then says "Deleted without review". Rejecting (`loadout review reject del-ID --note`) or the author withdrawing (`loadout review withdraw del-ID`) leaves the package alone. A registry admin can also delete at once: the package page's **Delete package…** asks for a reason and the whole `TEAM/NAME`. Either way loadout saves a copy of the database first (`before-delete-TEAM-NAME-<time>.db` in `LOADOUT_BACKUP_DIR`, kept 14 days) and refuses to delete if it cannot, and keeps a record of who asked, who decided, why and what went. The web pages are off when the registry has no backup folder. Tell the person to check the list on the page, since it cannot be undone except by restoring that copy. Ask before approving anything for them: approving deletes the package for everyone.

On the registry host, as the operator, and as the fallback when the web delete is off: `loadout package delete TEAM/NAME --data /var/lib/loadout` lists what a package holds (releases, proposals with reviews and prompts, courses, open deletion requests) and deletes nothing. Add `--yes` and `--reason "why"` to delete it (the reason goes in the record). It takes no copy, so ask first and back up the database before `--yes`. People who have it installed keep their files and see "removed from the registry" on their next `sync`.

## Teams and review

```sh
loadout namespace list                                   # projects and teams the person belongs to, with their role
loadout namespace create mobile --parent acme            # owners: a new team in their project
loadout namespace set payments --require-review on           # owners and admins: every version goes through a proposal
loadout namespace set payments --wiki on                     # owners and admins: a Wiki tab, one page the team writes
loadout namespace set payments --course payments/onboarding  # owners and admins: a knowledge base as the wiki's Course box (none clears it)
```

With review required nobody publishes directly, owners included: they propose, then approve as owner.

The wiki is off until an owner turns it on. Then the namespace has one Markdown page: its members write it (on the website or with `loadout wiki push`), everyone in it reads it, and members of the teams below read a project's wiki too. Turning it off hides the page and keeps its history.

A course is a knowledge base the wiki shows as lessons in a Course box at the end of the page: its Markdown files at the top level, in name order (`01-setup.md`, `02-propose.md`). It must live in the team or a project above it, and it follows the package's latest release. A team without a course of its own shows its project's.

## Machine tokens for CI

A machine token belongs to one team, never expires, reads or reads and publishes, can propose but never approve or reject, and counts as the person who made it.

```sh
loadout token create --team payments --publish --label ci-release
loadout token list --team payments
loadout token revoke TOKEN_ID --team payments
```

In CI the token comes from a secret: `echo "$LOADOUT_TOKEN" | loadout remote add registry https://registry.example.com --token-stdin`.

## Model settings

Agents ask for `fast`, `balanced` or `deep`; the project decides per AI tool which model and effort that means, and teams inherit value by value. Maintainers, owners and admins change it:

```sh
loadout namespace tiers payments                             # what applies, and where each value comes from
loadout namespace set acme --model copilot:deep="Claude Sonnet 5" --effort copilot:deep=high
loadout namespace set payments --effort claude:deep=xhigh
loadout namespace set payments --model copilot:fast=         # an empty value inherits again
```

What the values look like per tool, and Copilot's caveats: [references/models.md](references/models.md).

## The website

The registry's website does the same for people who prefer it: **Settings** (members: add, change, remove, listed owners first, then maintainers, then members, with a search box for name, email or team and a "Teams:" line under each person; machine tokens; and for owners the review and wiki switches and the wiki's course), **Invite someone** in a project's Settings, **Choose models** for the model settings, and **New** to create a team (owners) or project (admins). Creating projects, making owners and deleting namespaces are admin actions on the website or the registry host, not from a login token.
