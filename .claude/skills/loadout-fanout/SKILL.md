---
name: loadout-fanout
description: >-
  Split a larger loadout change across parallel agents safely: one git worktree and branch per
  agent with disjoint files, clear briefs, then merge, resolve conflicts, run make check and clean
  up. Use when the user says "fan out", "use agents", "in parallel", or when a plan has independent
  parts (for example CLI, web and tests).
---

# Fan out loadout work

1. **Settle the shared core first**, yourself: types, interfaces and migrations every part depends on. Commit it to a feature branch (e.g. `skills-kb`). Agents that all edit the core collide.
2. **One worktree per agent**, from that branch, outside the repo:
   ```sh
   cd ~/personal/loadout
   git worktree add -q -b <feature>-<part> ~/personal/loadout-wt/<part> <feature>
   ```
   (The Agent tool's own worktree isolation needs the session's folder to be a git repo; `~/work` is not, so create worktrees by hand.)
3. **Brief each agent completely**: the worktree path and "work only there"; `export PATH=/opt/homebrew/bin:$HOME/go/bin:$PATH`; what to read first (`CLAUDE.md`, the plan); its exact scope (packages and files) and what is out of scope; the tests it must add; "`make check` must pass"; commit rules (short plain messages, no Co-Authored-By trailer, no em-dashes); "do not push or deploy"; and a short final report (files, checks, anything it changed outside its scope, questions). Use the project agents (`loadout-dev`, `loadout-ui`, `loadout-tester`, `loadout-planner`) as `subagent_type` when they fit.
4. **Never edit an agent's worktree while it works.**
5. **Merge one branch at a time** into the feature branch: `git merge --no-edit <branch>`, then `make check`. Common conflicts: two agents fixing the same lint warning, and `golangci-lint fmt` reflowing the same core files; keep one version. `git merge --squash` a branch whose middle commits do not build.
6. **Answer agent questions** with SendMessage to the same agent, which keeps its context; ask it to merge the feature branch into its worktree first if it needs your newer commits.
7. **Clean up**: `git worktree remove --force ~/personal/loadout-wt/<part>` and `git branch -D <feature>-<part>` for each, then merge the feature branch to `main` and ship it with the release steps in CONTRIBUTING.md if the user wants it live.
