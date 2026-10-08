package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/jsonedit"
	"github.com/petervlad/loadout/internal/target"
)

var hookCmd = target.HookCommand{
	POSIX:      "loadout notices --hook claude 2>/dev/null || true",
	PowerShell: "if (Get-Command loadout -ErrorAction SilentlyContinue) { loadout notices --hook claude 2>$null }; exit 0",
}

// personal is a settings.json with the person's own hooks and settings,
// written the way Claude Code writes it, with a number and a string whose
// exact text must survive.
const personal = `{
  "model": "opus",
  "cleanupPeriodDays": 30.0,
  "env": {
    "GREETING": "caf\u00e9 <&>"
  },
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup",
        "hooks": [
          {
            "type": "command",
            "command": "echo hello"
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "check.sh"
          }
        ]
      }
    ]
  },
  "permissions": {
    "allow": []
  }
}
`

func TestWithHook(t *testing.T) {
	older := strings.Replace(mustWith(t, []byte(personal), hookCmd), "loadout notices", "/old/bin/loadout notices", 1)
	tests := []struct {
		name string
		in   string
	}{
		{"no file", ""},
		{"empty object", "{}\n"},
		{"no hooks", "{\n  \"model\": \"opus\",\n  \"n\": 1e3\n}\n"},
		{"other hooks only", "{\n  \"hooks\": {\n    \"Stop\": []\n  }\n}\n"},
		{"own session hooks", personal},
		{"an older loadout command", older},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in []byte
			if tc.in != "" {
				in = []byte(tc.in)
			}
			got := mustWith(t, in, hookCmd)
			golden(t, strings.ReplaceAll(tc.name, " ", "-"), got)
			if !json.Valid([]byte(got)) {
				t.Fatalf("WithHook() is not valid JSON:\n%s", got)
			}
			if again := mustWith(t, []byte(got), hookCmd); again != got {
				t.Errorf("WithHook twice changed the file:\n%s", again)
			}
			if strings.Count(got, "notices --hook claude") != 1 {
				t.Errorf("WithHook() has %d loadout hooks, want 1:\n%s", strings.Count(got, "notices --hook claude"), got)
			}
			back, err := New().WithoutHook([]byte(got))
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.in == "" && back != nil:
				t.Errorf("WithoutHook() of a file loadout made = %q, want nil (delete it)", back)
			case tc.name == "an older loadout command":
				if strings.Contains(string(back), "loadout") {
					t.Errorf("WithoutHook() left a loadout hook:\n%s", back)
				}
			case tc.in != "" && tc.name != "empty object" && string(back) != tc.in:
				t.Errorf("WithoutHook(WithHook(x)) =\n%s\nwant x back:\n%s", back, tc.in)
			}
		})
	}
}

func TestWithHookOnWindows(t *testing.T) {
	cmd := hookCmd
	cmd.Windows = true
	got := mustWith(t, nil, cmd)
	if !strings.Contains(got, `"shell": "powershell"`) || !strings.Contains(got, "Get-Command loadout") {
		t.Errorf("WithHook() on Windows =\n%s\nwant the PowerShell command", got)
	}
}

func TestWithHookRefuses(t *testing.T) {
	for _, in := range []string{
		"{ \"model\": \"opus\", }",
		"// my settings\n{}",
		"[]",
		`{"hooks": []}`,
		`{"hooks": {"SessionStart": {}}}`,
		`{"a": 1, "a": 2}`,
	} {
		if out, err := New().WithHook([]byte(in), hookCmd); err == nil || !strings.Contains(err.Error(), "loadout leaves the file as it is") {
			t.Errorf("WithHook(%q) = %q, %v; want a refusal", in, out, err)
		}
	}
}

func TestWithoutHookKeepsOthersInTheGroup(t *testing.T) {
	// The person put their own handler next to loadout's.
	in := `{"hooks": {"SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "echo hi"}, {"type": "command", "command": "loadout notices --hook claude 2>/dev/null || true", "timeout": 5}]}]}}`
	got, err := New().WithoutHook([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"hooks": {"SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "echo hi"}]}]}}`; string(got) != want {
		t.Errorf("WithoutHook() =\n%s\nwant\n%s", got, want)
	}
	if same, _ := New().WithoutHook([]byte(personal)); string(same) != personal {
		t.Error("WithoutHook() changed a file without loadout's hook")
	}
}

// FuzzWithoutWithHook: for any settings file loadout accepts, adding the
// hook and removing it keeps every member the person had, with its exact
// text.
func FuzzWithoutWithHook(f *testing.F) {
	for _, s := range []string{personal, "{}", `{"hooks":{}}`, `{"hooks":{"SessionStart":[]}}`, `{"a":[1,{"b":null}],"hooks":{"Stop":[{"hooks":[]}]}}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		with, err := New().WithHook(data, hookCmd)
		if err != nil {
			return // refused: the file stays as it is
		}
		if !json.Valid(with) {
			t.Fatalf("WithHook() gave invalid JSON:\n%s", with)
		}
		back, err := New().WithoutHook(with)
		if err != nil {
			t.Fatalf("WithoutHook(WithHook(x)): %v", err)
		}
		before, err := jsonedit.Parse(data)
		if err != nil || bytes.Contains(data, []byte("notices --hook claude")) {
			return // a new file, or one that had loadout's hook already
		}
		after := jsonedit.Node{Kind: '{'}
		if back != nil {
			if after, err = jsonedit.Parse(back); err != nil {
				t.Fatalf("WithoutHook() gave invalid JSON: %v\n%s", err, back)
			}
		}
		keepsMembers(t, data, before, back, after, "hooks")
		hooks, ok := before.Get("hooks")
		if !ok || hooks.Kind != '{' {
			return
		}
		afterHooks, _ := after.Get("hooks")
		keepsMembers(t, data, hooks, back, afterHooks, "SessionStart")
		if ss, ok := hooks.Get("SessionStart"); ok && ss.Len() > 0 {
			afterSS, _ := afterHooks.Get("SessionStart")
			if afterSS.Len() != ss.Len() {
				t.Fatalf("SessionStart has %d groups, had %d:\nbefore %s\nafter  %s", afterSS.Len(), ss.Len(), data, back)
			}
			for i, g := range ss.Elems {
				if !bytes.Equal(afterSS.Elems[i].Raw(back), g.Raw(data)) {
					t.Fatalf("SessionStart group %d changed:\nbefore %s\nafter  %s", i, data, back)
				}
			}
		}
	})
}

// keepsMembers fails unless every member of before but skip is in after,
// byte for byte.
func keepsMembers(t *testing.T, data []byte, before jsonedit.Node, back []byte, after jsonedit.Node, skip string) {
	t.Helper()
	for _, m := range before.Members {
		if m.Name == skip {
			continue
		}
		v, ok := after.Get(m.Name)
		if !ok || !bytes.Equal(v.Raw(back), m.Value.Raw(data)) {
			t.Fatalf("member %q lost or changed:\nbefore %s\nafter  %s", m.Name, data, back)
		}
	}
}

func mustWith(t *testing.T, in []byte, cmd target.HookCommand) string {
	t.Helper()
	out, err := New().WithHook(in, cmd)
	if err != nil {
		t.Fatalf("WithHook(%q): %v", in, err)
	}
	return string(out)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "hook", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("WithHook() =\n%s\nwant\n%s", got, want)
	}
}

// TestHookRoundTripsOddFormatting: files people and other tools write in
// their own style get loadout's entry added as valid JSON, a second
// WithHook changes nothing, and WithoutHook gives back the exact bytes.
func TestHookRoundTripsOddFormatting(t *testing.T) {
	tests := map[string]string{
		"tabs and CRLF":           "{\r\n\t\"model\": \"opus\",\r\n\t\"hooks\": {\r\n\t\t\"PreToolUse\": []\r\n\t}\r\n}\r\n",
		"one line":                `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo hi"}]}]},"model":"opus"}`,
		"four spaces, two groups": "{\n    \"hooks\": {\n        \"SessionStart\": [\n            {\"matcher\": \"startup\", \"hooks\": [{\"type\": \"command\", \"command\": \"echo mine\"}]},\n            {\"matcher\": \"resume\", \"hooks\": []}\n        ]\n    }\n}\n",
		"no final newline":        "{\n  \"model\": \"opus\"\n}",
		"escapes and numbers":     "{\n  \"s\": \"caf\\u00e9 \\/ ✓ <&>\",\n  \"n\": 1.50e3,\n  \"z\": -0\n}\n",
		"other hook events only":  "{\n  \"hooks\": {\n    \"PreToolUse\": [\n      {\"matcher\": \"Bash\", \"hooks\": [{\"type\": \"command\", \"command\": \"python3 \\\"$HOME/guard.py\\\"\"}]}\n    ]\n  }\n}\n",
	}
	for name, original := range tests {
		t.Run(name, func(t *testing.T) {
			with, err := New().WithHook([]byte(original), hookCmd)
			if err != nil {
				t.Fatalf("WithHook() error: %v", err)
			}
			if !json.Valid(with) || !bytes.Contains(with, []byte("notices --hook claude")) {
				t.Fatalf("WithHook() =\n%s", with)
			}
			again, err := New().WithHook(with, hookCmd)
			if err != nil || !bytes.Equal(again, with) {
				t.Errorf("WithHook() twice changed the file (%v):\n%s", err, again)
			}
			back, err := New().WithoutHook(with)
			if err != nil || string(back) != original {
				t.Errorf("WithoutHook() = %q, %v; want the original back", back, err)
			}
		})
	}
}
