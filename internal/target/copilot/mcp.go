package copilot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/petervlad/loadout/internal/jsonedit"
	"github.com/petervlad/loadout/internal/target"
)

// mcpEntry is a server in mcpServers: a local program Copilot starts, with
// all of its tools on.
type mcpEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Tools   []string `json:"tools"`
}

func entryFor(s target.MCPServer) mcpEntry {
	return mcpEntry{Type: "local", Command: s.Command, Args: append([]string{}, s.Args...), Tools: []string{"*"}}
}

func (e mcpEntry) server(name string) target.MCPServer {
	return target.MCPServer{Name: name, Command: e.Command, Args: e.Args}
}

func (e mcpEntry) equal(o mcpEntry) bool {
	return e.Type == o.Type && e.Command == o.Command && slices.Equal(e.Args, o.Args) && slices.Equal(e.Tools, o.Tools)
}

// MCPFile is Copilot CLI's user-level MCP configuration.
// Source: https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers
func (*Adapter) MCPFile() string { return ".copilot/mcp-config.json" }

// mcpDoc is a parsed mcp-config.json down to mcpServers.
type mcpDoc struct {
	root, servers jsonedit.Node
	hasServers    bool
}

func parseMCP(data []byte) (mcpDoc, error) {
	root, err := jsonedit.Parse(data)
	if err != nil {
		return mcpDoc{}, mcpRefusal(fmt.Sprintf("is %v", err))
	}
	if root.Kind != '{' {
		return mcpDoc{}, mcpRefusal("is not a JSON object")
	}
	d := mcpDoc{root: root}
	if d.servers, d.hasServers = root.Get("mcpServers"); d.hasServers && d.servers.Kind != '{' {
		return mcpDoc{}, mcpRefusal(`has an "mcpServers" that is not an object`)
	}
	return d, nil
}

func mcpRefusal(what string) error {
	return fmt.Errorf("~/.copilot/mcp-config.json %s; loadout leaves the file as it is", what)
}

// ours decodes the server at member i of d.servers and reports whether
// loadout wrote it.
func (d mcpDoc) ours(data []byte, i int) (mcpEntry, bool) {
	m := d.servers.Members[i]
	var e mcpEntry
	if err := json.Unmarshal(m.Value.Raw(data), &e); err != nil {
		return e, false
	}
	return e, e.server(m.Name).IsLoadout()
}

// WithMCPServer adds s to mcpServers, creating the file or the object when
// they are not there. An older entry of loadout's under the same name is
// replaced in place.
func (*Adapter) WithMCPServer(cur []byte, s target.MCPServer) ([]byte, error) {
	entry := entryFor(s)
	if len(bytes.TrimSpace(cur)) == 0 {
		out, err := jsonedit.Marshal(map[string]any{"mcpServers": map[string]any{s.Name: entry}}, "", "  ")
		return append(out, '\n'), err
	}
	d, err := parseMCP(cur)
	if err != nil {
		return nil, err
	}
	if !d.hasServers {
		return jsonedit.Append(cur, d.root, "mcpServers", map[string]any{s.Name: entry})
	}
	i := d.servers.Index(s.Name)
	if i < 0 {
		return jsonedit.Append(cur, d.servers, s.Name, entry)
	}
	have, ok := d.ours(cur, i)
	if !ok {
		return nil, mcpRefusal(fmt.Sprintf("already has a server called %q that is not loadout's", s.Name))
	}
	if have.equal(entry) {
		return cur, nil
	}
	return jsonedit.Replace(cur, d.servers.Members[i].Value, entry)
}

// WithoutMCPServer removes loadout's entry called name, then mcpServers
// and the file's object, each only when that left it empty. nil means the
// file held nothing else.
func (*Adapter) WithoutMCPServer(cur []byte, name string) ([]byte, error) {
	if len(bytes.TrimSpace(cur)) == 0 {
		return cur, nil
	}
	d, err := parseMCP(cur)
	if err != nil {
		return nil, err
	}
	i := -1
	if d.hasServers {
		i = d.servers.Index(name)
	}
	if i < 0 {
		return cur, nil
	}
	if _, ok := d.ours(cur, i); !ok {
		return cur, nil // the person's own server with a similar name
	}
	switch {
	case d.servers.Len() > 1:
		return jsonedit.Remove(cur, d.servers, i), nil
	case d.root.Len() > 1:
		return jsonedit.Remove(cur, d.root, d.root.Index("mcpServers")), nil
	}
	return nil, nil
}

// LoadoutServers lists the names of the entries loadout wrote.
func (*Adapter) LoadoutServers(cur []byte) ([]string, error) {
	if len(bytes.TrimSpace(cur)) == 0 {
		return nil, nil
	}
	d, err := parseMCP(cur)
	if err != nil || !d.hasServers {
		return nil, err
	}
	var names []string
	for i, m := range d.servers.Members {
		if _, ok := d.ours(cur, i); ok {
			names = append(names, m.Name)
		}
	}
	return names, nil
}
