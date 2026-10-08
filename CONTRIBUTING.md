# Contributing

Thanks for helping. This file covers how to build, check and change loadout.
`CLAUDE.md` holds the one rule and the code and writing rules; `docs/DESIGN.md`
is the architecture. Read both before changing code.

## The one rule

loadout never runs a model and never holds an AI key. A feature that calls a
model from the registry, or needs a vendor key, will not be accepted, however
useful. Everything that runs an agent (only `loadout review`) runs on the
user's machine in their own tool.

## Build and check

- Go 1.27 or later, [golangci-lint](https://golangci-lint.run) v2, and for the
  guide [pandoc](https://pandoc.org). `shellcheck` and `caddy` are optional
  locally; CI runs them.
- `make build` builds `./bin/loadout`. `make check` runs formatting, lint,
  `go test -race -cover ./...` and govulncheck. It must pass before a commit.
- `sh docs/guide/build.sh` renders `docs/GUIDE.md` and `docs/CHANGES.md` into
  `deploy/site/docs/`. Commit the result with the change.
- `sh scripts/notices.sh` rewrites `THIRD_PARTY_NOTICES.md` after a Go
  dependency, a vendored library or a borrowed skill changes; CI runs it with
  `--check`. A skill copied into `.claude/skills` must keep its `LICENSE`.
- `make release-snapshot` builds every release archive locally (needs
  goreleaser).

## Code rules

The full list is in `CLAUDE.md`. In short:

- Dependencies point inwards: `internal/cli` parses flags, calls a service and
  prints; services are built in `cmd/loadout` and reach commands through
  `cli.Deps`. The code that uses something owns the small interface for it.
- Add behaviour through the extension points (the kinds table, one adapter per
  AI tool, `Store` interfaces with shared contract suites), not with a `switch`
  spread over packages.
- Errors wrap with `%w`; the registry's sentinels decide HTTP status. Messages
  are plain sentences that say what to do next.
- Files go through `os.Root` or the install `tree`; paths from the network pass
  `pack.CheckPath`; never write or delete through a symlinked folder.
- Table-driven tests, and a test that pins the behaviour before the change. New
  database columns get a numbered migration and a default that keeps old rows
  valid.

## Plan, then implement

Small fixes go straight to a pull request. A larger feature starts with a plan
in `docs/plans/<feature>.md`: the goal, what exists today, the design, data and
API changes, migration, tests, risks and what is not in the step. Open an issue
or a draft pull request with the plan first so the design is agreed before the
code. Then build it in small steps, each ending with `make check` green, and
note in the pull request where you deviated from the plan.

## Writing rules

- No em-dashes anywhere: code, comments, docs, commits or UI text.
- Commit messages are short and plain, say what changed, and carry no
  Co-Authored-By trailer.
- Comments say why, at the density of the file you are in.
- Change user-visible behaviour, change the docs: `docs/GUIDE.md` (then rebuild
  it), `docs/DESIGN.md` and `README.md` where they describe it.

## Pull requests

- One topic per pull request, with tests, and `make check` green.
- Contributions are accepted under the project's MIT licence (inbound equals
  outbound); there is no CLA. A `Signed-off-by` line is welcome, not required.
- AI-assisted contributions are welcome and get the same review as any other.
  You are responsible for what you submit.

## Add a provider recipe

The host skills (`internal/builtin/host/loadout-host-provision/`) have one file
per cloud in `references/providers/`. To add one:

1. Copy the closest existing provider file and keep its level 2 headings in the
   same order (Before you start, Create the machine, Firewall, Name, Deploy,
   Off-host backups, Cost, Tear down).
2. Add a `Verified how:` line with a date that says whether you ran the recipe
   or only checked it against the provider's documentation, a `Sources:` list,
   and a `Shape:` line naming a shape file (`vm.md` or `container.md`).
3. Add one row to the provider table in `SKILL.md`, listing the file exactly
   once.
4. Run `go test ./internal/builtin/`. The recipe lint checks the shape, that no
   command asks for, reads or prints a secret, and that every `loadout` command
   uses real flags.

## Add an email recipe

Email providers for sign-in mail live in
`internal/builtin/host/loadout-host-setup/references/email/`, one file each, listed in
the provider table in that skill's `SKILL.md`. loadout speaks SMTP with
STARTTLS, so a recipe says how to get SMTP credentials from the provider, the
host, port and the sender-domain DNS records (SPF, DKIM, DMARC), any port limit
of the cloud it is likely used from, and how to prove it with `loadout mail
test`; follow `resend.md` for the shape. Secrets are typed by the person into the settings file; the recipe never
asks an agent to read or print one. Same lint, same `go test
./internal/builtin/`.

## Report a bug

Open an issue with the bug template: what you ran, what you expected, what
happened, and the output of `loadout version`. Remove tokens, invites, emails
and passwords from anything you paste. `~/.loadout/loadout.log` and `loadout -v`
help; check them for secrets first. A security problem is not a bug report:
follow `SECURITY.md` instead.

## What's new

Every deploy that changes something a person sees adds a section to
`docs/CHANGES.md` first.

1. `sh scripts/changes-since.sh` prints the commits since the newest
   `<!-- through HASH -->` marker.
2. Draft one item per user-facing change, merged across commits, each
   `- Feature: ` or `- Fix: ` and a plain sentence ending in a full stop. Say
   what a person can now do and where (page or command), not how. Drop
   refactors, tests, plans and scripts. Security fixes say what is safe now,
   not the attack. Name no clients or teams.
3. Put them under `### D Month YYYY` inside `## Month YYYY`, with
   `<!-- through HASH -->` under the date.
4. A person reads and approves the draft before it ships.
5. `sh docs/guide/build.sh && go test ./docs/guide/`, then commit
   `docs/CHANGES.md` and `deploy/site/docs/`.

## Releases

1. `make check` on a clean checkout of the branch to release.
2. Back up the registry database first when the change adds a migration
   (`deploy/README.md` shows the backup command).
3. Deploy with `deploy/deploy.sh` (see its header for the variables and
   `deploy/README.md` for a walk-through), then check
   `https://YOUR-REGISTRY/v1/health` and the sign-in page.
4. To cut a release, tag a clean commit `vX.Y.Z` and push the tag; the
   release workflow builds the archives, checksums, SBOMs and build
   provenance with GoReleaser (`make release-snapshot` tries it locally). A registry advertises
   a newer release to older CLIs only when it was built from a clean tag.
5. Never push or deploy without the maintainer's say-so, and never put a token
   in git config or in a file git tracks.
