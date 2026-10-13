package pipeline

import (
	"strings"
	"testing"
)

func TestParseEdgeCases(t *testing.T) {
	base := read(t, "dev-loop.yaml")
	tests := []struct {
		name string
		in   string
		want string // empty means accepted
	}{
		{"self loop with cap", strings.Replace(base, "  - {from: test,   on: PASS,              to: end,    via: gate}\n", "  - {from: test,   on: PASS,              to: test,   via: gate, max_rounds: 3}\n", 1), ""},
		{"max_rounds on end edge", strings.Replace(base, "on: BLOCKED,           to: end,    via: gate", "on: BLOCKED,           to: end,    via: gate, max_rounds: 1", 1), "only allowed on a back edge"},
		{"max_rounds zero is no cap", strings.Replace(base, "max_rounds: 2", "max_rounds: 0", 1), ""},
		{"unicode verdict", strings.Replace(base, "verdicts: [READY]", "verdicts: [BEREITÉ]", 1), "verdict"},
		{"verdict leading space", strings.Replace(base, "verdicts: [READY]", "verdicts: [' READY']", 1), "verdict"},
		{"verdict trailing newline", strings.Replace(base, "verdicts: [READY]", "verdicts: [\"READY\\n\"]", 1), "verdict"},
		{"file name too long", strings.Replace(base, "writes: plan.md", "writes: "+strings.Repeat("a", 200)+".md", 1), "longer than"},
		{"file name with space", strings.Replace(base, "writes: plan.md", "writes: 'my plan.md'", 1), "only letters"},
		{"yaml alias", "start: a\nslots: &s\n  a: {brief: x, writes: a.md, verdicts: [OK]}\nedges: [{from: a, on: OK, to: end, via: gate}]\nextra: *s\n", "extra"},
		{"tabs", "start: a\nslots:\n\ta: {}\n", "parse"},
		{"binding with start", "uses: acme/d@1\nbind: {a: b}\nwork_folder: x/\nstart: a\n", "both slots and uses"},
		{"binding duplicate key", "uses: acme/d@1\nbind: {a: b, a: c}\nwork_folder: x/\n", "parse"},
		{"binding home folder", "uses: acme/d@1\nbind: {a: b}\nwork_folder: ~/runs/\n", ""},
		{"binding gate twice", "uses: acme/d@1\nbind: {a: b}\nwork_folder: x/\ngate: [{from: a, on: B}, {from: a, on: B}]\n", ""},
		{"binding model deep", "uses: acme/d@1\nbind: {a: b}\nwork_folder: x/\nmodel: deep\n", ""},
		{"only whitespace", "  \n\n", "no YAML document"},
		{"comment only", "# hi\n", "no YAML document"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Parse() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse() = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestBoundSameAgentTwice(t *testing.T) {
	src := "uses: acme/dev-loop@1\nwork_folder: x/\nbind: {plan: a, dev: a, review: b, test: b}\n"
	b, err := Bind(mustBinding(t, src), devLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(b.Agents(), ","); got != "a,b" {
		t.Errorf("Agents() = %s, want a,b", got)
	}
}

func TestScaffoldRoundTripAnyTemplate(t *testing.T) {
	p, err := Parse(ScaffoldTemplate())
	if err != nil {
		t.Fatal(err)
	}
	tmpl := p.(*Template)
	out := string(Scaffold(Ref{"acme", "example", 1}, tmpl))
	filled := strings.NewReplacer("  draft:\n", "  draft: x-draft\n", "  check:\n", "  check: x-check\n").Replace(out)
	b, err := Bind(mustBinding(t, filled), tmpl)
	if err != nil {
		t.Fatalf("Bind(filled scaffold of the example) = %v", err)
	}
	if _, err := b.Conductor("x-example", copilotWords{}); err != nil {
		t.Errorf("Conductor() = %v", err)
	}
}
