# Demo: run loadout locally with fake teams and data

Status: built.

## Goal

Someone who clones the repository sees loadout working in one command, with a populated registry they can click through in the browser and a CLI they can try, without Go and without touching their own `~/.claude` or `~/.loadout`.

## What exists

- `scripts/preview/`: bash scripts that build the binary, create project `acme` with teams `lime` and `koala`, people alice, bogdan, ada and olga, agents, a skill, a KB with diagrams, proposals with reports, a course and deletion requests. They publish through a running server with tokens and need Go, bash, curl, Python (cookie proxy) and Node (screenshots). Built for UI screenshots, not for newcomers.
- `deploy/container/Dockerfile`: a distroless registry image built from a GitHub release. No data, no shell.
- Login: on when `LOADOUT_PUBLIC_URL` is set. `LOADOUT_LOGIN=simple` gives password sign-in, `LOADOUT_ADMIN_PASSWORD` makes the first admin, `loadout user add` makes more. `LOADOUT_DEV=1` logs mail instead of sending it.

## Design

### 1. `docker compose up`

`compose.yaml` at the repository root with two services.

- `registry`: built from the checkout by a new `deploy/demo/Dockerfile` (golang stage builds `./cmd/loadout`, distroless stage runs it). On start it runs `loadout demo seed --data /data` (a no-op when `/data` already holds a registry), then `loadout serve`. Published on `127.0.0.1:8080` only. Data in a named volume, so a restart keeps edits and `docker compose down -v` starts over.
- `cli`: same build, a small Debian stage with a shell, git and the binary, under a profile so `up` does not start it. `docker compose run --rm cli` opens a shell as alice, already logged in to the registry, in a sample project folder.

The first `up` prints the URL and the demo accounts.

### 2. Demo mode

`LOADOUT_DEMO=1` on `serve`:

- Turns on simple login and gives each demo account the shared demo password.
- Shows a banner on every page ("Demo registry, fake data, resets with docker compose down -v") and lists the accounts and the fixed password on the sign-in page. No guided tour or try-this panel.
- Implies `LOADOUT_DEV=1` mail (logged, never sent) and refuses Google sign-in.
- Refuses a data dir that `demo seed` did not create (a marker in the database).
- Refuses to start unless `LOADOUT_PUBLIC_URL` is a localhost URL. The fixed password must never reach a reachable registry. (Inside compose the listener is `0.0.0.0` in the container but published on `127.0.0.1` on the host.)

### 3. `loadout demo seed`

A Go command that writes the story straight through the registry, account and memory services (no server, no tokens, no shell), so it runs in the distroless image and is tested like any other command.

The story, all fake: project `acme` with teams `lime` (web) and `koala` (mobile), and project `globex` with teams `pear` (web) and `otter` (releases). People: alice maintains lime and is a member of koala and pear, bogdan is a lime member, olga owns acme, chen owns globex, dana maintains pear, emil is an otter member, ada is an admin. Each project has two pipeline templates and each team binds one to its own agents. Team memory is on everywhere, with edits, a retraction and a move. Content:

- agents, a skill and a KB with Mermaid and draw.io diagrams, several releases each so the history and diff pages have something to show
- proposals: one open with a report, one approved, one rejected, one behind
- a deletion request
- a pipeline template `acme/dev-loop` and lime's binding to it, so the pipeline pages draw a diagram
- team memory entries in lime, a few marked new this week
- a wiki and a short course for lime

It moves its clock back so entries spread over the past weeks. The fixtures are shared with the e2e tests where they overlap, and the preview scripts switch to `loadout demo seed` for the common part.

### 4. The CLI container

The shell starts in `/work/sample-app` (a small git repo with a README), set up for both Claude Code and GitHub Copilot, so an install writes `.claude/` and `.github/` files side by side. A sample personal memory file is there for `loadout memory import`. The MCP bridge is documented, not demoed. The prompt banner suggests five commands: `loadout status`, `loadout install lime/planner`, `loadout sync`, `loadout propose ...`, `loadout new pipeline ...`. Files appear in `.claude/` and `.github/` of the sample repo so people can see what an install writes. No Claude Code or Copilot inside the container. loadout runs no model.

### 5. Docs

README gets a "Try it" section at the top: `docker compose up`, open the URL, sign in, then `docker compose run --rm cli`. GUIDE gets a short demo section. What's new gets a bullet.

## Tests

- `demo seed` on an empty dir produces the expected counts, a second run changes nothing, a non-empty non-demo dir is refused.
- Demo mode refuses a non-localhost public URL, shows the banner, lists accounts, never offers Google.
- e2e: seed, serve, sign in as each account, open each main page with a 200, CLI login and install as alice.
- `docker compose config` in CI, an image build in the release job.

## Out of scope

- A hosted public demo.
- Running real models in the demo.

## Decisions (2026-10-13)

1. Local only. A hosted public demo is a later, separate plan.
2. Built from source by compose. A prebuilt `ghcr.io/petervlad/loadout-demo` image comes with the first release that has a release pipeline for it.
3. Two projects (`acme` with `lime` and `koala`, `globex` with `pear` and `otter`), several packages per team and per project, backdated over five weeks. Superseded on 2026-10-13 by the owner: it was one project and about ten packages.
4. Data only in the UI: banner and the account list, no tour.
5. Demo mode is in the normal binary, behind hard guards (localhost public URL, a data dir seeded by `demo seed`).
6. One fixed demo password, shown on the sign-in page.
7. The sample project targets both Claude Code and Copilot.
8. `scripts/preview/seed.sh` calls `loadout demo seed` for the shared world, the screenshot-only extras stay as scripts on top.
9. Ships in this public sync, before the push.
10. Memory is shown through seeded entries in the UI and `memory import` in the shell, no MCP demo.

## As built

- The distroless image has no shell to chain a seed and a serve, so `loadout demo up` does both. `demo seed` and the marker (SQLite `application_id`) are as planned.
- The `cli` service shares the registry's network (`network_mode: service:registry`), so the registry is `localhost` there as well. `memory import` and the sign-in origin checks need that address.
- Web deletes need a backup folder, so the registry image sets `LOADOUT_BACKUP_DIR=/backups`, outside `/data` so the volume stays empty for the seed.
- The compose file is checked and both images are built in CI (`demo` job). A release job image waits for the prebuilt image of decision 2.
- No Docker was available where this was built, so `docker compose up` itself was not run. The binary, the shell script and the preview scripts were run natively.
