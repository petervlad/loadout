// Package agent defines the tool-neutral agent format loadout stores and
// shares. Adapters translate a Definition into each AI tool's own format.
//
// On disk an agent is Markdown with YAML frontmatter:
//
//	---
//	name: planner
//	description: Turn a ticket into an implementation plan.
//	model: deep
//	tools: [read, search, shell]
//	---
//	You are the team's planner. ...
package agent

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/frontmatter"
	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// Tool is a capability an agent may use, named independently of any AI tool.
type Tool string

// The canonical tools. Adapters map each one to their tool's own names.
const (
	ToolRead   Tool = "read"
	ToolSearch Tool = "search"
	ToolEdit   Tool = "edit"
	ToolShell  Tool = "shell"
	ToolWeb    Tool = "web"
	ToolAgent  Tool = "agent"
	// ToolSkill lets the agent load skills.
	ToolSkill Tool = "skill"
)

var knownTools = []Tool{ToolRead, ToolSearch, ToolEdit, ToolShell, ToolWeb, ToolAgent, ToolSkill}

// Tools returns every known tool, in a stable order.
func Tools() []Tool { return slices.Clone(knownTools) }

// Valid reports whether t is a known tool.
func (t Tool) Valid() bool { return slices.Contains(knownTools, t) }

// Model is a hint about how capable a model the agent needs. It is never a
// vendor model ID: each person's own tool maps it to a model they have.
type Model string

// Model hints, from cheapest to most capable. ModelUnset leaves the choice
// to the tool.
const (
	ModelUnset    Model = ""
	ModelFast     Model = "fast"
	ModelBalanced Model = "balanced"
	ModelDeep     Model = "deep"
)

// Valid reports whether m is a known model hint or unset.
func (m Model) Valid() bool {
	switch m {
	case ModelUnset, ModelFast, ModelBalanced, ModelDeep:
		return true
	}
	return false
}

// Definition is one agent in canonical form.
type Definition struct {
	Name        string
	Description string
	Model       Model
	Tools       []Tool
	Prompt      string
}

// ErrNoFrontmatter is returned when an agent file does not start with a
// "---" frontmatter block.
var ErrNoFrontmatter = frontmatter.ErrMissing

// header is the on-disk shape of the YAML header.
type header struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       Model  `yaml:"model,omitempty"`
	Tools       []Tool `yaml:"tools,omitempty,flow"`
}

// Parse reads an agent file and validates it.
func Parse(data []byte) (Definition, error) {
	meta, body, err := frontmatter.Split(data)
	if err != nil {
		return Definition{}, err
	}

	var fm header
	if err := strictyaml.Decode(meta, &fm); err != nil && !errors.Is(err, strictyaml.ErrEmpty) {
		return Definition{}, fmt.Errorf("parse frontmatter: %w", err)
	}

	// Claude Code and Copilot both read a missing tools list as "every
	// tool", so an explicit empty list cannot mean "no tools". Refuse it
	// rather than silently granting shell access.
	if fm.Tools != nil && len(fm.Tools) == 0 {
		return Definition{}, errors.New("tools: [] would give the agent every tool (Claude Code and Copilot read an empty list as no restriction); list the tools it needs, or remove the tools line to allow all")
	}

	d := Definition{
		Name:        fm.Name,
		Description: strings.TrimSpace(fm.Description),
		Model:       fm.Model,
		Tools:       fm.Tools,
		Prompt:      strings.TrimSpace(string(body)),
	}
	if err := d.Validate(); err != nil {
		return Definition{}, err
	}
	return d, nil
}

// Validate reports every problem with d at once.
func (d Definition) Validate() error {
	var errs []error
	if err := ident.Check("agent", d.Name); err != nil {
		errs = append(errs, err)
	}
	if d.Description == "" {
		errs = append(errs, errors.New("description is empty"))
	}
	if !d.Model.Valid() {
		errs = append(errs, fmt.Errorf("unknown model hint %q: want fast, balanced or deep", d.Model))
	}
	for _, t := range d.Tools {
		if !t.Valid() {
			errs = append(errs, fmt.Errorf("unknown tool %q: want one of %v", t, knownTools))
		}
	}
	if d.Prompt == "" {
		errs = append(errs, errors.New("prompt (the text after the frontmatter) is empty"))
	}
	return errors.Join(errs...)
}

// Marshal writes d in canonical form. Parse(d.Marshal()) returns d.
func (d Definition) Marshal() ([]byte, error) {
	meta, err := yaml.Marshal(header{
		Name:        d.Name,
		Description: d.Description,
		Model:       d.Model,
		Tools:       d.Tools,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}
	return frontmatter.Join(meta, d.Prompt), nil
}
