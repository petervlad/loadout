package pipeline

import (
	"fmt"
	"strings"
)

// exampleTemplate is what `loadout new pipeline` writes for a project: the
// smallest pipeline that shows a gate, a verdict and an edge back.
const exampleTemplate = `# A pipeline is a set of slots and the edges between them. Teams bind each
# slot to one of their agents; a generated conductor then runs the slots.
start: draft
slots:
  draft:
    brief: Write the first version of the work into draft.md.
    writes: draft.md
    reads: [ticket]
    verdicts: [DONE]
  check:
    brief: Judge draft.md and say whether it is good enough.
    writes: check.md
    reads: [draft.md]
    verdicts: [APPROVE, CHANGES REQUESTED]
# Every verdict of every slot has exactly one edge. via is gate (the person
# decides) or auto. max_rounds is only for an edge that goes back.
edges:
  - {from: draft, on: DONE,              to: check, via: gate}
  - {from: check, on: APPROVE,           to: end,   via: gate}
  - {from: check, on: CHANGES REQUESTED, to: draft, via: gate, max_rounds: 2}
`

// ScaffoldTemplate returns a small example template for a new pipeline.
func ScaffoldTemplate() []byte { return []byte(exampleTemplate) }

// Scaffold returns a binding for t with every slot listed and left empty, and
// the template's edges as comments. It does not parse as a finished binding
// until each agent is filled in, which is what the registry check reports.
func Scaffold(ref Ref, t *Template) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "uses: %s\n", ref)
	b.WriteString("# Name one of your agents for each slot.\nbind:\n")
	for _, s := range t.slots {
		fmt.Fprintf(&b, "  %s:\n", s.Name)
	}
	b.WriteString("work_folder: explorations/\n")
	b.WriteString("# model: deep          # the conductor's model hint, balanced when unset\n")
	b.WriteString("# gate:                 # auto edges you want as gates, for example:\n")
	if from, on, ok := t.gateExample(); ok {
		fmt.Fprintf(&b, "#   - {from: %s, on: %s}\n", from, on)
	}
	fmt.Fprintf(&b, "#\n# Edges of %s (a binding may only turn auto into gate):\n", ref)
	for _, e := range t.edges {
		fmt.Fprintf(&b, "#   %s on %s -> %s (%s", e.From, e.On, e.To, e.Via)
		if e.MaxRounds > 0 {
			b.WriteString(", max " + Rounds(e.MaxRounds))
		}
		b.WriteString(")\n")
	}
	return []byte(b.String())
}

// gateExample picks an edge to show in the binding's gate comment: the
// first auto edge, else the first verdict of the first slot.
func (t *Template) gateExample() (from, on string, ok bool) {
	for _, e := range t.edges {
		if e.Via == Auto {
			return e.From, e.On, true
		}
	}
	for _, s := range t.slots {
		if len(s.Verdicts) > 0 {
			return s.Name, s.Verdicts[0], true
		}
	}
	return "", "", false
}
