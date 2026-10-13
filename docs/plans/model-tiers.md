# Model tiers per tool

Status: built and deployed (2026-09-29). Not recorded as done: checking by hand that VS Code accepts the `models` key.

## Goal

An agent says how capable a model it needs (`fast`, `balanced`, `deep`), never a
vendor model. Today only Claude Code maps the hint (haiku, sonnet, opus) and
Copilot always leaves the model to the person, because which models a person
has depends on their company plan. A project (L0) keeps a small table that says,
per tool and hint, which model and which reasoning effort to write into the
installed agent file. Its teams inherit it and may override a cell. loadout still
only writes a text value into a file on the user's machine: it never runs a
model and never holds a key, so the one rule holds.

## What the tools read (researched 2026-09-29)

| Setting | Claude Code subagent | Copilot CLI agent | VS Code `.agent.md` |
|---|---|---|---|
| model | `model`: alias (`haiku`, `sonnet`, `opus`, `fable`), full ID (`claude-sonnet-5`) or `inherit` | `model` (string, ID such as `claude-sonnet-4.6`). `models` (list, first one the plan allows) overrides `model` | `model`: `Model Name (vendor)`, e.g. `Claude Sonnet 4.5 (copilot)`, or a list tried in order |
| effort | `effort`: `low`, `medium`, `high`, `xhigh`, `max` | documented as `reasoningEffort`. The key that works in 1.0.88 is `reasoning-effort` | `reasoning-effort` (`low` to `max`), from VS Code 1.136 |
| context size | only through the model value (`opus[1m]`). Sonnet 5 and Opus 4.7+ always run with 1M | not in the file: `--context default\|long_context`, or Tab in `/model` | not in the file: the model picker only |

- A declared model or effort Copilot cannot honour falls back to the session's
  value. A personal `subagents` override in `~/.copilot/settings.json` outranks
  the file. When the session model is Auto, subagents always use it.
- Copilot's picker "Thinking Effort" levels (Low, Medium, High, Extra High, Max)
  are the same five values as Claude Code's `effort`, so one `Effort` type fits both.
- Claude Code skills also read `model` and `effort`. Loadout skills carry no hint,
  so skills are left out (authors may already write those fields, they are kept verbatim).

Sources: code.claude.com/docs/en/sub-agents, code.claude.com/docs/en/model-config,
code.claude.com/docs/en/skills, docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference
(custom agent frontmatter fields, `--context`, `--effort`),
docs.github.com/en/copilot/reference/custom-agents-configuration,
code.visualstudio.com/docs/copilot/customization/custom-agents,
code.visualstudio.com/docs/copilot/customization/language-models,
github.com/github/copilot-cli/issues/4963, github.com/microsoft/vscode/pull/329263.

Context size is therefore not a table column. For Claude Code it can be written
into the model cell (`opus[1m]`). For Copilot the guide says to pick it in the picker.

## Decisions (settled 2026-09-29)

1. A Copilot cell stores the picker name ("Claude Sonnet 5"). The adapter writes
   `model: "Claude Sonnet 5 (copilot)"` and `models: [claude-sonnet-5]`. Check by
   hand that VS Code accepts the `models` key before release.
2. Owners of the namespace or of a project above, and admins, edit the table.
   Teams inherit it cell by cell. Team maintainers do not edit it.
3. Effort is optional per cell and empty by default. Context size stays out of
   the table and is documented as guidance.

## Design

### New package `internal/tier`: the table as an immutable value

```go
// Effort is a reasoning effort level Claude Code and Copilot both read.
type Effort string // "", low, medium, high, xhigh, max

// Choice is what one tool runs for one hint. The zero value leaves both to the person.
type Choice struct {
    Model  string // the tool's own value: "opus", "Claude Sonnet 5"
    Effort Effort
}

type Key struct {
    Tool string      // target name: "claude", "copilot"
    Hint agent.Model // fast, balanced or deep; never unset
}

// Table maps keys to choices. Built only by New, never changed after.
type Table struct{ cells map[Key]Choice }

func New(cells map[Key]Choice) (Table, error)       // validates every cell
func (t Table) Get(tool string, h agent.Model) (Choice, bool)
func (t Table) Over(farther Table) Table             // closer cells win, per cell
func (t Table) Cells() []Cell                        // sorted, for API, CLI and web
func (t Table) Digest() string                       // "" for an empty table
```

- Validation is loose on purpose, because names change often: a model is 1 to
  80 runes, printable, no control characters, no leading or trailing space.
  YAML marshalling quotes it, so no value can break the frontmatter. Effort is one
  of the five values or empty. A tool name follows `ident` rules. The registry
  accepts tools it does not know (a newer client may), while CLI and web offer only
  the installer's targets. At most 8 tools, so 24 cells.
- A cell with no model and no effort is not stored: it means "inherit".

### target: adapters get the resolved choice

```go
// RenderAgent gets the choice for this tool and def.Model, already resolved.
RenderAgent(def agent.Definition, run tier.Choice, scope Scope, stamp Stamp) (File, error)

// ModelDefaults is an adapter with built-in choices, used where no table cell applies.
type ModelDefaults interface{ DefaultModel(agent.Model) string }
```

- `RenderOptions` gains `Models tier.Table`. `target.Render` resolves once:
  hint unset gives the zero Choice. Else the table cell, else the adapter's
  default model, else zero. Adapters never look at the table.
- `claudecode` implements `ModelDefaults` with today's `modelAliases`, and
  renders `effort` when set. `copilot` has no defaults (unset as today) and, when
  a model is set, renders both keys:
  `model: "Claude Sonnet 5 (copilot)"` for VS Code (the ` (copilot)` suffix is
  added unless the value already ends in `(...)`), and `models: [claude-sonnet-5]`
  for Copilot CLI, derived by lowercasing and turning spaces into hyphens. That rule
  gives every ID in GitHub's list (`claude-haiku-4.5`, `gpt-5.3-codex`,
  `gemini-3.6-flash`, `gpt-6-luna`). The CLI reads `models` over `model`. A wrong ID
  falls back to the session model, which is today's behaviour. Effort renders as
  `reasoning-effort`, the key both Copilot products read.
- With an empty table both adapters produce byte-identical files to today.

### registry, account: storage and inheritance

- `registry.NamespaceStore` gains `ModelTiers(ctx, ns) (tier.Table, error)` and
  `SetModelTiers(ctx, ns, tier.Table) error` (replaces the namespace's cells in one
  transaction. `ErrNotFound` for a missing namespace). SQLite and `MemStore`
  implement both. The contract suite in `registrytest` covers them.
- `registry.Service.ResolvedTiers(ctx)` walks the view's chain (at most
  `MaxLayers`) farthest first and applies `Over`, so the team's cells win. It also
  returns which namespace each cell came from, for display.
- `account.Service.SetModelTiers` allows owners (of the namespace or a project
  above) and admins, exactly like `SetRequireReview`. Unlike `require_review`, the
  table is inherited down the chain.

### install, lock and sync: how a change reaches installed agents

- `install.Options` gains `Models tier.Table`. The installer passes it to
  `target.Render`. `lock.Entry` gains `Models string yaml:"models,omitempty"`,
  the digest of the table used. Old lock entries have none, which equals the empty
  table, so they need no rewrite.
- The "already installed" check in `installLocked` also compares the freshly
  rendered file hashes with the recorded ones, so a changed model is never
  mistaken for unchanged, and a table change that does not touch this package
  (a skill, an agent with no hint) only updates the lock entry.
- `update.Registry` gains `Tiers(ctx) (tier.Table, error)`, fetched once per
  source. When a package is up to date but its entry's `Models` differs, sync
  follows the policy: `auto` fetches the same version and installs it again (local
  edits still block it), `notify` reports "model settings changed", `pin` does
  nothing. New outcome `OutcomeRerendered`. A 404 from an older registry means the
  empty table.

## Data and API changes

- `GET /v1/tiers`: the resolved table for the request's view, with each cell's
  source namespace and the digest. `PUT /v1/namespaces/{ns}/tiers`: that
  namespace's own cells (full replace). Types `api.TierCell`, `api.TiersResponse`.
- Old clients never call them and keep rendering today's defaults.

## Migration

`0012_model_tiers.sql` (after owner-merge's 0010 and review prompts' 0011): table
`model_tiers (namespace TEXT REFERENCES namespaces(name), tool TEXT, hint TEXT
CHECK (hint IN ('fast','balanced','deep')), model TEXT NOT NULL DEFAULT '',
effort TEXT NOT NULL DEFAULT '', PRIMARY KEY (namespace, tool, hint))`. No
existing row changes. No rows means built-in defaults. `DeleteNamespace` removes
the namespace's cells in the same transaction.

## CLI and web changes

- `loadout namespace set bro --model copilot:deep="Claude Sonnet 5" --effort copilot:deep=high`.
  An empty value clears a cell (`--model copilot:fast=`). Repeatable flags. Logic
  in a small parser in `internal/tier`, `internal/cli` only calls it.
- `loadout namespace tiers lime`: the resolved table, with a FROM column
  (`lime`, `bro` or `built-in`).
- `review compare` renders both versions with the reviewer's resolved table, so it
  runs what the team would get. `--model` is still passed to the tool as a
  per-call value, which Copilot documents as the highest precedence.
- Web: `/t/{ns}/tiers`, linked from the members page next to the review setting.
  A form table: rows are hints, columns tools, each cell a text input (the
  inherited value as its placeholder, "from bro") and an effort select. Works
  without JavaScript, CSRF like the other forms. Read-only for those who may not edit.

## Order of work

1. `tier`: types, `New`, `Over`, `Digest`, flag parser. Unit tables.
2. `target`: new `RenderAgent` signature, `ModelDefaults`, resolution in `Render`.
   Adapters render model and effort. Golden tests that an empty table is unchanged.
3. `install` and `lock`: `Options.Models`, `Entry.Models`, hash-aware unchanged check.
4. Store: migration 0012, SQLite and `MemStore`, contract suite. Service resolution
   and the account rule. API routes. Client.
5. CLI: `namespace set --model/--effort`, `namespace tiers`. Install and sync fetch
   the table. `OutcomeRerendered`. Review compare.
6. Web page, UI review against the HIG checklist.
7. Docs: GUIDE "Model" section (table per tool, context size guidance), DESIGN.md,
   rebuild the site. Back up the live database, deploy. The GUIDE and the
   `/t/{ns}/tiers` page both tell Copilot users, in plain words:
   - When the Copilot model picker is on Auto, Copilot uses the session's model
     for every agent and ignores the one from this table. Pick a model in the
     picker for the table to apply.
   - If a model in the table is not allowed on your Copilot plan, Copilot falls
     back to the session's model without an error. A wrong or unavailable name
     never breaks an agent. It just runs on your own model choice.

## Tests

- `tier`: validation table (length, control characters, unknown effort), `Over` per
  cell, digest stable and empty for an empty table.
- `target` and adapters: every hint times {no cell, cell, effort only}. Copilot
  suffix and ID rules. An injected newline in a model stays quoted.
- `install`: same version, new table rewrites the file. Skill unaffected. Edited
  file blocks. `update`: each policy with a changed digest. 404 means empty.
- `registrytest` contract for both stores. Account rule (owner yes, maintainer no).
- e2e in `cmd/loadout`: owner sets a Copilot tier, a member installs and sees
  `models:` and `reasoning-effort`, the owner changes it, `sync` rewrites the file.
- `internal/web`: form round trip, placeholders show inherited values, 403 for a member.

## Risks

- Copilot picker names and IDs change. The loose check and the fallback keep a
  stale cell harmless (it acts like unset), and the page shows every cell to fix.
- VS Code may flag the `models` key, and the CLI docs name `reasoningEffort` while
  `reasoning-effort` is what works. Check both products by hand before release
  and keep the source list in the adapter comment.
- A Copilot session in Auto ignores agent models. The guide says so.
- Copilot's HydraFusion may ignore them too. See `hydrafusion-note.md`.
- Re-rendering on sync touches files people use. The edit check and policies apply.

## Not in this step

- Context size in the table (no tool reads it from an agent file except the Claude
  `[1m]` model suffix, which already fits in a model cell).
- An `effort` field in `agent.md`, and tiers for skills.
- Importing a Copilot vendor model name back to a hint through the table.
- A list of known Copilot models on the registry.
