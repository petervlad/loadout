// Package claudecode renders packages for Claude Code.
//
// Claude Code reads subagents from .claude/agents/<name>.md and skills from
// .claude/skills/<name>/SKILL.md, under the home directory for user scope
// and under the repository for project scope.
//
// A subagent's model is an alias (haiku, sonnet, opus), a full model ID or
// "inherit", and its effort one of low, medium, high, xhigh and max.
//
// Sources: https://code.claude.com/docs/en/skills,
// https://code.claude.com/docs/en/sub-agents,
// https://code.claude.com/docs/en/model-config
package claudecode

import (
	"path"
	"strings"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

// Name is the target name users pass to --target.
const Name = "claude"

// toolNames maps canonical tools to Claude Code tool names. A wrong name is
// dropped silently by Claude Code and the agent starts without that tool, so
// this table is covered by tests.
var toolNames = map[agent.Tool][]string{
	agent.ToolRead:   {"Read"},
	agent.ToolSearch: {"Grep", "Glob"},
	agent.ToolEdit:   {"Edit", "Write"},
	agent.ToolShell:  {"Bash"},
	agent.ToolWeb:    {"WebFetch", "WebSearch"},
	agent.ToolAgent:  {"Agent"},
	agent.ToolSkill:  {"Skill"},
}

// modelAliases maps model hints to Claude Code's model aliases, used where
// the team's table sets no model.
var modelAliases = map[agent.Model]string{
	agent.ModelFast:     "haiku",
	agent.ModelBalanced: "sonnet",
	agent.ModelDeep:     "opus",
}

// Adapter renders agents for Claude Code. The zero value is ready to use.
type Adapter struct{}

var (
	_ target.Adapter       = (*Adapter)(nil)
	_ target.ModelDefaults = (*Adapter)(nil)
	_ target.ModelGuide    = (*Adapter)(nil)
)

// New returns a Claude Code adapter.
func New() *Adapter { return &Adapter{} }

// Name returns the target name.
func (*Adapter) Name() string { return Name }

// MarkerDir returns the folder that shows Claude Code is used; it is the
// same in both scopes.
func (*Adapter) MarkerDir(target.ScopeKind) string { return ".claude" }

// frontmatter is Claude Code's subagent header. Field order is the output order.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Tools       string `yaml:"tools,omitempty"`
	Model       string `yaml:"model,omitempty"`
	Effort      string `yaml:"effort,omitempty"`
}

// DefaultModel returns the Claude Code alias for h, empty for no hint.
func (*Adapter) DefaultModel(h agent.Model) string { return modelAliases[h] }

// Product names the tool for people.
func (*Adapter) Product() string { return "Claude Code" }

// ModelHelp says what a model value looks like.
func (*Adapter) ModelHelp() string {
	return "An alias (haiku, sonnet, opus), a full model ID such as claude-sonnet-5, or opus[1m] for the 1M context window."
}

// ModelNotes has nothing to add: Claude Code runs the model it is given.
func (*Adapter) ModelNotes() []string { return nil }

// SkillsDir returns .claude/skills: Claude Code reads skills from there in
// the home directory and in a repository.
func (*Adapter) SkillsDir(target.ScopeKind) (string, error) {
	return path.Join(".claude", "skills"), nil
}

// RenderAgent returns the subagent file for def. Both scopes use the same path
// relative to their root.
func (*Adapter) RenderAgent(def agent.Definition, run tier.Choice, _ target.Scope, stamp target.Stamp) (target.File, error) {
	tools, err := mapTools(def.Tools)
	if err != nil {
		return target.File{}, err
	}
	data, err := target.Markdown(def.Name, frontmatter{
		Name:        def.Name,
		Description: def.Description,
		Tools:       strings.Join(tools, ", "),
		Model:       run.Model,
		Effort:      string(run.Effort),
	}, stamp, def.Prompt)
	if err != nil {
		return target.File{}, err
	}

	return target.File{
		Path: path.Join(".claude", "agents", def.Name+".md"),
		Data: data,
	}, nil
}

func mapTools(tools []agent.Tool) ([]string, error) {
	return target.MapTools(toolNames, "Claude Code", tools)
}
