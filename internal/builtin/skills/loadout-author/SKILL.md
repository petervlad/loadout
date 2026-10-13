---
name: loadout-author
description: Write and share team packages with loadout, the CLI that syncs AI agents, skills and knowledge bases into Claude Code and GitHub Copilot. Use when the user wants to create, import, try out, publish or propose a new version of an agent, skill or knowledge base for their team, or asks about loadout.yaml, agent.md, SKILL.md frontmatter, model hints (fast, balanced, deep), package limits, diagrams, --bump, or a folder that is behind a newer release (loadout rebase). Also for writing the team's wiki page.
---
# Writing loadout packages

A package is an agent, a skill or a knowledge base (KB) with an id `namespace/name` (for example `payments/planner`). loadout only moves the files; every package runs in each person's own AI tool. It never runs a model and never holds an AI key.

## First, look for local rules

Look for an installed skill named `<project>-loadout` (for example `acme-loadout`) or `loadout-local`. It may say which team to publish to, naming rules and whether review is required. Prefer it for anything company specific.

## Rules for you, the agent

- Ask the person before `loadout publish`, `loadout propose` or `loadout wiki push`: all three are visible to the whole team. Show them what will go out first (the folder, the version, the note and prompts).
- Never read, print or pass a token. The person signs in with `loadout login` themselves; never open `~/.loadout/credentials.yaml`.
- Never add `--force` to overwrite a file the person edited without asking.
- A line after a command saying a newer loadout is out, or that a registry needs a newer one, is for the person: tell them once, as the `loadout-setup` skill says, and do not update loadout yourself unless they ask.
- loadout never calls a model; `loadout review` is the only command that starts an agent, on the reviewer's own machine. It runs a teammate's package with the tool's full access; say so and offer `--safe`.

## Create or bring in a package

```sh
loadout new agent payments/planner            # creates ./planner
loadout new skill payments/release-checklist
loadout new kb payments/runbooks
loadout new skill payments/notes --dir packages/notes
```

A new package starts at `0.1.0` and is valid right away. Existing files become packages with `import`, which never publishes:

```sh
loadout import agent ~/.claude/agents/planner.md --team payments
loadout import agent .github/agents/reviewer.agent.md --team payments --out packages
loadout import skill ~/.claude/skills/release-checklist --team payments
loadout import kb docs/runbooks --team payments --name runbooks --description "How we deploy, roll back and escalate."
```

Import lists everything it could not carry over. A tool loadout has no name for stops an agent's import; `--drop-unknown-tools` leaves it out. `--name` renames one input; one name per team whatever the kind.

## The three kinds

Every package folder has `loadout.yaml`:

```yaml
name: payments/planner
kind: agent            # agent, skill or kb
version: 0.1.0
description: Turns a ticket into an implementation plan.
```

`targets: [claude]` limits a package to one tool.

- **agent**: `agent.md`, one prompt in a tool-neutral format. Frontmatter: `name` (equals the package name), `description` (say when to use it), optional `model` hint, optional `tools` list. loadout writes one agent file per tool.
- **skill**: `SKILL.md` with frontmatter `name` (equals the package name) and `description` (up to 1,024 characters; say when to load it), then the steps. Put long material in `references/`, scripts in `scripts/`, and link them relatively. Other frontmatter fields (`allowed-tools`, `metadata`) are kept as written.
- **kb**: a folder of text documents, in folders if you like. loadout writes its `SKILL.md` (an index), so do not add one. The description in `loadout.yaml` is required: tools read it to decide when to look.

Details, the tools table, model hints and every limit: [references/formats.md](references/formats.md).

A skill must not point at files outside its own folder (`~/docs/x.md`, `../shared/y.md`): they do not exist on a teammate's machine. Copy them into `references/`.

## Text only

Packages hold UTF-8 text, so every change can be diffed and reviewed. Diagrams are Mermaid (a `.mmd` file or a mermaid block in Markdown) or draw.io saved uncompressed (a `.drawio` file or a drawio block in Markdown). PDFs and images: turn them into text and diagrams first (you can do that for the person, in their tool), then add the result.

## Try it before sharing

```sh
loadout install ./planner
```

Then the person starts a new session in their tool and tries it.

## Publish or propose

Ask first. Publishing needs the maintainer role (or owner or admin) and is refused when the team requires review.

```sh
loadout publish planner                  # the version in loadout.yaml
loadout publish planner --bump patch     # 1.4.0 -> 1.4.1; also minor, major
```

`--bump` counts from the higher of the local version and the latest published one and writes the new version into `loadout.yaml`. A published version never changes.

When review is required, or for a second pair of eyes, propose instead. Any member may propose, also to the project above their team:

```sh
loadout propose ./planner --bump minor \
  --note "Cites the ticket in every step" \
  --prompt "Plan PROJ-1234" \
  --prompt "Plan a database migration"
```

Give 1 to 20 prompts the author tested with, or put them in a file separated by lines with only `---` and pass `--prompts-file prompts.md`. A teammate then reviews it (see the `loadout-review` skill).

Proposing from a folder whose open proposal the person made (same version, or rebased) replaces that proposal in one step; its prompts come along, so `--prompt` is optional then.

## When a newer release came out

To get a whole package deleted, ask for it instead of deleting: `loadout propose delete lime/planner --reason "why"`. A maintainer or owner who is not you approves it, and until then nothing changes. You can take it back with `loadout review withdraw del-ID`. Ask the person before sending it, since the whole team sees it. (A package folder that is itself named `delete` is proposed as `./delete`.)

`publish` and `propose` refuse a folder made from an older release than the latest, before uploading anything: "planner is based on 2.0.0, and 3.0.0 by Dana was released since; its changes are not in your folder". Publishing it as it is would drop that release's changes for everyone. Do not reach for `--base` to get past it. Bring the changes in instead, after the person committed the folder:

```sh
loadout rebase planner                       # merge the folder's changes onto the latest release
loadout rebase --proposal 3f2a9c01b7de       # or start from an open proposal's files
```

Rebase runs on this machine and merges line by line. Changes to the same lines are left between `<<<<<<<`, `|||||||`, `=======` and `>>>>>>>` markers: show each one to the person, resolve it with them (keep what should stay, delete the marker lines) and never pick a side on your own. A file deleted on one side and changed on the other is for the person to decide: delete it, or keep it with `loadout rebase planner --resolved references/old.md`. `propose` and `publish` refuse the folder until every conflict is resolved; then propose again, which replaces the old proposal.

Only when the person says the folder already has the newer changes, pass `--base 3.0.0` (or `--base latest`, as CI jobs whose checkout always holds the latest content do).

## The team's wiki

A project or team whose owner turned the wiki on has one Markdown page that its members write and everyone in it reads. It is not a package and is never installed; every save is a revision with who saved it. Edit it in a file:

```sh
loadout wiki pull --team payments                                      # writes wiki.md
loadout wiki push wiki.md --team payments --note "Add the release checklist"
loadout wiki log --team payments                                       # who saved which revision
```

Ask before each push. If someone saved after the pull, the push is refused and the file stays as it is: pull theirs into another file (`loadout wiki pull theirs.md --team payments`), merge it into `wiki.md` with the person, then push with `--base` set to their revision, as the message says. A page is at most 256 KB; MDX components show as code and images are not shown. The wiki needs the person's `loadout login`; a machine token cannot write it.

The wiki can also end with a **Course** box: a knowledge base an owner chose shown as lessons. To write one, make a knowledge base whose top-level Markdown files are the lessons in name order (`01-setup.md`, `02-propose.md`; reference documents go in folders). A lesson may ask questions in a `quiz` fence: the question first, then answers at the start of a line as `- [ ] wrong` or `- [x] right`, each explained on the lines below it indented by two spaces (loadout writes "Right." or "Wrong." itself), 2 to 8 answers. Check it on the package's file preview page, which shows a note naming the line of a quiz it cannot read.
