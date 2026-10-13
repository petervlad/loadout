# loadout

loadout shares a team's AI agents, skills and knowledge bases with everyone on the team, in the AI tools they already use: Claude Code and GitHub Copilot. You publish a package once to a registry; each teammate installs it with one command and gets updates with `loadout sync`. Packages can sit in layers (a project above its teams), people sign in with accounts and roles, and a change can be proposed and reviewed before it is released, like a pull request for an agent.

## The one rule

loadout moves files. It never runs a model and never holds an AI key. Every agent, skill and knowledge base runs in each person's own AI tool, on their own subscription. Even `loadout review`, the one command that starts an agent, runs on the reviewer's machine in the reviewer's own tool. The registry only stores text.

## Install

Pick one.

- **Release binary.** Linux, macOS and Windows archives for amd64 and arm64, with `checksums.txt`, are on the project's GitHub releases page. This script downloads the right one, checks it and puts `loadout` in `~/.local/bin`:

  ```sh
  curl -fsSL https://raw.githubusercontent.com/petervlad/loadout/main/scripts/install.sh | sh
  ```

- **Homebrew** (macOS), once the cask is published: `brew install --cask petervlad/tap/loadout`.
- **Your team's registry**, if it publishes the CLI: `curl -fsSL https://YOUR-REGISTRY/install.sh | sh`. See [the guide](docs/GUIDE.md#publish-the-cli-from-your-registry).
- **Go 1.27 or newer:** `go install github.com/petervlad/loadout/cmd/loadout@latest`.

Check it with `loadout version`, and update it later with `loadout update`, which follows however you installed it.

## Try it in five minutes, on one machine

You need Claude Code or GitHub Copilot CLI installed (loadout writes into their folders).

```sh
# 1. Start a registry on this machine (data in ~/.loadout/server).
loadout serve &

# 2. Make a project, a team in it, and an invite that may publish and read.
loadout namespace create acme --kind project
loadout namespace create demo --parent acme
loadout token create --team demo --publish --invite --url http://localhost:7420 \
  | grep -o 'ldi_[A-Za-z0-9_=-]*' > invite.txt
loadout team join < invite.txt

# 3. Write a package, publish it, install it.
loadout new agent demo/planner
$EDITOR planner/agent.md
loadout publish planner
loadout install demo/planner      # into ~/.claude/agents and ~/.copilot/agents
loadout status
```

Edit the package and run `loadout publish planner --bump minor`, then `loadout sync` to see an update arrive. Skills (`loadout new skill`) and knowledge bases (`loadout new kb`) work the same way, and `loadout import` turns agents and skills you already have into packages. The [guide](docs/GUIDE.md) covers all of it.

## Run your own registry

A registry is one static binary (`loadout serve`) with a SQLite database. For a team you want it on an always-on machine behind HTTPS: any Debian or Ubuntu host you can ssh to (a cloud VM, a VPS, a home server) or a container platform such as Fly.io.

Two ways to do it:

- **Let your own AI agent do it.** Install the two host skills, then ask your agent to "set up a loadout registry":

  ```sh
  loadout setup --host-skills
  ```

  `loadout-host-provision` creates the machine with your own cloud CLI and logins, names it, installs loadout with HTTPS and backups, and checks it. `loadout-host-setup` then chooses how people sign in (email codes through SES, Resend, Postmark or any SMTP provider, Google, or simple login with no mail), checks the settings with `loadout serve --check`, proves mail with `loadout mail test`, and makes the first admin, project and team. The agent asks before anything that costs money, changes DNS or deletes, never asks you to paste a secret and never reads your settings file. loadout calls no cloud and runs no model for this.
- **By hand.** [deploy/README.md](deploy/README.md) lists the files and each step: the machine, DNS, the settings file, `deploy.sh`, backups, and the container recipe.

A fresh simple-login registry (`LOADOUT_LOGIN=simple`) comes with one admin, `admin@loadout.local`, so a stranger can sign in at once: deploy, read the one-time password from `journalctl -u loadout` (or set `LOADOUT_ADMIN_PASSWORD` first), sign in at `https://YOUR-DOMAIN/auth/login`, then change the password with `loadout user password --email admin@loadout.local` on the host, because the journal keeps the printed one. With email sign-in the address cannot receive a code, so make the first admin with `loadout admin grant` instead. See [deploy/README.md](deploy/README.md).

Run one instance only: the database is SQLite and the sign-in limits live in the process.

## How it works

- A **package** is an agent (`agent.md`), a skill (`SKILL.md` plus files) or a knowledge base (Markdown documents). Each has a `loadout.yaml` with its id (`team/name`), kind, version and description. Published versions never change.
- A **registry** stores packages by namespace: a team, or a project that sits above several teams. A closer layer wins when two layers ship the same name.
- An **adapter** per AI tool renders a package into that tool's folders. `loadout sync` keeps installs current per policy (`auto`, `notify`, `pin`) and never overwrites a file you edited.
- **Accounts and roles** (member, maintainer, owner, admin) decide who may install, propose, publish and approve. `loadout login` signs in through the browser, like `gh auth login`; without accounts, invites carry a token.
- **Review**: `propose` uploads a candidate, a teammate runs both versions in their own AI tool with `review`, and `review approve` releases it. A package that should go is deleted the same way: `propose delete` asks, a second person approves, and the registry saves a copy of its database first.

Supported tools: Claude Code and GitHub Copilot. Packages go only into the tools each person has.

## Documentation

- [docs/GUIDE.md](docs/GUIDE.md): the user guide and command reference.
- [docs/DESIGN.md](docs/DESIGN.md): packages, safety rules, login, review, the source of truth for the architecture.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): diagrams and a map of the technical docs.
- [deploy/README.md](deploy/README.md): run the registry on your own host or in a container.
- [CONTRIBUTING.md](CONTRIBUTING.md): build, check, and how to contribute.
- [SECURITY.md](SECURITY.md): report a vulnerability.
- [docs/CHANGES.md](docs/CHANGES.md): what each update added and fixed.

## Develop

Requires Go 1.27 and golangci-lint v2.

```sh
make check              # format, lint, test with -race, govulncheck
make release-snapshot   # all platform archives into ./dist (needs goreleaser)
```

## Licence

MIT, see [LICENSE](LICENSE). The licences of what loadout links or embeds are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
