# Skills and knowledge bases

Status: built and deployed (2026-09-29).

## Goal

A package can hold an agent (today), a skill, or a knowledge base (KB). All three
publish, install, sync, layer (project L0 over team L1), roll back, propose and
review the same way. Nothing about the registry, tokens, roles or the web pages
changes shape; they only learn to show the kind.

## What each kind is

| Kind  | Author writes                                   | Installed as                                   |
|-------|-------------------------------------------------|------------------------------------------------|
| agent | `loadout.yaml`, `agent.md`                      | one agent file per tool (unchanged)            |
| skill | `loadout.yaml`, `SKILL.md`, optional folders (`references/`, `scripts/`, `assets/`) | a skill folder `<skills>/<name>/` |
| kb    | `loadout.yaml`, any Markdown or text documents, in folders | a skill folder with a generated `SKILL.md` that indexes the documents |

A KB is installed as a skill on purpose. Both Claude Code and Copilot load a
skill's description at startup and read its files only when a task needs them,
which is exactly what a knowledge base wants: no tool needs a separate KB
feature, and no model runs anywhere but on the user's own machine.

`SKILL.md` follows the Agent Skills format both tools read: YAML frontmatter
with `name` (the package name) and `description`, then Markdown. loadout adds its
"managed by" comment after the frontmatter, as for agents.

## Where files land

| Tool    | Scope   | Agents                    | Skills and KBs          |
|---------|---------|---------------------------|-------------------------|
| claude  | user    | `~/.claude/agents/`       | `~/.claude/skills/<name>/` |
| claude  | project | `.claude/agents/`         | `.claude/skills/<name>/` |
| copilot | user    | `~/.copilot/agents/`      | `~/.copilot/skills/<name>/` |
| copilot | project | `.github/agents/`         | `.claude/skills/<name>/` |

Copilot reads project skills from `.github/skills`, `.claude/skills` and
`.agents/skills`. Using `.claude/skills` for both tools in a repository means one
copy, not two copies of the same skill that Copilot would both see. The installer
writes a path two targets render identically once.

Sources: code.claude.com/docs/en/skills, docs.github.com/en/copilot/concepts/agents/about-agent-skills.

## Design

### pack: one package, a kind-specific content

- `pack.Kind` gains `skill` and `kb`. Each kind is a `kindSpec` in a table
  (`kinds.go`): its main file, how it validates the files, how it scaffolds.
  Adding a kind is one entry plus one content type; nothing switches on kind
  strings elsewhere.
- `Pack.Content()` returns a `Content` interface (`Kind()`, `Name()`,
  `Description()`), implemented by `agent.Definition`, `*skill.Skill` and
  `*kb.Base`. Callers that need specifics type-switch once, in `target`.
- `Pack.Agent()` stays for the agent-only paths (review of agents).
- Files may sit in folders. A path is validated once, in `pack.CheckPath`:
  relative, slash-separated, no `..`, no hidden segment, at most 8 levels, only
  `[A-Za-z0-9._-]` in a segment, and unique ignoring case (macOS and Windows
  would clobber `A.md` with `a.md`).
- Only UTF-8 text files, so every change can be diffed and reviewed. Binary
  assets are a later step (they need a review story first).

### New packages `skill` and `kb`

- `skill.Parse(files)` reads `SKILL.md` (strict frontmatter: `name`,
  `description`, optional `license`), keeps the other files as `Extra`.
- `kb.Parse(files)` keeps the documents, reads each one's first `#` heading as
  its title, and refuses a `SKILL.md` (loadout writes that one).
- `kb.Base.Index()` builds the generated `SKILL.md` body: what the KB is, when
  to read it, and the list of documents with their titles.

### target: adapters render packs, not only agents

```go
type Adapter interface {
    Name() string
    MarkerDir(ScopeKind) string
    RenderAgent(agent.Definition, Scope, Stamp) (File, error)
    SkillsDir(ScopeKind) (string, error)
}

// Render renders any package for adapter a: the one place that looks at the kind.
func Render(a Adapter, p *pack.Pack, scope Scope, stamp Stamp, opts RenderOptions) (Unit, error)
```

- `Unit` is what one package renders for one tool: its root (the agent file, or
  the skill folder) and its files. Layering and conflicts work on units, so a
  closer layer shadows a farther skill as a whole folder, never file by file.
- `RenderOptions.Name` renames the unit (review renders both versions side by
  side under different names).
- Skill and KB rendering is shared (`target/skills.go`); adapters only say where
  skills live. A new tool that reads Agent Skills gets skills for free.

### install

- `Renderer` becomes `target.Adapter`; `Install` calls `target.Render` per tool.
- Identical paths from two tools are written once.
- Shadowing is decided per unit; stale files are removed and the skill folder is
  pruned when it ends up empty.

### bundle, registry, server

- Bundles carry nested paths (validated with `pack.CheckPath` on decode).
- Limits grow for KBs: 4 MB compressed, 512 KB per file, 200 files. Caddy's
  request limit goes to 6 MB.
- `registry.Release` gains `Kind`, stored in a new column (migration 0009,
  default `agent`), set from the bundle on publish. The API and `loadout list`
  show it; the web pages show a kind badge and the right main file.

### CLI

- `loadout new skill TEAM/NAME`, `loadout new kb TEAM/NAME` (`new agent` stays).
- `list`, `status`, `diff`, `history` and `review show` handle every file, main
  file first.
- `review compare` renders any kind into the throwaway folder. Agents run with
  `--agent`; skills and KBs run the plain prompt, so the comparison shows whether
  the tool picks the skill up and what it does with it.

### Web

- Packages list: a kind column. Package page: the main file (agent.md,
  SKILL.md, or the generated index) and the file list. Proposal diff: every file.

## Order of work

1. pack: kinds table, nested paths, Content, skill and kb packages, tests.
2. target: Adapter, Render, Unit, skills renderer; adapters; tests.
3. install: units, dedupe, per-unit shadowing, pruning; tests.
4. bundle limits and paths; registry Kind + migration; API; server limits.
5. CLI: new, list, diff/history/review show, compare for all kinds.
6. Web: kind badge, package page, proposal diff.
7. Docs (DESIGN.md, README), Caddy limit, deploy.

## Not in this step

- Binary files in skills and KBs.
- Kinds for commands, hooks or MCP servers (the kinds table is where they go).
- Search inside a KB on the registry.
