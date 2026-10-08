package importer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/frontmatter"
	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/pack"
)

// AgentOptions are the settings of an agent import.
type AgentOptions struct {
	Options Options
	// DropUnknownTools imports an agent without the tools loadout has no
	// equivalent for, with a notice for each, instead of refusing it.
	DropUnknownTools bool
}

// toolNames maps the tool names of Claude Code and Copilot's tool aliases,
// lowercased, to canonical tools. Copilot compares aliases case
// insensitively and accepts Claude Code's names as aliases, so one table
// reads both.
//
// Sources:
//   - https://code.claude.com/docs/en/sub-agents
//   - https://docs.github.com/en/copilot/reference/custom-agents-configuration
var toolNames = map[string]agent.Tool{
	"read":         agent.ToolRead,
	"notebookread": agent.ToolRead,
	"search":       agent.ToolSearch,
	"grep":         agent.ToolSearch,
	"glob":         agent.ToolSearch,
	"ls":           agent.ToolSearch, // older Claude Code
	"edit":         agent.ToolEdit,
	"multiedit":    agent.ToolEdit,
	"write":        agent.ToolEdit,
	"notebookedit": agent.ToolEdit,
	"execute":      agent.ToolShell,
	"shell":        agent.ToolShell,
	"bash":         agent.ToolShell,
	"powershell":   agent.ToolShell,
	"web":          agent.ToolWeb,
	"webfetch":     agent.ToolWeb,
	"websearch":    agent.ToolWeb,
	"agent":        agent.ToolAgent,
	"task":         agent.ToolAgent,
	"custom-agent": agent.ToolAgent,
	"skill":        agent.ToolSkill,
}

// harmlessTools are tools that only touch the AI tool's own state, such as
// its task list. Dropping them takes nothing from the agent that matters.
var harmlessTools = []string{"todowrite", "todo"}

// allTools is Copilot's name for every tool, the same as no tools line.
const allTools = "*"

// modelHints maps Claude Code's model aliases, and the hints themselves,
// to model hints. Anything else is a vendor model name.
var modelHints = map[string]agent.Model{
	"haiku":    agent.ModelFast,
	"sonnet":   agent.ModelBalanced,
	"opus":     agent.ModelDeep,
	"fast":     agent.ModelFast,
	"balanced": agent.ModelBalanced,
	"deep":     agent.ModelDeep,
	// Claude Code's "use the main conversation's model", which is what
	// an unset hint means.
	"inherit": agent.ModelUnset,
}

// UnknownToolsError is returned for an agent that lists tools loadout has
// no equivalent for.
type UnknownToolsError struct {
	Source string
	Tools  []string
}

func (e *UnknownToolsError) Error() string {
	return fmt.Sprintf("%s: loadout has no equivalent for tools %s (it knows %s and their Claude Code and Copilot names)",
		e.Source, strings.Join(e.Tools, ", "), joinTools(agent.Tools()))
}

// Agents converts Claude Code and Copilot agent files into agent package
// folders in opts.Options.Out. Nothing is written unless every file
// converts.
func Agents(files []string, opts AgentOptions) ([]Result, error) {
	if err := opts.Options.checkName(len(files)); err != nil {
		return nil, err
	}
	plans := make([]plan, 0, len(files))
	var errs []error
	for _, f := range files {
		p, err := planAgent(f, opts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		plans = append(plans, p)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return write(plans)
}

func planAgent(file string, opts AgentOptions) (plan, error) {
	data, err := os.ReadFile(file) //nolint:gosec // the person names the file to import
	if err != nil {
		return plan{}, err
	}
	def, notices, err := convertAgent(file, data, opts.DropUnknownTools)
	if err != nil {
		return plan{}, err
	}
	if opts.Options.Name != "" {
		def.Name = opts.Options.Name
		if err := def.Validate(); err != nil {
			return plan{}, fmt.Errorf("%s: %w", file, err)
		}
	}
	main, err := def.Marshal()
	if err != nil {
		return plan{}, fmt.Errorf("%s: %w", file, err)
	}
	p, err := opts.Options.plan(file, def.Name, pack.KindAgent, summary(def.Description), map[string][]byte{pack.AgentFile: main}, notices)
	if err != nil {
		return plan{}, fmt.Errorf("%s: %w", file, err)
	}
	return p, nil
}

// convertAgent reads a Claude Code or Copilot agent file into canonical
// form. file names the source in errors and supplies the name when the
// frontmatter has none.
func convertAgent(file string, data []byte, dropUnknownTools bool) (agent.Definition, []string, error) {
	fail := func(err error) (agent.Definition, []string, error) {
		return agent.Definition{}, nil, fmt.Errorf("%s: %w", file, err)
	}
	data, notices, err := quoteFields(data)
	if err != nil {
		return fail(fmt.Errorf("parse frontmatter: %w", err))
	}
	meta, body, err := frontmatter.Split(data)
	if err != nil {
		return fail(err)
	}
	fields, err := frontmatter.Fields(meta)
	if err != nil {
		return fail(fmt.Errorf("parse frontmatter: %w", err))
	}

	var (
		def  = agent.Definition{Prompt: strings.TrimSpace(string(body))}
		name string
	)
	for i := 0; i+1 < len(fields); i += 2 {
		k, v := fields[i], fields[i+1]
		switch k.Value {
		case "name":
			if err := v.Decode(&name); err != nil {
				return fail(fmt.Errorf("name: %w", err))
			}
		case "description":
			if err := v.Decode(&def.Description); err != nil {
				return fail(fmt.Errorf("description: %w", err))
			}
			def.Description = strings.TrimSpace(def.Description)
		case "tools":
			tools, n, err := convertTools(file, v, dropUnknownTools)
			if err != nil {
				return agent.Definition{}, nil, err
			}
			def.Tools, notices = tools, append(notices, n...)
		case "model":
			var n string
			def.Model, n = convertModel(v)
			if n != "" {
				notices = append(notices, n)
			}
		case "disallowedTools":
			// Dropping a denial would give the agent the tools it was
			// denied, so it is never done quietly.
			return fail(errors.New("disallowedTools: loadout agents list the tools they may use and cannot deny any; list the allowed ones under tools and remove disallowedTools"))
		default:
			notices = append(notices, fmt.Sprintf("dropped field %s: loadout agents have no equivalent", k.Value))
		}
	}

	def.Name, notices = agentName(file, name, notices)
	if err := def.Validate(); err != nil {
		return fail(err)
	}
	return def, notices, nil
}

// agentName is the frontmatter name, else the file name without .md or
// .agent.md. A frontmatter name loadout cannot use (Copilot allows "Test
// Specialist") gives way to a usable file name.
func agentName(file, name string, notices []string) (string, []string) {
	base := filepath.Base(file)
	stem := strings.TrimSuffix(strings.TrimSuffix(base, ".md"), ".agent")
	switch {
	case name == "":
		return stem, notices
	case ident.Check("agent", name) != nil && ident.Check("agent", stem) == nil:
		return stem, append(notices, fmt.Sprintf("name %q is not a valid package name; used %q from the file name", name, stem))
	}
	return name, notices
}

// convertTools maps a tools value, a comma-separated string (Claude Code)
// or a list (Copilot), to canonical tools.
func convertTools(file string, v *yaml.Node, dropUnknown bool) ([]agent.Tool, []string, error) {
	var names []string
	switch {
	case v.Kind == yaml.ScalarNode && v.Tag == "!!null":
		// "tools:" with no value, the same as no line.
		return nil, nil, nil
	case v.Kind == yaml.ScalarNode:
		for n := range strings.SplitSeq(v.Value, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	case v.Kind == yaml.SequenceNode:
		if err := v.Decode(&names); err != nil {
			return nil, nil, fmt.Errorf("%s: tools: %w", file, err)
		}
	default:
		return nil, nil, fmt.Errorf("%s: tools: want a list or a comma-separated line", file)
	}

	var (
		tools   []agent.Tool
		notices []string
		unknown []string
		dropped []string
	)
	for _, n := range names {
		lower := strings.ToLower(strings.TrimSpace(n))
		if t, ok := toolNames[lower]; ok {
			if !slices.Contains(tools, t) {
				tools = append(tools, t)
			}
			continue
		}
		switch {
		case lower == allTools:
			return nil, []string{fmt.Sprintf("tools %s allow every tool; the agent gets every tool", strings.Join(names, ", "))}, nil
		case slices.Contains(harmlessTools, lower):
			notices = append(notices, fmt.Sprintf("dropped tool %s: the AI tool manages it on its own", n))
			dropped = append(dropped, n)
		case dropUnknown:
			notices = append(notices, fmt.Sprintf("dropped tool %s: loadout has no equivalent", n))
			dropped = append(dropped, n)
		default:
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		return nil, nil, &UnknownToolsError{Source: file, Tools: unknown}
	}
	// No tools line means every tool, so an agent that asked for a few
	// must never end up with none listed.
	if len(tools) == 0 {
		why := "tools is an empty list"
		if len(dropped) > 0 {
			why = "no tool is left after dropping " + strings.Join(dropped, ", ")
		}
		return nil, nil, fmt.Errorf("%s: %s, and an agent without tools gets every tool; list a tool loadout knows (%s) or remove the tools line to allow all", file, why, joinTools(agent.Tools()))
	}
	return tools, notices, nil
}

// convertModel maps a model value to a hint. A vendor model name, or a
// list of them, becomes unset with a notice.
func convertModel(v *yaml.Node) (agent.Model, string) {
	if v.Kind == yaml.ScalarNode {
		if v.Tag == "!!null" {
			return agent.ModelUnset, ""
		}
		if m, ok := modelHints[strings.ToLower(strings.TrimSpace(v.Value))]; ok {
			return m, ""
		}
		return agent.ModelUnset, fmt.Sprintf("model %q is a vendor model name; left unset, so each person's tool picks the model (hints are fast, balanced and deep)", v.Value)
	}
	return agent.ModelUnset, "model is a list of vendor models; left unset, so each person's tool picks the model (hints are fast, balanced and deep)"
}

func joinTools(tools []agent.Tool) string {
	s := make([]string, len(tools))
	for i, t := range tools {
		s[i] = string(t)
	}
	return strings.Join(s, ", ")
}
