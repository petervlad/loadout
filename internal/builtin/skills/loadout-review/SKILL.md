---
name: loadout-review
description: Review a teammate's proposed version of a loadout package (agent, skill, knowledge base or pipeline) by running both versions in the person's own AI tool and reading the report. Use when the user asks to review, try, compare, approve, reject or withdraw a loadout proposal, runs loadout review, asks what a review report says, asks about a proposal that is behind a newer release, or asks how to review a pipeline.
---
# Reviewing a loadout proposal

A proposal is a candidate version of a package that is not a release until someone approves it. `loadout review` is the one loadout command that starts an agent: it runs on the reviewer's machine, in the reviewer's own Claude Code or Copilot CLI, on their own subscription. The registry never runs anything and holds no AI key.

## First, look for local rules

Look for an installed skill named `<project>-loadout` (for example `acme-loadout`) or `loadout-local`. It may say who reviews what and how. Prefer it for anything company specific.

## Rules for you, the agent

- `loadout review` runs a teammate's package with the tool's **full access** by default: it can read and change files, run commands and reach the web, and its answers may quote the person's files. Before running it, say so and offer `--safe`, which refuses anything that would need approval. Suggest reading the proposal first with `loadout review show ID`.
- Ask before `loadout review approve`, `loadout review reject` or `loadout review withdraw`: each is a decision the whole team sees. Never decide for the person.
- Ask before uploading a report: `loadout review TEAM/NAME` attaches it to the proposal by default; `--no-upload` keeps it on this machine.
- Never read, print or pass a token; never open `~/.loadout/credentials.yaml`. The person signs in with `loadout login` themselves.
- Never add `--force` to overwrite a file the person edited without asking.
- A line after a command saying a newer loadout is out, or that a registry needs a newer one, is for the person: tell them once, as the `loadout-setup` skill says, and do not update loadout yourself unless they ask.
- loadout never calls a model and holds no AI key; the review runs on the person's own subscription.

## Find and read the proposal

```sh
loadout review list                      # open proposals of the person's teams
loadout review list --all --package payments/planner
loadout review show 3f2a9c01b7de         # note, prompts, earlier reviews and the diff
loadout review report 3f2a9c01b7de       # a review someone attached
```

A proposal is **behind** when a newer release came out after it was made: `review list` says "open, behind 3.0.0", the web page shows a **Behind 3.0.0** pill, and `review show` says what it was made from, who published the newer release ("3.0.0 by Dana"), and diffs from what it was made from. Approving it as it is would release it without that release's changes. Tell the person.

## A pipeline proposal

A pipeline is a package of kind `pipeline` with one file, `pipeline.yaml`. It is either a project's **template** (slots, verdicts and the edges between them) or a team's **binding** (which of the team's agents fills each slot). A conductor agent is generated from a binding and starts a whole run, so `loadout review PACKAGE` refuses a pipeline, with a sentence that says to read its changes instead. Do not look for a flag to get past it. Review a pipeline by reading:

- `loadout review show ID` for the note and the diff, and the package's page on the registry for the diagram (gate edges dashed, automatic edges solid, back edges with their round limit) and the tables of slots, edges and bound agents.
- In a template, a slot that was added, removed or renamed, or a verdict that was added or removed, is a **major** change. The registry refuses it inside a major version, so a proposal that does it must also bump the major. Teams then move on purpose with `uses: ...@2`. Changing an edge from `gate` to `auto` removes a place where a person decides: ask the author why, and tell the person.
- In a binding, check that every slot has an agent, that the agents are the ones the team means, and that `gate:` entries only add gates. A binding cannot turn a gate into an automatic edge.
- The registry already checked, when the proposal was made, that the slots are bound, that the agents exist in the team's layers and that the template major exists. Approving does not check again.
- An agent that ends with a line naming the next agent (`Next: run lime-code-review`) was warned about at propose time. Suggest ending with a `Verdict:` line instead.

## Run both versions

```sh
loadout review payments/planner              # installed version (else the latest release) against the newest open proposal
loadout review payments/planner --safe       # refuse anything that needs approval
loadout review payments/planner --no-upload  # keep the report on this machine
loadout review payments/planner --from 1.2.0 --to 3f2a9c01b7de
```

- `--from` and `--to` take a version such as `1.2.0`, `latest`, or a proposal id.
- The proposal's prompts are asked; `--prompt` asks your own instead (needed when both sides are releases). `--runs 3` runs each prompt three times.
- `--tool copilot` uses the Copilot CLI (Claude Code by default); Copilot refuses every tool call in safe mode, so it answers from the prompt alone. `--model` asks for a model, `--timeout` limits each run, `--out` picks the report file.
- Each side runs in its own throwaway folder next to the team's other packages; `--no-context` leaves them out.
- `loadout review compare ID` is the older form: latest release against one proposal, uploaded only with `--upload`.

## Read the report

The report is `review-ID.md` (or the `--out` file). For each prompt it shows both answers, time and tokens, where each side came from (installed, release or proposal), the mode (full access or safe) and the team packages beside each side. Summarise for the person: where the candidate is better, worse or different, and anything surprising it did. On the proposal's web page the answers sit side by side.

Teammates add their own prompts on the proposal's web page under **Prompts**; the next `loadout review` asks them too.

## Decide

Ask the person first, then:

```sh
loadout review approve 3f2a9c01b7de                       # it becomes a release everyone gets with sync
loadout review reject 3f2a9c01b7de --note "Loses the test step"
loadout review withdraw 3f2a9c01b7de                      # only the author takes their own back
```

- Approving and rejecting need a maintainer (or owner) of the package's namespace who is not the author. Machine tokens never decide.
- An owner of the project may release their own proposal without a second person, with a note that everyone sees; the proposal and release are marked "Released without review":

```sh
loadout review approve 3f2a9c01b7de --as-owner --note "Hotfix, nobody else is around today"
```

On the website the same is **Approve as owner…** on the proposal page.

A request to delete a package is reviewed the same way, with an id that starts with `del-` (`loadout review list` shows it as a `delete` row). Read it first, tell the person what approving does (the whole package goes for everyone, after a copy of the registry's database, and people who have it installed keep their files), and ask before deciding:

```sh
loadout review show del-3f9a1c2b7d4e                        # the reason, who asked, whether a release came out since
loadout review approve del-3f9a1c2b7d4e --note "agreed"     # deletes the package, not your own request
loadout review reject del-3f9a1c2b7d4e --note "The demo still installs it"
```

An owner may approve their own request with `--as-owner --note`, recorded as deleted without review, only when the person asks for that. There is nothing to compare, so `review compare` and `review report` refuse it. A request cannot be approved once a release came out after it was made, so the author withdraws it and asks again.

A plain approval of a behind proposal is refused. Usually the author brings it up to date with `loadout rebase --proposal 3f2a9c01b7de` and proposes again; suggest that first. Only if the person says dropping the newer release's changes is intended (the proposal redoes a hotfix, for example), and after asking them, approve over it, naming the latest release exactly:

```sh
loadout review approve 3f2a9c01b7de --over 3.0.0 --note "Redoes the 3.0.0 hotfix"
```

The proposal records it. A proposal whose version was already released ("blocked by") cannot be approved; the author brings it up to date with `loadout rebase --proposal 3f2a9c01b7de`, which raises its version, and proposes again.
