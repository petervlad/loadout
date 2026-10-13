# Handoffs live in pipelines, never in agents

An agent can fill slots in more than one pipeline, so if an agent named the agent that comes after it, that name would be wrong in every pipeline but one. We decided that only a pipeline decides a handoff. An agent may delegate a job to a helper that returns to it, ends its slot with a `Verdict:` line, and never names the next agent. The conductor follows the pipeline's edges and gives each agent its slot contract in the prompt, so agents declare nothing about the pipelines they are in. A lint warns about an agent that names its next agent (an error in a later version).

## Considered Options

- **A footer on each agent with the next agent to call, overridden by the pipeline when the two disagree.** Rejected: nobody could predict which one wins, and every reused agent would carry a footer that is wrong somewhere.

See `docs/plans/pipelines.md` and `GLOSSARY.md` (handoff, delegation, conductor).
