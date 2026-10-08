# A HydraFusion warning on the models page

Status: built (2026-10-03).

## Goal

Tell the people who set models, and the people who use them, that Copilot's
HydraFusion setting may make Copilot ignore the model set for an agent.

HydraFusion (research preview, in Copilot CLI since September 2026 and in
VS Code and the Copilot app since 2026-09-30) is turned on in Copilot's
settings ("Agents > HydraFusion") and then chosen in the model picker, like
Auto. Copilot then picks a model, or several, for each task. GitHub does not
say whether it keeps a model declared in an agent file. One user's log shows
subagents keeping their own model, but nothing confirms it. Until we have
checked it by hand, we say it may ignore the setting.

Sources: github.blog/changelog/2026-09-30-hydrafusion-in-vs-code-and-the-github-copilot-app,
github.com/orgs/community/discussions/206492.

## Design

The models page (`/t/{ns}/tiers`) already shows each tool's `ModelNotes()`
under that tool's table, so a new note needs no template, CSS or handler
change. One source, the Copilot adapter, feeds the page; the guide and the
admin skill say the same in their own words.

New note, third in `copilot.Adapter.ModelNotes` (after Auto, before the plan
fallback, since it is the same kind of picker choice as Auto):

> HydraFusion, a Copilot setting in research preview, lets Copilot choose or
> combine models for each task. When it is chosen in the model picker, Copilot
> may ignore the model set here, as it does on Auto. Pick a single model in the
> picker for these settings to apply.

Plain sentences, full stops, no em-dashes. Wording stays "may" until the hand
check below.

## Files

1. `internal/target/copilot/copilot.go`: add the note to `ModelNotes`; change
   the doc comment ("the two ways" becomes "the ways") and the block comment
   above `Adapter` ("ignores agent models while the picker is on Auto" gains
   "and may on HydraFusion"), with the two source URLs in the adapter's list.
2. `internal/web/ui_tiers_test.go`: `wantIn` "HydraFusion" next to
   "model picker is on Auto" for the owner and the maintainer view.
3. `docs/GUIDE.md`, "Models for agents": "Two things to know about Copilot"
   becomes "Three things", with the same note. Rebuild with
   `docs/guide/build.sh` so `deploy/site/docs/index.html` matches.
4. `internal/builtin/skills/loadout-admin/references/models.md`: a third
   Copilot caveat, so an agent helping an admin says it too.
5. `docs/plans/model-tiers.md`: one line under Risks pointing here.

## UI check

The note is one more line in an existing list. Take screenshots of
`/t/lime/tiers` with the `loadout-preview` skill in light and dark, at 390 px
and desktop, and check the list still reads well (HIG checklist,
`accessibility-audit`: plain text, no new controls).

## Tests and release

- `make check` passes.
- Deploy with `loadout-deploy`. No migration, so no database change; the
  usual backup still runs.

## Open: check by hand, then firm up the wording

In VS Code and in Copilot CLI, install an agent with a Copilot model set in
the table, choose HydraFusion in the picker, and see which model runs the agent
when picked directly and when called as a subagent.

- If HydraFusion ignores it: change "may ignore" to "ignores".
- If it keeps the agent's model: drop the warning, or turn it into "agents keep
  the model set here under HydraFusion", and consider recommending it.

## Not in this step

- Detecting the person's Copilot settings. loadout never reads them, and the
  picker choice is per session anyway.
- A warning in the CLI (`loadout namespace tiers`). It prints no notes today;
  add them there only if admins ask.
