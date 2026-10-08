# Package formats and limits

## An agent

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

**Tools.** loadout maps each name to the tool's own:

| loadout | Claude Code | Copilot |
| --- | --- | --- |
| `read` | Read | read |
| `search` | Grep, Glob | search |
| `edit` | Edit, Write | edit |
| `shell` | Bash | execute |
| `web` | WebFetch, WebSearch | web |
| `agent` | Agent | agent |
| `skill` | Skill | nothing to grant: Copilot loads skills on its own |

Leave `tools` out to allow every tool. `tools: []` is refused, because both tools read an empty list as every tool.

**Model.** A hint, never a vendor model name: `fast`, `balanced` or `deep`. Leave it out to use the person's own choice. The project (and its teams) decide per AI tool which model and reasoning effort a hint means (`loadout namespace tiers payments` shows it). Without settings:

| Hint | Claude Code | Copilot |
| --- | --- | --- |
| `fast` | haiku | the person's own choice |
| `balanced` | sonnet | the person's own choice |
| `deep` | opus | the person's own choice |

Import turns `haiku`, `sonnet` and `opus` into `fast`, `balanced` and `deep`, and leaves other vendor model names unset.

## A skill

```markdown
---
name: release-checklist
description: Walk through the release checklist. Use when the user asks to cut a release.
---
# Release checklist

1. Check the changelog. The format is in [references/changelog.md](references/changelog.md).
2. Run `scripts/check-version.sh`.
```

The description is what the tool reads at startup to decide when to load the skill, so say what it does and when to use it.

## A knowledge base

```
runbooks/
  loadout.yaml
  README.md
  deploy/rollback.md
  oncall/escalation.md
  hosts.tsv
```

- Documents: `.md`, `.markdown`, `.txt`, `.rst`, `.adoc`, `.csv`, `.tsv`, `.json`, `.yaml`, `.yml`, `.mmd`, `.mermaid`, `.drawio`.
- The first `# Heading` of a Markdown file is its title in the index; other files use their file name.
- With more than 150 documents the index lists folders instead of every document, so keep related documents in folders with clear names.

## Names and paths

- Package names: lowercase letters, digits and single hyphens, at most 64 characters.
- Versions: `MAJOR.MINOR.PATCH`, each new one higher than the last.
- File paths: letters, digits, `.`, `_` and `-`; at most 8 folders deep; no hidden files or folders; no two names that differ only in case.

## Limits

| Kind | Files | Per file | Total |
| --- | --- | --- | --- |
| agent | 16 | 512 KB | 1 MB |
| skill, knowledge base | 5,000 | 2 MB | 64 MB |

An upload is at most 32 MB compressed. Every file counts, `loadout.yaml` included.
