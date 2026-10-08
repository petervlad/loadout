# loadout

A Go CLI plus a registry that syncs a team's AI agents, skills and knowledge bases (KB) into Claude Code and GitHub Copilot, with layers (a project L0 above teams L1), accounts and roles, and review of proposed versions.

## The one rule

loadout never runs a model and never holds an AI key. Everything that runs an agent (only `loadout review`, and its alias `review compare`) runs on the user's own machine in their own tool. Never add a feature that calls a model from the registry or needs a vendor key.

## Read first

- `docs/DESIGN.md`: packages (Go), layers, roles, login, package kinds, rules that protect users. The source of truth for architecture.
- `docs/GUIDE.md`: the user guide (rendered to `deploy/site/docs/` by `docs/guide/build.sh`). Update it when user-facing behaviour changes, then rebuild.
- `docs/CHANGES.md`: What's new, one section per deploy (rendered to `deploy/site/docs/whats-new/` by the same script). Draft the entries from `scripts/changes-since.sh` before each deploy; a person approves them (see CONTRIBUTING.md).
- `docs/UI.md`: web UI rules and the Apple HIG review record.
- `docs/plans/`: one plan per larger feature.

## Build and check

- Go 1.27 or later must be on `PATH` (check `go version`; a system `go` may be older).
- `make check` runs golangci-lint fmt and run, `go test -race -cover ./...` and govulncheck. It must pass before any commit that is meant to ship.
- `go install ./cmd/loadout` updates the CLI on this machine.
- UI previews and screenshots: `scripts/preview/README.md`.

## Code rules

- Dependencies point inwards: `internal/cli` only parses flags, calls a service and prints; services are built in `cmd/loadout` and reach commands through `cli.Deps`. Consumers own small interfaces (`install.Renderer`, `registry.Store`, `web.Teams`).
- Extension points, used instead of switches spread around: the kinds table (`internal/pack/kinds.go`) plus `target.Render`; one adapter per AI tool (`internal/target/*`); `Store` interfaces with shared contract suites (`registrytest`, `accounttest`).
- Errors wrap with `%w`; sentinels (`registry.ErrNotFound`, `ErrForbidden`, `ErrInvalid`, `ErrExists`) decide HTTP status and messages. User-facing sentences are plain and end with a full stop.
- Files on disk go through `os.Root` or the install `tree`; paths from the network pass `pack.CheckPath`. Never write or delete through a symlinked folder.
- YAML is decoded with `strictyaml` (unknown fields refused) unless keeping unknown fields is the point (skill frontmatter keeps them verbatim).
- Table-driven tests; e2e tests in `cmd/loadout` drive the real binary with `person`/`newSession` helpers.
- New database columns get a numbered migration in `internal/store/sqlitestore/migrations` and a default that keeps old rows valid.

## Writing rules

- Never use em-dashes, in code, comments, docs, commits or UI text.
- Commit messages: short, plain, human; no Co-Authored-By trailer.
- Comments explain why, at the density of the surrounding code.

## UI rules

Every change to a page (registry pages, docs page) follows, in this order:

1. The rules here and in `docs/UI.md` (server-rendered, no frameworks, strict CSP, works without JavaScript, tokens in light and dark, 390 px phones).
2. The `apple-design` skill, if you have it installed (`~/.claude/skills/apple-design`, from [dickwu/apple-design-skill](https://github.com/dickwu/apple-design-skill); it is not in this repository), for look, feel and HIG review.
3. The rampstack UI skills for general UI development: `design-standards`, `frontend-component-build`, `form-strategy`, `information-architecture`, `design-system`, `accessibility-audit`, `performance-optimization`, `qa-testing`.

The `loadout-ui` agent and the `loadout-preview` skill (screenshots) do the work.

## Borrowed skills

Update one by fetching it again from its repo and naming the new commit here. Changes for loadout live in an "In loadout" section at the end of a SKILL.md, so an update is a copy plus that section. Then run `sh scripts/notices.sh` (it rewrites `THIRD_PARTY_NOTICES.md` and fails on a skill without its LICENSE).

**[mattpocock/skills](https://github.com/mattpocock/skills)** (MIT, commit d81f3a1, each with its LICENSE): the 27 skills his Claude Code plugin ships, for the daily engineering flow. Start with `/ask-matt` for the map; `/grill-with-docs` before a change, `/to-spec` and `/to-tickets` to plan it, `/implement` or `/implement-spec` to build, `/tdd`, `/diagnosing-bugs`, `/code-review`, `/prototype`, `/handoff`. Not copied: his `misc/` and `in-progress/` skills (TypeScript tooling and beta ones). `/code-review` here is his skill; it shadows Claude Code's built-in command of the same name in this repo. To learn the flow, take his free seven-lesson email course [AI Skills for Real Engineers](https://aihero.dev/skills/subscribe): grill, prototype and handoff, spec and tickets, AFK runs in [Sandcastle](https://github.com/mattpocock/sandcastle), review, then the whole loop. Every skill it teaches is here; Sandcastle is a TypeScript tool, not a skill, and is not set up in this repo.

**[rampstackco/claude-skills](https://github.com/rampstackco/claude-skills)** (MIT, commit 3d4510a, each with its LICENSE). They are written for websites in general: apply them to this one binary, its SQLite database and the host, and keep the rules above when they disagree.

- UI: the seven skills listed under UI rules, plus `accessibility-audit` on every UI change.
- `launch-runbook`: moving to another host or domain.
- `security-baseline`: before a security review, and when headers, TLS or secrets change.
- `backup-and-disaster-recovery`: recovery targets and a tested restore of the registry.
- `monitoring-and-alerting`: uptime checks and alerts for the registry.
- `beta-program-management`: rolling out to more teams.
- `incident-response`: when a live registry is down or wrong.
- `documentation-strategy`: when the docs in `docs/` need sorting out.

**[dickwu/apple-design-skill](https://github.com/dickwu/apple-design-skill)** (commit 904b0ee) is optional and installed per user at `~/.claude/skills/apple-design`. It has no license: never copy it into this repo.

## Agent skills

### Issue tracker

Local files: specs in `docs/plans/<feature>.md`, tickets in `.scratch/<feature>/issues/`. See `docs/agents/issue-tracker.md`.

### Triage labels

The five default roles, as a `Status:` line in each ticket. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` (lazily) and `docs/adr/`, with `docs/DESIGN.md` as the source of truth. See `docs/agents/domain.md`.

## Safety

- `secrets.env`, `*.env` and credentials files hold tokens and keys; never print, echo or commit them, and never put a token in git config.
- A live registry holds real teams' data. Back up before a deploy that migrates the database, and ask before deleting anything there.
- The apple-design skill has no license: use it as a review checklist only, never copy its content into this repo.
- Personal notes for one maintainer's checkout (their hosts, repositories and accounts) go in `CLAUDE.local.md`, which git ignores and Claude Code reads. Never put such names in a file git tracks.
