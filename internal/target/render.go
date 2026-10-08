package target

import (
	"fmt"
	"path"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/skill"
	"github.com/petervlad/loadout/internal/tier"
)

// Renderer writes packages in one AI tool's format. The claudecode and
// copilot packages implement it; a new tool is a new renderer.
type Renderer interface {
	// Name is the target name users pass to --target.
	Name() string
	// RenderAgent returns the tool's agent file for def. run is what the
	// tool runs for def.Model, already resolved by Render; its zero value
	// leaves model and effort to the person.
	RenderAgent(def agent.Definition, run tier.Choice, scope Scope, stamp Stamp) (File, error)
	// SkillsDir is the folder, relative to the scope root, the tool reads
	// skills from: each skill is a folder in it.
	SkillsDir(kind ScopeKind) (string, error)
}

// Adapter is a Renderer that can also tell whether its tool is used.
type Adapter interface {
	Renderer
	// MarkerDir is the folder that shows the tool is used in a scope.
	MarkerDir(kind ScopeKind) string
}

// ModelDefaults is a Renderer with built-in models for the hints, used
// where no table value applies. Claude Code has them (haiku, sonnet, opus);
// Copilot has none, since which models a person has depends on their plan.
type ModelDefaults interface {
	DefaultModel(h agent.Model) string
}

// ModelGuide is a Renderer that tells people how its tool reads a model
// table value, for the pages where owners fill the table in.
type ModelGuide interface {
	// Product is the tool's name for people: "Claude Code".
	Product() string
	// ModelHelp says what a model value looks like, as one sentence.
	ModelHelp() string
	// ModelNotes are what to know before relying on the table, as
	// sentences; none when there is nothing to add.
	ModelNotes() []string
}

// Choose resolves what tool a runs for hint h: nothing for an unset hint;
// else the table's values, with the adapter's default model where the
// table sets none.
func Choose(a Renderer, models tier.Table, h agent.Model) tier.Choice {
	if h == agent.ModelUnset {
		return tier.Choice{}
	}
	c, _ := models.Get(a.Name(), h)
	if d, ok := a.(ModelDefaults); ok {
		c = c.Over(tier.Choice{Model: d.DefaultModel(h)})
	}
	return c
}

// Unit is what one package renders for one tool.
type Unit struct {
	// Root is the path that belongs to the package as a whole: the agent
	// file, or the skill folder. Layering decides per unit, so a closer
	// layer's skill replaces a farther one's whole folder.
	Root  string
	Files []File
}

// RenderOptions change how a package renders.
type RenderOptions struct {
	// Name replaces the package name, so two versions can be installed
	// side by side (review). Empty keeps the package name.
	Name string
	// Models is the resolved model table of the installing team. The empty
	// table renders what loadout always did.
	Models tier.Table
}

// Render renders p for a. It is the one place that looks at the package
// kind: a new kind is a new case here and in package pack.
func Render(a Renderer, p *pack.Pack, scope Scope, stamp Stamp, opts RenderOptions) (Unit, error) {
	switch p.Kind() {
	case pack.KindAgent:
		def, _ := p.Agent()
		if opts.Name != "" {
			def.Name = opts.Name
		}
		f, err := a.RenderAgent(def, Choose(a, opts.Models, def.Model), scope, stamp)
		if err != nil {
			return Unit{}, err
		}
		return Unit{Root: f.Path, Files: []File{f}}, nil
	case pack.KindSkill, pack.KindKB:
		s, _ := p.Skill()
		if opts.Name != "" {
			renamed, err := s.Renamed(opts.Name)
			if err != nil {
				return Unit{}, err
			}
			s = renamed
		}
		dir, err := a.SkillsDir(scope.Kind)
		if err != nil {
			return Unit{}, err
		}
		return RenderSkill(dir, s, stamp)
	}
	return Unit{}, fmt.Errorf("render %s: %s cannot install a %s", p.ID(), a.Name(), p.Kind().Describe())
}

// RenderSkill lays s out as a skill folder under dir: SKILL.md with the
// "managed by" note, and the supporting files beside it. Every tool that
// reads Agent Skills reads this layout, so adapters share it.
func RenderSkill(dir string, s *skill.Skill, stamp Stamp) (Unit, error) {
	root := path.Join(dir, s.Name)
	main, err := s.Marshal(ManagedNote(stamp))
	if err != nil {
		return Unit{}, err
	}
	files := []File{{Path: path.Join(root, skill.File), Data: main}}
	extra := s.Files()
	for _, p := range sortedKeys(extra) {
		files = append(files, File{Path: path.Join(root, p), Data: extra[p]})
	}
	return Unit{Root: root, Files: files}, nil
}
