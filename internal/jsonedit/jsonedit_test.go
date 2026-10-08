package jsonedit

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParsePositions(t *testing.T) {
	data := []byte(`{ "a" : 1.50, "b": ["x", {"c": null}], "d\u0041": true }`)
	n, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != '{' || n.Start != 0 || n.End != len(data) || len(n.Members) != 3 {
		t.Fatalf("root = %+v", n)
	}
	a, _ := n.Get("a")
	if got := string(a.Raw(data)); got != "1.50" {
		t.Errorf("a = %q, want the number as written", got)
	}
	b, _ := n.Get("b")
	if len(b.Elems) != 2 || b.Elems[0].String(data) != "x" || string(b.Elems[1].Raw(data)) != `{"c": null}` {
		t.Errorf("b = %+v", b)
	}
	if n.Index("dA") != 2 || string(data[n.Members[2].Start:n.Members[2].Start+9]) != `"d\u0041"` {
		t.Errorf("escaped name: index %d, start %d", n.Index("dA"), n.Members[2].Start)
	}
}

func TestParseRefuses(t *testing.T) {
	for _, in := range []string{``, `{`, `{"a":1,}`, `// c` + "\n{}", `{"a":1,"a":2}`, `{} {}`, "{\"a\":\"\xff\"}"} {
		if _, err := Parse([]byte(in)); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalid", in, err)
		}
	}
}

func TestAppend(t *testing.T) {
	tests := []struct {
		name, in, path, want string
	}{
		{"empty object", `{}`, "", "{\n  \"k\": {\n    \"x\": 1\n  }\n}"},
		{"object with members", "{\n    \"a\": 1\n}\n", "", "{\n    \"a\": 1,\n    \"k\": {\n        \"x\": 1\n    }\n}\n"},
		{"one-line object", `{"a":1}`, "", "{\"a\":1,\n  \"k\": {\n    \"x\": 1\n  }}"},
		{"nested empty array", "{\n  \"h\": []\n}", "h", "{\n  \"h\": [\n    {\n      \"x\": 1\n    }\n  ]\n}"},
		{"tabs", "{\n\t\"h\": [\n\t\t1\n\t]\n}", "h", "{\n\t\"h\": [\n\t\t1,\n\t\t{\n\t\t\t\"x\": 1\n\t\t}\n\t]\n}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.in)
			n, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if tc.path != "" {
				n, _ = n.Get(tc.path)
			}
			got, err := Append(data, n, "k", map[string]int{"x": 1})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("Append() =\n%s\nwant\n%s", got, tc.want)
			}
			if !json.Valid(got) {
				t.Errorf("Append() is not valid JSON:\n%s", got)
			}
			// Removing what was added gives the bytes back.
			m, err := Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			if tc.path != "" {
				m, _ = m.Get(tc.path)
			}
			if back := Remove(got, m, m.Len()-1); string(back) != tc.in {
				t.Errorf("Remove(Append()) =\n%s\nwant\n%s", back, tc.in)
			}
		})
	}
}

func TestRemove(t *testing.T) {
	data := []byte("{\n  \"a\": 1,\n  \"b\": 2,\n  \"c\": 3\n}")
	n, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"{\n  \"b\": 2,\n  \"c\": 3\n}", "{\n  \"a\": 1,\n  \"c\": 3\n}", "{\n  \"a\": 1,\n  \"b\": 2\n}"} {
		if got := string(Remove(data, n, i)); got != want {
			t.Errorf("Remove(%d) =\n%s\nwant\n%s", i, got, want)
		}
	}
	one := []byte(`[ 1 ]`)
	m, _ := Parse(one)
	if got := string(Remove(one, m, 0)); got != "[]" {
		t.Errorf("Remove(only element) = %q, want []", got)
	}
}

func TestReplace(t *testing.T) {
	data := []byte("{\n  \"a\": {\"old\": true},\n  \"b\": 2\n}")
	n, _ := Parse(data)
	a, _ := n.Get("a")
	got, err := Replace(data, a, map[string]string{"cmd": "x 2>/dev/null || true"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": {\n    \"cmd\": \"x 2>/dev/null || true\"\n  },\n  \"b\": 2\n}"; string(got) != want {
		t.Errorf("Replace() =\n%s\nwant\n%s", got, want)
	}
}

// FuzzAppendRemove: whatever valid object a person has, adding a member
// and removing it again gives back exactly their bytes, and the file in
// between is valid JSON that keeps every member they had.
func FuzzAppendRemove(f *testing.F) {
	for _, s := range []string{`{}`, `{"a":1}`, "{\n  \"hooks\": {\"PreToolUse\": []},\n  \"n\": 1e3\n}", `{"x":[1,2,{"y":"z"}]}`, "{ }"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		n, err := Parse(data)
		if err != nil || n.Kind != '{' || n.Len() == 0 {
			return
		}
		got, err := Append(data, n, "loadout", map[string]any{"cmd": "loadout notices", "n": 5})
		if err != nil {
			t.Fatal(err)
		}
		m, err := Parse(got)
		if err != nil {
			t.Fatalf("Append() gave invalid JSON: %v\n%s", err, got)
		}
		for i, mem := range n.Members {
			if string(m.Members[i].Value.Raw(got)) != string(mem.Value.Raw(data)) || m.Members[i].Name != mem.Name {
				t.Fatalf("member %d changed", i)
			}
		}
		if back := Remove(got, m, m.Len()-1); string(back) != string(data) {
			t.Fatalf("Remove(Append(x)) =\n%q\nwant\n%q", back, data)
		}
	})
}
