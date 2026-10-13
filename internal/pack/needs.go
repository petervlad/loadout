package pack

import (
	"errors"
	"fmt"
	"slices"

	"github.com/petervlad/loadout/internal/pipeline"
)

// Needs is what a package needs from the view it is installed in.
type Needs struct {
	// Packages are packages at a major version: acme/dev-loop@1.
	Packages []Requirement
	// Agents are agent names that must exist in the view.
	Agents []string
}

// Requirement is a package at a major version.
type Requirement struct {
	ID    ID
	Major int
}

// String formats r as acme/dev-loop@1.
func (r Requirement) String() string { return fmt.Sprintf("%s@%d", r.ID, r.Major) }

// Needs returns what p needs to work, none for most kinds.
func (p *Pack) Needs() Needs {
	if needs := kinds[p.Kind()].needs; needs != nil {
		return needs(p)
	}
	return Needs{}
}

// CheckNeeds checks p against the releases of the packages Needs names, in
// any order. For a pipeline binding: every slot is bound and the conductor's
// name is not one of the bound agents. It does not look for the agents.
func (p *Pack) CheckNeeds(required []*Pack) error {
	if check := kinds[p.Kind()].checkNeeds; check != nil {
		return check(p, required)
	}
	return nil
}

// Standalone reports whether p installs something by itself. When it does
// not, the reason is one sentence for the person.
func (p *Pack) Standalone() (bool, string) {
	if standalone := kinds[p.Kind()].standalone; standalone != nil {
		return standalone(p)
	}
	return true, ""
}

// Compatible reports whether next may follow prev inside one major version.
// Kinds with no rule accept any change.
func Compatible(prev, next *Pack) error {
	if prev.Kind() != next.Kind() {
		return fmt.Errorf("%s changes kind from %s to %s: publish it under a new name", next.ID(), prev.Kind().Describe(), next.Kind().Describe())
	}
	if compatible := kinds[next.Kind()].compatible; compatible != nil {
		return compatible(prev, next)
	}
	return nil
}

func pipelineNeeds(p *Pack) Needs {
	n := p.pipeline.Needs()
	out := Needs{Agents: slices.Clone(n.Agents)}
	for _, t := range n.Templates {
		// The parser checked both parts, so this cannot fail.
		id, err := NewID(t.Layer, t.Name)
		if err == nil {
			out.Packages = append(out.Packages, Requirement{ID: id, Major: t.Major})
		}
	}
	return out
}

func pipelineStandalone(p *Pack) (bool, string) {
	if _, ok := p.pipeline.(*pipeline.Template); ok {
		return false, "a pipeline template installs through a team's binding"
	}
	return true, ""
}

func pipelineCompatible(prev, next *Pack) error {
	pt, ok := prev.pipeline.(*pipeline.Template)
	nt, ok2 := next.pipeline.(*pipeline.Template)
	switch {
	case ok && ok2:
		if err := pipeline.Compatible(pt, nt); err != nil {
			return fmt.Errorf("%s %s: %w", next.ID(), next.Version(), err)
		}
		return nil
	case ok != ok2:
		return errors.New("a pipeline cannot change between a template and a binding inside one major")
	}
	return nil
}

func pipelineCheck(p *Pack, required []*Pack) error {
	b, ok := p.pipeline.(*pipeline.Binding)
	if !ok {
		return nil
	}
	uses := b.Uses()
	var tmpl *pipeline.Template
	for _, r := range required {
		if r.ID().Team() != uses.Layer || r.ID().Name() != uses.Name {
			continue
		}
		t, ok := r.pipeline.(*pipeline.Template)
		if !ok {
			return fmt.Errorf("%s is not a template, so %s cannot use it", uses, p.ID())
		}
		tmpl = t
	}
	if tmpl == nil {
		return fmt.Errorf("the template %s was not provided", uses)
	}
	bound, err := pipeline.Bind(b, tmpl)
	if err != nil {
		return err
	}
	return CheckConductorName(p.ID(), bound.Agents())
}

// CheckConductorName refuses a binding whose conductor would take the name of
// one of its own agents, since both install as agents of that name.
func CheckConductorName(id ID, agents []string) error {
	name := pipeline.ConductorName(id.Team(), id.Name())
	if slices.Contains(agents, name) {
		return fmt.Errorf("the conductor of %s is called %s, which is also one of the agents it binds: rename the binding or the agent", id, name)
	}
	return nil
}
