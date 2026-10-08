---
name: implement
description: "Implement a piece of work based on a spec or set of tickets."
disable-model-invocation: true
---

Implement the work described by the user in the spec or tickets.

Use /tdd where possible, at pre-agreed seams.

Run typechecking regularly, single test files regularly, and the full test suite once at the end.

Once done, use /code-review to review the work.

Commit your work to the current branch.

## In loadout

- Read `CLAUDE.md` and the plan in `docs/plans/` first; follow the `loadout-dev` agent rules for Go and the `loadout-ui` agent rules for pages.
- "Typechecking" is `go build ./... && go vet ./...`; single tests are `go test -race ./internal/<pkg>/ -run <Test>`; the full suite is `make check`, which must pass before the commit.
- Commit messages: short, plain, human, no Co-Authored-By trailer, no em-dashes. Never push or deploy from here; that follows the release steps in CONTRIBUTING.md, on request.
