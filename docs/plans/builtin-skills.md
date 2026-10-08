# Built-in skills

Status: built and deployed (2026-09-29).

## Goal

A person's AI agent should know how to use loadout without anyone writing
that down for each team. loadout ships four skills inside its binary and
installs them for the person when they sign in. Because the text travels
with the CLI, it always names the commands and flags the installed CLI has.
Nothing here runs a model: the skills are files the person's own tool reads.

## The four skills

| Skill | Loads when the person wants to |
|-------|--------------------------------|
| `loadout-setup` | install the CLI, sign in, join teams, install, sync, status, uninstall, roll back, choose update policies, fix a problem |
| `loadout-author` | create or import an agent, skill or KB, try it locally, publish or propose a version |
| `loadout-review` | review a teammate's proposal: run `loadout review TEAM/NAME`, read the report, approve, reject or withdraw |
| `loadout-admin` | manage members, roles, invites, machine tokens, require review, model settings |

Each is a normal skill: `SKILL.md` with `name` and a `description` that says
when to load it, short, with `references/` files for tables it only needs
sometimes. Every skill carries the same guardrails:

- Ask the person before anything outward-facing or team-visible: publish,
  propose, review approve, reject or withdraw, member invite, add, set or
  remove, namespace set, token create or revoke.
- Never read, print or pass a token. The person runs `loadout login`
  themselves; never open `~/.loadout/credentials.yaml`.
- Never pass `--force` over an edited file without asking.
- `loadout review` runs a teammate's package with the tool's full access;
  say so and offer `--safe`.
- The one rule: loadout never runs a model and holds no AI key.

Company specifics (registry address, which team to join, local rules) are
not in these skills. Each skill tells the agent to look for an installed
skill named `<project>-loadout` or `loadout-local` and prefer it. A project
publishes that as a normal L0 skill; the guide shows how.

## Where the text lives

`internal/builtin/skills/<name>/` holds the files, embedded with `embed.FS`.
`builtin.Packs` turns each folder into a normal `*pack.Pack` (kind `skill`)
with a generated manifest, so the skill format is validated by the same
`skill.Parse` and `pack.FromFiles` as any package.

## Identity in the lock

- Package id `loadout/<skill>` (`loadout/loadout-setup`). The skill name must
  equal the package name, and the folder must be `~/.claude/skills/loadout-setup`,
  so a pseudo name such as `loadout/builtin-setup` would need a rename path
  through the installer for no gain. The namespace `loadout` makes the id
  readable in `status` and `uninstall`.
- Lock `source: builtin`, no remote. `sync`'s registry pass already skips
  entries without a remote, and only entries with source `builtin` are
  touched by the built-in pass, so a registry team that happens to be called
  `loadout` is never mistaken for them (its entry has a URL as source and is
  left alone).
- Version: the CLI version (`v1.4.0-3-gabc` counts as `1.4.0`); a build
  without a release version (`dev`) uses `0.0.1`. The digest covers the
  manifest and the text, so any change to the text is a new digest.
- Policy `auto`; `loadout policy loadout/loadout-setup pin` holds a version.

## Behaviour

`builtin.Service` (in `internal/builtin`) owns the packs, a small state file
`~/.loadout/builtin.yaml` (`wanted`, `declined`), the installer and the
detector. Consumers own interfaces with only what they call.

- `loadout login`: after a successful sign-in, `Want` then `Ensure`. A
  failure only warns; the sign-in stands.
- `Ensure`: nothing unless wanted. For each skill that is not declined and
  not a registry package's entry: install for the detected tools when it is
  missing, when the digest differs from the lock (a new CLI), or when a tool
  appeared that it is not installed for. An up-to-date install is not touched,
  so an edited `SKILL.md` never blocks or nags until there is a new version;
  then the installer refuses to overwrite it and the report says so.
- No tool folder (`~/.claude`, `~/.copilot`): nothing is written and no
  folder is created. Login prints one line naming both tools, the next
  `loadout sync`, and `loadout setup --skills --target claude`. The wish is
  already recorded (`wanted`), so `sync` installs once a folder appears.
- `loadout sync`: after the registry pass, `Ensure`; quiet when nothing
  changed or no tool is found (cron).
- `loadout setup --skills [--target claude,copilot] [--force]`: clears the
  opt-outs, sets `wanted` and installs now, for the given targets (which may
  create the tool's folder, since the person asked) or the detected ones.
- `loadout uninstall loadout/loadout-review`: the normal uninstall, then the
  skill is recorded as declined, so login and sync never bring it back.
  `setup --skills` is the way back.
- `status` lists them like any package. The working folder never matters:
  they are always user scope.

## Honesty test

`internal/cli/builtin_skills_test.go` reads the embedded skills, extracts
every `loadout ...` command from code spans and code blocks, walks the cobra
tree from `NewRoot`, and fails when a subcommand or a flag does not exist on
the command it is given to. Every `--flag` anywhere in the text must exist on
some command. It also fails on an em-dash. Frontmatter is linted by building
the packs (`skill.Parse`, name equals folder, description limits).

## Tests

- `internal/builtin`: packs build; Ensure installs, is quiet when unchanged,
  updates on a new version, keeps an edited file, respects declined and pin,
  installs for a tool that appears, does nothing without a tool and without
  wanted; Setup with targets.
- `cmd/loadout` e2e: login installs them; login on a machine with no tool
  folders prints the line and creates nothing; sync after `~/.claude`
  appears installs them; uninstall then login does not reinstall; `setup
  --skills` brings it back.

## Website (added during the work)

- `/skills` (cards) and `/skills/{name}` (SKILL.md and its files as safe
  Markdown, the install line, the CLI version), readable by anyone, signed
  out included. `web.UI.Skills` gets the packs `builtin.Packs` builds, so the
  site shows what the registry build ships.
- A sidebar entry above the projects, "loadout skills", marked "Built in".
- Not a namespace: `registry.Reserved` refuses new namespaces `loadout` and
  `skills`; the API server mounts `/skills` on the pages.

## Docs

GUIDE.md: a section on the built-in skills and on writing a project's local
skill; command reference; troubleshooting. DESIGN.md: the package, the lock
identity, the rules. README: one paragraph.

## Not in this step

- Project-scope built-ins (a repository can install the project's own
  `loadout-local` skill with `--scope project` already).
- Translating the skills; they are English like the CLI.
