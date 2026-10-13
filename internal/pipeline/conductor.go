package pipeline

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/petervlad/loadout/internal/agent"
)

// Starter says, in one tool's words, how the conductor starts a named agent
// as a subagent. It is owned here, the consumer, and implemented by the
// target adapters.
type Starter interface {
	// StartAgent returns one sentence telling the conductor how to start the
	// custom agent called name, for example by setting a tool parameter to
	// exactly that name.
	StartAgent(name string) string
}

// Conductor is the generated agent for a bound pipeline, ready for an adapter
// to render with the tools it needs: read, search and agent, never edit.
type Conductor struct {
	Name        string
	Description string
	// Model is the hint from the binding, or ModelUnset.
	Model  agent.Model
	Prompt string
}

//go:embed conductor.md.tmpl
var conductorText string

var conductorTmpl = template.Must(template.New("conductor").Parse(conductorText))

type conductorSlot struct {
	Name, Agent, Brief, Reads, Writes, Verdicts, StartHow string
	Edges                                                 []string
}

// Conductor builds the conductor agent called name (see ConductorName) for
// the bound pipeline. The text is one for every tool except the sentence s
// supplies for starting an agent.
func (b *Bound) Conductor(name string, s Starter) (Conductor, error) {
	data := struct {
		Name, WorkFolder, Start string
		Slots                   []conductorSlot
	}{Name: name, WorkFolder: b.workFolder, Start: b.tmpl.start}

	for _, bs := range b.Slots() {
		data.Slots = append(data.Slots, conductorSlot{
			Name:     bs.Name,
			Agent:    bs.Agent,
			Brief:    bs.Brief,
			Reads:    b.readsText(bs.Reads),
			Writes:   writesText(bs.Slot),
			Verdicts: strings.Join(append(slices.Clip(bs.Verdicts), Questions), ", "),
			StartHow: s.StartAgent(bs.Agent),
			Edges:    b.edgeLines(bs),
		})
	}
	var out strings.Builder
	if err := conductorTmpl.Execute(&out, data); err != nil {
		return Conductor{}, fmt.Errorf("build conductor %s: %w", name, err)
	}
	return Conductor{
		Name: name,
		Description: fmt.Sprintf("Runs the %s pipeline by starting %s in turn and stopping at each gate. Start the session with this agent.",
			name, strings.Join(b.Agents(), ", ")),
		Model:  b.model,
		Prompt: strings.TrimSpace(out.String()),
	}, nil
}

// readsText lists what a slot reads. A file that only some slot lists under
// also is not checked for a verdict and may be absent, so it is optional.
func (b *Bound) readsText(reads []string) string {
	if len(reads) == 0 {
		return "nothing"
	}
	parts := make([]string, len(reads))
	for i, r := range reads {
		parts[i] = "`" + r + "` in the run folder"
		switch {
		case r == ticket:
			parts[i] = "the ticket"
		case b.isExtra(r):
			parts[i] += " (if it exists)"
		}
	}
	return strings.Join(parts, ", ")
}

func writesText(s Slot) string {
	out := "`" + s.Writes + "` in the run folder"
	if len(s.Also) > 0 {
		quoted := make([]string, len(s.Also))
		for i, a := range s.Also {
			quoted[i] = "`" + a + "`"
		}
		out += " (also " + strings.Join(quoted, ", ") + ", which are not checked)"
	}
	return out
}

func (b *Bound) edgeLines(bs BoundSlot) []string {
	lines := make([]string, 0, len(bs.Edges)+1)
	for _, e := range bs.Edges {
		var line string
		switch {
		case e.Via == Auto && e.To == End:
			line = fmt.Sprintf("%s: automatic. The run finishes, with no stop.", e.On)
		case e.Via == Auto:
			line = fmt.Sprintf("%s: automatic. Start slot `%s` (agent `%s`) at once, with no stop.", e.On, e.To, b.agents[e.To])
		case e.To == End:
			line = fmt.Sprintf("%s: GATE. Stop and wait for the person's go, then the run finishes.", e.On)
		default:
			line = fmt.Sprintf("%s: GATE. Stop and wait for the person's go, then slot `%s` (agent `%s`).", e.On, e.To, b.agents[e.To])
		}
		if e.MaxRounds > 0 {
			line += fmt.Sprintf(" Round cap: follow it at most %d times.", e.MaxRounds)
		}
		lines = append(lines, line)
	}
	return append(lines, Questions+" goes to no slot: it is a question for the person.")
}

func (b *Bound) isExtra(file string) bool {
	return slices.ContainsFunc(b.tmpl.slots, func(s Slot) bool { return slices.Contains(s.Also, file) })
}
