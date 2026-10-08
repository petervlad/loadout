# Troubleshooting loadout

Find the message the person saw, then do what the right column says. Ask before anything that needs `--force`.

| Message or symptom | What it means | What to do |
| --- | --- | --- |
| "file(s) would be overwritten, use --force to overwrite" | A file is in the way that loadout did not write, or that the person edited. The message names it. | Show the person the file. Rename or move it, or, only if the person says it can go, run the command again with `--force`. |
| "Form expired" on the website | The page was open too long or came from another tab. | Go back, reload the page and try again. |
| "missing or invalid token" | The login token expired (after 90 days) or was revoked. | The person runs `loadout login` again. |
| "you are no longer a member of payments; run loadout login again" | A maintainer removed the person from that team. `list` and `install --all` skip it. | `loadout login` again updates the teams on this machine. |
| "this login token is for payments only" | A sign-in from before one login covered all teams. | `loadout login` once; it replaces the older sign-ins of that registry. |
| A new team does not show up in `loadout list` | The token already works there; this machine does not know the team yet. | `loadout login` again. |
| An agent or skill does not show up in the tool | Tools read agents and skills only when a session starts, or the tool was skipped. | Start a new session. Check `loadout status` for where the files went. A skipped tool may have no folder yet: install with `--target claude` or `--target copilot`. |
| "no supported AI tool found" | Neither `~/.claude` nor `~/.copilot` exists (or `.claude` and `.github` in a repository). | Start the tool once, or use `--target`. |
| "--scope project needs a git repository" | `--scope project` outside a repository. | Run it inside a git repository, or use `--scope user`. |
| A package shows as "shadowed" | A closer layer (the team) ships a package with the same name, and its file wins. | Nothing: that is expected. |
| "compressed draw.io file" | A draw.io diagram saved compressed; packages hold text only. | In draw.io turn off File > Properties > Compressed, save, and publish again. |
| "held back by your edits" in `loadout sync` (exit code 2) | An update or new model settings would overwrite a file the person edited, so loadout kept their version. | Offer `loadout install TEAM/NAME --merge`, which takes the release and keeps the edit. Or keep the edit and stop updates with `loadout policy TEAM/NAME pin`, or ask before `loadout install TEAM/NAME --force`, which replaces the edit. `loadout diff TEAM/NAME` shows what the release changed. |
| "update available, you edited 1 file" in `loadout sync` | A newer version is out (policy notify), and installing it would stop at the person's edit. | The same choices as "held back by your edits". |
| A session starts with "loadout: 1 update is held back by your edits" | The session hook found an update the person's edits hold back. | Mention it once, run `loadout status` with them, and explain the choices. `loadout setup --no-hooks` removes the hook if they do not want it. |
| "left ~/.claude/settings.json alone" from `loadout setup --hooks` or login | The file is a symbolic link or not valid JSON, so loadout did not change it. | Show the person the entry loadout printed; they add it under `hooks.SessionStart` themselves, or fix the file and run `loadout setup --hooks` again. Do not edit their settings without asking. |
| "1 file has conflict markers" after `loadout install --merge`, or "merged: resolve the conflicts in ..." in `loadout status` | The person and the release changed the same lines; the file holds both between markers. | Show both sides, agree with the person what stays, and delete the marker lines. To start over with the release instead, ask before `loadout install TEAM/NAME --force`. |
| "still has conflict markers from the last loadout install --merge" | An earlier merge left markers that are not resolved, so loadout will not merge another release into them. | Help the person resolve them first, then run `--merge` again; or ask before `loadout install TEAM/NAME --force`, which discards their edits. |
| "--merge keeps your edits and --force replaces them; choose one" | Both flags were given. | Ask which the person wants. |
| `loadout sync` exits with code 2 | An auto update is held back by an edit. A failed registry check is code 1. | Resolve the hold as above; `loadout policy TEAM/NAME pin` is the quiet way to keep the edit. |
| "loadout 0.5.0 is out (you have 0.4.0)", or "loadout, for the agent: a newer loadout CLI is available" | A registry runs a newer loadout release than the person's. Nothing failed. | Tell the person once, with the update command loadout printed (from a clone: `git pull && go install ./cmd/loadout` in their loadout folder). Do not update it yourself unless they ask. |
| "registry.example.com needs loadout 0.3.0 or newer and this is 0.2.0, so some commands may fail" | That registry no longer supports this loadout; it is said after every command. | Say this is likely why a command failed, suggest updating with the command it printed, then run the command again. |
| "which registry? pass --remote ..." | No registry is known on this machine yet. | Ask for the address (or read it from the local `<project>-loadout` skill), then the person runs `loadout login --remote URL`. |

## Where files land

| | Claude Code | Copilot |
| --- | --- | --- |
| Agents, user scope | `~/.claude/agents/NAME.md` | `~/.copilot/agents/NAME.md` |
| Agents, project scope | `.claude/agents/NAME.md` | `.github/agents/NAME.agent.md` |
| Skills and KBs, user scope | `~/.claude/skills/NAME/` | `~/.copilot/skills/NAME/` |
| Skills and KBs, project scope | `.claude/skills/NAME/` | `.claude/skills/NAME/` (the same folder) |

## Checking in the tool

- Claude Code: `/agents` lists the installed agents. Skills and KBs are picked up automatically.
- Copilot CLI: `/agent` picks an installed agent. Skills are picked up automatically.
