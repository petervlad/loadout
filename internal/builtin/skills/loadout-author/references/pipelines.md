# Pipelines: the format, the checks, the fixes

A pipeline is the package kind `pipeline`. Its main file is `pipeline.yaml`, next to `loadout.yaml`. It has two shapes: a **template** (a project writes it once) and a **binding** (a team fills it with its own agents). Having both shapes in one file, or neither, is refused. A template installs nothing by itself. Only a binding installs, and it installs a generated **conductor** agent.

## Words

- **Slot**: a named stage with a contract (what it reads, what it writes, which verdicts it may return). It names no agent.
- **Edge**: the move from one slot to the next on a verdict, through a **gate** (a person decides) or automatically.
- **Handoff**: control moving from one slot to the next. Only the pipeline decides it.
- **Delegation**: an agent starting a helper that does one job and returns, inside the same slot.

## Create

```sh
loadout new pipeline acme/dev-loop                           # a template, version 1.0.0
loadout new pipeline lime/dev-loop --uses acme/dev-loop@1    # a binding, every slot empty
```

`--uses` reads the template through the team's layers, so the team must be signed in and must see the template's project. Both commands create a folder and publish nothing.

## A template

```yaml
start: plan
slots:
  plan:
    brief: Turn the ticket into plan.md.
    writes: plan.md
    also: [spec.md]            # optional extra outputs, never checked for a verdict
    reads: [ticket]
    verdicts: [READY]
  dev:
    brief: Implement the plan on a branch.
    writes: dev.md
    reads: [spec.md, plan.md, ticket]
    verdicts: [DONE, SPEC CONFLICT]
edges:
  - {from: plan, on: READY,         to: dev,  via: gate}
  - {from: dev,  on: DONE,          to: end,  via: gate}
  - {from: dev,  on: SPEC CONFLICT, to: plan, via: gate, max_rounds: 2}
```

- `start` names the first slot. Slot names follow the rules of package names.
- Every slot needs a one-line `brief` (at most 500 bytes), one `writes` file, `reads` (files other slots write, or the word `ticket`, never the slot's own file) and `verdicts`.
- A verdict is upper-case words with single spaces, at most 32 bytes: `DONE`, `CHANGES REQUESTED`. The agent ends its file with the line `Verdict: CHANGES REQUESTED`. `QUESTIONS` and `end` are reserved. Every slot may return `QUESTIONS` without listing it.
- Every verdict of every slot has exactly one edge. `to` is a slot or `end`. `via` is `gate` or `auto`.
- `max_rounds` (1 to 10) is allowed only on a back edge, one whose `to` comes before or at `from` in the order the slots are written. The conductor enforces it and stops at a gate when it is reached.
- File names are one path segment from letters, digits, `.`, `_` and `-`, unique across slots.
- At most 12 slots, 8 verdicts per slot and 64 edges. The package is at most 4 files and 128 KB.
- A new template starts at `1.0.0`. A binding names a template by major, and majors begin at 1.

## A binding

```yaml
uses: acme/dev-loop@1
bind:
  plan: lime-planner
  dev: lime-dev
work_folder: explorations/
# model: deep          # the conductor's model hint, balanced when unset
# gate:                # auto edges this team wants as gates
#   - {from: dev, on: DONE}
```

- `uses` is `LAYER/NAME@MAJOR`. A major only, never `@1.2.0`. The template must be published in the team's own layer or a layer above it, and it must be a template and not a binding.
- `bind` has one agent name for every slot and no other slot names. Each agent must be published in the team's layers (the team and the projects above it). An agent in a sibling team does not count.
- `work_folder` is where runs live: `<work_folder>/<TICKET>-<slug>/`. It resolves against the folder the session starts in. `~/` is allowed. No `..`, no backtick.
- `model` is `fast`, `balanced` or `deep`. The default is `balanced`.
- `gate` turns an `auto` edge into a gate. There is no field for the other direction, on purpose.
- The conductor is named `TEAM-NAME` of the binding: `lime/dev-loop` becomes the agent `lime-dev-loop`. A binding whose conductor name equals a bound agent is refused.

## What publish and propose refuse

The registry checks a pipeline before it accepts it and lists every problem at once. Each message is a sentence that says what to change.

| Message | Fix |
| --- | --- |
| `slot test has no agent: name one under bind` | Fill the empty slot under `bind`. |
| `The agent lime-test is not published in lime, acme (the layers lime sees). Publish it there first.` | Publish the agent in the team or the project, or bind another agent. |
| `bind names slot "verify", which acme/dev-loop@1 does not have` | Fix the slot name, or the major in `uses`. |
| `gate names the edge dev on DONE, which acme/dev-loop@1 does not have` | Fix the `gate` entry. |
| `... needs acme/dev-loop@2, but acme/dev-loop has no release with major 2.` | Use a major that exists. |
| `... but acme is not one of the layers lime sees` | Point `uses` at a template in the team's layers. |
| `... is not a template, so ... cannot use it` | `uses` names a binding. Name the template. |
| `the conductor of lime/dev-loop is called lime-dev-loop, which is also one of the agents it binds` | Rename the binding or the agent. |
| `slot test was removed or renamed. That is a major change: publish 2.0.0.` | A template may not add, remove or rename a slot, or change a slot's verdicts, inside a major. Publish the next major (`--bump major`), and teams move with `uses: ...@2`. |

Changing a brief, the files a slot reads, the edges or `via` is fine inside a major. Renaming a slot's file is accepted too, but runs that already wrote the old file lose their place, so say so in the release note. Proposing is checked the same way as publishing, and approving a proposal does not check again.

## Never name the next agent in an agent

An agent that fills a slot ends its round with a `Verdict:` line and nothing more. It must not say which agent comes next (`Next: run lime-code-review`, `then run lime-test`, `hand off to lime-dev`), because the same agent can fill slots in other pipelines and only the pipeline decides what follows. `loadout publish` and `loadout propose` print a warning for such a line and still go through. A later version will refuse it. The fix is to delete the line and end with the verdict line, for example:

```markdown
When you finish a round, append `## Round N` to your file and end it with exactly one last line:
`Verdict: DONE` (or one of the other verdicts the person gave you).
```

The conductor tells each agent its files and its allowed verdicts in the prompt, so an agent needs no pipeline text of its own. An agent may still delegate a job to a helper that returns to it. On Claude Code the helper must be one the conductor may start, which loadout works out from the bound agents' `tools` lines (an agent with the `agent` tool, or with no `tools` line, allows any helper). A planner that cannot talk to the person writes its questions into its own file and ends with `Verdict: QUESTIONS`. The conductor shows them and starts the planner again with the answers, so the planner reads its earlier file for the rest.

## Updating a template

A team's conductor follows the newest release of the major in `uses`. `loadout sync` renders it again when that release changes, following the package's update policy. When a higher major exists, `sync` prints one line saying which binding should move and how.
