package copilot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/jsonedit"
	"github.com/petervlad/loadout/internal/target"
)

var lime = target.MCPServer{Name: "loadout-memory-lime", Command: "loadout", Args: []string{"mcp", "--team", "lime"}}

// personMCP is a file of the person's, with a server of their own, a
// number and a string whose exact text must survive.
const personMCP = `{
  "mcpServers": {
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "tools": ["*"]
    }
  },
  "ratio": 1.50e3,
  "note": "caf\u00e9 <&>"
}
`

func goldenMCP(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "mcp", name+".golden.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("WithMCPServer() =\n%s\nwant\n%s", got, want)
	}
}

func TestWithMCPServer(t *testing.T) {
	tests := map[string]string{
		"missing file":        "",
		"person file":         personMCP,
		"no mcpServers":       "{\n  \"banner\": false\n}\n",
		"empty mcpServers":    "{\n  \"mcpServers\": {}\n}\n",
		"four spaces and tab": "{\n    \"mcpServers\": {\n        \"a\": {\"type\": \"local\", \"command\": \"a\", \"args\": [], \"tools\": [\"*\"]}\n    }\n}\n",
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			var cur []byte
			if in != "" {
				cur = []byte(in)
			}
			got, err := New().WithMCPServer(cur, lime)
			if err != nil {
				t.Fatal(err)
			}
			goldenMCP(t, strings.ReplaceAll(name, " ", "-"), got)
			if !json.Valid(got) {
				t.Fatalf("invalid JSON:\n%s", got)
			}
			if again, err := New().WithMCPServer(got, lime); err != nil || !bytes.Equal(again, got) {
				t.Errorf("WithMCPServer() twice = %q, %v; want the same bytes", again, err)
			}
			names, err := New().LoadoutServers(got)
			if err != nil || !slices.Equal(names, []string{lime.Name}) {
				t.Errorf("LoadoutServers() = %v, %v", names, err)
			}
		})
	}
}

func TestEntryShape(t *testing.T) {
	got, err := New().WithMCPServer(nil, lime)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"local","command":"loadout","args":["mcp","--team","lime"],"tools":["*"]}`
	root, _ := jsonedit.Parse(got)
	servers, _ := root.Get("mcpServers")
	e, _ := servers.Get(lime.Name)
	var compact bytes.Buffer
	if err := json.Compact(&compact, e.Raw(got)); err != nil || compact.String() != want {
		t.Errorf("entry = %s, want %s", compact.String(), want)
	}
}

func TestMCPRoundTripGivesTheOriginal(t *testing.T) {
	tests := map[string]string{
		"person file":      personMCP,
		"no mcpServers":    "{\n  \"banner\": false\n}\n",
		"tabs and CRLF":    "{\r\n\t\"mcpServers\": {\r\n\t\t\"a\": {\"command\": \"a\"}\r\n\t},\r\n\t\"x\": 1\r\n}\r\n",
		"one line":         `{"mcpServers":{"a":{"command":"a"}},"x":[1,2]}`,
		"no final newline": "{\n  \"x\": 1\n}",
		"escapes":          "{\n  \"s\": \"caf\\u00e9 \\/ <&>\",\n  \"n\": -0\n}\n",
	}
	for name, original := range tests {
		t.Run(name, func(t *testing.T) {
			with, err := New().WithMCPServer([]byte(original), lime)
			if err != nil {
				t.Fatal(err)
			}
			back, err := New().WithoutMCPServer(with, lime.Name)
			if err != nil || string(back) != original {
				t.Errorf("WithoutMCPServer(WithMCPServer(x)) = %q, %v; want %q", back, err, original)
			}
		})
	}
}

func TestWithoutMCPServerOnlyFile(t *testing.T) {
	with, _ := New().WithMCPServer(nil, lime)
	if out, err := New().WithoutMCPServer(with, lime.Name); err != nil || out != nil {
		t.Errorf("WithoutMCPServer() = %q, %v; want nil: the file held only loadout's entry", out, err)
	}
	if out, err := New().WithoutMCPServer(nil, lime.Name); err != nil || out != nil {
		t.Errorf("WithoutMCPServer(nil) = %q, %v", out, err)
	}
}

func TestMCPKeepsServersThatAreNotLoadouts(t *testing.T) {
	mine := `{"mcpServers": {"loadout-memory-lime": {"type": "local", "command": "my-thing", "args": [], "tools": ["*"]}, "x": {}}}`
	if out, err := New().WithoutMCPServer([]byte(mine), lime.Name); err != nil || string(out) != mine {
		t.Errorf("WithoutMCPServer() changed the person's own server: %q, %v", out, err)
	}
	if _, err := New().WithMCPServer([]byte(mine), lime); err == nil || !strings.Contains(err.Error(), "not loadout's") {
		t.Errorf("WithMCPServer() over the person's server = %v, want a refusal", err)
	}
	if names, _ := New().LoadoutServers([]byte(mine)); len(names) != 0 {
		t.Errorf("LoadoutServers() = %v, want none", names)
	}
}

func TestWithMCPServerUpdatesInPlace(t *testing.T) {
	old := target.MCPServer{Name: lime.Name, Command: "/old/loadout", Args: lime.Args}
	first, _ := New().WithMCPServer([]byte(personMCP), old)
	second, err := New().WithMCPServer(first, lime)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(second), "/old/") || strings.Count(string(second), lime.Name) != 1 {
		t.Errorf("entry not replaced:\n%s", second)
	}
	back, _ := New().WithoutMCPServer(second, lime.Name)
	if string(back) != personMCP {
		t.Errorf("remove after update =\n%s", back)
	}
}

func TestMCPRefuses(t *testing.T) {
	tests := map[string]string{
		"invalid JSON":         `{"mcpServers": {},}`,
		"comments":             "{\n // hi\n}",
		"not an object":        `[]`,
		"mcpServers is a list": `{"mcpServers": []}`,
		"duplicate names":      `{"a": 1, "a": 2}`,
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			if out, err := New().WithMCPServer([]byte(in), lime); err == nil || out != nil || !strings.Contains(err.Error(), "loadout leaves the file as it is") {
				t.Errorf("WithMCPServer() = %q, %v; want a refusal", out, err)
			}
			if out, err := New().WithoutMCPServer([]byte(in), lime.Name); err == nil || out != nil {
				t.Errorf("WithoutMCPServer() = %q, %v; want a refusal", out, err)
			}
		})
	}
}

func TestMCPFile(t *testing.T) {
	if got := New().MCPFile(); got != ".copilot/mcp-config.json" {
		t.Errorf("MCPFile() = %q", got)
	}
}

// FuzzMCPRoundTrip: for any file loadout accepts, adding the entry and
// removing it keeps every other member with its exact text. When the file
// had no empty mcpServers, it gives back the same bytes.
func FuzzMCPRoundTrip(f *testing.F) {
	for _, s := range []string{personMCP, "{}", `{"mcpServers":{}}`, `{"a":[1,{"b":null}],"mcpServers":{"x":{"command":"y"}}}`, "{\n\t\"mcpServers\": {\n\t\t\"x\": 1\n\t}\n}"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		with, err := New().WithMCPServer(data, lime)
		if err != nil {
			return // refused: the file stays as it is
		}
		if !json.Valid(with) {
			t.Fatalf("WithMCPServer() gave invalid JSON:\n%s", with)
		}
		back, err := New().WithoutMCPServer(with, lime.Name)
		if err != nil {
			t.Fatalf("WithoutMCPServer(WithMCPServer(x)): %v", err)
		}
		before, err := jsonedit.Parse(data)
		if err != nil {
			return // a new file
		}
		if servers, ok := before.Get("mcpServers"); ok && servers.Len() == 0 || before.Len() == 0 || bytes.Contains(data, []byte(lime.Name)) {
			return // an empty object or file goes with our entry, and an entry that was there is removed
		}
		if string(back) != string(data) {
			t.Fatalf("round trip changed the file:\nbefore %q\nafter  %q", data, back)
		}
	})
}
