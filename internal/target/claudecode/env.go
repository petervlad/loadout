package claudecode

import (
	"bytes"

	"github.com/petervlad/loadout/internal/jsonedit"
)

// Claude Code keeps 2,048 characters of an MCP server's instructions
// unless this variable says more, and the team memory index lives in those
// instructions.
// Source: https://code.claude.com/docs/en/mcp
const (
	descriptionLimit      = "CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH"
	descriptionLimitValue = "24000"
)

// MCPEnvFile is Claude Code's user settings, whose "env" object sets
// environment variables for every session.
func (*Adapter) MCPEnvFile() string { return ".claude/settings.json" }

// MCPEnvVar is the description limit loadout suggests.
func (*Adapter) MCPEnvVar() (name, value string) { return descriptionLimit, descriptionLimitValue }

// WithEnvDefault sets env.NAME when settings.json has no such variable.
func (*Adapter) WithEnvDefault(cur []byte, name, value string) ([]byte, error) {
	if len(bytes.TrimSpace(cur)) == 0 {
		out, err := jsonedit.Marshal(map[string]any{"env": map[string]string{name: value}}, "", "  ")
		return append(out, '\n'), err
	}
	s, err := parseSettings(cur)
	if err != nil {
		return nil, err
	}
	env, ok := s.root.Get("env")
	switch {
	case !ok:
		return jsonedit.Append(cur, s.root, "env", map[string]string{name: value})
	case env.Kind != '{':
		return nil, errSettings(`"env" in ~/.claude/settings.json is not an object`)
	}
	if _, set := env.Get(name); set {
		return cur, nil
	}
	return jsonedit.Append(cur, env, name, value)
}
