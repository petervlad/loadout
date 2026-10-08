// Package copilot renders packages for GitHub Copilot: agents as custom
// agent files, skills and knowledge bases as skill folders.
//
// Copilot CLI reads user agents from ~/.copilot/agents/, and Copilot (CLI,
// cloud agent and IDEs) reads repository agents from .github/agents/. The
// repository docs name files <name>.agent.md; Copilot CLI also loads plain
// <name>.md files from ~/.copilot/agents/, which is the form used here for
// user scope.
//
// The header is YAML frontmatter with a required description, an optional
// name and an optional tools list. Omitting tools grants every tool, so an
// agent with no canonical tools is rendered without the key, matching the
// Claude Code adapter. Unknown tool names are ignored silently by Copilot,
// which is why the mapping below uses only the documented aliases and is
// covered by tests.
//
// Skills follow the Agent Skills format; see SkillsDir for where they go.
//
// The model comes only from the team's table (see the note above the
// Adapter type); without a table the person's own model choice applies.
//
// Sources:
//   - https://docs.github.com/en/copilot/concepts/agents/about-agent-skills
//   - https://docs.github.com/en/copilot/reference/custom-agents-configuration
//   - https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/create-custom-agents-for-cli
//   - https://docs.github.com/en/copilot/concepts/agents/copilot-cli/about-custom-agents
//   - https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/customize-cloud-agent/create-custom-agents
//   - https://github.com/github/copilot-cli/blob/main/changelog.md
//   - https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference
//   - https://code.visualstudio.com/docs/copilot/customization/custom-agents
//   - https://code.visualstudio.com/docs/copilot/customization/language-models
//   - https://github.com/github/copilot-cli/issues/4963
//   - https://github.com/microsoft/vscode/pull/329263
//   - https://github.blog/changelog/2026-09-30-hydrafusion-in-vs-code-and-the-github-copilot-app
//   - https://github.com/orgs/community/discussions/206492
package copilot

import (
	"fmt"
	"path"
	"strings"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

// Name is the target name users pass to --target.
const Name = "copilot"

// toolNames maps canonical tools to Copilot's documented tool aliases. The
// aliases are case insensitive and work across Copilot CLI, the cloud agent
// and the IDEs, unlike the product-specific names (such as VS Code's
// "read/readFile"). A wrong name is ignored silently and the agent starts
// without that tool, so this table is covered by tests.
//
// Copilot's cloud agent has no web tool; there "web" is ignored, which is
// the documented behaviour and not an error.
//
// Copilot has no alias for skills: it loads the skills it finds on its own,
// whatever the agent's tools list says. So skill maps to nothing to grant
// (an empty list), which is not the same as a tool Copilot lacks.
var toolNames = map[agent.Tool][]string{
	agent.ToolRead:   {"read"},
	agent.ToolSearch: {"search"},
	agent.ToolEdit:   {"edit"},
	agent.ToolShell:  {"execute"},
	agent.ToolWeb:    {"web"},
	agent.ToolAgent:  {"agent"},
	agent.ToolSkill:  {},
}

// Model hints have no built-in mapping. Copilot's models are vendor names
// whose availability depends on each person's plan and changes often, so
// only a team's table maps fast, balanced or deep; without one the model is
// left to the person. A table value is the picker name ("Claude Sonnet 5"),
// rendered twice:
//
//   - model: "Claude Sonnet 5 (copilot)", the form VS Code reads;
//   - models: [claude-sonnet-5], the ID list Copilot CLI reads over model.
//
// Copilot falls back to the session's model, without an error, for a model
// the person's plan does not allow, and ignores agent models while the
// picker is on Auto and may on HydraFusion. Effort renders as
// reasoning-effort, the key both Copilot CLI (1.0.88; its docs say
// reasoningEffort) and VS Code (1.136) read.

// Adapter renders agents for GitHub Copilot. The zero value is ready to use.
type Adapter struct{}

var (
	_ target.Adapter    = (*Adapter)(nil)
	_ target.ModelGuide = (*Adapter)(nil)
)

// New returns a Copilot adapter.
func New() *Adapter { return &Adapter{} }

// Product names the tool for people.
func (*Adapter) Product() string { return "GitHub Copilot" }

// ModelHelp says what a model value looks like.
func (*Adapter) ModelHelp() string {
	return "The model's name as the Copilot model picker shows it, for example Claude Sonnet 5."
}

// ModelNotes are the ways Copilot quietly uses another model; the
// guide says the same.
func (*Adapter) ModelNotes() []string {
	return []string{
		"When the Copilot model picker is on Auto, Copilot uses the session's model for every agent and ignores the one set here. Pick a model in the picker for these settings to apply.",
		"HydraFusion, a Copilot setting in research preview, lets Copilot choose or combine models for each task. When it is chosen in the model picker, Copilot may ignore the model set here, as it does on Auto. Pick a single model in the picker for these settings to apply.",
		"If a model set here is not allowed on your Copilot plan, Copilot falls back to the session's model without an error. A wrong or unavailable name never breaks an agent; it just runs on your own model choice.",
	}
}

// Name returns the target name.
func (*Adapter) Name() string { return Name }

// MarkerDir returns the folder that shows Copilot is used: ~/.copilot for
// the person, .github for a repository.
func (*Adapter) MarkerDir(kind target.ScopeKind) string {
	if kind == target.ScopeProject {
		return ".github"
	}
	return ".copilot"
}

// frontmatter is Copilot's custom agent header. Field order is the output order.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Tools is nil to allow every tool, and points at an empty list to
	// allow none: Copilot reads tools: [] as "no tools".
	Tools           *[]string `yaml:"tools,omitempty,flow"`
	Model           string    `yaml:"model,omitempty"`
	Models          []string  `yaml:"models,omitempty,flow"`
	ReasoningEffort string    `yaml:"reasoning-effort,omitempty"`
}

// SkillsDir returns where Copilot reads skills: ~/.copilot/skills for the
// person; in a repository .claude/skills, which Copilot reads beside
// .github/skills and .agents/skills. Sharing Claude Code's folder means a
// repository installed for both tools holds each skill once; two copies
// would both be loaded by Copilot.
func (*Adapter) SkillsDir(kind target.ScopeKind) (string, error) {
	switch kind {
	case target.ScopeUser:
		return path.Join(".copilot", "skills"), nil
	case target.ScopeProject:
		return path.Join(".claude", "skills"), nil
	}
	return "", fmt.Errorf("skills: unknown scope kind %q", kind)
}

// RenderAgent returns the custom agent file for def. User scope writes under
// .copilot/agents in the home directory, project scope under .github/agents
// in the repository.
func (*Adapter) RenderAgent(def agent.Definition, run tier.Choice, scope target.Scope, stamp target.Stamp) (target.File, error) {
	p, err := filePath(def.Name, scope.Kind)
	if err != nil {
		return target.File{}, err
	}
	tools, err := mapTools(def.Tools)
	if err != nil {
		return target.File{}, err
	}
	fm := frontmatter{Name: def.Name, Description: def.Description, ReasoningEffort: string(run.Effort)}
	if run.Model != "" {
		fm.Model = pickerModel(run.Model)
		fm.Models = []string{modelID(run.Model)}
	}
	// Tools that all map to nothing (only skill) still restrict the
	// agent, so they render as an empty list, never as a missing one.
	if len(def.Tools) > 0 {
		if tools == nil {
			tools = []string{}
		}
		fm.Tools = &tools
	}
	data, err := target.Markdown(def.Name, fm, stamp, def.Prompt)
	if err != nil {
		return target.File{}, err
	}

	return target.File{Path: p, Data: data}, nil
}

// pickerModel is the VS Code form of a picker name: "Name (vendor)". A
// value that names its vendor already is kept.
func pickerModel(name string) string {
	if vendorSuffix(name) != "" {
		return name
	}
	return name + " (copilot)"
}

// modelID is Copilot CLI's ID for a picker name: without the vendor, in
// lower case, spaces as hyphens. "Claude Haiku 4.5" gives claude-haiku-4.5
// and "GPT-5.3-Codex" gpt-5.3-codex, the IDs in GitHub's model list.
func modelID(name string) string {
	name = strings.TrimSuffix(name, vendorSuffix(name))
	return strings.ToLower(strings.Join(strings.Fields(name), "-"))
}

// vendorSuffix returns a trailing " (...)" of name, or "".
func vendorSuffix(name string) string {
	if !strings.HasSuffix(name, ")") {
		return ""
	}
	i := strings.LastIndex(name, " (")
	if i <= 0 {
		return ""
	}
	return name[i:]
}

// filePath returns the agent file's path relative to the scope root.
func filePath(name string, kind target.ScopeKind) (string, error) {
	switch kind {
	case target.ScopeUser:
		return path.Join(".copilot", "agents", name+".md"), nil
	case target.ScopeProject:
		return path.Join(".github", "agents", name+".agent.md"), nil
	}
	return "", fmt.Errorf("render %s: unknown scope kind %q", name, kind)
}

func mapTools(tools []agent.Tool) ([]string, error) {
	return target.MapTools(toolNames, "Copilot", tools)
}
