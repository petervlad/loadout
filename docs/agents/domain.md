# Domain docs

How the engineering skills read loadout's domain documentation. Layout: single-context.

## Before exploring, read these

- **`GLOSSARY.md`** at the repo root, when it exists: the shared words (project, team, layer, package kind, proposal, review, ...). `/domain-modeling` creates it lazily. Do not create it upfront.
- **`docs/DESIGN.md`**: the source of truth for architecture, layers (L0 project, L1 team), roles, login and package kinds. Use its words.
- **`docs/adr/`**: decisions with their reasons, created lazily by `/domain-modeling`. Older decisions live in `docs/DESIGN.md` and `docs/plans/`. Treat those as ADRs.
- **`docs/plans/<feature>.md`**: the plan or spec of a feature that is being built or was built ("As built" sections say what shipped).

If a file does not exist, proceed silently.

## Use the glossary's vocabulary

Name domain concepts the way `GLOSSARY.md` and `docs/DESIGN.md` do, in issue titles, test names, proposals and UI text. If the two disagree, `docs/DESIGN.md` wins and the glossary gets fixed. A new word is a signal: either it is not the project's language, or it is a gap for `/domain-modeling`.

## Flag conflicts

If your output contradicts an ADR, `docs/DESIGN.md` or a rule in `CLAUDE.md`, say so explicitly instead of silently overriding it. "The one rule" in `CLAUDE.md` (loadout never runs a model and never holds an AI key) is never reopened by a skill.
