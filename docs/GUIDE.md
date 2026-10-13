# loadout user guide

loadout shares your team's AI agents, skills and knowledge bases with everyone on the team, in the AI tools they already use: Claude Code and GitHub Copilot. You publish a package once; each teammate installs it with one command and gets updates with `loadout sync`.

[What's new](https://loadout.lat/docs/whats-new/) lists what each update of loadout added and fixed.

## The one rule

loadout moves files. It never runs a model and never holds an AI key. Every agent, skill and knowledge base runs in each person's own AI tool, on their own subscription.

That includes `loadout review`, the one command that starts an agent: it runs on the reviewer's laptop, in the reviewer's own Claude Code or Copilot CLI. The registry only stores text.

## Concepts

### Packages

A package is one of three kinds:

| Kind | You write | Your AI tool gets |
| --- | --- | --- |
| agent | `agent.md`: a prompt plus the tools and model it needs | one agent file per tool |
| skill | `SKILL.md` plus optional files (`references/`, `scripts/`, `assets/`) | a skill folder |
| knowledge base (`kb`) | Markdown or text documents, in folders if you like | a skill folder whose `SKILL.md` loadout writes: an index of the documents |

Every package folder also has a `loadout.yaml` with its id, kind, version and a one-line description.

A package id is `namespace/name`, for example `payments/planner`. Names use lowercase letters, digits and single hyphens, at most 64 characters.

Versions look like `1.4.0`. A published version never changes, and every new version must be higher than the last. You install the newest version unless you ask for one: `payments/planner@1.2.0`.

A knowledge base costs nothing until it is used: both tools read only a skill's description at startup and open its files when a task needs them.

### Registry, namespaces, projects and teams

The registry is the server your team publishes to and installs from, for example `https://registry.example.com`. It has a website too.

A namespace is the first part of a package id. There are two kinds:

- A **team** (such as `payments`) holds one team's packages.
- A **project** (such as `acme`) sits above several teams and holds what they all share, such as `acme/python-standards`.

Every team belongs to a project. Members of a team read their team's packages and the project's, and count as members of the project: they may propose changes to it, but not publish there. They never see a sibling team's packages.

### Layers

| Layer | What it is | Managed by |
| --- | --- | --- |
| L0 | the project: shared by every team in it | loadout |
| L1 | your team | loadout |
| L2 | your own files in your tools' folders (agents and skills you made yourself) | you, not loadout |

When two layers ship a package with the same name, the closer layer wins: your team's `payments/planner` shadows the project's `acme/planner`, and only the team's file is installed. `loadout status` then shows the project's copy as "shadowed by payments/planner". Uninstall the team's package and the project's comes back when you install it again.

loadout never touches L2. If a package would overwrite a file you made yourself, the install stops and names the file. Your file stays unless you pass `--force`.

On a machine an older setup filled (a dotfiles repo, or a script that linked agents and skills into `~/.claude`, `~/.copilot` and `~/.agents`), the same install stops on every link and file in the way. Run it with `--adopt`: loadout moves them, and same-named copies in `~/.agents` that Copilot also reads, into `~/.loadout/backup/<date>/` and installs. Nothing is deleted, and a link's target is never touched. To undo, move them back.

`loadout cleanup` finds the old ones without installing anything: agents, skills and knowledge bases with the name of a package your registry offers that loadout did not write, in the folders your tool reads (`--tool copilot`, the default, also looks in `~/.agents`, `--tool claude`, `--tool vscode` for VS Code, which reads `~/.copilot`, `~/.agents` and `~/.claude`, so it lists an agent twice when an old copy sits in `~/.claude`, and reads `~/.agents/skills` before `~/.copilot/skills`). With `--duplicates` it also finds agents and skills that sit in more than one folder the tool reads, even when no package has that name (an old setup that installed other teams' files), keeps one copy and lists the rest. loadout's own copies are never moved: an agent installed for both Claude Code and Copilot is in `~/.claude/agents` and `~/.copilot/agents`, and VS Code lists it twice (fix it in VS Code's user settings.json with `"chat.agentFilesLocations": {".claude/agents": false, "~/.claude/agents": false}`, then reload the window. Claude Code is not affected). It only lists them until you add `--apply`, which moves them into the same backup folder.

### Roles

Everyone who signs in has a role in a team or project:

| Role | Can do |
| --- | --- |
| member | install, sync, propose new versions, ask to delete a package, edit the wiki |
| maintainer (of a team) | everything a member can, plus publish, approve or reject proposals and requests to delete a package (not their own), add and remove members, make machine tokens |
| owner (of a project) | everything a maintainer can, as owner of the project and of every team below it; create teams in the project; add maintainers; turn review and the wiki on or off and choose its course; approve their own proposals and deletion requests as owner, with a note, which everyone can see |
| admin (whole registry) | everything: create projects, make owners, delete empty namespaces, delete a package at once |

Anyone in a team is also a member of the project above it: they install its packages and may propose changes to it, and the project's maintainers or owners decide. They read the project's wiki too, but edit it only with a role in the project. A team maintainer is not a project maintainer; an owner adds that role separately.

Team memory has its own rules: members read and save to their team's memory, maintainers and owners also retract anyone's memory, move one to the project and switch it on or off. In a project, saving needs a maintainer.

On a registry in simple login mode there is no sign-up: an admin gives you an account and a password (`loadout user password --email E` on the registry host). A brand-new simple-login registry has one account, the admin `admin@loadout.local`: its one-time password is printed once in the service's log (`journalctl -u loadout`) unless the operator set `LOADOUT_ADMIN_PASSWORD`. Sign in with it, then have the operator change it with `loadout user password --email admin@loadout.local`, because the log keeps the printed one. It is created only while the registry has no account at all.

A new member needs no account first. A maintainer adds their email; they sign up with that address (or Google) and run `loadout login`.

### Review

A team can require review. Then nobody publishes directly, not even maintainers: every new version is a **proposal** that a teammate approves.

A proposal carries the prompts the author tested with. A reviewer reads the diff and can run `loadout review TEAM/NAME`, which runs the version they have and the candidate on those prompts in the reviewer's own AI tool and shows the answers side by side. Approving turns the proposal into a normal release that everyone gets with `sync`.

A proposal remembers the release it was made from. When someone releases a newer version while it is open, the proposal is **behind**: approving it as it is would release the author's files without that newer release's changes, and everyone's next sync would undo them. loadout says so wherever the proposal shows, refuses a plain approval, and gives the author `loadout rebase` to bring the proposal up to date on their own machine.

Nobody approves their own proposal, with one exception: an owner of the project (or of the team's project above it) may approve their own, so a small team, or an owner alone, can still release. They say so explicitly and say why, and the proposal and the release are marked "Released without review" for everyone to see. Admins who are not owners there, and machine tokens, never do. Any member may propose, even to the project layer above their team; only that layer's maintainers or owners decide.

## Get started

### 1. Install the CLI

Pick one:

- **The release binary.** `curl -fsSL https://raw.githubusercontent.com/petervlad/loadout/main/scripts/install.sh | sh` downloads the archive for your system from the project's GitHub releases, checks it against `checksums.txt` and puts `loadout` in `~/.local/bin` (`LOADOUT_INSTALL_DIR` changes it, `LOADOUT_VERSION` picks a release). Or download the archive from the releases page yourself; Windows ships a `.zip`.
- **Homebrew** (macOS), once the cask is published: `brew install --cask petervlad/tap/loadout`.
- **Your registry.** If your team's registry publishes the CLI (see [Publish the CLI from your registry](#publish-the-cli-from-your-registry)), `curl -fsSL https://REGISTRY/install.sh | sh` puts the build for your system in `~/.local/bin` (`LOADOUT_BIN_DIR` changes it), checks it against the registry's `checksums.txt` and installs nothing if it does not match. It needs no `sudo`, and the download uses your computer's normal trust store like any other HTTPS address.
- **Go** 1.27 or newer: `go install github.com/petervlad/loadout/cmd/loadout@latest`, or from a clone, `go install ./cmd/loadout`. Make sure `$(go env GOPATH)/bin` is on your `PATH`.

```sh
loadout version
```

`loadout update` updates the CLI the way it was installed: from a registry it runs that registry's install script again, with Homebrew it runs `brew upgrade --cask loadout`, and for the release binary or a clone it prints what to do (run the install script again, or `git pull && go install ./cmd/loadout`). It goes by what registries said the last time loadout talked to one, so run it after a command that asks a registry, such as `loadout sync`. `loadout update --check` shows the command without running it, and `--force` runs it when no newer release is known.

`loadout version` names the release you built. Built from a release tag it says `loadout 0.4.0`; built from a commit after it, `loadout after 0.4.0 (built 4 Oct 2026 from b42ddb9)`; from a clone without any release yet, `loadout dev (built 4 Oct 2026 from b42ddb9)`. `modified` in the brackets means you built it with changes of your own in the folder.

### 2. Sign in

Your team lead gives you the registry address and adds your email to the team. Then:

```sh
loadout login --remote https://registry.example.com
```

loadout shows a code and opens the registry in your browser. Sign up or sign in (email and password plus a mailed code, or Google), check that the code matches and approve.

One sign-in covers every team you belong to on that registry, and the projects above them. The token says who you are; what you may do is your role in each team at that moment, so joining another team or getting a new role needs no new login. `loadout team list` shows the teams you joined; if a maintainer adds you to a new team, run `loadout login` again to see it in `list`.

Your login token works for 90 days. Run `loadout login` again to renew it.

Signing in also gives your AI agent loadout's own skills, so you can ask it things like "install the team's packages" or "propose my change to the planner" (see [loadout's own skills](#loadouts-own-skills)).

### 3. Install everything

```sh
loadout list              # what your team and project publish
loadout install --all     # install all of it
```

Or pick one:

```sh
loadout install payments/planner
loadout install payments/runbooks
```

### 4. Stay up to date

```sh
loadout sync
```

`sync` checks every installed package. What happens next depends on its policy (see [Updates](#updates-and-policies)). It is safe to run from cron:

```sh
# every weekday at 9:00
0 9 * * 1-5  loadout sync
```

`sync` exits with code 0 when all went well, 1 when a registry could not be checked, and 2 while an `auto` update is held back by a file you edited (see [When your edits hold an update back](#when-your-edits-hold-an-update-back)). A cron job that mails on failure therefore mails you about a held-back update every run until you resolve it; scripts that treat any non-zero code as a registry failure should look for 2.

#### A newer loadout

Your registry says which loadout release it runs, and loadout compares it with its own version on the requests it makes anyway; it adds no request of its own. When yours is older, one line after a command says so, at most once a day for each new release:

```text
loadout 0.5.0 is out (you have 0.4.0). To update, in your loadout folder: git pull && go install ./cmd/loadout
```

The last words depend on how you installed it: `brew upgrade --cask loadout` for Homebrew, `curl -fsSL https://REGISTRY/install.sh | sh` for a download from a registry, and a pointer to the install page for the release binary. `loadout update` runs the first two for you.

- The line goes to standard error, after the command's own output, and never changes what the command prints or its exit code. A script or cron job sees it at most once a day too; set `LOADOUT_UPDATE_NOTICE=off` in its environment to keep it out. `status`, `notices` and `version` still show it when you run them.
- If a registry says your loadout is too old for it ("registry.example.com needs loadout 0.3.0 or newer"), that line comes after every command, `LOADOUT_UPDATE_NOTICE=off` included, because it explains why a command may fail. Update, then try again.
- When an AI agent (Claude Code, Copilot) runs loadout, the line is written for the agent and asks it to tell you, once, with the command. It is told not to update loadout for you unless you ask.
- `loadout version` shows the same from what a registry last said, without asking one: `0.5.0 is out ...`, or `Up to date as of 4 Oct`.
- A build of your own with changes in it, or one without a release (`loadout dev ...`), is never told.

### Joining with an invite instead

A registry without accounts uses invites. Your team lead sends you one (`ldi_...`); it holds the address, the team and a token, so keep it private. Save it to a file and run:

```sh
loadout team join < invite.txt
```

## Set up Claude Code and Copilot

loadout writes into the tools you have. It finds them by their folders: `~/.claude` for Claude Code and `~/.copilot` for Copilot. If a tool's folder does not exist, loadout skips that tool. Use `--target` to choose yourself:

```sh
loadout install payments/planner --target claude
loadout install payments/planner --target claude,copilot
```

### User scope and project scope

By default a package is installed for you, in your home folder (`--scope user`). With `--scope project` it goes into the git repository you are in, so everyone who clones the repo gets it:

```sh
cd ~/src/billing-service
loadout install payments/planner --scope project
```

In a repository loadout looks for `.claude` (Claude Code) and `.github` (Copilot).

### Where files land

| | Claude Code | Copilot |
| --- | --- | --- |
| Agents, user scope | `~/.claude/agents/NAME.md` | `~/.copilot/agents/NAME.md` |
| Agents, project scope | `.claude/agents/NAME.md` | `.github/agents/NAME.agent.md` |
| Skills and knowledge bases, user scope | `~/.claude/skills/NAME/` | `~/.copilot/skills/NAME/` |
| Skills and knowledge bases, project scope | `.claude/skills/NAME/` | `.claude/skills/NAME/` (the same folder) |

In a repository both tools read `.claude/skills`, so loadout writes one copy for both.

### Check that it worked

Start a **new session** after installing: both tools read agents and skills when a session starts.

- **Claude Code:** run `/agents` to see the installed agents. Skills and knowledge bases are picked up automatically; ask for something the skill's description covers.
- **Copilot CLI:** run `/agent` to pick an installed agent. Skills are picked up automatically, like in Claude Code.

On the loadout side, `loadout status` lists every installed file and whether it is intact.

### Messages in Claude Code and Copilot

When your edits hold an update back (see [When your edits hold an update back](#when-your-edits-hold-an-update-back)), a new session in your AI tool says so in one line, so you do not have to remember to run `loadout status`:

```text
loadout: 1 update is held back by your edits (payments/planner 1.3.0). Run loadout status.
```

The same line tells you when a newer loadout is out (see [A newer loadout](#a-newer-loadout)), at most once a day.

`loadout login` sets this up for you and says so in one line. It adds a session-start hook that runs `loadout notices`, which reads only files on your machine: what your last `sync` found, checked against your files, and what a registry last said about loadout releases. It never asks a registry and never calls a model, and it prints nothing when there is nothing to say.

- **Claude Code** shows the line when a new session starts (not when you resume one), and your agent may mention it once. loadout adds one entry under `hooks.SessionStart` in `~/.claude/settings.json` and leaves everything else in the file exactly as it was. Before its first change it keeps a copy as `~/.claude/settings.json.loadout.bak`.
- **Copilot** gets its own file, `~/.copilot/hooks/loadout.json`, which Copilot CLI and VS Code both read. VS Code shows the line; Copilot CLI hands it to the agent, which is asked to mention it in its first reply.

Hooks go only into your home folder, never into a repository, and only into tools whose folder exists. If `~/.claude` or `settings.json` is a symbolic link (a dotfiles setup), or the file is not valid JSON, loadout leaves it alone and prints the entry for you to add by hand.

```sh
loadout setup --hooks       # set them up now (signed in before they existed? run this once)
loadout setup --no-hooks    # remove them; login will not add them again
```

Until you choose either, `sync` reminds you with one tip line when something is held back.

## Team memory in your AI tools

When a team has team memory on, `loadout install` and `loadout sync` add its memory server to Claude Code (through `claude mcp add-json`, so `~/.claude.json` is changed by Claude Code itself) and to Copilot (`~/.copilot/mcp-config.json`). Each change is one line, for example `added lime's team memory to Claude Code (~/.claude.json)`. When a team switches memory off, the next `sync` removes the entry. loadout also makes the memory token the server needs and keeps it on your machine, and replaces it when it is close to its end. Nothing else in those files is touched, a tool that is not installed is skipped, and a problem is a warning that never fails the install or the sync. Offline, nothing changes.

Claude Code cuts a server's instructions at 2,048 characters, which would hide most of the memory index. The first time loadout adds a memory server to Claude Code it also sets `CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH` to 24000 in `~/.claude/settings.json`, and says so. If you set that variable yourself, loadout leaves it alone.

```sh
loadout setup --no-memory                  # remove the entries and stop adding them
loadout setup --no-memory --target copilot # only for Copilot
loadout setup --memory                     # allow them again and add them now
```

## loadout's own skills

loadout comes with four skills that teach your AI agent to use loadout. Their text is inside the loadout program, so it always names the commands and flags of the loadout you have.

| Skill | Your agent uses it when you want to |
| --- | --- |
| `loadout-setup` | install loadout, sign in, join a team, install, sync, check, uninstall or roll back packages, choose how updates arrive, or fix an error |
| `loadout-author` | create or import an agent, skill or knowledge base, try it, publish it or propose a new version |
| `loadout-review` | review a teammate's proposal: run both versions, read the report, approve, reject or withdraw |
| `loadout-admin` | add, invite or remove people, change roles, make machine tokens, require review, choose models |

Every one of them tells your agent to ask you before anything your team would see (publish, propose, approve, reject, withdraw, member and namespace changes, tokens), never to read or pass on a token (you run `loadout login` yourself), never to overwrite a file you edited with `--force` without asking, and that `loadout review` runs a teammate's package with full access, offering `--safe`.

**How they arrive.** `loadout login` installs them for you, in `~/.claude/skills` and `~/.copilot/skills`, whichever tools you have; the folder you run it in does not matter. `loadout status` lists them as `loadout/loadout-setup` and so on. `loadout sync` updates them when you install a newer loadout, and never overwrites one you edited.

**No Claude Code or Copilot yet?** Login still works and says so in one line. The skills are added by the next `loadout sync` after you install one of them, or right away with `--target` (which creates the tool's folder):

```sh
loadout setup --skills --target claude
```

**Don't want one?** Uninstall it; login and sync will not add it back:

```sh
loadout uninstall loadout/loadout-admin
loadout setup --skills               # changed your mind: all four again
```

`loadout policy loadout/loadout-setup pin` keeps a skill at its version.

**Skills for running a registry.** Two more skills ship with loadout for the person who wants a registry of their own, and are not installed by login: `loadout setup --host-skills` adds `loadout-host-setup` and `loadout-host-provision` the same way. After that `sync` keeps them in step with the CLI but never adds them anywhere new, and `loadout uninstall` removes one for good until you run the command again. They are text your own AI agent follows with your own cloud account and tools; loadout calls no cloud and holds no key.

**Read them on the website.** Every registry has a **Start here** page at the top of the sidebar (`/skills`), open to anyone, even before signing in. It says what loadout is, explains the words (agent, skill, knowledge base, package, registry, project and team), lists the first commands with this registry's address, and shows the four skills with things you can ask your AI tool. Each skill's page has its text and reference files, and the loadout version they belong to.

### Your project's own loadout skill

loadout's skills know loadout, not your company. They tell the agent to look for an installed skill named `<project>-loadout` (for example `acme-loadout`) or `loadout-local`, and to prefer it for anything company specific. A project owner writes one like any skill and publishes it on the project, so every team gets it:

```sh
loadout new skill acme/acme-loadout
```

```markdown
---
name: acme-loadout
description: How Acme uses loadout - registry address, which team to join, our naming and review rules. Use together with the loadout skills whenever the user works with loadout at Acme.
---
# loadout at Acme

- Registry: https://registry.acme.example. Sign in with `loadout login --remote https://registry.acme.example`.
- New starters join their squad's team; ask in #dev-tools if you do not know which.
- Package names start with the squad, for example `payments/payments-planner`.
- Every team requires review; propose with at least three prompts from real tickets.
- Never put customer data in a knowledge base.
```

Then `loadout publish acme-loadout`. Keep it short and factual: addresses, which team, local rules. Leave the commands to loadout's skills, which stay in step with the CLI. Put the description in quotes if it contains `: `.

## Team memory

Your AI agent learns facts that help the whole team: which Jira host a project uses, which label a release needs, how a known failure is fixed. Team memory is a shared place where the agents of a team's members save such facts and find each other's. It holds short facts, not documents. Anything long belongs in the team's wiki or a knowledge base, and a memory links to it.

loadout only stores the text and searches it by words. It never calls a model and never holds an AI key. Your own agent does all the thinking.

### Switch it on

A maintainer or owner of a team (or an admin) switches it on. It is off at first. While it is off the team has no memory pages and no memory server at all.

- On the web: **Settings** of the team, **Team memory**.
- From the CLI: `loadout namespace set lime --memory on --remote office`. Use `--memory off` to switch it off again. On the registry host the operator has no role, so the command asks you to use `--remote` there.

A team reads its own memory and its project's, as long as the project has switched memory on too. It saves only to its own. A project can switch memory on for its own facts, and its maintainers write to it.

### Memory tokens

Your AI tool reaches team memory with a memory token. It works on the memory server only. It cannot install, publish or change anything else, so a leaked tool setting carries no publish rights. A token belongs to you, works for every team where you may use memory, and lasts 90 days.

```sh
loadout memory token create                      # makes one and keeps it on this machine
loadout memory token create --label work-laptop
loadout memory token list
loadout memory token revoke ID
```

`create` stores the token in `~/.loadout/memory-credentials.yaml`, which only you can read, and does not print it. Add `--show` to print it once, for a tool you configure by hand. You can also make one on the **Account** page of the website, under **Memory tokens**, where it is shown once. `LOADOUT_MEMORY_TOKEN` in the environment overrides the stored token. You can hold at most 20 valid memory tokens, and a new one is refused with that number until you revoke one. Signing out everywhere ends your memory tokens too, so after it the memory server answers that your token was refused. Run `loadout sync` or `loadout memory token create` to get a new one.

You need to be signed in with `loadout login` first. A machine token for CI cannot make a memory token.

### Connect your AI tool

Each team has its own server, at `/mcp/t/TEAM` on the registry. The simplest way to reach it is the bridge:

```sh
loadout mcp --team lime
```

This is a small program that an AI tool starts and talks to over standard input and output, so you rarely run it yourself. It sends every call to the registry with your memory token and trusts the registry's CA file if you gave one at login. A tool that uses it sees the same tools and answers as one that talks to the registry directly. If the registry cannot be reached within 5 seconds, the bridge still starts and every call answers that memory cannot be reached, so a closed VPN never stops your tool from starting. `--remote NAME` picks the registry. Without it the one the team is joined on is used.

`loadout install` and `loadout sync` add this for you. When a team has team memory on, they add its memory server to Claude Code (through `claude mcp add-json`, so `~/.claude.json` is changed by Claude Code itself) and to Copilot (`~/.copilot/mcp-config.json`). Each change is one line, for example `added lime's team memory to Claude Code (~/.claude.json)`. When a team switches memory off, the next `sync` removes the entry. A registry that is too old to know team memory, or that refuses your login for it, counts as no answer, so `sync` removes nothing because of it. Nothing else in those files is touched, a tool that is not installed is skipped, and a problem is a warning that never fails the install or the sync. Offline, nothing changes.

They also make the memory token the server needs, keep it on your machine and replace it when it has less than 14 days left (the one it replaces is revoked), so you never run `memory token create` yourself for this.

Claude Code cuts a server's instructions at 2,048 characters, which would hide most of the memory index. The first time loadout adds a memory server to Claude Code it also sets `CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH` to 24000 in `~/.claude/settings.json`, and says so. That setting raises the limit for every MCP server in Claude Code, not only loadout's. If you set that variable yourself, loadout leaves it alone, and you can remove the line from `~/.claude/settings.json` to go back to the default.

```sh
loadout setup --no-memory                  # remove the entries and stop adding them
loadout setup --no-memory --target copilot # only for Copilot
loadout setup --memory                     # allow them again and add them now
```

To add the bridge yourself instead (after `--no-memory`, or for a tool loadout does not know):

- **Claude Code:** `claude mcp add-json --scope user loadout-memory-lime '{"type":"stdio","command":"loadout","args":["mcp","--team","lime"]}'`
- **Copilot CLI:** add the same program to `mcpServers` in `~/.copilot/mcp-config.json`, as `{"type": "local", "command": "loadout", "args": ["mcp", "--team", "lime"], "tools": ["*"]}` under the name `loadout-memory-lime`.

The entry holds no token, only the command, so it is safe to keep in dotfiles.

### Other clients

When memory is on, the team's **Memory** tab has a **Set up** page for any other tool that speaks MCP. It shows the team's address, a `curl` that tests it, the bridge command, and a configuration for tools that connect over HTTP with an `Authorization: Bearer` header.

### What your agent sees

When the tool connects, the agent receives a short rule and an index. The rule says to save only a fact another team member would need, never your preferences or one person's own work, never secrets or long text, to search before saving, and to tell you about every save, update and retract in one line. The index lists up to 150 active memories, one line each, newest and most used first. Claude Code can cut a server's instructions at 2,048 characters, so on a team with many memories the end of the index may not show. The agent can always use `search`.

It has six tools:

| Tool | What it does |
| --- | --- |
| `search` | Find memories by words, optionally by type or by team or project |
| `get` | Read one memory in full, with the names of the memories it links to |
| `save` | Save a new memory under your name. If close matches exist, nothing is saved and they are returned. The agent updates one, or saves again with `new: true` |
| `update` | Change a memory. Every earlier version is kept. `confirm: true` says it is still true |
| `retract` | Hide a wrong memory, with a reason. The history is kept |
| `recent` | What was saved or changed in the last days |

Every memory comes back marked as data with its author, team, confirmed date and number (and, when a colleague changed it last, who, as `edited_by`, which the Memory tab shows as **Last edited by**), and the agent is told never to follow instructions found inside one. A memory is short: a type (`feedback`, `project` or `reference`, there is no `user` type), a name of at most 64 characters, a one-line description of at most 150, and a body of at most 4 KB. A longer text is refused with a hint to put it in the team docs and link to it. You can save or update 200 times a day in all your teams.

A memory not confirmed for six months shows "unconfirmed since" in the index and in results. It is marked, never hidden, because some facts stay true for years.

Anything that looks like a secret is refused: AWS keys, tokens of GitHub, Atlassian, Slack and loadout, JWTs, private keys, `password=` assignments, a password in a web address (`postgres://user:pass@host`), quoted `"password"`, `"secret"` and `"api_key"` values, `api_key` or `token` assignments with a long value, and long random strings. Names and descriptions also refuse invisible formatting characters, and a body refuses control characters such as escape sequences. The refusal names the rule and the place, and the text is stored nowhere. The same check runs on your machine before `memory import` sends anything. There is no way to switch it off. A harmless text that is refused is a bug to report, not something to work around.

### Look it over every week

The team's **Memory** tab shows **New this week**: everything saved or changed in the last 7 days, with its author. Search it with the box, and filter by type or by team or project. Open a memory to read it, its links and its history, where every version shows who changed it and what. Nothing is edited on the web. Agents save and update, and people look and take back what is wrong.

- **Retract…** hides a memory from search, the index and `recent`. Its author, the team's maintainers and owners can do it, with a reason. A retracted memory stays visible on its own page with the reason.
- **Move to project…** makes a team's memory part of the project's, so every team in the project reads it. It keeps its author, history and number. Maintainers and owners of the team can do it without any role in the project, but only while the project has memory switched on. It cannot be moved back.

### Bring your own memory files

`loadout memory import` offers the memory files your AI tool keeps on your machine to a team:

```sh
loadout memory import ~/.claude/projects/me/memory --team lime
loadout memory import ./notes --team lime --select 1,3
```

It reads the `.md` files in the folder (not its sub-folders, and not `MEMORY.md`) that have `name`, `description` and `type` in their header. Claude Code's own memory folder works as it is: it keeps the type under `metadata:` (`metadata:` then an indented `type: feedback`), and loadout reads that too. A `type` at the top of the header wins when both are there. A file of type `user` is never offered. Entries of type `reference` and `project` are pre-selected and `feedback` is not. Nothing that speaks of one person ("I", "my", "the user prefers", an email address) is pre-selected either. Each line says why. Files with a secret show as refused and are never sent. In a terminal you toggle numbers and press Enter to save. With `--select` the numbers you list are saved without asking. Without a terminal and without `--select` the list is printed and nothing is saved. Each entry goes through the same checks as a `save`. A likely duplicate is shown and skipped unless you add `--new`. It needs a memory token and the team's memory switched on.

### Remove a memory for good (admins)

Retract keeps the history, and every team member can read it. If a secret slipped through, rotate the secret first. That is the real fix. Then an admin on the registry host removes the memory and all its versions:

```sh
loadout memory purge --id 42 --data /var/lib/loadout           # shows it and stops
loadout memory purge --id 42 --data /var/lib/loadout --yes     # removes it
```

If a purge says the database was busy, the memory is already gone: run the same command again to finish the cleanup. A running registry keeps the purged name in its index for up to 30 seconds. The purge is logged without the text. It then lists the nightly backups in `--backups DIR` (or `LOADOUT_BACKUP_DIR`) that still hold the memory. Copies made off the machine cannot be listed. Team memory lives in its own `memory.db` next to `registry.db`, and the nightly backup covers both.

## Everyday commands

### See what you have

```sh
loadout list          # packages you can install, with KIND and LAYER (L1 team, L0 project)
loadout list --kind skill --sort updated   # only skills, newest first
loadout list --layer payments   # only your team's own packages, not the project's
loadout layers        # for each of your teams, the layers it reads, closest first
loadout status        # what is installed: ok, modified, missing or shadowed
loadout status --files   # every file of each skill folder
loadout notices       # updates your edits hold back, one line each (nothing if none)
```

Below its table, `status` says what the last `sync` found, without asking a registry, and only what is still true:

```text
As of the last sync (4 Oct 09:00):
  payments/planner   1.3.0 held back: you edited planner.md (claude)
  payments/runbooks  2.1.0 available: loadout install payments/runbooks
```

Revert your edit and the line turns into "available"; take the update or uninstall the package and it goes away. Run `loadout sync` for news from the registry.

### Updates and policies

Each installed package has a policy:

| Policy | What `sync` does |
| --- | --- |
| `notify` | tells you a new version is out |
| `auto` (default) | installs the new version |
| `pin` | nothing; you stay on this version |

A new install starts with `auto`, so what your team publishes reaches you on your next `loadout sync`. A file you edited is never overwritten: the update is held back and `sync` tells you (see below). Installs you made earlier keep the policy they have. Pick another with `loadout install TEAM/NAME --policy notify` or `loadout policy TEAM/NAME notify`.

```sh
loadout policy payments/planner auto
loadout install payments/runbooks --policy auto
```

A file you edited is never overwritten by `sync`. When your project changes which model its agents run, `sync` treats it like a new version of the agents it changes: `auto` applies it, `notify` reports "model settings changed", `pin` leaves the file alone.

### When a package is removed from the registry

If the person who runs the registry deletes a package, `sync` shows **removed from the registry** for it, prints the command to remove it from your machine (`loadout uninstall TEAM/NAME --scope user`), and still exits 0: nothing failed. `sync` never removes your files itself, because you may have edited them. The package stays in `status` until you uninstall it. Older versions of loadout show a failed check ("404") for it on every `sync` until you update loadout or uninstall the package.

### When your edits hold an update back

If you edit an installed agent, skill or knowledge base, loadout keeps your version: an update never overwrites your edit. `sync` says so and gives you the choices:

```text
$ loadout sync
PACKAGE           SCOPE  INSTALLED  LATEST  RESULT
payments/planner  user   1.2.0      1.3.0   held back by your edits

payments/planner 1.3.0 is held back.
Edited since loadout wrote it:
  ~/.claude/agents/planner.md
Keep your edits and take 1.3.0:   loadout install payments/planner --merge
Replace your edits with 1.3.0:    loadout install payments/planner --force
Keep your version, stop updates:  loadout policy payments/planner pin
See what changed:                 loadout diff payments/planner
```

- **Keep your edits and take the release.** `loadout install payments/planner --merge` (see below).
- **Take the release as it is.** `loadout install payments/planner --force` replaces your edits with 1.3.0. Copy anything you want to keep first.
- **Keep yours.** `loadout policy payments/planner pin` stops updates for it; `sync` stops reporting it.
- **Look first.** `loadout diff payments/planner` shows what the release changed.

With `auto` the result reads "held back by your edits" and `sync` exits with code 2 until you choose. With `notify` it reads "update available, you edited 1 file" and exits 0: the update was never going to install by itself, but installing it would stop at your edit. A project install adds `--scope project`, and you run the commands in that repository.

#### Keep your edits with `--merge`

```text
$ loadout install payments/planner --merge
updated payments/planner 1.2.0 to 1.3.0 (user scope), keeping your edits:
  merged           ~/.claude/agents/planner.md
  took the release 2 files you had not edited
```

loadout works out what it wrote for your version, compares it with your file and with 1.3.0, and puts both changes into your file, for each AI tool. A file you did not edit simply takes 1.3.0; a file the release did not change keeps your edit; a file you deleted comes back. Afterwards the package is at 1.3.0 and your merged files still count as your edits, so the next release is held back again and you merge again.

When you and the release changed the same lines (or lines right next to each other), the file holds both versions between markers, as `loadout rebase` writes them:

```text
<<<<<<< yours (edited 1.2.0)
Always cite the ticket.
||||||| installed 1.2.0
Cite the ticket when there is one.
=======
Cite the ticket and its priority.
>>>>>>> release 1.3.0
```

Keep what should stay and delete the marker lines. Until you do, `--merge` refuses to merge the next release into the same files, and your AI tool reads the markers too; `loadout status`, `loadout notices` and the message at the start of a session say which files are left. A file too long to merge line by line (over 5,000 lines), or one whose earlier version loadout cannot rebuild exactly (your team changed its model settings since), holds both versions whole in one conflict. `--merge` needs a package installed from a registry; it refuses files loadout did not write, like any install, and cannot be combined with `--force`. loadout's own skills have no merge: `loadout setup --skills --force` replaces an edited one.

loadout remembers what each sync found in `~/.loadout/syncstate.yaml`. With Pushover notifications on (set `LOADOUT_PUSHOVER_TOKEN`, your Pushover application token, and `LOADOUT_PUSHOVER_USER`, your user key, where `loadout sync` runs), each new version, held back or available, is pushed once, not on every sync. A push about a held-back update says how many files you edited, never their names. `sync` also pushes once when a newer loadout is out ("loadout 0.5.0 is out", with the command to update), or when a registry needs a newer loadout than yours; a push that fails is tried again on the next sync. `LOADOUT_UPDATE_NOTICE=off` leaves out the first of those.

### Look at changes, go back

```sh
loadout diff payments/planner                 # installed version vs latest
loadout diff payments/planner 1.2.0 1.3.0     # any two versions
loadout rollback payments/planner             # back to the previous release, then pinned
loadout rollback payments/planner 1.1.0       # back to a given version
loadout policy payments/planner auto          # take updates again
```

### Remove a package

```sh
loadout uninstall payments/planner
```

Files you edited are kept; add `--force` to remove them too.

## Write packages

### Create one

```sh
loadout new agent payments/planner          # creates ./planner
loadout new skill payments/release-checklist
loadout new kb payments/runbooks
```

`--dir` picks another folder. Every new package starts at version `0.1.0` and is valid right away.

`loadout.yaml` looks like this:

```yaml
name: payments/planner
kind: agent
version: 0.1.0
description: Turns a ticket into an implementation plan.
```

Add `targets: [claude]` if a package only makes sense in one tool.

### Agents

An agent is written once, in a tool-neutral format, and loadout translates it for each tool:

```markdown
---
name: planner
description: Turn a ticket into an implementation plan. Use when the user says "plan this".
model: deep
tools: [read, search, shell]
---
You are the team's planner. Read the ticket, find the code it touches,
and return an ordered plan with the files to change and how to test it.
```

The `name` must match the package name. The `description` is what the tool reads to decide when to use the agent, so say when to use it.

**Tools.** List what the agent needs. loadout maps each name to the tool's own:

| loadout | Claude Code | Copilot |
| --- | --- | --- |
| `read` | Read | read |
| `search` | Grep, Glob | search |
| `edit` | Edit, Write | edit |
| `shell` | Bash | execute |
| `web` | WebFetch, WebSearch | web |
| `agent` | Agent | agent |
| `skill` | Skill | nothing to grant: Copilot loads skills on its own |

Leave the `tools` line out to allow every tool. An empty list, `tools: []`, is refused, because both tools would read it as "every tool" too.

**Model.** A hint, never a vendor model name: `fast`, `balanced` or `deep`. Leave it out to use whatever the person has chosen in their tool. What each hint runs is up to the project, per tool (see [Models for agents](#models-for-agents)); without any settings it is:

| Hint | Claude Code | Copilot |
| --- | --- | --- |
| `fast` | haiku | the person's own choice |
| `balanced` | sonnet | the person's own choice |
| `deep` | opus | the person's own choice |

### Skills

A skill is `SKILL.md` with steps, plus any files it needs next to it:

```markdown
---
name: release-checklist
description: Walk through the release checklist. Use when the user asks to cut a release.
---
# Release checklist

1. Check the changelog. The format is in [references/changelog.md](references/changelog.md).
2. Run `scripts/check-version.sh`.
```

Keep `SKILL.md` short and put long material in `references/`: the tool reads it only when a step needs it. The description may be up to 1,024 characters. Other frontmatter fields you use (such as `allowed-tools` or `metadata`) are kept as they are.

### Knowledge bases

A knowledge base is a folder of documents. loadout writes its `SKILL.md` for you, so don't add one.

```
runbooks/
  loadout.yaml
  README.md
  deploy/rollback.md
  deploy/hotfix.md
  oncall/escalation.md
  hosts.tsv
```

- Documents are text: `.md`, `.markdown`, `.txt`, `.rst`, `.adoc`, `.csv`, `.tsv`, `.json`, `.yaml`, `.yml`, `.mmd`, `.mermaid`, `.drawio`.
- The first `# Heading` of a Markdown file is its title in the index; other files use their file name.
- The description in `loadout.yaml` is required: it is what the tool reads to decide when to look in the knowledge base.
- Diagrams: Mermaid (a `.mmd` file or a mermaid block in Markdown), or draw.io saved uncompressed (a `.drawio` file or a drawio block in Markdown holding the XML; in draw.io turn off File > Properties > Compressed, then save).
- PDFs and images: turn them into text and diagrams first, for example by asking your own AI tool to convert them, and add the result.
- With more than 150 documents the index lists folders instead of every document, so keep related documents in folders with clear names.

#### A knowledge base as a course

A team's wiki can show a knowledge base as a **course**: a box of lessons at the end of the page (an owner chooses it, see [The website](#the-website)). Any knowledge base works; its Markdown files at the top level are the lessons, in name order, and documents in folders are reference material the lessons link to. Number the files with a leading zero, so `10-` comes after `09-`:

```
lime-course/
  loadout.yaml
  01-setup.md
  02-propose.md
  03-review.md
  reference/commands.md
```

A lesson's title is its first `# Heading`. A lesson may ask questions in a `quiz` block, a task list whose ticked boxes are the right answers:

````markdown
```quiz
Your proposal is behind the latest release. What do you run first?

- [ ] loadout publish --base latest
  That skips the merge, and a teammate may lose the release's changes.
- [x] loadout rebase
  It merges your folder onto the latest release on your machine; then you propose again.
```
````

- Everything before the first answer is the question, in Markdown.
- An answer starts at the beginning of a line with `- [ ]` (wrong) or `- [x]` (right). Several answers may be right.
- The lines below an answer, indented by two spaces, explain it. loadout writes **Right.** or **Wrong.** in front, so explain why rather than repeat the verdict.
- 2 to 8 answers per question, at least one right.

On the page each answer folds out to show its verdict and explanation. A quiz block renders in every Markdown document the registry shows, so open a lesson's preview on the package page to check it before you choose the knowledge base as a course; a block loadout cannot read stays as text, with a note saying which line to fix. In the AI tool the knowledge base installs like any other, so an agent there reads the same lessons, quizzes included.

### Try it before you publish

Install straight from the folder:

```sh
loadout install ./planner
```

Start a new session and try it. When you are happy, publish.

### Publish

```sh
loadout publish planner                  # publish the version in loadout.yaml
loadout publish planner --bump patch     # 1.4.0 -> 1.4.1
loadout publish planner --bump minor     # 1.4.0 -> 1.5.0
loadout publish planner --bump major     # 1.4.0 -> 2.0.0
```

`--bump` counts from the higher of the local version and the latest published one, and writes the new version into `loadout.yaml`. Publishing needs the maintainer role (or an owner or admin) and is refused when your team requires review.

Before it uploads, `publish` works out which release your folder was made from: the release at the version in `loadout.yaml`, or the one your last `loadout rebase` merged onto. If someone released a newer version since, publishing would drop their changes, so it stops:

```text
error: planner is based on 1.4.0, and 1.4.1 by dana@example.com was released since; its changes are not in your folder. Bring them in with: loadout rebase planner. If your folder already has them, publish with --base 1.4.1
```

Run `loadout rebase planner` (see [Bring a folder up to date](#bring-a-folder-up-to-date)), or, when your folder already holds those changes, say so with `--base 1.4.1` (or `--base latest`).

### Propose instead

When review is required, or you simply want a second pair of eyes:

```sh
loadout propose planner --bump minor \
  --note "Cites the ticket in every step" \
  --prompt "Plan PROJ-1234" \
  --prompt "Plan a database migration"
```

Give 1 to 20 prompts you tested with. For many prompts, put them in a file separated by lines that contain only `---`, and pass `--prompts-file prompts.md`.

`propose` makes the same check as `publish`: a folder made from an older release than the latest is refused before anything is uploaded, with the same two ways out (`loadout rebase`, or `--base`). The proposal records which release it was made from, so reviewers see what it changed.

Proposing from a folder whose open proposal you made (the folder still has its version, or you rebased it) **replaces** that proposal in one step: the old one closes, links to the new one, and its prompts come along with the names of whoever added them. `--prompt` is then optional; prompts you give are added to the ones that came along. Its reports stay with the old proposal, since they compared other content.

### Bring a folder up to date

When a newer release came out after your folder or proposal was made, `loadout rebase` merges your changes onto it, on your machine:

```sh
loadout rebase planner                       # the folder in ./planner
loadout rebase --proposal 3f2a9c01b7de       # start from a proposal's files, in ./planner
```

Commit the folder first: rebase rewrites its files. It fetches the release your folder was made from, the latest release and your folder, and merges them file by file and line by line. Changes to different lines both apply, and the same change made on both sides applies once:

```text
$ loadout rebase planner
planner: proposal 3f2a9c01b7de, made from 2.0.0, onto 3.0.0
  merged      SKILL.md
  from 3.0.0  references/api.md
  yours       references/plan.md
No conflicts; propose it with: loadout propose planner
```

Changes to the same lines (or lines right next to each other) are a **conflict**. The file then holds all three versions between markers, for you to resolve in your editor or AI tool:

```text
<<<<<<< yours (proposal 3f2a9c01b7de)
Always cite the ticket.
||||||| base 2.0.0
=======
Answer in English.
>>>>>>> release 3.0.0
```

Keep what should stay and delete the marker lines. `propose` and `publish` refuse the folder while any marker is left. A file deleted on one side and changed on the other has no markers: rebase keeps the changed one and asks you to decide. Delete it, or keep it with `loadout rebase planner --resolved references/old.md`.

Then propose again: `loadout propose planner` replaces your open proposal. A folder that was not a proposal (you were about to publish) can be published instead. If your folder's version is not above the latest release any more, rebase raises it to the next patch and says so.

rebase works out what the folder was made from in this order: `--base`, your last rebase of the folder, the open proposal at the folder's version, the release at that version, and otherwise the newest release below it (it says when it guessed). Give `--base 2.0.0` when it guesses wrong. It never calls an AI model; resolving conflicts is up to you and your own tools.

### Review a proposal

```sh
loadout review list                          # open proposals
loadout review show 3f2a9c01b7de             # note, prompts, diff
loadout review payments/planner                  # run both versions in your own AI tool
loadout review approve 3f2a9c01b7de          # it becomes a release
loadout review reject 3f2a9c01b7de --note "Loses the test step"
loadout review withdraw 3f2a9c01b7de         # the author takes it back
```

If you own the project, you may release your own proposal without a second person's review. Say why; teammates see the note, and the proposal, `review show`, `review list --all` and the package page mark it as released without review:

```sh
loadout review approve 3f2a9c01b7de --as-owner --note "Hotfix, nobody else is around today"
```

When a newer release came out after a proposal was made, the proposal is **behind**. `review list` says "open, behind 3.0.0" (or "blocked by 3.1.0" once its own version, or a higher one, is released), and the website shows a **Behind 3.0.0** pill and a notice naming each newer release and who published it ("3.0.0 by Dana, 2 days ago"), and the files it would lose. `review show` says what the proposal was made from and diffs from there, so you read what the author changed:

```text
Made from 2.0.0, proposes 3.1.0. The latest release is 3.0.0 by dana@example.com, released after it: approving releases 3.1.0 without 3.0.0's changes.
```

The diff page has three views: **This proposal** (what the author changed), **Released since** (what the proposal does not have) and **After approval** (what everyone gets on their next sync).

A plain approval of a behind proposal is refused. Usually the author brings it up to date with `loadout rebase --proposal 3f2a9c01b7de` and proposes again. When the newer release is one the proposal deliberately replaces (a hotfix it redoes better, say), approve it over that release, which is recorded on the proposal:

```sh
loadout review approve 3f2a9c01b7de --over 3.0.0 --note "Redoes the 3.0.0 hotfix"
```

`--over` must name the latest release exactly; if another release came out while you were reading, the approval is refused and names it. On the website the button reads **Approve over 3.0.0…** and asks first. A proposal whose version was already released cannot be approved at all; the author brings it up to date with `loadout rebase --proposal ID`, which raises its version, and proposes again. A proposal replaced after a rebase reads "Replaced" and links to the new one.

On a proposal's page on the website, anyone on the team can add the prompts they care about under **Prompts**; the next `loadout review` asks them too. You can edit or remove only the prompts you added, and a proposal keeps at least one. The page's **Try it yourself** box has the command to copy, and each report says where its two sides came from (installed, release or proposal), whether the tool ran with full access or in safe mode, and which team packages sat beside it.

On the website the same is **Approve as owner…** on your own proposal. Publishing directly stays refused when review is required, for owners too: propose, then approve as owner.

### Ask to delete a package

Anyone who reads a package may ask to delete it, from the package page (**Ask to delete…**) or with the CLI. A reason is required: the registry keeps it with your name.

```sh
loadout propose delete lime/lime-app-dev --reason "Replaced by lime/planner; nobody installs it any more"
```

Nothing is deleted yet. The request appears with the proposals under **Reviews** (and in `loadout review list`, as a `delete` row with an id that starts with `del-`). A package has one open request at a time, and you can withdraw yours (`loadout review withdraw del-3f9a1c2b7d4e`, or **Withdraw…** on its page) until someone decides.

A maintainer or owner of the package's team (or project) who is not you decides, on the request's page or with the CLI:

```sh
loadout review show del-3f9a1c2b7d4e                       # the reason, who asked, what would go
loadout review approve del-3f9a1c2b7d4e                    # deletes the package
loadout review reject del-3f9a1c2b7d4e --note "The demo still installs it"
```

The page lists what approving removes (releases, proposals with the open ones linked, reviews, prompts, any wiki course) and says who may have the package installed: loadout does not record installs, so it names the namespace, and for a project's package its teams. Before it deletes, loadout saves a copy of the database in the backup folder, and if the copy fails nothing is deleted. (A registry without a backup folder shows no request pages: the CLI can still ask, and approving is refused with a sentence that names the folder.) If a release came out after the request was made, it cannot be approved: it was asked about something else, so withdraw it and ask again. A request still open when the package is deleted another way (an admin deleting it at once, or the person who runs the registry) closes as **deleted another way**.

If you own the project, you may approve your own request with a note, as with a proposal. It is deleted at once, and the request, the pages and `review list --all` say **Deleted without review**:

```sh
loadout review approve del-3f9a1c2b7d4e --as-owner --note "The team moved to acme/python-standards"
```

After a decision the request stays as the record: who asked and why, who decided and when, and what went (releases, proposals, reviews and prompts). The CLI refuses `review compare` and `review report` for it, since there is nothing to compare. A registry or CLI older than requests to delete a package cannot ask for or approve one: update loadout first.

`loadout review payments/planner` compares the version you have installed (else the latest release) with the newest open proposal (else the latest release), on the proposal's prompts. Pick other versions with `--from` and `--to`: a version such as `1.2.0`, `latest`, or a proposal id. Each version runs in its own throwaway folder next to the team's other packages (the latest release of each, as `install --all` gets them), so an agent can use the team's skills and knowledge bases; `--no-context` leaves them out.

```text
$ loadout review payments/planner
reviewing payments/planner
  to:     proposal 3f2a9c01b7de (0.2.0) by alice@example.com
  from:   installed 0.1.0
  beside: 1 team package(s): payments/runbooks 0.1.0
the report will be attached to proposal 3f2a9c01b7de, where everyone who reads payments on office can see it; add --no-upload to keep it on this machine
running 1 prompt(s) x 1 run(s) x 2 version(s) = 2 claude run(s) on your own subscription, full access
warning: running proposal 3f2a9c01b7de by alice@example.com with full access to this machine (files, shell, web); add --safe to refuse anything that needs approval.
```

Your tool runs a teammate's package with the access it normally has: it can read and change files, run commands and reach the web, and its answers may quote your files. Read the proposal first with `review show`, or add `--safe` to refuse anything that would need your approval. Copilot CLI refuses every tool call in safe mode, so it answers from the prompt alone; loadout says so before the run and in the report.

The report is written to `review-ID.md` (`--out` picks another file) and, when the version under review is an open proposal, attached to it so teammates see the answers side by side on the website; `--no-upload` keeps it on this machine. Claude Code runs by default; `--tool copilot` uses the Copilot CLI. `--runs 3` runs each prompt three times, `--prompt` runs your own prompts instead (needed when both versions are releases), `--model` asks for a model, `--timeout` limits each run.

`loadout review compare ID` still works: it compares the latest release with that proposal and attaches the report only with `--upload`. It takes the same `--safe` and `--no-context`.

## Bring your existing agents and skills

Already have agents or skills in `~/.claude` or `.github`? `loadout import` turns them into package folders. It does not publish; you check the result and publish as usual.

```sh
loadout import agent ~/.claude/agents/planner.md --team payments
loadout import agent .github/agents/*.agent.md --team payments --out packages/
loadout import skill ~/.claude/skills/release-checklist --team payments
loadout import kb ~/notes/runbooks --team payments --name runbooks \
  --description "How we deploy, roll back and escalate."
loadout publish planner
```

- **Agents:** Claude Code tool names (Read, Grep, Bash, ...) and Copilot ones (read, search, execute, ...) become loadout's tools, and a model of haiku, sonnet or opus becomes fast, balanced or deep. If an agent uses a tool loadout has no name for, `--drop-unknown-tools` leaves it out. `--version` sets the first version.
- **Skills:** the folder is copied as it is and a `loadout.yaml` is written next to `SKILL.md`.
- **Knowledge bases:** the documents are copied; `--description` is required, `--name` sets the package name.
- **Names:** a package takes the name its file gives; `--name` renames one input. Two inputs that would get the same name (an agent and a skill both called `setup-doctor`) are refused, so import one of them with `--name`.
- **Headers:** a description with an unquoted `: ` in it is not valid YAML; import quotes it in the copy and says so. Your original file is never changed.
- `--out` chooses where the package folders go.

**Tip:** a skill must not point at files outside its own folder, such as `~/docs/runbook.md` or `../shared/style.md`. Those paths do not exist on a teammate's machine. Copy the documents into the skill's `references/` folder and link them relatively: `[runbook](references/runbook.md)`.

## For team leads and admins

### The website

Open the registry address in a browser and sign in. The sidebar lists your projects with their teams below them; with one project, its teams are open already. The **Docs** link at the bottom opens this guide, and **What's new** below it lists what each update added and fixed; the title of either page takes you back to the page you came from. On a phone both are icons at the top, next to your name.

- **loadout skills:** the skills that come with loadout (see [loadout's own skills](#loadouts-own-skills)), readable by anyone, signed in or not.
- **Packages:** everything the team reads, from its own layer and the project above, with the command that installs it. Tabs filter by kind (Agents, Skills, KBs) and by layer (All layers, the team, the project), with a count on each; **Sort by** orders the list by layer, name, kind, last update or number of releases. The search box looks at names, descriptions and kinds. A package page shows its releases and files.
- **A package page:** **Download** gives the version you are looking at as a zip (`lime-planner-1.4.0.zip`, a folder with every file, `loadout.yaml` included), and each row of the releases table has its own download. Click a version to look at an older release; the page says which is the newest. Click a file (or **Preview** on the main file) to open it on a page of its own: Markdown is rendered, Mermaid and draw.io diagrams are drawn (a draw.io file gets a tab per page), anything else shows as text. **Show source** shows a Markdown file as written, **Download** saves one file, and **Previous** and **Next** walk through the package's files. Images are not shown: an image in Markdown appears as its alt text, and an `.svg` file shows as its text. Diagrams need JavaScript; without it you see their source. Links inside a draw.io diagram do not open on the page; download the file and open it in draw.io to follow them.
- **Reviews:** open proposals, the answers of the current and candidate versions side by side (long answers show their first 150 words and a word count; **Show whole answers** opens both), the diff, and Approve, Reject or Withdraw. On your own proposal, an owner also gets **Approve as owner…**, which asks why.
- **Settings:** the team's people and how it works. Under **Members**, add people by email, change roles and remove people. The list shows owners first, then maintainers, then members, each by name, and under a name a muted line, "Teams: a, b", names the person's other teams in the same project (only teams you may open). The search box in the panel's header finds people by name, email or team name and also filters **Through the project**; it filters as you type, and without JavaScript it reloads the page with the matching rows. Changing a role or removing someone keeps your search; adding someone clears it. Below that: for owners, turn review and the wiki on or off and choose the wiki's **Course**. Make and revoke machine tokens (a new token is shown once). Maintainers and owners also get the **Team memory** switch (see [Team memory](#team-memory)). On a team, **Through the project** lists the project's owners, who are owners of the team without being added to it. You change them in the project's Settings. Maintainers and owners get **Choose models**, which opens the model settings (see below). A **Settings** link at its top leads back. The tab was called Members before. Old links to it still work.
- **Wiki:** a tab that appears once an owner ticks **Show a wiki** in Settings (each project and team has its own switch, off at first). It holds one page that the team's members write and everyone in the team reads; members of a team also read the project's wiki, and a team's wiki links to it. The page shows its revision, who saved it and when; **Show source** shows it as written, **History** lists every save with its note and a **Changes** link, and the download button saves it as `wiki.md`. **Edit** opens the text with **Write** and **Preview** buttons that switch between the text and how it will look, without losing anything, then **Save**, with an optional line saying what changed. Mermaid and draw.io blocks become diagrams once you save; the preview shows their source. Each section that starts with a `##` heading folds: the page opens with its title, the text above the first `##` and a list of its sections, and you open the ones you need (**Open all** opens every one). The preview shows them open. A link to a heading, such as `#how-to-deploy`, opens the section it is in; headings get their link names from their text, in lower case with hyphens for spaces. In Chrome and Edge, find in page searches closed sections too and opens the one it finds. To go back to an older version, open it from History and choose **Edit from this revision**, then save it as the newest. If someone saved while you were editing, the page does not save: it shows your text again with what they changed above it. Merge their change into your text, then save again. Turning the wiki off hides the tab and keeps the history.
- **Course:** at the end of the wiki page, below its sections, a box lists the lessons of the knowledge base an owner chose in Settings (**Course**, next to the wiki switch). It may be the team's own knowledge base or one of a project above it. A team without a course of its own shows its project's, labelled "From acme"; choosing one for the team replaces it. The box always shows the package's newest release. Open a lesson to read it in the box, with **Previous** and **Next** and the list folded above it; each lesson has its own address, so you can send a new teammate "lesson 3". If the chosen package cannot be shown (it is gone, it is no longer a knowledge base, or it has no top-level Markdown file), owners see a line saying so with a link to Settings, and everyone else sees no box.
- **Invite someone:** a fold-out at the top of a project's Settings (click it to open). Owners and admins (and the project's maintainers, for the project alone) add a person to the project and any of its teams in one step: their email, a role in the project, and a tick with a role for each team. Either every role is given or none is, and a role they already have is raised, never lowered. The page then shows what changed where and a sign-in link to pass on; when the registry sends mail, loadout also mails them the invitation, naming you. The link holds no secret: it opens sign-in with their address filled in and brings them to the project afterwards.
- **New:** admins create projects; owners create teams in their project. The name starts every package id and cannot be changed later. `loadout` and `skills` are taken by loadout itself.
- **Account:** your roles, light or dark appearance, and "Sign out everywhere". Admins also get **Server details** (see below).
- **Approving a terminal sign-in:** the page shows the code, your account and every team the token will cover, with your role in each. Approve only a code from your own terminal: whoever started that sign-in gets your access.

### The same from the CLI

After `loadout login`, these commands act on the registry you signed in to, with your role (`--remote NAME` picks another registry):

```sh
loadout member add --namespace payments --email bob@example.com                 # a member
loadout member add --namespace payments --email alice@example.com --role maintainer
loadout member set --namespace payments --email alice@example.com --role member
loadout member list --namespace payments
loadout member remove --namespace payments --email bob@example.com              # also revokes his login tokens
loadout member invite --namespace acme --email dana@example.com \
  --team payments=maintainer --team mobile=member                             # the project and two teams at once, with a sign-in link

loadout namespace create mobile --parent acme                               # owners: a new team in the project
loadout namespace set payments --require-review on                              # owners and admins
loadout namespace set payments --wiki on                                        # owners and admins: a Wiki tab
loadout namespace set payments --course payments/onboarding                     # owners and admins: the wiki's course (none clears it)
loadout namespace list                                                      # every project and team you belong to, with your role
```

Maintainers add members; owners add maintainers; admins add owners.

Anyone who can edit the wiki can also write it in their own editor:

```sh
loadout wiki pull --team payments                     # writes wiki.md (wiki.mdx for an MDX page)
loadout wiki push wiki.md --team payments --note "Add the on-call rota"
loadout wiki log --team payments                      # every revision: who, when, size, note
loadout wiki show --team payments --revision 3        # print one revision
```

`pull` remembers which revision your file is based on, and `push` saves your file as the revision after it. `pull` never replaces a file with changes that are not on the registry; pass `--force` to replace it anyway. If someone saved after your pull, `push` is refused and your file stays as it is. The message says what to do: pull their revision into another file (`loadout wiki pull theirs.md --team payments`), merge it into yours, then push with `--base` set to their revision. A file you never pulled can only start an empty page.

### Models for agents

An agent asks for a `fast`, `balanced` or `deep` model. The project decides, per AI tool, which model and which reasoning effort that means; its teams inherit every value and may override single ones. A namespace's maintainers and owners (an owner of the project is an owner of its teams too) and admins change them, on the website (Settings, **Choose models**) or with the CLI. Members don't see the page; `sync` applies the settings for them. loadout only writes the name into the installed agent file: the tool runs it on the person's own subscription.

| | Claude Code | Copilot (CLI and VS Code) |
| --- | --- | --- |
| Model value | an alias (`haiku`, `sonnet`, `opus`), a full ID (`claude-sonnet-5`) | the name the model picker shows (`Claude Sonnet 5`) |
| Written as | `model: opus` | `model: Claude Sonnet 5 (copilot)` for VS Code and `models: [claude-sonnet-5]` for Copilot CLI |
| Nothing set | haiku, sonnet, opus | nothing: the person's own choice |
| Reasoning effort | `effort:` low, medium, high, xhigh or max | `reasoning-effort:`, the same five levels |
| Context size | in the model: `opus[1m]` for 1M tokens (Sonnet 5 and Opus 4.7 and later always have it) | not in the agent file: choose it in the model picker, or `copilot --context long_context` |

Three things to know about Copilot:

- When the Copilot model picker is on Auto, Copilot uses the session's model for every agent and ignores the one set here. Pick a model in the picker for these settings to apply.
- HydraFusion, a Copilot setting in research preview, lets Copilot choose or combine models for each task. When it is chosen in the model picker, Copilot may ignore the model set here, as it does on Auto. Pick a single model in the picker for these settings to apply.
- If a model set here is not allowed on your Copilot plan, Copilot falls back to the session's model without an error. A wrong or unavailable name never breaks an agent; it just runs on your own model choice.

A personal `subagents` setting in `~/.copilot/settings.json` also wins over the file.

```sh
loadout namespace set acme --model copilot:deep="Claude Sonnet 5" --effort copilot:deep=high
loadout namespace set payments --effort claude:deep=xhigh      # payments keeps acme's models, raises one effort
loadout namespace set payments --model copilot:fast=           # an empty value inherits again
loadout namespace tiers payments                               # what applies, and where each value comes from
```

A change reaches installs on their next `sync` like a new version: `auto` installs write the agent again, `notify` installs report "model settings changed" (run `loadout install TEAM/NAME` to apply it), `pin` installs stay as they are. Skills, knowledge bases and agents without a hint are not touched, and a file someone edited is never overwritten. `loadout review` runs both versions with your team's settings; `--model` still wins for a single run.

### Machine tokens for CI

A machine token belongs to one team and never expires. It can read, or read and publish. It can propose but never approve or reject, and it counts as the person who made it, so nobody proposes with CI and approves the same change.

```sh
loadout token create --team payments --publish --label ci-release
loadout token list --team payments
loadout token revoke TOKEN_ID --team payments
```

The token is shown once. In CI, read it from a secret:

```sh
echo "$LOADOUT_TOKEN" | loadout remote add registry https://registry.example.com --token-stdin
loadout publish ./planner --remote registry --bump patch --base latest
```

`--base latest` tells `publish` that the checkout holds the latest content. A job that publishes with `--bump` and never commits the new version back otherwise looks behind from its second run on, since `loadout.yaml` still names an old release. Leave it out where the checkout may lag behind what was released.

### Run your own registry

Any always-on machine will do:

```sh
loadout serve                                        # listens on :7420
loadout token create --team payments --read --invite --url http://10.0.4.12:7420
```

Without accounts, teammates join with invites. For the website and `loadout login`, run it behind HTTPS with a public address; the repository's `deploy/` folder has a ready setup for any Debian or Ubuntu host you can ssh to (cloud VM, VPS or a home server) and for a container: copy `deploy/loadout.env.example`, fill it in, check it with `loadout serve --check --env-file FILE`, and deploy with `deploy/deploy.sh` (from a checkout, or `LOADOUT_VERSION=X.Y.Z` to install a checked release). If a proxy that is not on the registry's machine sits in front (a container platform, a tunnel, a load balancer), tell the registry which addresses it connects from and which header carries the client's address, so the sign-in limits count each person and not the proxy: `LOADOUT_TRUSTED_PROXIES=203.0.113.0/24` and `LOADOUT_CLIENT_IP_HEADER=Fly-Client-IP` (use the header your platform documents, one address per request; `X-Forwarded-For` is refused, and so is a range that covers every address). Without them loadout trusts only a proxy on the same machine. `loadout serve --check` shows what is set. Commands with `--data` run on the registry host as the operator, who alone makes the first admin: `loadout admin grant --email you@example.com`. (In simple login mode a new registry makes `admin@loadout.local` itself on its first start; see "Roles".)

**Let your own AI agent set it up.** `loadout setup --host-skills` installs two skills, and the first, `loadout-host-provision`, takes you from nothing to a running registry: ask your agent to "set up a loadout registry on Hetzner" (or AWS Lightsail, DigitalOcean, Fly.io, any VPS with ssh, or a home server). It checks that your cloud's command line tool is logged in, shows what it will create and what that costs, and asks before it spends money, changes DNS or deletes anything. It creates the machine with your public key, opens ports 22, 80 and 443, waits until your name points at the machine (Let's Encrypt limits failed attempts), installs loadout from a checked release with systemd and Caddy, turns on off-host backups and proves them with a restore into a scratch folder, then writes down what it made and how to tear it down. The second skill, `loadout-host-setup`, then chooses the login mode with you (email codes, email codes plus Google, simple login, or tokens only), has you fill in the settings file in your own editor (it checks it with `loadout serve --check`, which prints one line per part and never a secret), walks the sender-domain DNS records (SPF, DKIM, DMARC) for Amazon SES, Resend, Postmark or any SMTP provider, proves the mail with `loadout mail test` and the headers of the message that arrives, makes the first admin (`loadout admin grant`, and `loadout user password` for simple login), and creates the first project and team. There is one file per provider in the skill, with the provider's mail-port limits (DigitalOcean blocks 25, 465 and 587, so sign-in mail there needs port 2525 or 2587). The agent runs on your computer with your own logins: it never asks you to paste a secret (cloud tokens stay in the provider tool's store; you type passwords yourself), never reads your settings file, and loadout calls no cloud and runs no model for it.

The website's **Docs** and **What's new** links go to the public guide at `https://loadout.lat/docs/` unless you set `LOADOUT_DOCS_URL` in the server's environment. A registry with no route to loadout.lat can serve its own copy of the guide on its own address, and the "loadout" link at the top of those pages then leads back to the registry page you came from.

### Publish the CLI from your registry

A registry can hand out the CLI itself, so people on the team need neither Go nor a clone. Deploy from a checkout of loadout with `LOADOUT_DOWNLOADS=1` (it cannot be combined with `LOADOUT_BINARY` or `LOADOUT_VERSION`, because the CLI is built from the checkout). `deploy.sh` builds it for macOS and Linux on arm64 and amd64, and Caddy serves `https://YOUR-DOMAIN/install.sh` and the archives, `checksums.txt` and `VERSION` under `/dl/`. The registry serves only these files over the same HTTPS address as the rest, with the certificate Caddy got for it, so it must be one that people's machines already trust. A deploy without the variable takes the downloads down again. Each CLI it hands out knows where it came from, so `loadout update` and the "newer loadout" line name that registry's install script.

### Delete a package at once (admins)

Deleting a package at once is for registry admins. Owners, maintainers and members ask for the deletion instead (see Ask to delete a package under Review), and a second person approves it; an owner may approve their own with a note. A login token cannot delete a package. The page needs the registry to keep a backup folder (`LOADOUT_BACKUP_DIR`, which the `deploy/` setup sets); without one neither the requests nor this page are there, and the host command below still works.

1. Open the package and scroll to **Delete package**, then choose **Delete package…**.
2. The next page lists what goes: every release, every proposal (the open ones linked), the reviews and prompts, any wiki that shows it as its course, and any open request to delete it (it closes as deleted another way). It says who may have it installed: loadout does not record installs, so it names the namespace, and for a project's package its teams.
3. Say why, type the whole id, for example `lime/lime-app-dev`, and choose **Delete package**. A wrong name or a missing reason deletes nothing. If a release or proposal appeared since you opened the page, the page shows the new list and asks again.

Before it deletes, loadout saves a copy of the database in the backup folder as `before-delete-TEAM-NAME-<time>.db`. If the copy fails, nothing is deleted. The nightly backup job removes these copies after 14 days. You land on the packages page with what was removed, and the registry keeps a record of the delete, with your name and your reason, as a request that reads **Deleted without review** under Reviews. People who have the package installed keep their files and see "removed from the registry" on their next `sync`, as below.

It cannot be undone, except by restoring that copy, which also undoes everything changed since. The person who runs the registry restores it on the host:

```sh
sudo systemctl stop loadout
sudo cp /var/backups/loadout/before-delete-lime-lime-app-dev-20261006T101500Z.db /var/lib/loadout/registry.db
sudo rm -f /var/lib/loadout/registry.db-wal /var/lib/loadout/registry.db-shm
sudo chown loadout:loadout /var/lib/loadout/registry.db
sudo systemctl start loadout
```

### Delete a package (on the host)

The person who runs the registry can also delete a package on the host with `--data`, without the web page and without the automatic copy, because it cannot be undone except by restoring a backup. Back up the database first. Without `--yes` the command lists what would go and deletes nothing:

```sh
loadout package delete lime/lime-app-dev --data /var/lib/loadout
loadout package delete lime/lime-app-dev --data /var/lib/loadout --yes --reason "Replaced by lime/planner"
```

It removes every release, every proposal with its reviews and prompts (open proposals too, listed by id in the dry run), and clears the package as the course of any wiki. Tokens, members and wiki pages are not touched. The delete is recorded like the others, with the author "operator" and your `--reason` (default "Deleted on the registry host."). People who have it installed keep their files and see "removed from the registry" on their next `sync` (see [When a package is removed from the registry](#when-a-package-is-removed-from-the-registry)). The name is free again afterwards: publish it again only above the version that was deleted, or machines that kept the old version report "up to date" for a lower number.

### The server page (admins)

Admins open **Account**, then **Server details** (`/account/server`). Its numbers cover the whole registry, every project included, so nobody else sees the page, not even a project's owners (they get "Nothing here"). It shows:

- at the top: the host (or, on a Hetzner server, the estimated monthly cost), how full the disk is, when the last backup was made, and how long the server has run;
- the host: provider, region, size and contact as the operator wrote them in the environment file, plus what the machine says itself (host name, operating system, CPUs loadout sees) and the registry's address, with a link to the host's console when one is set (it may open only from the company network or VPN). The stated values are not measured, and without any the panel says which variables to set;
- only with a Hetzner token, the Hetzner server: name, type, location, status, backups, public IPs, volumes and this billing period's traffic, with a monthly cost estimated from Hetzner's list prices (VAT included as Hetzner states it; the Cloud API has no invoices, so the bill itself stays in the Hetzner Console);
- storage: the disk, the database and its write-ahead log, what releases, proposals and review reports take up, and the whole data folder;
- the nightly backups, the registry's counts (projects, teams, packages by kind, releases, proposals, accounts, memberships, sessions, tokens), the login mail queue and the process (version, uptime, memory).

Each part works on its own: one that cannot be read says why, and the rest still shows. The page needs these settings in the server's environment (with the `deploy/` setup, in `/etc/loadout/loadout.env`):

| Variable | Effect |
| --- | --- |
| `LOADOUT_HOST_PROVIDER`, `LOADOUT_HOST_REGION`, `LOADOUT_HOST_SIZE` | Where the registry runs and how big it is, in plain words (up to 200 characters, one line each). Unset: the row is left out |
| `LOADOUT_HOST_CONTACT` | Who looks after the host, a team or an address. Unset: no contact row |
| `LOADOUT_HOST_CONSOLE` | An `https` link to the host's console. Anything else stops `loadout serve` at startup, naming the variable |
| `LOADOUT_HETZNER_TOKEN` | A **read-only** Hetzner Cloud API token of the server's project. Unset: the Hetzner and cost panels are not shown. Needed only on a Hetzner server |
| `LOADOUT_HETZNER_SERVER` | The server's id or name. Unset: the server whose public IPv4 is this machine's |
| `LOADOUT_BACKUP_DIR` | Where the nightly backups are, and where the registry saves a copy of its database before a package is deleted on the web; the `deploy/` unit sets `/var/backups/loadout`. Unset: packages cannot be deleted or asked about on the web |

On a host that is not Hetzner, leave the `LOADOUT_HETZNER_` lines out: with a token set the page also shows that Hetzner server, and the log warns about it. Hetzner answers are kept for 5 minutes and a slow answer is given up after 5 seconds.

## Rules and limits

- **Text only.** Packages hold UTF-8 text, so every change can be diffed and reviewed. The registry's pages draw Mermaid diagrams and uncompressed draw.io files, in your browser; nothing is sent anywhere else.
- **File paths:** letters, digits, `.`, `_` and `-` only; at most 8 folders deep; no hidden files or folders; no two names that differ only in case (macOS and Windows would treat them as one file).
- **Sizes:**

  | Kind | Files | Per file | Total |
  | --- | --- | --- | --- |
  | agent | 16 | 512 KB | 1 MB |
  | skill, knowledge base | 5,000 | 2 MB | 64 MB |

  An upload is at most 32 MB compressed.
- **Wiki pages:** one page per project or team, at most 256 KB, as `.md`, `.markdown` or `.mdx`. The newest 200 revisions are kept; older ones are deleted. A save's note is one line of at most 200 characters. In an MDX page, components and `import` or `export` lines show as code; nothing in a page ever runs. Front matter at the top shows as code too. Images are not shown, as in packages. The wiki needs the sign-in of `loadout login`, so every revision names a person. Machine tokens are refused, and so are most sign-ins from before one login covered all teams; run `loadout login` again if the wiki refuses yours.
- **Courses:** the Course box lists the first 50 lessons. A quiz has 2 to 8 answers and is at most 16 KB; a document shows at most 50 quizzes, and the rest stay as text.
- **Team memory:** a name is one line of at most 64 characters, a description one line of at most 150, a body at most 4 KB, a memory links to at most 10 others, a note or reason is one line of at most 200. One person saves or updates at most 200 times in 24 hours. The index in the server's instructions holds at most 150 memories, each description cut to 100 characters. A memory token lasts 90 days, you can hold 20 at a time, and a token may make up to 120 calls at once, then two a second. A request to the memory server is at most 64 KB. Team memory is off until a maintainer or owner switches it on.
- **Your edits are safe.** loadout never overwrites a file it did not write, or one you edited since, without `--force`. `--merge` keeps your edits and adds the release's changes.
- **One namespace per token.** A token belongs to one team or project. In two teams? Sign in to each.
- **Login tokens expire after 90 days.** Run `loadout login` again. Machine tokens do not expire.
- **Admin actions** (creating projects, making owners, deleting namespaces) are done on the website by an admin, or on the registry host. They are not available with a login token from the CLI.
- **Logs:** `-v` shows debug output; every run is also logged to `~/.loadout/loadout.log`.

## Command reference

| Command | What it does |
| --- | --- |
| `loadout login [--remote URL]` | Sign in in the browser; one token for every team you belong to on that registry |
| `loadout logout [--remote R] [--team T] [--all]` | Revoke that token and forget the registry; `--team` leaves one team on this machine only; `--all` signs you out everywhere |
| `loadout team join` | Join a team with an invite read from standard input |
| `loadout team list` | The teams you joined and their registries |
| `loadout team leave T` | Leave a team |
| `loadout list [--kind K] [--layer L] [--sort S] [--wide] [--remote R]` | Packages you can install, with kind and layer; `--kind agent\|skill\|kb`, `--layer` a namespace name, `--sort layer\|name\|kind\|updated`, `--wide` for full descriptions |
| `loadout layers` | For each of your teams, the layers it reads, closest first |
| `loadout install TEAM/NAME[@V] \| FOLDER \| --all` | Install into your AI tools (`--scope`, `--target`, `--policy`, `--force`, `--adopt`) |
| `loadout cleanup [--tool copilot\|claude\|vscode] [--duplicates] [--apply]` | List, or with `--apply` move aside, old agents, skills and knowledge bases an earlier setup left |
| `loadout install TEAM/NAME[@V] --merge` | Take a new release and keep your edits, merged; conflicts are marked in the files |
| `loadout uninstall TEAM/NAME [--scope S] [--force]` | Remove a package's files |
| `loadout status [--files]` | Installed packages and whether their files are intact, the updates the last sync found, and a newer loadout |
| `loadout notices [--hook claude\|copilot]` | Updates your edits hold back and a newer loadout, offline; `--hook` is the session-start hook of an AI tool |
| `loadout sync` | Apply updates according to each package's policy |
| `loadout policy TEAM/NAME auto\|notify\|pin` | Choose how updates arrive |
| `loadout diff TEAM/NAME [FROM [TO]]` | What changed between versions |
| `loadout rollback TEAM/NAME [VERSION]` | Go back to an older version and pin it |
| `loadout new agent\|skill\|kb TEAM/NAME [--dir D]` | Create a package folder |
| `loadout import agent FILE... --team T` | Turn existing agent files into packages (`--out`, `--version`, `--drop-unknown-tools`) |
| `loadout import skill DIR... --team T [--out D]` | Turn existing skill folders into packages |
| `loadout import kb DIR --team T --description TEXT` | Turn a folder of documents into a knowledge base (`--name`, `--out`) |
| `loadout publish [FOLDER] [--bump patch\|minor\|major] [--base V\|latest]` | Publish a new version; refused when the folder is behind the latest release, unless `--base` says what it was made from |
| `loadout propose [FOLDER] --prompt TEXT...` | Upload a candidate for review (`--bump`, `--note`, `--prompts-file`, `--base`); replaces your open proposal of the folder |
| `loadout propose delete TEAM/NAME --reason TEXT` | Ask to delete a whole package; a maintainer or owner who is not you approves it (a folder named `delete` is proposed as `./delete`) |
| `loadout rebase [FOLDER] [--proposal ID] [--base V\|latest]` | Merge the folder's changes onto the latest release, conflicts marked in the files |
| `loadout rebase [FOLDER] --resolved FILE` | Keep a file a rebase left deleted on one side and changed on the other |
| `loadout review list [--all] [--package P]` | Proposals and requests to delete a package (`del-` ids) of your team |
| `loadout review show ID` | A proposal's note, prompts, reviews and diff (teammates add prompts on the proposal's web page), or a deletion request's reason and outcome |
| `loadout review TEAM/NAME [--from V] [--to V]` | Run two versions (installed and the newest proposal by default) in your own AI tool and attach the report to the proposal (`--safe`, `--no-context`, `--no-upload`, `--tool`, `--runs`, `--prompt`, `--model`, `--timeout`, `--out`) |
| `loadout review compare ID [--upload]` | The latest release against one proposal; attaches the report only with `--upload` (same run flags) |
| `loadout review report ID [N]` | Print a review attached to a proposal |
| `loadout review approve\|reject\|withdraw ID [--note T]` | Decide on a proposal or a deletion request, or take back your own; approving a `del-` request deletes the package |
| `loadout review approve ID --as-owner --note T` | Owners: release your own proposal, or delete a package on your own request, without review, marked for everyone |
| `loadout review approve ID --over V` | Approve a proposal that is behind, over the latest release V, dropping its changes; recorded |
| `loadout member add\|set\|list\|remove --namespace NS --email E [--role R]` | Manage who is in a team or project |
| `loadout member invite --namespace PROJECT --email E [--role R] [--team T=ROLE]...` | Add a person to a project and some of its teams in one step; prints what changed and a sign-in link, and mails it when the registry sends mail |
| `loadout namespace create NAME [--parent P] [--kind team\|project]` | Create a team (or, on the host, a project) |
| `loadout namespace set NAME [--require-review on\|off] [--wiki on\|off] [--memory on\|off] [--course TEAM/NAME\|none] [--model TOOL:HINT=MODEL] [--effort TOOL:HINT=LEVEL]` | Require review for a namespace; turn its wiki or its team memory on or off; choose the knowledge base its wiki shows as a course; choose which model and effort each AI tool runs per hint (an empty value inherits) |
| `loadout namespace tiers NAME` | Which model and effort each AI tool runs per hint, and where each value comes from |
| `loadout namespace list`, `namespace delete NAME` | List namespaces; delete an empty one (admins) |
| `loadout wiki show --team T [--revision N]` | Print the wiki page, or an older revision |
| `loadout wiki pull [FILE] --team T [--revision N] [--force]` | Write the wiki page to FILE (default `wiki.md`, or `wiki.mdx`) and remember its revision |
| `loadout wiki push FILE --team T [--note TEXT] [--base N]` | Save FILE as the next revision; refused if someone saved since your pull |
| `loadout wiki log --team T` | The wiki's revisions, newest first |
| `loadout token create --team T --read\|--publish [--label L] [--invite --url U]` | Make a machine token or an invite |
| `loadout token list --team T`, `token revoke ID` | List and revoke tokens |
| `loadout remote add NAME URL --token-stdin`, `remote list`, `remote remove NAME` | Manage registries by hand (for CI) |
| `loadout serve [--addr A] [--data D]` | Run a registry |
| `loadout serve --check [--env-file F]` | Check the registry's configuration and exit, without serving; prints one line per part and never a secret, and exits 1 when a variable is wrong or misspelt (on the host) |
| `loadout mail test --to ADDRESS` | Send one test mail with the registry's mail settings and say "sent", or the mail server's refusal (on the host) |
| `loadout admin grant\|revoke\|list --email E` | Registry admins (on the host) |
| `loadout user delete --email E` | Delete an account (on the host) |
| `loadout user password --email E` | Set an account's password, creating it; the password comes from standard input, never a flag (on the host) |
| `loadout user add --email E --password-file F [--admin]` | Add an account with a password read from a file or `-`, never a flag, keeping the password of one that exists (on the host) |
| `loadout package delete TEAM/NAME [--yes] [--reason T]` | List what a package holds and, with `--yes`, delete it with its releases and proposals, recorded (on the host; admins delete on the web, everyone else asks with `propose delete`) |
| `loadout setup --skills [--target T] [--force]` | Install loadout's own skills for your AI agent now (login does it for you); brings back ones you uninstalled |
| `loadout setup --host-skills [--target T] [--force]` | Install the two skills that set up a registry of your own; login and sync never add them, but keep installed ones up to date |
| `loadout setup --hooks [--target T]`, `setup --no-hooks` | Set up, or remove, the session hooks that say when your edits hold an update back or a newer loadout is out (login sets them up) |
| `loadout setup --memory`, `setup --no-memory` [`--target T`] | Allow or stop loadout adding your teams' memory server to your AI tools (install and sync do it) |
| `loadout memory token create [--label L] [--show] [--remote R]` | Make a memory token for your AI tools and keep it on this machine, `--show` prints it once |
| `loadout memory token list`, `memory token revoke ID` | List and revoke your memory tokens |
| `loadout mcp --team T [--remote R]` | The bridge an AI tool starts to reach a team's memory over standard input and output |
| `loadout memory import DIR --team T [--select N,...] [--new]` | Offer your personal memory files to a team's memory, and you confirm what is saved |
| `loadout memory purge --id N --data DIR [--yes] [--backups DIR]` | Remove a memory and its history for good, and list the backups that hold it (on the host) |
| `loadout version` | Print the version, and whether a newer one is out (from what a registry last said) |
| `loadout update [--check] [--force]` | Update the CLI the way it was installed (a registry's install script, or `brew upgrade`), or print what to do for a release download or a clone |

Every command takes `--help`.

## Troubleshooting

**"file(s) would be overwritten, use --force to overwrite"**
A file is in the way that loadout did not write, or that you edited. The message names it. Rename or move your file, or pass `--force` if you are sure it can go.

**"Form expired" on the website**
The page was open too long or came from another tab. Go back, reload the page and try again.

**"missing or invalid token"**
Your login token expired (after 90 days) or was revoked. Run `loadout login` again.

**"you are no longer a member of payments; run loadout login again"**
A maintainer removed you from that team. `list` and `install --all` skip it and go on with your other teams; `loadout login` again updates the list of teams on this machine.

**"this login token is for payments only"**
You signed in before one login covered all teams. Run `loadout login` once; it replaces the older per-team sign-ins of that registry.

**A new team does not show up in `list`**
Your token already works there; this machine just does not know the team yet. Run `loadout login` again.

**An agent or skill does not show up**
Start a new session in Claude Code or Copilot; tools read agents and skills only at startup. Check `loadout status` to see where the files went. If the tool was skipped, it may have no folder yet: install with `--target claude` or `--target copilot`.

**"no supported AI tool found"**
loadout found neither `~/.claude` nor `~/.copilot` (or `.claude` and `.github` in a repository). Start the tool once, or use `--target`.

**"No Claude Code or Copilot found here ..., so loadout's own skills were not added"**
`loadout login` found neither `~/.claude` nor `~/.copilot`. Nothing is wrong with the sign-in. Install Claude Code or Copilot and run `loadout sync`, or run `loadout setup --skills --target claude` now.

**"held back by your edits" in `loadout sync`, or exit code 2**
An update (or new model settings from your team) would overwrite a file you edited, so loadout kept your version. The sync output names the files and the choices: `loadout install TEAM/NAME --merge` to take the release and keep your edits, `loadout install TEAM/NAME --force` to take the release as it is, `loadout policy TEAM/NAME pin` to keep yours, `loadout diff TEAM/NAME` to look. See [When your edits hold an update back](#when-your-edits-hold-an-update-back).

**"update available, you edited 1 file"**
A newer version is out and the install's policy is `notify`. Installing it would stop at your edit; the choices are the same as above.

**"left ~/.claude/settings.json alone: ~/.claude/settings.json is a symbolic link"** (or "is not valid JSON")
loadout does not change a settings file it cannot change safely. Add the entry it printed under `hooks.SessionStart` yourself, in the file the link points to, or fix the JSON and run `loadout setup --hooks` again.

**"Tip: loadout setup --hooks makes Claude Code and Copilot say this when a session starts"**
You signed in before loadout had session hooks. Run `loadout setup --hooks` to get them, or `loadout setup --no-hooks` to stop the tip.

**"kept your own loadout-setup skill: a file you edited is in the way"**
A newer loadout ships new text for one of its skills, and you edited the installed one. Your edit stays. `loadout setup --skills --force` replaces it with the new text.

**"loadout 0.5.0 is out (you have 0.4.0)"**
A registry you use runs a newer release. Run `loadout update`, or the command in the line yourself (for Homebrew `brew upgrade --cask loadout`, from a registry `curl -fsSL https://REGISTRY/install.sh | sh` with your registry's address, from a clone `git pull && go install ./cmd/loadout` in your loadout folder), then `loadout version` says the new one. To keep the line out of scripts, set `LOADOUT_UPDATE_NOTICE=off`.

**"registry.example.com needs loadout 0.3.0 or newer and this is 0.2.0, so some commands may fail"**
That registry no longer supports your loadout. Update with the command in the line and run the command again. This line comes after every command until you do.

**"--scope project needs a git repository"**
Run the command inside a git repository, or use `--scope user`.

**A package shows as "shadowed"**
A closer layer (your team) ships a package with the same name, and its file wins. That is expected.

**"lime has no wiki; an owner turns it on in Settings"**
The wiki of that project or team is off. Ask an owner to tick **Show a wiki** in Settings, or to run `loadout namespace set lime --wiki on`.

**"the wiki of lime changed since revision 3: revision 4 by dana@example.com ... is the newest"**
Someone saved while you were editing. Your file is untouched. Run the `pull` the message names into another file, merge their change into yours, and push with the `--base` it names.

**"wiki.md has changes that are not on the registry"**
`pull` would replace an edit of yours. Push it first, pull into another file, or pass `--force` to throw it away.

**"lime/planner is an agent; a course is a knowledge base (kind: kb)"** or **"web/notes is not in lime or a project above it"**
A course must be a knowledge base of the team itself or of a project above it, so everyone who reads the wiki can read it. Choose another, or publish the lessons as a knowledge base in the team.

**"This quiz could not be read: line 7: ..."** on a page
The `quiz` block breaks a rule, and the page shows it as text. Fix the line it names: answers start with `- [ ]` or `- [x]` at the beginning of the line, and each explanation is indented by two spaces under its answer.

**"planner is based on 2.0.0, and 3.0.0 was released since; its changes are not in your folder"**
Someone released a newer version after your folder was made, and `propose` or `publish` would drop it. Run `loadout rebase planner`, resolve any conflicts, and try again. If your folder already has those changes (you copied them in by hand, or it is a fresh CI checkout), pass `--base 3.0.0` or `--base latest`. A CI job that publishes with `--bump` and never commits the version back looks behind from its second run on; give it `--base latest`.

**"(the newest release below its version)"**
loadout guessed what your folder was made from, because no release or open proposal has its version. If the guess is wrong, give `--base VERSION`.

**"proposal 3f2a9c01b7de is based on 2.0.0 and 3.0.0 was released since"**
You tried to approve a proposal that is behind. Ask the author to run `loadout rebase --proposal 3f2a9c01b7de` and propose again, or, if dropping 3.0.0's changes is what you want, approve with `--over 3.0.0`.

**"1 file has conflict markers" after `loadout install --merge`, or "merged: resolve the conflicts in planner.md"**
You and the release changed the same lines. Open the file named, keep what should stay and delete the `<<<<<<<`, `|||||||`, `=======` and `>>>>>>>` lines. Your AI tool reads the file as it is until then. To start over with the release instead, run `loadout install TEAM/NAME --force`.

**"lime/planner still has conflict markers from the last loadout install --merge"**
An earlier merge left markers you have not resolved yet, so loadout will not merge another release into them. Resolve them first, or discard your edits with the `install --force` the message gives.

**"1 file still has conflict markers from loadout rebase"**
A rebase left `<<<<<<<` markers in the files named. Keep what should stay, delete the marker lines, and propose or publish again.

**"deleted on one side and changed on the other when loadout rebase merged"**
One side deleted the file and the other changed it. Delete it, or keep it with the `loadout rebase ... --resolved FILE` command the message gives.

**"already has an open proposal"**
A teammate's open proposal has your folder's version. Only its author replaces it, so raise the version (`--bump patch`) to propose your own.

**"compressed draw.io file"
In draw.io turn off File > Properties > Compressed, save, and publish again.

**"No team memory here."**
Team memory is off for that team, the team does not exist, or you are not in it. The answer is the same for all three on purpose. Ask a maintainer or owner of the team to switch it on in Settings.

**"A valid memory token is needed."**
The token is missing, wrong, expired (after 90 days) or revoked, or it is a login token. Only a memory token works on the memory server. Run `loadout memory token create`.
