package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWithEnvDefault(t *testing.T) {
	a := New()
	name, value := a.MCPEnvVar()
	tests := []struct {
		name, in string
		keeps    string // text of the input that must survive
		same     bool
		fails    bool
	}{
		{name: "no file", in: ""},
		{name: "no env", in: "{\n  \"model\": \"opus\"\n}\n", keeps: `"model": "opus"`},
		{name: "env without it", in: "{\n  \"env\": {\n    \"A\": \"1\"\n  }\n}\n", keeps: `"A": "1"`},
		{name: "already set", in: `{"env": {"` + name + `": "100"}}`, same: true},
		{name: "env not an object", in: `{"env": []}`, fails: true},
		{name: "not json", in: `{`, fails: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := a.WithEnvDefault([]byte(tt.in), name, value)
			if (err != nil) != tt.fails {
				t.Fatalf("err = %v, want failure %v", err, tt.fails)
			}
			if tt.fails {
				return
			}
			if tt.same {
				if string(out) != tt.in {
					t.Errorf("changed a variable that was set: %s", out)
				}
				return
			}
			if !strings.Contains(string(out), tt.keeps) {
				t.Errorf("lost %q:\n%s", tt.keeps, out)
			}
			var doc struct{ Env map[string]string }
			if err := json.Unmarshal(out, &doc); err != nil || doc.Env[name] != value {
				t.Errorf("env = %v, err %v\n%s", doc.Env, err, out)
			}
		})
	}
}
