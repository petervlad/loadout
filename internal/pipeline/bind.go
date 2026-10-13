package pipeline

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/agent"
)

// BoundSlot is a slot with its agent and the edges that leave it.
type BoundSlot struct {
	Slot
	Agent string
	Edges []Edge
}

// Bound is a template with every slot bound to an agent and the binding's
// extra gates applied. It holds everything the conductor needs. Immutable.
type Bound struct {
	tmpl       *Template // edges already carry the binding's gates
	agents     map[string]string
	ref        Ref
	workFolder string
	model      agent.Model
}

// Bind checks a binding against one release of its template and returns the
// resolved pipeline: every slot bound, no unknown slot named, and every extra
// gate on an edge of the template. A gate on an edge that is already a gate
// is accepted and changes nothing.
func Bind(b *Binding, t *Template) (*Bound, error) {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	var unbound []string
	for _, s := range t.slots {
		if b.bind[s.Name] == "" {
			unbound = append(unbound, s.Name)
		}
	}
	if len(unbound) > 0 {
		add("slot %s has no agent: name one under bind", strings.Join(unbound, ", "))
	}
	for _, name := range b.Slots() {
		if t.index(name) < 0 {
			add("bind names slot %q, which %s does not have", name, b.uses)
		}
	}

	edges := t.Edges()
	for _, g := range b.gates {
		i := slices.IndexFunc(edges, func(e Edge) bool { return e.From == g.From && e.On == g.On })
		if i < 0 {
			add("gate names the edge %s on %s, which %s does not have", g.From, g.On, b.uses)
			continue
		}
		edges[i].Via = Gate
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	tmpl := &Template{start: t.start, slots: t.Slots(), edges: edges}
	return &Bound{tmpl: tmpl, agents: maps.Clone(b.bind), ref: b.uses, workFolder: b.workFolder, model: b.model}, nil
}

// Uses returns the template the pipeline was bound from.
func (b *Bound) Uses() Ref { return b.ref }

// WorkFolder returns where runs live.
func (b *Bound) WorkFolder() string { return b.workFolder }

// Model returns the conductor's model hint, or ModelUnset.
func (b *Bound) Model() agent.Model { return b.model }

// Start returns the name of the first slot.
func (b *Bound) Start() string { return b.tmpl.start }

// Slots returns the slots in order, each with its agent and outgoing edges.
func (b *Bound) Slots() []BoundSlot {
	out := make([]BoundSlot, 0, len(b.tmpl.slots))
	for _, s := range b.tmpl.slots {
		bs := BoundSlot{Slot: s.clone(), Agent: b.agents[s.Name]}
		for _, e := range b.tmpl.edges {
			if e.From == s.Name {
				bs.Edges = append(bs.Edges, e)
			}
		}
		out = append(out, bs)
	}
	return out
}

// Agents returns the bound agent names, once each, in slot order.
func (b *Bound) Agents() []string {
	var out []string
	for _, s := range b.tmpl.slots {
		if a := b.agents[s.Name]; !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// Mermaid draws the bound pipeline with its agent names.
func (b *Bound) Mermaid() string { return Mermaid(b.tmpl, b.agents) }

// ConductorName returns the name of the conductor agent for the binding
// layer/name: lime/dev-loop becomes lime-dev-loop. A name that already starts
// with the layer is kept, so lime/lime-dev-loop is lime-dev-loop too.
func ConductorName(layer, name string) string {
	if strings.HasPrefix(name, layer+"-") {
		return name
	}
	return layer + "-" + name
}
