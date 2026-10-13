package target

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/pipeline"
	"github.com/petervlad/loadout/internal/tier"
)

// ErrNotStandalone is returned when a package that installs nothing by
// itself (a pipeline template) is rendered. Callers ask Pack.Standalone first.
var ErrNotStandalone = errors.New("package installs nothing by itself")

// Delegator is a Renderer whose tool can start a named custom agent as a
// subagent. Conductors render only for such tools. It is the method
// pipeline.Starter asks for.
type Delegator interface {
	Renderer
	StartAgent(name string) string
}

// AgentLimiter is a Delegator whose tool can limit which agents an agent may
// start. It renders def with the agent tool limited to helpers.
type AgentLimiter interface {
	RenderLimited(def agent.Definition, helpers []string, run tier.Choice, scope Scope, stamp Stamp) (File, error)
}

// FolderMaker is a Renderer whose edit tool creates missing parent folders.
// Without it a conductor's first agent needs a shell to create the run folder.
type FolderMaker interface {
	EditCreatesFolders() bool
}

// renderPipeline renders a binding as its conductor agent.
func renderPipeline(a Renderer, p *pack.Pack, scope Scope, stamp Stamp, opts RenderOptions) (Unit, error) {
	if ok, why := p.Standalone(); !ok {
		return Unit{}, fmt.Errorf("render %s: %s: %w", p.ID(), why, ErrNotStandalone)
	}
	d, ok := a.(Delegator)
	if !ok {
		return Unit{}, fmt.Errorf("render %s: %s cannot start a named agent as a subagent, so it cannot run a pipeline", p.ID(), a.Name())
	}
	pl, _ := p.Pipeline()
	binding, ok := pl.(*pipeline.Binding)
	if !ok {
		return Unit{}, fmt.Errorf("render %s: not a binding", p.ID())
	}
	tmpl, err := templateFor(p, binding, opts.Requires)
	if err != nil {
		return Unit{}, err
	}
	bound, err := pipeline.Bind(binding, tmpl)
	if err != nil {
		return Unit{}, fmt.Errorf("render %s: %w", p.ID(), err)
	}

	if err := pack.CheckConductorName(p.ID(), bound.Agents()); err != nil {
		return Unit{}, fmt.Errorf("render %s: %w", p.ID(), err)
	}
	name := opts.Name
	if name == "" {
		name = pipeline.ConductorName(p.ID().Team(), p.ID().Name())
	}
	c, err := bound.Conductor(name, d)
	if err != nil {
		return Unit{}, err
	}
	hint := c.Model
	if hint == agent.ModelUnset {
		hint = agent.ModelBalanced
	}
	def := agent.Definition{
		Name:        c.Name,
		Description: c.Description,
		Model:       hint,
		Tools:       []agent.Tool{agent.ToolRead, agent.ToolSearch, agent.ToolAgent},
		Prompt:      c.Prompt,
	}
	run := Choose(a, opts.Models, hint)

	var warnings []string
	var f File
	if l, ok := a.(AgentLimiter); ok {
		allowed := bound.Allowed(opts.Agents)
		var helpers []string
		if !allowed.Unrestricted {
			helpers = allowed.Names
			if len(allowed.Unknown) > 0 {
				warnings = append(warnings, fmt.Sprintf("%s: the definition of %s is not available, so the conductor can start only the bound agents and not their helpers.", c.Name, strings.Join(allowed.Unknown, ", ")))
			}
		}
		f, err = l.RenderLimited(def, helpers, run, scope, stamp)
	} else {
		f, err = a.RenderAgent(def, run, scope, stamp)
	}
	if err != nil {
		return Unit{}, err
	}
	warnings = append(warnings, runFolderWarning(a, c.Name, bound, opts.Agents)...)
	return Unit{Root: f.Path, Files: []File{f}, Warnings: warnings}, nil
}

// templateFor picks the release of the binding's template out of requires.
func templateFor(p *pack.Pack, b *pipeline.Binding, requires []*pack.Pack) (*pipeline.Template, error) {
	uses := b.Uses()
	for _, r := range requires {
		if r.ID().Team() != uses.Layer || r.ID().Name() != uses.Name || r.Version().Major() != uses.Major {
			continue
		}
		pl, _ := r.Pipeline()
		if t, ok := pl.(*pipeline.Template); ok {
			return t, nil
		}
		return nil, fmt.Errorf("render %s: %s is a binding, not a template", p.ID(), uses)
	}
	return nil, fmt.Errorf("render %s: the template %s was not provided", p.ID(), uses)
}

// runFolderWarning says when the conductor cannot get its run folder made:
// the tool's edit tool does not create folders and the start slot's agent has
// no shell to do it with.
func runFolderWarning(a Renderer, conductor string, b *pipeline.Bound, defs []agent.Definition) []string {
	if m, ok := a.(FolderMaker); ok && m.EditCreatesFolders() {
		return nil
	}
	shell, known := b.StartShell(defs)
	if shell || !known {
		return nil
	}
	start := ""
	if i := slices.IndexFunc(b.Slots(), func(s pipeline.BoundSlot) bool { return s.Name == b.Start() }); i >= 0 {
		start = b.Slots()[i].Agent
	}
	return []string{fmt.Sprintf("%s: %s starts the run and has no shell tool, so on %s nothing can create the run folder. Give %s the shell tool, or create the folder before the run.", conductor, start, a.Name(), start)}
}
