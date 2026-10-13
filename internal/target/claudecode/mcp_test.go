package claudecode

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/target"
)

var limeServer = target.MCPServer{Name: "loadout-memory-lime", Command: "loadout", Args: []string{"mcp", "--team", "lime"}}

func TestAddMCPArgs(t *testing.T) {
	got, err := New().AddMCPArgs(limeServer)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp", "add-json", "--scope", "user", "loadout-memory-lime", `{"type":"stdio","command":"loadout","args":["mcp","--team","lime"]}`}
	if !slices.Equal(got, want) {
		t.Errorf("AddMCPArgs() = %q, want %q", got, want)
	}
}

func TestAddMCPArgsKeepsShellCharacters(t *testing.T) {
	s := target.MCPServer{Name: "loadout-memory-lime", Command: `/Users/a b/loadout&co/<x>`, Args: []string{"mcp", "--team", "lime"}}
	got, err := New().AddMCPArgs(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[5], `"command":"/Users/a b/loadout&co/<x>"`) {
		t.Errorf("JSON = %s, want no HTML escapes", got[5])
	}
}

func TestRemoveMCPArgs(t *testing.T) {
	want := []string{"mcp", "remove", "--scope", "user", "loadout-memory-lime"}
	if got := New().RemoveMCPArgs("loadout-memory-lime"); !slices.Equal(got, want) {
		t.Errorf("RemoveMCPArgs() = %q, want %q", got, want)
	}
}

func TestMCPProgramAndFile(t *testing.T) {
	a := New()
	if a.MCPProgram() != "claude" {
		t.Errorf("MCPProgram() = %q", a.MCPProgram())
	}
	home := filepath.Join(string(filepath.Separator), "h")
	abs := filepath.Join(string(filepath.Separator), "cfg")
	for dir, want := range map[string]string{
		"":        filepath.Join(home, ".claude.json"),
		abs:       filepath.Join(abs, ".claude.json"),
		"~":       filepath.Join(home, ".claude.json"),
		"~/cfg":   filepath.Join(home, "cfg", ".claude.json"),
		"rel/cfg": filepath.Join(home, ".claude.json"),
	} {
		if got := a.MCPStateFile(func(string) string { return dir }, home); got != want {
			t.Errorf("MCPStateFile with CLAUDE_CONFIG_DIR=%q = %q, want %q", dir, got, want)
		}
	}
}

func TestLoadoutServersFromClaudeJSON(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "mcp", "claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := New().LoadoutServers(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []target.MCPServer{
		limeServer,
		{Name: "loadout-memory-acme", Command: "/opt/bin/loadout", Args: []string{"mcp", "--team", "acme"}},
	}
	if len(got) != len(want) {
		t.Fatalf("LoadoutServers() = %+v, want %+v", got, want)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("LoadoutServers()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestLoadoutServersEdgeCases(t *testing.T) {
	a := New()
	for _, in := range [][]byte{nil, []byte(" \n"), []byte(`{}`), []byte(`{"mcpServers": []}`), []byte(`{"mcpServers": {"x": 5}}`)} {
		if got, err := a.LoadoutServers(in); err != nil || len(got) != 0 {
			t.Errorf("LoadoutServers(%q) = %v, %v; want none", in, got, err)
		}
	}
	for _, in := range []string{`{`, `[]`, `{"a":1,"a":2}`} {
		if got, err := a.LoadoutServers([]byte(in)); err == nil {
			t.Errorf("LoadoutServers(%q) = %v, want an error", in, got)
		}
	}
}
