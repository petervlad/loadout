package target

import (
	"slices"
	"strings"
)

// MemoryServerPrefix starts the name of every MCP server entry loadout
// writes for a team's memory: loadout-memory-lime.
const MemoryServerPrefix = "loadout-memory-"

// MCPServer is a stdio MCP server entry loadout manages.
type MCPServer struct {
	Name    string   // "loadout-memory-lime"
	Command string   // "loadout" or its absolute path
	Args    []string // ["mcp", "--team", "lime"]
}

// Equal reports whether s and o run the same command under the same name.
func (s MCPServer) Equal(o MCPServer) bool {
	return s.Name == o.Name && s.Command == o.Command && slices.Equal(s.Args, o.Args)
}

// IsLoadout reports whether s is an entry loadout wrote: a name with the
// memory prefix and a command line that runs loadout's bridge. A server of
// the person's that happens to be named alike is not loadout's, and neither is
// one whose command only has "loadout" somewhere in its path.
func (s MCPServer) IsLoadout() bool {
	if !strings.HasPrefix(s.Name, MemoryServerPrefix) || !isLoadoutCommand(s.Command) {
		return false
	}
	return slices.Contains(s.Args, "mcp") && slices.Contains(s.Args, "--team")
}

// isLoadoutCommand is true when the command's base name is loadout: the bare
// word, or a path ending in /loadout (or \loadout.exe on Windows).
func isLoadoutCommand(cmd string) bool {
	cmd = strings.ReplaceAll(cmd, `\`, "/")
	base := cmd[strings.LastIndexByte(cmd, '/')+1:]
	return base == "loadout" || base == "loadout.exe"
}

// MCPConfig is an Adapter whose tool reads user-level MCP servers from a
// file loadout may edit. Pure functions on bytes, like SessionHook.
type MCPConfig interface {
	// MCPFile is the file, relative to the home folder and slash-separated.
	MCPFile() string
	// WithMCPServer returns cur (nil when the file does not exist) with the
	// entry s, or cur itself when it is already there. Everything else in
	// cur stays byte for byte; cur that cannot be read safely, or a server
	// of the person's under the same name, is refused, never repaired.
	WithMCPServer(cur []byte, s MCPServer) ([]byte, error)
	// WithoutMCPServer returns cur without loadout's entry called name, or
	// cur itself when it has none; nil means the file held nothing else
	// and goes.
	WithoutMCPServer(cur []byte, name string) ([]byte, error)
	// LoadoutServers lists the names of the entries loadout added.
	LoadoutServers(cur []byte) ([]string, error)
}

// MCPCommands is an Adapter whose tool rewrites its own MCP configuration
// (Claude Code and ~/.claude.json), so loadout asks the tool's command
// line to add and remove entries and only reads the file. Pure too: it
// builds argument lists and parses bytes, and runs nothing.
type MCPCommands interface {
	// MCPProgram is the program to run, found on the PATH: "claude".
	MCPProgram() string
	// AddMCPArgs is the argument list that adds s at user scope.
	AddMCPArgs(s MCPServer) ([]string, error)
	// RemoveMCPArgs is the argument list that removes the entry called
	// name at user scope.
	RemoveMCPArgs(name string) []string
	// MCPStateFile is the absolute path of the file that lists the
	// entries, given the person's home folder and getenv for the tool's
	// own config folder variable. loadout reads it and never writes it.
	MCPStateFile(getenv func(string) string, home string) string
	// LoadoutServers returns the entries loadout added, as cur (nil when
	// the file does not exist) lists them.
	LoadoutServers(cur []byte) ([]MCPServer, error)
}

// MCPEnv is an Adapter whose tool cuts what an MCP server sends it (Claude
// Code keeps 2,048 characters of a server's instructions) unless a setting
// in its own file raises the limit. loadout sets the variable once, only
// when the person has not.
type MCPEnv interface {
	// MCPEnvFile is the settings file, relative to the home folder.
	MCPEnvFile() string
	// MCPEnvVar is the variable and the value loadout suggests.
	MCPEnvVar() (name, value string)
	// WithEnvDefault returns cur (nil when the file does not exist) with
	// the variable set, or cur itself when it is already set, to anything.
	// Every other byte stays, and a file that cannot be read safely is
	// refused.
	WithEnvDefault(cur []byte, name, value string) ([]byte, error)
}
