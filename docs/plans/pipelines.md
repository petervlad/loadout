# Pipelines

Status: planned (2026-10-11). Design settled in a grill session with the user. See `GLOSSARY.md` (Pipelines) and `docs/adr/0001-handoffs-live-in-pipelines.md`.

## Goal

A project (L0) writes a pipeline once: its slots, what each slot reads and writes, which verdicts it may return, and the edges between slots. A team (L1) binds the slots to its own agents. loadout renders the binding as a generated conductor agent that the person runs as their session's agent (`claude --agent lime-dev-loop`). The conductor starts each slot's agent as a subagent, gives it the slot contract in the prompt, reads the verdict it ends with and follows the edge, through a gate or automatically.

Today the Acme dev loop (plan, dev, review, test with gates between) is a convention only: the L0 `dev-loop` skill, agents named `<team>-planner`, `-dev`, `-code-review`, `-test` that end with `Next: run <team>-...`, and a `dev-loop.yaml` per team layer. Pipelines turn the convention into a package the registry checks and the installer renders, and take the handoff out of the agents.

The one rule holds. A pipeline is a YAML file. The conductor is an agent file loadout writes on the person's machine, and the person's own tool runs it. loadout never starts it, never runs a model and holds no AI key. The registry only stores, checks and draws the YAML.

## Decisions (settled 2026-10-11)

1. **Terms.** Pipeline, slot, edge, binding, gate, conductor, handoff, delegation, as in `GLOSSARY.md`.
2. **A new package kind `pipeline`**: an entry in `internal/pack/kinds.go` and a case in `target.Render`. A project pipeline (a template) has slots without agents. A team pipeline (a binding) says `uses: <ns>/<name>@<major>` plus `bind:` (slot to agent name). Only a binding renders, and it renders a generated conductor agent.
3. **The conductor always runs as the session's agent** (`claude --agent lime-dev-loop`). It starts each slot's agent as a subagent and relays grill questions from a planner that cannot talk to the user (the dev-loop skill's relay rule).
4. **Agents may delegate but never hand off.** Handoffs live only in the pipeline. A lint warns (v1) about an agent body that names a next agent ("Next: run lime-code-review"), and becomes an error in a later version.
5. **The conductor puts each slot's contract into the prompt** when it starts the agent: the files to read, the file to write, the allowed verdicts and the required `Verdict: <WORD>` line at the end of each round. Agents declare nothing about pipelines.
6. **Each edge is `gate` or `auto`.** The template sets each edge. A binding may turn `auto` into `gate`, never `gate` into `auto`.
7. **Round caps** are `max_rounds` on back edges, enforced by the conductor with a gate when reached. This replaces the reviewer's own two-round rule.
8. **Nothing is stored apart from the files.** The conductor works out where a run stands from which slot files exist in the run folder and their `Verdict:` lines.
9. **The template names each slot's file** (`plan.md`, `dev.md`, ...). The binding sets `work_folder`. The run folder is `<work_folder>/<TICKET>-<slug>/`. `dev-loop.yaml` keeps its other fields (`glossary`, `adr_dir`, ...).
10. **Checks.** At publish (and propose) the registry refuses a binding unless every slot is bound, each bound agent exists in the team's view (own namespace plus ancestors, as `GET /v1/layers` resolves) and the `uses:` major exists. At install a missing agent is a warning.
11. **`uses:` pins a major.** Adding, removing or renaming a slot, or changing a slot's verdicts, is a major bump of the template. `sync` tells the team when a newer major exists.
12. **The conductor's model hint** is `balanced` by default. A binding may override it.
13. **Copilot** gets a real conductor that names each slot's agent explicitly when it delegates (Copilot CLI runs a custom agent as a subagent when the prompt names it). The conductor checks that the slot's output file exists before it follows an edge. The tester proves this on a real Copilot CLI. If it does not work, Copilot gets a generated fallback skill that the main session follows by hand.
14. **v1 scope**: the kind, a `acme/dev-loop` template that expresses today's dev loop (four slots, back edges for CHANGES REQUESTED, REPLAN and FAIL, `max_rounds: 2` on dev and review. FAIL back to dev is deliberately uncapped, as today: the dev-loop skill caps only dev and review, and every hop is a gate), and one binding for Lime as the pilot. The other three teams move after the pilot works.
15. **Out of v1**: topic-dependent slots, nested pipelines, and moving the other teams. The schema leaves room for them.
16. **Package page.** A pipeline's page (`internal/web`, `package.html`) shows a Mermaid diagram generated from the YAML (slots as nodes, gate and auto edges styled apart, back edges labelled with `max_rounds`), drawn through the existing diagram frame. For a binding, a table below it: slot, bound agent linked to its package page in the team's view, and a warning for an unbound slot or a missing agent. It follows `docs/UI.md` (light and dark, 390 px, works without script).
17. **Creating a binding**: `loadout new pipeline lime/dev-loop --uses acme/dev-loop@1` fetches the template and scaffolds a binding with every slot listed and left empty, and the template's edges as YAML comments. A template's page shows that command under "Use this pipeline". No web editor in v1.
18. **Creating a template**: `loadout new pipeline acme/dev-loop` (no `--uses`) scaffolds a small example template (two slots, one edge). The built-in `loadout-author` skill learns the pipeline format, and the builtin honesty test keeps passing.
19. **`work_folder` resolves against the folder the session starts in**, and `~/` paths are allowed. Lime starts its conductor from its L1 repo.
20. **Plan and dev get verdicts**: plan returns `READY`, dev returns `DONE` or `SPEC CONFLICT`. `SPEC CONFLICT` is a gate back to the planner.
21. **Older CLIs**: `CHANGES.md` says so, and the release that ships pipelines may raise `server.MinimumCLI` (it only warns). Pipelines are not hidden from old clients.

## Design

### What a pipeline package holds

One kind, two shapes, one file. A pipeline package is `loadout.yaml` plus `pipeline.yaml` (the main file). A template has `slots:` and `edges:` and no `uses:`. A binding has `uses:` and `bind:` and no `slots:`. Having both or neither is refused.

The template that expresses today's dev loop (`acme/dev-loop`):

```yaml
# pipeline.yaml of acme/dev-loop
start: plan
slots:
  plan:
    brief: Turn the ticket into plan.md, and spec.md after a grill.
    writes: plan.md
    also: [spec.md]              # optional extra outputs, never checked for a verdict
    reads: [ticket]
    verdicts: [READY]
  dev:
    brief: Implement the requirement on a branch.
    writes: dev.md
    reads: [spec.md, plan.md, ticket]
    verdicts: [DONE, SPEC CONFLICT]
  review:
    brief: Judge the change against the requirement.
    writes: review.md
    reads: [spec.md, plan.md, dev.md, ticket]
    verdicts: [APPROVE, CHANGES REQUESTED, REPLAN, BLOCKED]
  test:
    brief: Run the reviewed change in a safe venue.
    writes: test.md
    reads: [spec.md, plan.md, review.md]
    verdicts: [PASS, FAIL, INCONCLUSIVE]
edges:
  - {from: plan,   on: READY,             to: dev,    via: gate}
  - {from: dev,    on: DONE,              to: review, via: gate}
  - {from: dev,    on: SPEC CONFLICT,     to: plan,   via: gate}
  - {from: review, on: APPROVE,           to: test,   via: gate}
  - {from: review, on: CHANGES REQUESTED, to: dev,    via: gate, max_rounds: 2}
  - {from: review, on: REPLAN,            to: plan,   via: gate}
  - {from: review, on: BLOCKED,           to: end,    via: gate}
  - {from: test,   on: PASS,              to: end,    via: gate}
  - {from: test,   on: FAIL,              to: dev,    via: gate}
  - {from: test,   on: INCONCLUSIVE,      to: end,    via: gate}
```

The Lime binding (`lime/dev-loop`):

```yaml
# pipeline.yaml of lime/dev-loop
uses: acme/dev-loop@1
bind:
  plan: lime-planner
  dev: lime-dev
  review: lime-code-review
  test: lime-test
work_folder: explorations/
# model: deep          # optional, the conductor's hint; balanced when unset
# gate:                # optional, auto edges this team wants as gates
#   - {from: dev, on: DONE}
```

Rules the parser enforces (`internal/pipeline`, strict YAML):

- Slot names and verdicts. A slot name follows `ident` rules. A verdict is upper-case words with single spaces (`[A-Z]+( [A-Z]+)*`, at most 32 bytes), so today's `CHANGES REQUESTED` stays one verdict and the line is `Verdict: CHANGES REQUESTED`. `QUESTIONS` and `end` are reserved. `QUESTIONS` is returned by any slot without being listed, so there is no `relay` field.
- Every verdict of every slot has exactly one edge. An edge's `from` and `to` are slots of the template, or `to: end`. `via` is `gate` or `auto`.
- `max_rounds` (1 to 10) is allowed only on a back edge: one whose `to` comes before or at `from` in the order the slots are written.
- `writes` and `also` are file names (`pack.CheckPath`, one segment, unique across slots). `reads` names files of other slots or the word `ticket`.
- Limits: at most 12 slots, 8 verdicts per slot, 64 edges, a `brief` of at most 500 bytes. The kind's package limits are 4 files, 64 KB per file, 128 KB in all.
- A binding's `uses` is `<ns>/<name>@<major>` (a major only, `@1`, never a full version). `bind` values are agent names (`ident`). `gate` lists `{from, on}` pairs. There is no field that makes an edge `auto`, so decision 6 holds by construction. `model` is a hint (`agent.Model`).

Room for v2 (decision 15) without breaking v1 files: a slot may later take `topic:` (the agent is chosen by a verdict-like word an earlier slot writes), and an edge's `to` may later name `pipeline:<ns>/<name>@<major>`. Both are unknown fields today and refused, so they arrive as a new major of the format, not as a silent change.

### `internal/pipeline`: pure values

A new package, like `internal/skill` and `internal/kb`. No I/O, no HTTP, no target.

```go
// Package is what pipeline.yaml holds: a *Template or a *Binding.
type Package interface {
	// Needs is what must exist for this package to work: the template a
	// binding uses and the agents it binds. A template needs nothing.
	Needs() Needs
	pipeline()
}

type Template struct { /* start, ordered slots, edges; immutable */ }
type Binding struct { /* uses, bind, work folder, model, extra gates */ }

func Parse(data []byte) (Package, error)

// Bind checks a binding against one template release and returns the
// resolved pipeline: every slot bound, every extra gate on an auto edge,
// the bind naming no unknown slot.
func Bind(b *Binding, t *Template) (*Bound, error)

// Compatible reports whether next may follow prev inside one major:
// the same slots, and the same verdicts per slot.
func Compatible(prev, next *Template) error

// Mermaid draws a template, or a bound pipeline with agent names, as a
// flowchart: gate edges dashed, auto edges solid, back edges labelled
// with their max_rounds.
func Mermaid(t *Template, agents map[string]string) string
```

`Bound` is immutable and holds everything the conductor needs: the conductor's name, its model hint, the work folder, the slots in order with their agent, contract and edges (with the binding's gates applied).

The conductor's text is built here too, from an embedded `conductor.md.tmpl`, because it is a property of the pipeline and not of a tool. The few words that differ per tool come from a consumer-owned interface:

```go
// Starter says, in one tool's words, how the conductor starts a named
// agent as a subagent with a prompt.
type Starter interface {
	StartAgent(name string) string
}

func (b *Bound) Conductor(s Starter) (name, description string, prompt string)
```

The conductor's text says, in this order:

1. **Start.** Ask for the ticket key if the person did not give one. Find the run folder: `<work_folder>/<TICKET>-*`. If there is none, make the slug from the ticket title and use `<work_folder>/<TICKET>-<slug>/`.
2. **Where the run stands** (decision 8). Read every slot file that exists. Each round of a slot ends with one `Verdict:` line. Replay the edges from `start`: take the first round of the start slot, follow the edge of its verdict to the next slot, take that slot's next round, and so on. Where a slot has no round left, that slot is next. The last `Verdict:` line of a file is its current verdict. The count of rounds a back edge was followed comes from the same replay.
3. **Starting a slot.** Start the bound agent (the tool's words from `Starter`, naming the agent explicitly) with: the run folder, the files to read, the file to write (append a numbered `## Round N` section, never overwrite), the allowed verdicts, the rule to end the round with exactly one `Verdict: <WORD>` line as the last line, and the person's notes from the last gate. Nothing in the prompt names the next slot or its agent.
4. **After a slot.** Check that the slot's file exists and that its last line is a `Verdict:` with an allowed verdict. If not, stop at a gate and say what is missing, never guess. Then follow the edge for that verdict.
5. **Relay** (decision 3). Every slot may end a round with `Verdict: QUESTIONS` and its questions in its own file, without listing it. Show them to the person as they are and stop at a gate, collect the answers, and start the same agent again as a new start with the answers (it reads its own file). Questions never follow an edge. Resuming the same subagent is not used.
6. **Gates and auto edges** (decision 6). On a gate: show the verdict, the agent's questions and the next slot with its agent, and wait for the person. On auto: go on.
7. **Round caps** (decision 7). When following a back edge would exceed its `max_rounds`, stop at a gate instead, state both positions (what the later slot asks for, what the earlier slot did and why), and let the person decide.
8. **Never** edit code or slot files itself, never start an agent that is not bound, never skip a gate.

### pack: the kind, and four small hooks in the table

`KindPipeline Kind = "pipeline"` with `main: "pipeline.yaml"`, `parse: parsePipeline`, `scaffold: scaffoldPipeline` (the two-slot example of decision 18), and `pipelineLimits`. `Pack.Pipeline() (pipeline.Package, bool)` returns the parsed content.

A pipeline is the first kind whose package depends on other packages and that installs nothing on its own (a template). Rather than switch on the kind in the installer, the registry and sync, `kindSpec` gains four optional functions that every caller reads without knowing the kind:

```go
type kindSpec struct {
	// ... existing fields
	// needs lists what must exist for the package to work. Nil: nothing.
	needs func(p *Pack) Needs
	// standalone is false when the package installs nothing by itself
	// (a pipeline template). The reason is one sentence for the person.
	standalone func(p *Pack) (bool, string)
	// compatible checks that next may follow prev inside one major.
	compatible func(prev, next *Pack) error
	// lint returns warnings that do not stop a publish.
	lint func(p *Pack) []Finding
}

// Needs is what a package needs from its view.
type Needs struct {
	Packages []Requirement // a package at a major: acme/dev-loop@1
	Agents   []string      // agent names that must exist in the view
}

type Requirement struct {
	ID    ID
	Major int
}
```

`Pack.Needs()`, `Pack.Standalone()`, `pack.Compatible(prev, next)` and `Pack.Lint()` read the table. `semver.Version` gains `Major() int`.

### The handoff lint (decision 4)

`agent.Definition.Handoffs() []Finding` finds lines in the prompt that hand control to a named next agent: a line starting with `Next:` (also in bold or as a list item) that says run, call, start, use, invoke or hand off to a name, and the phrases "hand off to" and "then run" followed by an agent-like name. Each finding has the line number and the line. The agent kind's `lint` returns them as `pack.Finding{Path, Line, Text, Rule: "handoff"}`.

`loadout publish` and `loadout propose` print each finding as a warning after the upload line ("warning: agent.md:69 names the next agent: Next: run lime-code-review. Handoffs live in pipelines. End with a Verdict line instead. A later version refuses this."). The registry does not lint in v1, so an older CLI's upload is unchanged. The later error is a one-line change: the registry calls `Lint` and refuses `Rule: handoff`.

### target: rendering a binding

`RenderOptions` gains the packages the binding needs, resolved by the caller:

```go
type RenderOptions struct {
	Name   string
	Models tier.Table
	// Requires holds the release of each package p.Needs() names, at its
	// major. Empty for kinds that need nothing.
	Requires []*pack.Pack
}
```

`Render` gets one new case:

```go
case pack.KindPipeline:
	return renderPipeline(a, p, scope, stamp, opts)
```

`renderPipeline` (in `target/pipeline.go`) takes the binding, finds its template in `opts.Requires`, calls `pipeline.Bind`, then:

- If the adapter implements `target.Delegator` (the same method as `pipeline.Starter`), it renders the conductor as an agent with `RenderAgent`: name, description, model hint (`balanced` unless the binding says otherwise, resolved through `Choose` like any agent), tools `read`, `search`, `agent`, and the generated prompt.
- If not, it renders the fallback skill with `RenderSkill`: a skill of the same name whose `SKILL.md` gives the main session the same steps, with "start the agent X" in place of the delegation (decision 13).

```go
// Delegator is a Renderer whose tool can start a named custom agent as a
// subagent. Conductors render as agents only for such tools.
type Delegator interface {
	StartAgent(name string) string
}
```

`claudecode.Adapter.StartAgent` says to use the Agent tool with `subagent_type` set to the name. `copilot.Adapter.StartAgent` says to ask the custom agent by its exact name in the delegation prompt ("Use the lime-planner agent to ..."). If the Copilot proof fails, the fallback is deleting that one method from the Copilot adapter, nothing else.

**The conductor's name** is `<team>-<name>` of the binding (`lime/dev-loop` renders `lime-dev-loop`), so the name in decision 3 and the package id in decision 17 agree. A binding whose name already starts with `<team>-` keeps it. This is the one place a rendered name is not the package name, and the registry refuses a binding whose conductor name equals a bound agent.

A template renders nothing: `Render` returns an error that wraps `target.ErrNotStandalone`, and callers ask `Pack.Standalone()` first so they never reach it.

### install: requirements, lock and warnings

- `install.Options.Requires []*pack.Pack` is passed to `target.Render`.
- `lock.Entry.Requires []lock.Requirement` (`id`, `version`, `digest`, `omitempty`) records the template release the conductor was rendered from. sync compares it, and `status` shows "uses acme/dev-loop@1.2.0".
- `Result.Warnings []string`: after an install, for each name in `p.Needs().Agents` with no agent entry of that name in the same scope, "lime-dev-loop: slot test is bound to lime-test, which is not installed here." (decision 10). Kind-blind, from the lock.
- `Installer.Install` refuses a package that is not standalone with its reason. `installAll` skips it with one line ("skipped acme/dev-loop: a pipeline template installs through a team's binding") and sorts packages with `Needs` after the others at the same depth, so the agents are installed before the warning is computed.
- The caller resolves requirements through a new helper in `internal/release`: `Resolve(ctx, reg, req) (*pack.Pack, error)` lists the releases, takes the highest with `Major() == req.Major`, and fetches it. The template is read through the binding's view, which reads the project.
- `install --merge` refuses an entry with `Requires` in v1 ("a conductor is generated, edit the binding instead"), since its base would need the recorded template fetched. `sync`'s next steps leave `--merge` out for such entries.
- `review` and `review compare` refuse to run a pipeline package (a conductor would start a whole run in the throwaway folder) and show the diff only. `reviewplan` leaves pipeline packages out of the context.

### registry: the checks (decisions 10 and 11)

A new file `internal/registry/needs.go` holds `(*Service).checkNeeds(ctx, ns, p)` and `checkCompatible(ctx, p)`, called by `Service.Publish` and `ProposalService.Propose` after the bundle decodes and the permission check passes. Both are kind-blind: they read `p.Needs()` and `pack.Compatible`.

- **Needs.** The view is `Chain(ctx, s.namespaces, ns)`. Each required package must sit in the chain, have a release with that major (`Store.Releases`), and be a template (the bundle of the highest release in the major, from `Store.Release`, decoded). `pipeline.Bind` must pass against it, so every slot is bound. Each needed agent must be the name of an agent package in one of the chain's namespaces (`Store.Packages` per layer, `Kind == agent`). The conductor's name must not equal a bound agent's. Failures are `ErrInvalid` with one sentence each, all listed.
- **Compatible.** When the package has a release in the same major, the highest one is decoded and `pack.Compatible(prev, next)` must pass, else `ErrInvalid`: "acme/dev-loop 1.3.0 renames slot test to verify. That is a major change: publish 2.0.0."
- **Approve** does not check again. Nothing the checks read can disappear: releases are immutable, packages are never deleted, and a namespace is deleted only when empty.

No new `Store` method. The checks use `Releases`, `Release` and `Packages`, which every store already passes in `registrytest`.

### sync: template updates and new majors

`update.Registry` gains `Releases(ctx, id) ([]semver.Version, error)` (the `registryAdapter` in `internal/cli/connect.go` implements it from `client.Releases`). For an entry with `Requires`:

- When the highest release in the pinned major differs from the recorded one, the conductor is rendered again, following the policy as model changes do (`auto`: install again, `OutcomeRerendered` with the reason "pipeline template updated". `notify`: report it. `pin`: nothing). Edited files hold it back as today.
- When a higher major of the template exists, sync prints one line per binding: "acme/dev-loop 2.0.0 is out. lime/dev-loop uses major 1. Move it with `uses: acme/dev-loop@2` and publish." It is a line, not a held state, so `syncstate` does not change.

### Web (decisions 16 and 17)

- **Kind everywhere.** `pack.Kinds()` lists `pipeline`, so the packages list gets a Pipelines tab and badge with no new code beyond `kindPlural` and the search words.
- **Diagram.** `frameOf` in `ui_files.go` learns `?pipeline=1` on `draw/pipeline.yaml`: it parses the stored file and draws `pipeline.Mermaid`. The source is still the stored release, never the URL. For a binding the page reads the template's highest release in the major through the page's principal and draws it with the agent names. Gate edges are dashed, auto edges solid, back edges labelled "max 2 rounds". It is a `figure.diagram` like every other, so without script it shows its fallback and the tables below carry the same facts.
- **Tables** (`templates/ui/package.html`, a `pipeline` block). A template: a slots table (slot, file, verdicts) and an edges table (from, on, to, gate or auto, max rounds), then "Use this pipeline" with the copyable `loadout new pipeline TEAM/NAME --uses acme/dev-loop@1`. A binding: slot, bound agent linked to `/t/{ns}/p/{team}/{agent}` in the page's view, and a warning row for an unbound slot or an agent missing from the view (a template's new minor can never add a slot, but a page can show a release made before a check). Tokens only from `static/style.css`, phone layout as a stacked list.
- No editor (decision 17).

### CLI (decisions 17 and 18)

- `loadout new pipeline TEAM/NAME` scaffolds the example template: two slots (`draft`, `check`), one gate edge, one end edge.
- `loadout new pipeline TEAM/NAME --uses NS/NAME@MAJOR` fetches the template through the team's view (`release.Resolve`), and writes a binding with `bind:` listing every slot with an empty value, `work_folder: explorations/`, and the template's edges as YAML comments. Publishing it unfilled fails the registry check, which names the empty slots. The scaffolding lives in `internal/pipeline` (`Scaffold(t *Template) []byte`), so `internal/cli` only fetches and writes.
- `list`, `status` and `history` show the kind and, for a binding, "uses acme/dev-loop@1".

### Built-in skills (decision 18)

The four skills people install to use loadout from their AI chat (`internal/builtin/skills/`) each learn their part. They change in the last step, after every command they name exists, because `builtin_skills_test.go` fails on a command or flag that does not.

- **`loadout-author`**: a Pipelines section. What a template and a binding are, `loadout new pipeline TEAM/NAME` and `--uses NS/NAME@MAJOR`, the verdict line rule, what is a major change of a template, and "never write `Next: run` in an agent, end with a Verdict line". The full format goes to `references/pipelines.md`, linked once from the SKILL.md.
- **`loadout-setup`**: running a conductor. Install the team (`loadout install --all`), then start the session as the conductor (`claude --agent lime-dev-loop`, or `/agent` in Copilot CLI, or the fallback skill if Copilot gets one), and what the install warning about a missing agent means.
- **`loadout-review`**: reviewing a pipeline proposal. Read the diff and the package page's diagram, check for a renamed slot or verdict (the registry refuses it inside a major), and know that `loadout review` does not run a conductor.
- **`loadout-admin`**: the publish checks. What each refusal sentence means (unbound slot, agent not in the view, missing major, slot change inside a major) and how to fix it, and that a template needs a project maintainer while a binding needs a team maintainer.

## Data and API changes

- **Package format.** New kind `pipeline` with `pipeline.yaml`. Nothing changes for other kinds.
- **Database.** None. `releases.kind` and `proposals.kind` are `TEXT` without a check, so `pipeline` fits.
- **API.** No new route. `api.Release.Kind` and `api.Proposal.Kind` document the new value. Publish and propose can answer 400 `invalid` with the check's sentences.
- **Lock.** `lock.Entry.Requires`, written only for pipeline installs.

## Migration

- **Old rows.** Nothing to migrate in the database.
- **Old CLIs.** An older CLI that reads a team view holding a pipeline fails that one package in `install --all` ("kind pipeline is not supported") and installs the rest. sync never installs new packages, so it is not affected. The release that ships pipelines may raise `server.MinimumCLI` to itself, so older CLIs are told on every run to update (it only warns). It is left unset until that release number is known.
- **Old locks.** `requires` is kept out of `lock.yaml` (see test round 1 under As built), so an older CLI still reads the lock and ignores the conductor's template record.
- **The Acme layers (outside this repository).** For the Lime pilot: publish `acme/dev-loop` 1.0.0 from the L0 layer, publish `lime/dev-loop` from the Lime layer, remove `work_folder` from Lime's `dev-loop.yaml` (the binding owns it), and take the `Next: run ...` lines out of Lime's composed agents (they come from `stages/*.md` in the L0 `dev-loop` skill, so the L0 skeleton changes for everyone. The other three teams keep their footers until they move, and the lint only warns). The L0 `dev-loop` skill's Gates and Round cap sections are rewritten to point at the pipeline. How these Acme repositories are changed and pushed is the user's call.

## Files to touch

| Where | What |
| --- | --- |
| `internal/semver/semver.go` | `Version.Major()` |
| `internal/pipeline/` (new) | `pipeline.go` (types, `Parse`), `bind.go`, `compat.go`, `mermaid.go`, `scaffold.go`, `conductor.go` and `conductor.md.tmpl`, tests and golden files |
| `internal/agent/agent.go`, `handoff.go` (new) | `Definition.Handoffs` |
| `internal/pack/kinds.go`, `pack.go`, `needs.go` (new), `scaffold.go` | `KindPipeline`, the `needs`, `standalone`, `compatible`, `lint` hooks, `Pack.Pipeline`, `Needs`, `Requirement`, `Finding` |
| `internal/target/render.go`, `pipeline.go` (new) | `RenderOptions.Requires`, the `KindPipeline` case, `Delegator`, `ErrNotStandalone`, the fallback skill |
| `internal/target/claudecode/claudecode.go`, `internal/target/copilot/copilot.go`, `testdata/` | `StartAgent`, golden conductors |
| `internal/install/install.go`, `internal/lock/lock.go` | `Options.Requires`, `Result.Warnings`, standalone refusal, `--merge` refusal, `Entry.Requires` |
| `internal/release/release.go` | `Resolve` |
| `internal/registry/needs.go` (new), `service.go`, `proposals.go` | the publish and propose checks |
| `internal/update/update.go`, `internal/cli/connect.go` | `Registry.Releases`, re-render on a new minor, the new-major line |
| `internal/cli/install.go`, `new.go`, `publish.go`, `propose.go`, `status.go`, `review.go` | resolve requirements, order and skip, `new pipeline [--uses]`, lint warnings, `uses` in status, review guard |
| `internal/review/`, `internal/reviewplan/` | refuse to run a pipeline, leave it out of the context |
| `internal/server/cli.go` | `MinimumCLI` raised when the release is known |
| `internal/web/ui_team.go`, `ui_files.go`, `templates/ui/package.html`, `static/style.css` | kind tab, `?pipeline=1` frame, tables, "Use this pipeline" |
| `internal/builtin/skills/loadout-author`, `-setup`, `-review`, `-admin` | the skills' new sections, `loadout-author/references/pipelines.md` |
| `docs/GUIDE.md`, `docs/CHANGES.md`, `docs/DESIGN.md`, `docs/ARCHITECTURE.md`, `docs/UI.md` | the docs step |
| `cmd/loadout/pipelines_test.go` (new) | e2e |
| Live registry (user approves) | `acme/dev-loop`, `lime/dev-loop`, the `acme` and `lime` wiki pages |

## Order of work

Each step merges with `make check` green. Steps 3 to 6 are deployed together: a registry must not accept a binding it cannot check, and a CLI must not meet a binding it cannot install.

1. **Prove the tools by hand.** In a scratch folder, hand-write a conductor and two tiny slot agents for Claude Code and for Copilot CLI. The tester proves on real CLIs: the session agent starts a named custom agent as a subagent, the output-file check works, a relayed question round trip works (resume or start again), and whether a slot agent may itself delegate (nested subagents). The result decides whether Copilot gets `Delegator` in step 4. Recorded under "As built" here.
2. **Handoff lint.** `agent.Definition.Handoffs`, `pack.Finding`, the kind table's `lint`, warnings in `publish` and `propose`. Ships alone.
3. **`internal/pipeline` and `semver.Major`.** Parse, validate, `Bind`, `Compatible`, `Mermaid`, `Scaffold`, the conductor text. Unit tests only, no caller yet.
4. **The kind and rendering.** `KindPipeline` in `kinds.go` with `needs`, `standalone`, `compatible`. `target.Render` case, `Delegator` on both adapters (or only Claude Code after step 1), the fallback skill path, golden files.
5. **Registry checks.** `registry/needs.go` from `Publish` and `Propose`. Service tests and an e2e publish.
6. **Install and lock.** `Options.Requires`, `lock.Entry.Requires`, warnings, standalone skip and ordering in `installAll`, `release.Resolve`, `install TEAM/NAME` resolving too, `status`, `--merge` refusal, review and reviewplan guards. Deploy steps 3 to 6 together, with `server.MinimumCLI` raised if the release number is known and the database backed up as before every deploy.
7. **sync.** `update.Registry.Releases`, re-render on a new minor, the new-major line.
8. **CLI `new pipeline`** with and without `--uses`.
9. **Web.** Kind tab and badge, `?pipeline=1` frame, the template and binding tables, "Use this pipeline". UI review (below).
10. **Pilot.** Publish `acme/dev-loop` 1.0.0 and `lime/dev-loop` 1.0.0 on the live registry (writes the user approves). A Lime member runs one real ticket through `claude --agent lime-dev-loop`, and one through Copilot CLI (or its fallback skill).
11. **Docs and skills.** Everything a person reads about the feature, in one step so it describes what the pilot proved:
    - `docs/GUIDE.md`: a Pipelines section. What a pipeline, slot, binding and conductor are (the glossary's words), creating a template (`loadout new pipeline acme/dev-loop`), creating a binding (`loadout new pipeline lime/dev-loop --uses acme/dev-loop@1`, fill `bind:`, publish), running the conductor as the session's agent in Claude Code and Copilot CLI, what a gate and a round cap look like, and what the publish refusals mean. Then rebuild with `docs/guide/build.sh`.
    - `docs/CHANGES.md`: the What's new entry for the deploy that ships it, drafted from `scripts/changes-since.sh` as CONTRIBUTING.md and the loadout-deploy skill say, for a person to approve before it is published.
    - `docs/DESIGN.md`: `pipeline` in the kinds table with its limits, the sentence "A new kind is an entry in the table" extended with `needs`, `standalone` and `compatible`, a Pipelines section (format, conductor, `Delegator`, registry checks, lock `requires`, sync), and `internal/pipeline` in the packages table. `docs/ARCHITECTURE.md`: one diagram of template, binding and conductor under Layers, since a package now depends on another one across layers.
    - The four built-in skills, as described above, and `references/pipelines.md`. `make check` runs the honesty tests.
    - **Registry wiki pages (a write to the live registry, the user approves each push).** On the live registry: the `acme` wiki's "How a change gets made" section (today it tells people to pick `lime-planner` and says no agent moves on by itself) gets the conductor as the way in: start `<team>-dev-loop` as the session agent, it starts each step and stops at each gate, and the grill still happens in that session through the relay. "I want to..." gets a row "Run a ticket through the dev loop" pointing at `<team>-dev-loop`, and "Words you'll hear in week one" gets pipeline, gate and conductor. The `lime` wiki's "Our agents" gets `lime-dev-loop` as the first row of the dev loop, and its dev loop paragraph says the conductor moves between the four agents and asks before each step. The other teams' wikis stay as they are until those teams move. The agent drafts each page with `loadout wiki pull` and `wiki push`, shows the diff, and pushes only after the user says yes.

## Tests

- **`internal/pipeline`** (tables): every parse rule above, accepted and refused, with the sentence. `Bind`: unbound slot, unknown slot, gate on an edge that is already a gate (accepted, no change), gate on an unknown edge. `Compatible`: slot added, removed, renamed, verdict added or removed (refused), brief, files, edges and `via` changed (accepted). `Mermaid` golden files. `Scaffold` round trip: its output parses as a binding once filled. Conductor text golden files for both `Starter`s, and a test that the text names no agent outside the binding and has every slot's contract. A fuzz test on `Parse`.
- **`internal/agent`**: `Handoffs` tables, true positives (`Next: run lime-dev`, `**Next:** call x`, `then run lime-test`) and false positives that must stay silent (`next steps`, a `Next:` without an agent name, prose about delegating).
- **`internal/pack`**: `kinds_test.go` for the new entry, `Needs`, `Standalone`, limits.
- **`internal/target`**: `render_test.go` for a binding with and without a `Delegator`, a template (not standalone), a missing requirement. Golden files in `claudecode/testdata` and `copilot/testdata`. Model hint default and override.
- **`internal/install`**: requirements recorded in the lock, warnings for a missing agent, template refused, `--merge` refused, an old lock without `requires` still reads.
- **`internal/registry`**: publish and propose of a binding with an unbound slot, an agent missing from the view, an agent only in a sibling team (refused), an agent in the project (accepted), a `uses` outside the chain, a missing major, a `uses` that is a binding. A template minor that renames a slot (refused), and a new major that does (accepted).
- **`internal/update`**: new minor re-renders under `auto`, reports under `notify`, nothing under `pin`, edits hold it back. A new major prints the line.
- **e2e in `cmd/loadout`** (`pipelines_test.go`, with `person` and `newSession`): a project maintainer publishes a template, a team maintainer scaffolds a binding with `new pipeline --uses`, publish fails until filled, `install --all` writes the conductor for both tools and skips the template, a missing agent warns, a template minor reaches the team on `sync`, a new major prints the line.
- **`internal/web`**: packages list Pipelines tab, the template page (diagram figure, both tables, the "Use this pipeline" command), the binding page (links into the view, the missing-agent warning), the frame route draws from the stored file and 404s for a non-pipeline file, a sibling team's pipeline is a 404.
- **Builtin**: `builtin_skills_test.go` and `recipes_test.go` pass with the new section.
- **UI review**: the `loadout-ui` agent and `loadout-preview` screenshots of the template and binding pages in light and dark, at 390 px and desktop, without script, and `accessibility-audit`. Recorded in `docs/UI.md`.
- **By hand** (step 1 and the pilot): real Claude Code and Copilot CLI runs. No test in the repository starts a real AI tool.

## Risks

- **Copilot CLI may not start a named agent reliably.** Delegation by naming is documented behaviour, not an explicit call. Step 1 decides, and the fallback skill is one method away.
- **Nested delegation.** A slot agent runs as a subagent of the conductor. Whether a subagent may start its own helper depends on the tool and its version (Claude Code's docs and changelog disagree on nesting). If it cannot, an agent that delegates (lime-dev to lime-infra) loses that inside a pipeline. Step 1 checks it. A way out is binding the helper as its own slot, or turning it into a skill.
- **The relay.** Resuming the same subagent is not available everywhere. The conductor falls back to starting the agent again, which only works because the planner writes its questions and the answers into its own file. Lime's planner must do that.
- **State from files only.** A person who edits or deletes a slot file changes where the run stands. That is the design (decision 8), but the conductor must say what it inferred at every start, so a surprise is visible.
- **The model follows the text.** The conductor is instructions, not code: it can skip a gate. Mitigation: short, numbered rules, the output-file check, and the tester's runs. A deterministic helper is v2 (below).
- **DESIGN.md says publishing is kind-blind** and that a package renders on its own. Bindings break the second: rendering needs another package. The kind table's `needs`, `standalone` and `compatible` keep the registry, install and sync free of `switch kind`, but DESIGN.md must describe the dependency, the lock field and the publish checks.
- **Renaming a slot's file** inside a major strands runs that already have the old file. Decision 11 does not make it major. `Compatible` accepts it, and the plan says so in the format docs.
- **The live registry** gets two new packages and no migration. The deploy backs up first as always.
- **Shared project skills folder.** If Copilot ends up with the fallback skill, a project-scope install puts it in `.claude/skills`, where Claude Code also reads it next to its conductor agent of the same name. The fallback's description says it is for Copilot only.

## Not in this step

- Topic-dependent slots, nested pipelines (the schema leaves room, see above).
- Moving the other teams to pipelines.
- The lint as an error, and the lint on the registry and the web.
- A web editor for bindings (v2, decision 17).
- `loadout pipeline status RUN_FOLDER`, a deterministic replay the conductor could call through the shell.
- `install --merge` for conductors.
- Restricting the conductor's agent tool to the bound agents only (Claude Code's `Agent(name, ...)` form).
- A once-per-version memory for the new-major line in `syncstate`.

## As built

### Step 1: the probe (2026-10-11), PASS

Real Claude Code 2.1.295 and Copilot CLI 1.0.91, hand-written conductor and slot agents, each check run twice per tool (nest checks four times), 22 runs, nothing flaky. The report and agents were scratch files, so this is the summary.

| Check | Claude Code | Copilot CLI |
| --- | --- | --- |
| Conductor starts a named agent as a subagent that does the work | pass | pass |
| Slot file missing: no edge followed | pass | pass |
| File without a `Verdict:` line: no edge followed | pass | pass |
| Question relay: `Verdict: QUESTIONS`, answer, start the agent again (no resume) | pass | pass |
| Nested: a slot agent starts its own helper | pass only if the conductor's `Agent(...)` list holds the helper (or is plain `Agent`) | pass |
| Resume from files, says what it inferred | pass | pass, but it said nothing before its first view call |

What it decided:

- Decision 13 holds. Both tools get a real conductor and a `Delegator`. No fallback skill in v1.
- The conductor must say what it is about to check before its first tool call, and what it inferred before it starts any agent.
- Keep the output-file check: no edge when the slot's file is missing or its last round has no valid `Verdict:` line.
- Relay: a slot that needs an answer writes the question and `Verdict: QUESTIONS` into its own file. The conductor shows it, stops, and on the answer starts the agent again with it. Resume was not tested (Copilot reports `resumable: false`) and is not used. QUESTIONS is allowed for every slot, so the `relay` field is gone from the format.
- Nested delegation stays. On Claude Code a subagent can only start agents on the conductor's `Agent(a, b, ...)` list, so the renderer needs the set of agents the conductor may start. `(*pipeline.Bound).Allowed(defs)` returns it.
- Claude Code's `Agent` tool may return before the subagent finishes in `-p` mode. The conductor text waits and then reads the file.
- Not tested: gates and back edges with `max_rounds` (the probe had one auto edge), interactive mode, chains of four slots, other models. Step 4 should re-run the probe against the generated conductor and the pilot covers the rest.

### Step 2: handoff lint

Branch `pipeline-lint`. As planned, with these differences:

- `agent` cannot import `pack`, so `agent.Definition.Handoffs()` returns `[]agent.Handoff{Line, Text}`, and the agent kind's `lintAgent` (in `internal/pack/lint.go`) turns them into `pack.Finding`. The prompt has no frontmatter, so `lintAgent` moves each line number to where the line sits in `agent.md`.
- `pack.Finding` also carries `Advice`, the sentence to print, and has a `String()` that prints `warning: agent.md:8 names the next agent: ...`. `cli.writeFindings` prints them to stdout after the upload line in `publish` and `propose`.
- The rules are regular expressions on lines with list markers, quote marks and `*` removed. A `Next:` line fires when it says run, call, start, use, invoke or hand off to a name that is hyphenated or in backticks (`Next: run lime-dev`, ``Next: call `x` ``). A bare word never fires, so `Next: run pytest`, `Next: use Docker` and `Next: run the tests` stay silent. `hand off to` and `then run` fire anywhere in a line, but only for a hyphenated name (in backticks or not), so prose about delegating stays silent. Lines inside fenced code blocks are skipped.
- I kept `then run lime-test` firing, which means `then run pre-commit` fires too: a hyphenated agent name and a hyphenated tool cannot be told apart by text.
- It misses handoffs worded another way ("afterwards, ask the reviewer"), a single-word agent name unless it is in backticks on a `Next:` line, and handoffs in skills.

### Step 3: `internal/pipeline` and `semver.Major`

Built as planned, with these differences:

- `Parse`, `Bind`, `Compatible`, `Mermaid`, `Scaffold` and the conductor exist as described. `Scaffold(ref, t)` takes the template reference, and `ScaffoldTemplate()` returns the example template for decision 18.
- The package does not import `internal/pack` (pack will import it), so it carries its own file-name check with the alphabet of `pack.CheckPath`. `Ref{Layer, Name, Major}` stands in for `pack.ID` plus a major, and `Needs()` returns `pipeline.Needs{Templates []Ref, Agents []string}`. `pack` converts it to its own `Needs`.
- `Conductor` is `(*Bound).Conductor(name, Starter) (Conductor, error)` returning `Conductor{Name, Description, Model, Prompt}`. The name comes from `pipeline.ConductorName(layer, name)` because a `Bound` does not know its binding's package id. The renderer adds the tools (`read`, `search`, `agent`).
- Step 4 gets one more input: `Bound.Allowed(defs []agent.Definition) Allowed{Names, Unrestricted, Unknown}`. The canonical agent format has no helper list, so an agent with the `agent` tool, or with no `tools` line (every tool), makes the set unrestricted. The Claude Code renderer writes `Agent(a, b, ...)` from `Names` when `Unrestricted` is false and plain `Agent` otherwise, and warns about `Unknown`. The renderer therefore needs the bound agents' definitions in `RenderOptions` (from the same view that resolved the binding), which the plan did not list. Copilot needs none of it.
- `Bind` and the template are checked as the plan says. Two additions: a slot needs a non-empty `brief`, and `reads` may not name the slot's own file.
- Review round 1: the conductor's replay passes over a QUESTIONS round, and the gate text covers an edge to `end`. `Compatible` accepts a reordering of slots, because a reorder that makes a `max_rounds` edge forward is refused when that release parses.
- Test round 1: the conductor prints a fixed `State: ...` line (rule 3) after reading the run folder and before any call that starts an agent, because free prose about the inferred state was skipped (Claude 1/9, Copilot 5/9). With the line: Claude 6/6 and Copilot 5/6, then with one more sentence ("printing it is not optional") 11/12 across both tools on resume and question runs. Slot file names are compared without case, a `brief` must be one line, `work_folder` may not hold a backtick, and the conductor description has no `: `.
- Test round 1, second part: the State line must now be printed in the same message as the call that starts the agent. Six resume runs per tool: Claude 6/6, Copilot 5/6 (earlier wording: Claude 6/6 and Copilot 5/6 on a mixed set, 1/9 and 5/9 with free prose). A separate `Inferred:` rule scored 4/6 and 3/6 in the tester's runs. Stop lines are split: `GATE:` waits for the person (gates, questions, round caps), `STOP:` is an error (no valid output, cannot replay). The start slot's prompt tells the agent to create the run folder with its parents, since Copilot's edit tool cannot. A file that is only an `also` of another slot renders as optional ("if it exists"). For step 4: `Bound.StartShell(defs)` says whether the start slot's agent has a shell tool, so `install` can warn when it has none (known=false when its definition is missing). Only Copilot needs the warning.

### Step 4: the kind and rendering

Branch `pipeline-kind`. Built as planned, with these differences:

- `pack.KindPipeline` takes only `pipeline.yaml` (limits: 4 files, 64 KB each, 128 KB in all). `kindSpec` gained `needs`, `standalone` and `compatible`, read by `Pack.Needs()`, `Pack.Standalone()` and `pack.Compatible(prev, next)`. `Compatible` also refuses a change of kind, and for pipelines a change between template and binding. A binding has no compatibility rule.
- `target.Render` returns warnings on `Unit.Warnings`, and `RenderOptions` has `Agents []agent.Definition` next to `Requires`. Step 6 passes both and prints the warnings at install.
- Capabilities are optional interfaces on the adapter, so a new tool opts in with one method: `Delegator` (`StartAgent`, required to render a conductor), `AgentLimiter` (`RenderLimited`, Claude Code only: the tools line becomes `Agent(a, b, ...)` from `Bound.Allowed`, or plain `Agent` when unrestricted) and `FolderMaker` (`EditCreatesFolders`, Claude Code only). A tool without `FolderMaker`, whose start slot agent has no shell tool, gets the warning that nothing can create the run folder (Copilot). An unknown definition gets a warning only when the Claude Code list is limited.
- The model hint is `balanced` unless the binding says otherwise, resolved with `Choose`. `opts.Name` replaces the conductor name, as for agents.
- A template renders to an error wrapping `target.ErrNotStandalone`. A tool without `Delegator` is refused with a sentence. No fallback skill, as step 1 decided.
- Golden files are `internal/target/testdata/conductor-{claude,copilot}-{user,project}.golden.md`, not in the adapter folders, because one test renders through `target.Render` for both. The same test parses each frontmatter with a YAML parser. The frontmatter was already written by the YAML encoder, so no adapter change was needed for the `: ` finding.
- Not re-run: the probe against the generated conductor on real Claude Code and Copilot CLI. The tester does that.

### Step 5: registry checks

Branch `pipeline-install`. Built as planned, with these differences:

- `registry/needs.go` holds an unexported `checker{store, namespaces}` with one `check(ctx, p)` that both `Service.Publish` and `ProposalService.Propose` call. It runs before the version-conflict check. All sentences come back in one `ErrInvalid`.
- `ProposalService` had no namespace store, so it gets `WithNamespaces(NamespaceStore)` (wired in `cmd/loadout/main.go` and the e2e registry). Without it, a package that needs anything is refused with a plain error and not the author's fault.
- Kind-blindness needed one more hook than the plan listed: `kindSpec.checkNeeds(p, required)`, read through `Pack.CheckNeeds(required)`. For a binding it runs `pipeline.Bind` against the template and refuses a conductor name equal to a bound agent (`pack.CheckConductorName`, also called by `target.renderPipeline`, so the render guard and the publish check say the same sentence). `Kind.ChecksCompatibility()` lets the registry skip loading the previous release for kinds with no rule.
- An agent counts as present when a package of kind agent with that name sits in one of the chain's namespaces. A namespace with no row falls back to its own layer, as `Layers` does.
- The slot-change check compares with the highest release in the same major, so inside 0.x every change counts. A stored release that cannot be decoded is a plain error, not a refusal.
- Approve does not check again, as planned. So a template major removed between propose and approve is not caught.

### Step 6: install and lock

Built as planned, with these differences:

- `lock.Entry.Requires` records id, version and digest of the template release. It lives in `lock.requires.yaml` beside the lock and not in `lock.yaml` (see the fix below). `install.Options` gets `Requires` and `Agents`, `Result.Warnings` carries the render warnings (`Unit.Warnings`) followed by one per needed agent with no entry of that name in the scope. The missing-agent sentence has no slot name ("lime/dev-loop needs the agent lime-test, which is not installed here."), because `Needs.Agents` is kind-blind and holds names only.
- `Install` refuses a template (wrapping `target.ErrNotStandalone`, with the reason). `installAll` prints the skip line and installs every package that needs agents after all the others, not only after the same depth, because a team's binding may bind an agent from a farther layer.
- `withNeeds` in `internal/cli/needs.go` resolves a package's requirements (`release.Resolve`, the highest release of the major, through the package's view) and the bound agents' definitions (the closest layer's agent of that name). `install TEAM/NAME`, a folder install of a binding, `install --all` and `rollback` all use it.
- `Merge` refuses a package that needs another (`install.ErrMergeGenerated`), and sync's held-file steps leave `--merge` out when the entry has `Requires`.
- `status` prints "lime/dev-loop uses acme/dev-loop@1.0.0" lines under the table. `list` and `history` are step 8.
- `loadout review` refuses a pipeline in `Options.check` with a sentence pointing at `loadout review show`. The kind table says so (`Kind.CanRunInReview`, false for pipelines), and `reviewplan` leaves such packages out of the context with a note.
- `server.MinimumCLI()` stays unset. Raise it in the commit of the release that ships pipelines.
- `new pipeline` is step 8: `loadout new pipeline x/y` prints the help and exits 0, as for any unknown subcommand. Nothing panics.
- sync is step 7: until then a `sync` that finds a new version of an installed conductor tries to render it without the template and reports an error for that package.
- e2e: `cmd/loadout/pipelines_test.go` covers a template publish, an unfilled binding refused with both problems named, a filled one accepted, install before the agents (warns), the template refused on `install` and skipped on `install --all`, the conductor written for both tools (the Copilot run-folder warning shows because the scaffolded agent has no shell), the `uses` line in `status`, and a slot rename refused inside a major. The sync lines of the plan's e2e bullet belong to step 7.

### Steps 5 and 6, test round 1

- **Lock compatibility.** The plan said an older CLI "fails on a lock with `requires`". The test showed it fails on every command, with advice that would make people discard their install records. `Entry.Requires` is now `yaml:"-"` and `lock.Store` keeps it in `lock.requires.yaml` next to the lock, the way `builtin.yaml` stays apart for the same reason. The file is written under the lock's flock just before the lock, and `Load` attaches it by package id and scope. `TestLockWithRequiresStaysReadableByV010` decodes the new lock with a copy of the v0.1.0 `Entry`. The migration note "old locks" in this plan no longer applies. `MinimumCLI` stays 0.2.0.
- **Review refusal.** `review.CheckRunnable` is the one check. `runReview` calls it before it prints the plan, the full-access warning or the run count, and `Options.check` calls it too. Pipelines are left out of the context without a note, since they never run.
- **Damaged requires file.** `Load` ignores it with one logged warning (`Store.WithLog`) and the entries load without requires, so a bad file never breaks the lock. Until step 7 uses the missing record to render again, nothing else happens.

### Step 7: sync

Branch `pipeline-sync`. Built as planned, with these differences:

- `update.Registry.Releases` returns `[]update.Release{Version, Kind}` and replaces `Latest` (sync computes the newest itself). The kind is how sync knows a package can need a template (`pack.Kind.HasNeeds`) without listing every install's pack. `update.Registry.Needs(ctx, p)` is new and resolves the template releases and bound agents through `withNeeds`, because `update` has no client. Every render sync does (new binding version, model change, template change) goes through it, so a new version of the binding installs with its template.
- Outcomes are `pipeline template updated` (auto, `OutcomeTemplateUpdated`) and `pipeline template changed` (notify, `OutcomeTemplateChanged`) and not `OutcomeRerendered` with a reason, because the model outcomes already own that name and wording. `Result.Template`, `Notification.Template`, `syncstate.Behind.Template` and `notice.Notice.Template` carry the release ("acme/dev-loop@1.1.0"), so status, the session hooks, Pushover and the held-back text name the template and not the models. A template update held by edits reuses `OutcomeBlocked` and the same next steps, without `--merge` and `diff`.
- New-major and vanished-major lines are `Result.Notes`, printed after the table for every policy. The new-major line is the plan's sentence. The vanished-major line reads "lime/dev-loop uses acme/dev-loop@1, which the registry no longer lists. The conductor you have keeps working. Point `uses:` at a major that exists and publish." The conductor is left as it is. Publishing already makes this unreachable (releases are immutable, packages are not deleted), so it guards a registry restored from an older backup. A new version of the binding whose pinned major is gone fails with `resolve what ... renders from`.
- A conductor with no recorded requirement is stale: sync fetches the installed binding, reads its `uses:` and renders again under auto, or reports under notify. When the files come out identical the install only records the template, and the result stays `up to date` (the e2e checks `status` instead).
- `Behind.Template` is not a field of `syncstate.yaml`. v0.1.0 decodes that file strictly and its session hooks read it on every start, so the field would make them report it damaged. `syncstate.Store` keeps it in `syncstate.templates.yaml` beside it, written before the state under the same lock, and a damaged or wrong-version file is ignored with one warning (`Store.WithLog`). `TestStateWithATemplateStaysReadableByV010` decodes a state written by this build with v0.1.0's `Behind`.
- A template release that cannot be listed (network, not a 404) fails that install's check.
- Cost: one extra `Releases` call per conductor and one fetch of the installed binding only when its record is missing.
- e2e (`cmd/loadout/pipelines_test.go`): a template minor reaches an auto and a notify team, a new major prints the line and changes nothing, and a lost requires file is recorded again.

### Step 8: `loadout new pipeline`

Branch `pipeline-new`. Built as planned, with these differences:

- `new pipeline` is a fourth entry in `newKinds`, with `--uses` and `--remote` only on that kind. The binding path is `Deps.newBinding` in `internal/cli/new_pipeline.go`. It reads the template through the registry and view of the new package's team (`connectFor(remote, TEAM)`), as `install` does for a folder binding, and not through the template's namespace, because the team's view is what is known to read the layer above it.
- `pack.ScaffoldWith(dir, id, kind, files)` is `Scaffold` with the content files given by the caller. `Scaffold` calls it.
- A template's hint says `loadout publish DIR` and not `install`, since a template cannot be installed. A binding's hint says to fill `bind:` and publish.
- Errors: a malformed `--uses`, a template or major with no release the team can read, and a package that is a binding. No folder is created when any of them happens.
- The built-in skills are untouched (step 11).
- Review fix: `kindSpec.start` is the version a new package begins at (zero means 0.1.0). A pipeline starts at 1.0.0, since `ParseRef` accepts majors from 1 and a fresh template must be usable with `--uses ...@1`. When `--uses` names a template that exists but not that major, the error lists the majors it has.

### Step 9: web

Branch `pipeline-web`. Built as planned, with these differences:

- The Pipelines tab and badge needed no code: `pack.Kinds()` and `kindName` already cover the kind, and the search finds it by "pipeline". Tests pin it.
- `ui_pipeline.go` holds the page's data and the frame. `drawFrame` hands any request with a `pipeline` query to `pipelineFrame`, which accepts only `?pipeline=1` on `pipeline.yaml` of a pipeline release and is a 404 for everything else. The query only asks for the drawing: the file always comes from the release `openRelease` resolved.
- A binding's template is the highest release of its major, read with `Releases` and `Fetch` through the page's principal and only from a layer at or above the page's own. A template the view cannot read gives a warning and no diagram, never a 404 of the binding. When `Bind` refuses (an unbound slot) the diagram is still drawn from the template's edges, without the binding's extra gates.
- An agent is "found" when a package of kind agent with that name sits in the page's layer or above, closest layer first, as the registry's publish check reads it. Releases made before that check (bypassing it in tests with `PutRelease`) show the warnings.
- The package page sends `frame-src 'self'` only when it has a diagram. The first screenshot caught the missing directive.
- The slots table has no separate "file" column: `writes` and `also` share one cell. The edges table is shown on a binding too, with its extra gates applied, so the page without script says everything the diagram does.
- Review record: `docs/UI.md`, "Pipeline pages".


### Step 11: docs and skills (local part)

Branch `pipeline-docs`. Built as planned, with these differences:

- `docs/GUIDE.md` has a top-level Pipelines section before "Bring your existing agents and skills". It uses the glossary's words, follows what was built (the `State:`, `GATE:` and `STOP:` lines, QUESTIONS, resuming from files, round caps, the new-major line, the known limits) and holds the "Do not name the next agent" paragraph in its place under Agents with a link. The kind count, the limits table and the command reference were updated. The guide page is rebuilt. The What's new page is rebuilt too, because `TestChangesFile` requires it to match `CHANGES.md`. Nothing is published until a deploy, so a person approves or edits the draft before one.
- `docs/DESIGN.md` has a Pipelines section. The two bullets steps 6 and 7 added to Model tiers (the lock `requires` side file and sync's template handling) moved into it, and the side files are described together. The kind table's hooks are named in Package kinds. `MinimumCLI` stays "none so far" until the release number is known.
- `docs/CHANGES.md` has a draft entry dated 11 October 2026, through `219d178`. The file's grammar leaves no room for a draft marker, so the draft status is carried by the commit message and the report. It covers pipelines only. Commits between the previous marker and the pipeline work (host skills, simple login, server page, `serve --check`, `user password` and others) are user-facing and not yet in CHANGES.md.
- The four built-in skills each got a section, and `loadout-author/references/pipelines.md` holds the format and the refusals. `TestBuiltinSkillsNameRealCommands` flagged `claude --agent`, a flag of another tool, so it has a short `otherToolFlags` list for flags a skill tells the person to type in their AI tool.
- The registry wiki pages are not touched. Drafts are in the step's report.

## Pilot (11 October 2026)

lime/dev-pipeline on PROJ-123 in Claude Code, headless, ran plan (QUESTIONS, READY), dev, review (CHANGES REQUESTED), dev, review (APPROVE), test (INCONCLUSIVE, the dev venue was skipped by the pilot's rules) and ended cleanly. Every slot ran as its bound agent, the gates held, resume from files worked and the round cap counted right. Fixed after it:

- The State line was skipped before starts after an automatic edge and before every gate after the first. Rule 3 now lists each moment it is printed, and the automatic-edge rule says to print it.
- Slots ran in the background and `claude -p` cut one off after 10 minutes. Rule 4 says to run each slot in the foreground, and Claude's start line sets `run_in_background` to false.
- The run folder took its slug from the person's message ("Run PROJ-123" gave `-run`). The first agent now names the folder from the ticket's title.
- `install` now brings loadout's own skills in step, as `sync` does: Copilot kept the 0.1.0 skills after a pipeline was installed and did not know what a pipeline was. The loadout-author and loadout-setup descriptions name pipelines early.
- In Copilot's autopilot the conductor skipped gates, started a general helper and committed. Rule 9 now says only the person's own message passes a gate and that the conductor never commits. After that, Copilot still read the plan to dev gate as automatic in 4 of 7 runs: the edge line ended in "through a gate". Each edge line now starts with its kind (`READY: GATE. Stop and wait...` or `DONE: automatic. Start...`), and rule 7 says an edge is automatic only when its line says so.
