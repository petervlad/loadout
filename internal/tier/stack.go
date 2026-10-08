package tier

import (
	"slices"

	"github.com/petervlad/loadout/internal/agent"
)

// Layer is one namespace's own table.
type Layer struct {
	Namespace string
	Table     Table
}

// Stack is the tables of a chain of namespaces, closest first: a team, then
// the project above it. The team's values win, value by value.
type Stack struct{ layers []Layer }

// NewStack returns the stack of layers, closest first.
func NewStack(layers ...Layer) Stack { return Stack{layers: slices.Clone(layers)} }

// Namespace is the closest layer's namespace, empty for an empty stack.
func (s Stack) Namespace() string {
	if len(s.layers) == 0 {
		return ""
	}
	return s.layers[0].Namespace
}

// Own is the closest layer's own table: what its namespace set itself.
func (s Stack) Own() Table {
	if len(s.layers) == 0 {
		return Table{}
	}
	return s.layers[0].Table
}

// Above is the stack without the closest layer: what it inherits.
func (s Stack) Above() Stack {
	if len(s.layers) == 0 {
		return s
	}
	return Stack{layers: slices.Clone(s.layers[1:])}
}

// Resolve merges the layers into the table installs use.
func (s Stack) Resolve() Table {
	var out Table
	for _, l := range slices.Backward(s.layers) {
		out = l.Table.Over(out)
	}
	return out
}

// Origin returns the namespaces the model and the effort of a cell come
// from; empty for a value no layer sets.
func (s Stack) Origin(tool string, h agent.Model) (model, effort string) {
	for _, l := range s.layers {
		c, _ := l.Table.Get(tool, h)
		if model == "" && c.Model != "" {
			model = l.Namespace
		}
		if effort == "" && c.Effort != "" {
			effort = l.Namespace
		}
	}
	return model, effort
}
