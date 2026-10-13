# Model settings per AI tool

loadout only writes the model name into the installed agent file. The person's own tool runs it on their own subscription.

| | Claude Code | Copilot (CLI and VS Code) |
| --- | --- | --- |
| Model value | an alias (`haiku`, `sonnet`, `opus`) or a full ID (`claude-sonnet-5`) | the name the model picker shows (`Claude Sonnet 5`) |
| Written as | `model: opus` | `model: Claude Sonnet 5 (copilot)` for VS Code and `models: [claude-sonnet-5]` for Copilot CLI |
| Nothing set | haiku, sonnet, opus | nothing: the person's own choice |
| Reasoning effort | `effort:` low, medium, high, xhigh or max | `reasoning-effort:`, the same five levels |
| Context size | in the model: `opus[1m]` for 1M tokens | not in the agent file: chosen in the model picker |

Copilot caveats to tell the person:

- With the Copilot model picker on Auto, Copilot uses the session's model for every agent and ignores the setting. Pick a model in the picker for the settings to apply.
- With HydraFusion (a Copilot setting in research preview) chosen in the model picker, Copilot chooses or combines models for each task and may ignore the setting, as on Auto. It is not yet known whether it keeps an agent's model. Pick a single model in the picker for the settings to apply.
- A model not allowed on the person's Copilot plan falls back to the session's model without an error.
- A personal `subagents` setting in `~/.copilot/settings.json` wins over the file.

How a change arrives: on the next `loadout sync`, `auto` installs write the agent again, `notify` installs report "model settings changed" (`loadout install TEAM/NAME` applies it), `pin` installs stay. Skills, knowledge bases and agents without a hint are not touched, and an edited file is never overwritten.
