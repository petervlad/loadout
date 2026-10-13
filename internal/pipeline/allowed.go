package pipeline

import (
	"slices"

	"github.com/petervlad/loadout/internal/agent"
)

// Allowed is the set of agents a conductor must be able to start. On Claude
// Code a subagent can only start the agents its session agent may start, so
// the conductor's list has to cover the bound agents and every helper they
// delegate to (observed on Claude Code 2.1.295).
type Allowed struct {
	// Names are the bound agents, in slot order.
	Names []string
	// Unrestricted is true when a bound agent may start any agent, so the
	// conductor's agent tool cannot carry a list.
	Unrestricted bool
	// Unknown are bound agents with no definition given: nothing is known
	// about their helpers, and the caller should warn.
	Unknown []string
}

// Allowed works out the agents the conductor may start, from the definitions
// of the bound agents. A definition with the agent tool, or with no tools
// list (every tool), delegates to helpers it does not name, so it makes the
// set unrestricted. The canonical agent format names no helpers.
func (b *Bound) Allowed(defs []agent.Definition) Allowed {
	a := Allowed{Names: b.Agents()}
	for _, name := range a.Names {
		i := slices.IndexFunc(defs, func(d agent.Definition) bool { return d.Name == name })
		switch {
		case i < 0:
			a.Unknown = append(a.Unknown, name)
		case defs[i].Tools == nil || slices.Contains(defs[i].Tools, agent.ToolAgent):
			a.Unrestricted = true
		}
	}
	return a
}

// StartShell reports whether the agent bound to the start slot has a shell
// tool, which it needs to create the run folder in tools whose edit tool
// cannot make parent folders. known is false when defs lacks that agent.
func (b *Bound) StartShell(defs []agent.Definition) (shell, known bool) {
	name := b.agents[b.tmpl.start]
	i := slices.IndexFunc(defs, func(d agent.Definition) bool { return d.Name == name })
	if i < 0 {
		return false, false
	}
	return defs[i].Tools == nil || slices.Contains(defs[i].Tools, agent.ToolShell), true
}
