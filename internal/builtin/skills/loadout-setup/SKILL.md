---
name: loadout-setup
description: Set up and use the loadout CLI, which copies a team's shared AI agents, skills and knowledge bases into Claude Code and GitHub Copilot. Use when the user asks to install loadout, sign in to a loadout registry, join a team, install, sync, update, check, uninstall or roll back team packages, choose how updates arrive, or when a loadout command fails.
---
# Using loadout

loadout moves files. It copies a team's agents, skills and knowledge bases (KBs) from a registry into the person's AI tools. It never runs a model and never holds an AI key: everything runs in the person's own tool, on their own subscription.

## First, look for local rules

Look for an installed skill named `<project>-loadout` (for example `acme-loadout`) or `loadout-local`. A company publishes one with its registry address, which team to join and its own rules. When it exists, follow it for anything company specific and use this skill for the commands.

## Rules for you, the agent

- Ask the person before anything other people will see: publishing, proposing, approving, rejecting or withdrawing a proposal, adding, inviting, changing or removing members, changing a namespace, making or revoking tokens.
- Never read, print, copy or pass a token or an invite. The person runs `loadout login` themselves. Never open `~/.loadout/credentials.yaml`.
- Never add `--force` to overwrite a file the person edited without asking them first. loadout refuses on purpose.
- `loadout review` runs a teammate's package with the tool's full access; say so and offer `--safe`.
- loadout never calls a model. Do not look for an AI key or suggest one.

## Install the CLI

For now loadout is built from source with Go 1.27 or newer. The team lead gives access to the source.

```sh
git clone <the loadout repository> loadout && cd loadout
go install ./cmd/loadout
loadout version
```

`$(go env GOPATH)/bin` must be on the `PATH`. `loadout version` names the release it was built from: 0.4.0, or "after 0.4.0 (built ...)" for a commit after it.

## When loadout says a newer version is out

A registry tells loadout which release it runs. When the person's loadout is older, a line follows a command on standard error, at most once a day per release. Run by you, it reads "loadout, for the agent: a newer loadout CLI is available (0.5.0; this is 0.4.0). Tell the user once ..."; at a terminal, "loadout 0.5.0 is out (you have 0.4.0). To update, in your loadout folder: git pull && go install ./cmd/loadout".

- Tell the person once, in one sentence, with the command loadout printed. It is not an error: the command before it worked as usual.
- Do not run `git pull` or `go install` yourself unless they ask: the loadout folder is their clone, and you do not know where it is.
- "registry.example.com needs loadout 0.3.0 or newer and this is 0.2.0, so some commands may fail" comes after every command. Say that this is likely why a command failed, and suggest updating before trying again.
- `loadout version` shows the same from what a registry last said. `LOADOUT_UPDATE_NOTICE=off` keeps the line out of scripts; the too-old one stays.

## Sign in

The registry address comes from the local skill above or from the team lead. The person signs in themselves; it opens their browser, where they sign up or sign in and approve the code the terminal shows:

```sh
loadout login --remote https://registry.example.com
```

- One sign-in covers every team the person belongs to on that registry, and the projects above them. `loadout team list` shows them.
- Added to a new team later, or a new team missing from `loadout list`: run `loadout login` again.
- The login token works for 90 days; `loadout login` again renews it.
- A registry without accounts uses invites instead. The person saves the invite to a file and runs `loadout team join < invite.txt`. Do not read or print the invite: it holds a token.
- `loadout logout` signs out of a registry; `loadout team leave payments` leaves one team on this machine.

## Install packages

```sh
loadout list                             # what the teams and the project publish, with KIND and LAYER
loadout list --kind skill --sort updated
loadout install --all                    # everything the person can read
loadout install payments/planner             # one package
loadout install payments/planner@1.2.0       # one version
loadout install payments/planner --target claude
loadout install payments/planner --scope project   # into the git repository, for everyone who clones it
```

- Packages go into the tools whose folder exists: `~/.claude` (Claude Code) and `~/.copilot` (Copilot). `--target claude,copilot` chooses.
- Layers: a team (L1) shadows its project (L0) when both ship a package with the same name. `loadout layers` shows each team's layers.
- Tell the person to start a new session in their tool afterwards: tools read agents and skills when a session starts.

## Stay up to date

```sh
loadout sync                             # checks every installed package
loadout policy payments/planner auto         # auto: install updates; notify (default): report them; pin: hold
```

`sync` never overwrites a file the person edited and is safe to run from cron. When the team changes which model its agents run, `sync` treats it like a new version.

## When an update is held back

An edited file keeps an update out. `sync` then says "held back by your edits" (policy auto; `sync` exits with code 2 until it is resolved) or "update available, you edited 1 file" (policy notify), names the files and prints the ways forward. Explain them and let the person choose:

- `loadout install payments/planner --merge` takes the release and keeps their edits, merged line by line. Offer this first.
- `loadout install payments/planner --force` replaces their edits with the release. Ask first: their edits are lost.
- `loadout policy payments/planner pin` keeps their version and stops updates, and the exit code 2.
- `loadout diff payments/planner` shows what the release changed.

After `--merge`, a file where both changed the same lines holds conflict markers (`<<<<<<< yours (edited 1.2.0)`, `||||||| installed 1.2.0`, `=======`, `>>>>>>> release 1.3.0`). Help the person resolve them: show both sides, agree what stays, delete the marker lines. Until then their AI tool reads the markers; `loadout status` lists the files left. `--merge` does not work for folder installs or loadout's own skills, and never together with `--force`.

For a project install add `--scope project` and run the command in that repository. A built-in skill the person edited is the same: `loadout setup --skills --force` replaces it.

`loadout status` repeats it below its table, and `loadout notices` prints one line per held update, both without asking a registry.

## The line at the start of a session

`loadout login` sets up a session-start hook in Claude Code (`~/.claude/settings.json`) and Copilot (`~/.copilot/hooks/loadout.json`). When an update is held back, a new session starts with a line like "loadout: 1 update is held back by your edits (payments/planner 1.3.0). Run loadout status." If you see it, or get it as context:

- Mention it to the person once, in one sentence, and offer to run `loadout status` with them.
- Then explain the choices above, `--merge` first. Never run `loadout install --force` without asking: it replaces their edits.
- A line about merge conflicts means a file still holds markers from `loadout install --merge`: offer to resolve them.
- "A newer loadout CLI is available" or "needs loadout 0.3.0 or newer": see [When loadout says a newer version is out](#when-loadout-says-a-newer-version-is-out).

The hook only reads files on the machine; it never calls a registry or a model. `loadout setup --hooks` sets it up for someone who signed in before it existed; `loadout setup --no-hooks` removes it for good.

## Check, compare, go back, remove

```sh
loadout status                           # ok, modified, missing or shadowed
loadout status --files                   # every file of each skill folder
loadout diff payments/planner                # installed version against the latest
loadout rollback payments/planner            # previous release, then pinned
loadout rollback payments/planner 1.1.0
loadout uninstall payments/planner           # edited files are kept
```

## loadout's own skills

This skill and three others (`loadout-author`, `loadout-review`, `loadout-admin`) come with the CLI. `loadout login` installs them for the person, `loadout sync` updates them when the CLI changes, and `loadout status` lists them as `loadout/loadout-setup` and so on.

- `loadout uninstall loadout/loadout-review` removes one; login and sync never add it back.
- `loadout setup --skills` installs them again, now. With no `~/.claude` or `~/.copilot` on the machine, add `--target claude` (or `copilot`) to create the folder.

## When something fails

Read [references/troubleshooting.md](references/troubleshooting.md): it lists the messages loadout prints and what to do. `-v` shows debug output, and every run is logged to `~/.loadout/loadout.log` (it never holds tokens).
