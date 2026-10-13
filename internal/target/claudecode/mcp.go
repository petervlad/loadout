package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/petervlad/loadout/internal/jsonedit"
	"github.com/petervlad/loadout/internal/target"
)

// Claude Code rewrites ~/.claude.json itself, often, so loadout never edits
// it: `claude mcp add-json` and `claude mcp remove` change it, and loadout
// only reads it to see which entries are its own.
// Source: https://code.claude.com/docs/en/mcp

// MCPProgram is the program that manages Claude Code's MCP servers.
func (*Adapter) MCPProgram() string { return "claude" }

// MCPStateFile is the file that lists user-scope MCP servers: .claude.json
// in CLAUDE_CONFIG_DIR when that names a folder, else in the home folder,
// which is where `claude mcp add-json` writes it.
func (*Adapter) MCPStateFile(getenv func(string) string, home string) string {
	dir := getenv("CLAUDE_CONFIG_DIR")
	switch {
	case dir == "~":
		dir = home
	case strings.HasPrefix(dir, "~/"):
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		dir = home
	}
	return filepath.Join(dir, ".claude.json")
}

// stdioServer is the JSON `claude mcp add-json` takes for a local server.
type stdioServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// AddMCPArgs is `mcp add-json --scope user NAME JSON`.
func (*Adapter) AddMCPArgs(s target.MCPServer) ([]string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(stdioServer{Type: "stdio", Command: s.Command, Args: append([]string{}, s.Args...)}); err != nil {
		return nil, err
	}
	return []string{"mcp", "add-json", "--scope", "user", s.Name, string(bytes.TrimSpace(b.Bytes()))}, nil
}

// RemoveMCPArgs is `mcp remove --scope user NAME`.
func (*Adapter) RemoveMCPArgs(name string) []string {
	return []string{"mcp", "remove", "--scope", "user", name}
}

// LoadoutServers reads the top-level mcpServers of ~/.claude.json, which
// holds the user-scope servers, and returns the entries loadout added.
func (*Adapter) LoadoutServers(cur []byte) ([]target.MCPServer, error) {
	if len(bytes.TrimSpace(cur)) == 0 {
		return nil, nil
	}
	root, err := jsonedit.Parse(cur)
	if err != nil {
		return nil, fmt.Errorf("~/.claude.json is %w", err)
	}
	if root.Kind != '{' {
		return nil, errors.New("~/.claude.json is not a JSON object")
	}
	servers, ok := root.Get("mcpServers")
	if !ok || servers.Kind != '{' {
		return nil, nil
	}
	var out []target.MCPServer
	for _, m := range servers.Members {
		var e stdioServer
		s := target.MCPServer{Name: m.Name}
		if json.Unmarshal(m.Value.Raw(cur), &e) == nil {
			s.Command, s.Args = e.Command, e.Args
		}
		if s.IsLoadout() {
			out = append(out, s)
		}
	}
	return out, nil
}
