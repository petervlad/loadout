package target

import "testing"

func TestMCPServerIsLoadout(t *testing.T) {
	tests := []struct {
		name string
		s    MCPServer
		want bool
	}{
		{"bare", MCPServer{"loadout-memory-lime", "loadout", []string{"mcp", "--team", "lime"}}, true},
		{"absolute path", MCPServer{"loadout-memory-lime", "/opt/bin/loadout", []string{"mcp", "--team", "lime"}}, true},
		{"other name", MCPServer{"github", "loadout", []string{"mcp", "--team", "lime"}}, false},
		{"windows path", MCPServer{"loadout-memory-lime", `C:\Tools\loadout.exe`, []string{"mcp", "--team", "lime"}}, true},
		{"loadout only in the path", MCPServer{"loadout-memory-lime", "/home/loadout/bin/my-memory", []string{"mcp", "--team", "lime"}}, false},
		{"loadout as a prefix of the command", MCPServer{"loadout-memory-lime", "loadout-evil", []string{"mcp", "--team", "lime"}}, false},
		{"loadout as a suffix of the command", MCPServer{"loadout-memory-lime", "/usr/bin/notloadout", []string{"mcp", "--team", "lime"}}, false},
		{"other command", MCPServer{"loadout-memory-lime", "my-memory", []string{"mcp", "--team", "lime"}}, false},
		{"no bridge arguments", MCPServer{"loadout-memory-lime", "loadout", []string{"sync"}}, false},
		{"no arguments", MCPServer{"loadout-memory-lime", "loadout", nil}, false},
	}
	for _, tc := range tests {
		if got := tc.s.IsLoadout(); got != tc.want {
			t.Errorf("%s: IsLoadout() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMCPServerEqual(t *testing.T) {
	a := MCPServer{"n", "loadout", []string{"mcp", "--team", "x"}}
	b := MCPServer{"n", "loadout", []string{"mcp", "--team", "x"}}
	if !a.Equal(b) || a.Equal(MCPServer{"n", "loadout", []string{"mcp"}}) || a.Equal(MCPServer{"m", "loadout", a.Args}) {
		t.Error("Equal() compares the name, the command and the arguments")
	}
}
