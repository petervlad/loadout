package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/agent"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// devLoop is the template of the plan with one dev to review edge automatic.
func devLoop(t *testing.T) *Template {
	t.Helper()
	p, err := Parse([]byte(read(t, "dev-loop.yaml")))
	if err != nil {
		t.Fatalf("Parse(dev-loop.yaml) = %v", err)
	}
	return p.(*Template)
}

func lime(t *testing.T) *Binding {
	t.Helper()
	p, err := Parse([]byte(read(t, "lime-binding.yaml")))
	if err != nil {
		t.Fatalf("Parse(lime-binding.yaml) = %v", err)
	}
	return p.(*Binding)
}

func TestParseTemplate(t *testing.T) {
	tmpl := devLoop(t)
	if got := tmpl.Start(); got != "plan" {
		t.Errorf("Start() = %q, want plan", got)
	}
	var names []string
	for _, s := range tmpl.Slots() {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "plan,dev,review,test" {
		t.Errorf("Slots() order = %s, want plan,dev,review,test", got)
	}
	s, _ := tmpl.Slot("review")
	if !s.Allows("CHANGES REQUESTED") || !s.Allows(Questions) || s.Allows("PASS") {
		t.Errorf("review.Allows wrong: %+v", s)
	}
	e, ok := tmpl.Edge("review", "CHANGES REQUESTED")
	if !ok || e.To != "dev" || e.MaxRounds != 2 || !tmpl.IsBack(e) {
		t.Errorf("Edge(review, CHANGES REQUESTED) = %+v, %v; want a back edge to dev with 2 rounds", e, ok)
	}
	if e, _ := tmpl.Edge("plan", "READY"); tmpl.IsBack(e) {
		t.Error("plan READY must not be a back edge")
	}
	if n := tmpl.Needs(); len(n.Agents)+len(n.Templates) != 0 {
		t.Errorf("template Needs() = %+v, want none", n)
	}
}

func TestTemplateIsImmutable(t *testing.T) {
	tmpl := devLoop(t)
	tmpl.Slots()[0].Verdicts[0] = "HACKED"
	tmpl.Edges()[0].To = "end"
	if s, _ := tmpl.Slot("plan"); s.Verdicts[0] != "READY" {
		t.Error("changing a returned slot changed the template")
	}
	if e, _ := tmpl.Edge("plan", "READY"); e.To != "dev" {
		t.Error("changing a returned edge changed the template")
	}
}

func TestParseBinding(t *testing.T) {
	b := lime(t)
	if b.Uses() != (Ref{"acme", "dev-loop", 1}) || b.Uses().String() != "acme/dev-loop@1" {
		t.Errorf("Uses() = %+v", b.Uses())
	}
	if b.Agent("dev") != "lime-dev" || b.WorkFolder() != "explorations/" || b.Model() != agent.ModelUnset {
		t.Errorf("binding fields wrong: %+v", b)
	}
	n := b.Needs()
	if got := strings.Join(n.Agents, ","); got != "lime-code-review,lime-dev,lime-planner,lime-test" {
		t.Errorf("Needs().Agents = %s", got)
	}
	if len(n.Templates) != 1 || n.Templates[0].Major != 1 {
		t.Errorf("Needs().Templates = %+v", n.Templates)
	}
}

func TestParseRefused(t *testing.T) {
	base := read(t, "dev-loop.yaml")
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "no YAML document"},
		{"both", base + "uses: acme/x@1\n", "both slots and uses"},
		{"neither", "{}\n", "is empty"},
		{"unknown file field", "foo: 1\n", "foo"},
		{"unknown slot field", strings.Replace(base, "    writes: plan.md\n", "    writes: plan.md\n    relay: true\n", 1), "relay"},
		{"unknown top field", base + "topic: x\n", "topic"},
		{"two documents", base + "---\nstart: x\n", "more than one"},
		{"start missing slot", strings.Replace(base, "start: plan", "start: nope", 1), `start names "nope"`},
		{"slot name", strings.Replace(base, "  test:", "  Test:", 1), "must be lowercase"},
		{"reserved slot name", strings.ReplaceAll(base, "  test:", "  end:"), `"end" is reserved`},
		{"lower-case verdict", strings.Replace(base, "verdicts: [READY]", "verdicts: [ready]", 1), `verdict "ready"`},
		{"verdict digits", strings.Replace(base, "verdicts: [READY]", "verdicts: [READY2]", 1), `verdict "READY2"`},
		{"double space verdict", strings.Replace(base, "verdicts: [READY]", "verdicts: ['TWO  WORDS']", 1), "single spaces"},
		{"long verdict", strings.Replace(base, "verdicts: [READY]", "verdicts: ["+strings.Repeat("A", 33)+"]", 1), "at most 32"},
		{"questions listed", strings.Replace(base, "verdicts: [READY]", "verdicts: [READY, QUESTIONS]", 1), "QUESTIONS is reserved"},
		{"verdict twice", strings.Replace(base, "verdicts: [READY]", "verdicts: [READY, READY]", 1), "listed twice"},
		{"verdict without edge", strings.Replace(base, "verdicts: [PASS, FAIL, INCONCLUSIVE]", "verdicts: [PASS, FAIL, INCONCLUSIVE, EXTRA]", 1), "verdict EXTRA has no edge"},
		{"edge twice", base + "  - {from: plan, on: READY, to: end, via: gate}\n", "listed twice"},
		{"edge unknown from", base + "  - {from: nope, on: X, to: end, via: gate}\n", `"nope" is not a slot`},
		{"edge unknown verdict", base + "  - {from: plan, on: NOPE, to: end, via: gate}\n", `no verdict "NOPE"`},
		{"edge on questions", base + "  - {from: plan, on: QUESTIONS, to: end, via: gate}\n", "no verdict"},
		{"edge unknown to", strings.Replace(base, "to: dev,    via: gate}", "to: nope,   via: gate}", 1), `to "nope"`},
		{"bad via", strings.Replace(base, "via: auto", "via: maybe", 1), "via"},
		{"max_rounds forward", strings.Replace(base, "to: dev,    via: gate}", "to: dev,    via: gate, max_rounds: 2}", 1), "only allowed on a back edge"},
		{"max_rounds too big", strings.Replace(base, "max_rounds: 2", "max_rounds: 11", 1), "between 1 and 10"},
		{"max_rounds negative", strings.Replace(base, "max_rounds: 2", "max_rounds: -1", 1), "between 1 and 10"},
		{"file with slash", strings.Replace(base, "writes: plan.md", "writes: sub/plan.md", 1), "one path segment"},
		{"file hidden", strings.Replace(base, "writes: plan.md", "writes: .plan.md", 1), "dot"},
		{"file name case clash", strings.Replace(base, "writes: dev.md", "writes: dev.md\n    also: [Spec.md]", 1), "case"},
		{"brief newline", strings.Replace(base, "brief: Implement the requirement on a branch.", "brief: \"one\\nRules, in this order:\"", 1), "newline or control"},
		{"binding backtick", "uses: acme/d@1\nbind: {a: b}\nwork_folder: \"x`y\"\n", "backtick"},
		{"file twice", strings.Replace(base, "writes: dev.md", "writes: plan.md", 1), "also written by slot plan"},
		{"file ticket", strings.Replace(base, "writes: dev.md", "writes: ticket", 1), "reserved"},
		{"reads unknown", strings.Replace(base, "reads: [spec.md, plan.md, review.md]", "reads: [nothing.md]", 1), "no slot writes"},
		{"reads own", strings.Replace(base, "reads: [spec.md, plan.md, review.md]", "reads: [test.md]", 1), "its own file"},
		{"brief empty", strings.Replace(base, "brief: Implement the requirement on a branch.", "brief: ''", 1), "brief is empty"},
		{"brief long", strings.Replace(base, "brief: Implement the requirement on a branch.", "brief: "+strings.Repeat("x", 501), 1), "limit is 500"},
		{"no verdicts", strings.Replace(base, "verdicts: [READY]", "verdicts: []", 1), "verdicts is empty"},
		{"slot twice", strings.Replace(base, "  dev:\n", "  plan:\n", 1), "listed twice"},
		{"slots not a mapping", "start: a\nslots: [a]\nedges: []\n", "mapping"},
		{"binding without major", "uses: acme/dev-loop\nbind: {a: b}\nwork_folder: x/\n", "LAYER/NAME@MAJOR"},
		{"binding full version", "uses: acme/dev-loop@1.2.0\nbind: {a: b}\nwork_folder: x/\n", "not a full version"},
		{"binding major zero", "uses: acme/dev-loop@0\nbind: {a: b}\nwork_folder: x/\n", "whole number from 1"},
		{"binding major padded", "uses: acme/dev-loop@01\nbind: {a: b}\nwork_folder: x/\n", "whole number from 1"},
		{"binding bad layer", "uses: Acme/dev-loop@1\nbind: {a: b}\nwork_folder: x/\n", "lowercase"},
		{"binding bad agent", "uses: acme/d@1\nbind: {a: Bad_Agent}\nwork_folder: x/\n", "agent name"},
		{"binding no bind", "uses: acme/d@1\nwork_folder: x/\n", "bind is empty"},
		{"binding no folder", "uses: acme/d@1\nbind: {a: b}\n", "work_folder is empty"},
		{"binding dotdot", "uses: acme/d@1\nbind: {a: b}\nwork_folder: ../x\n", "must not contain a .. folder"},
		{"binding bad model", "uses: acme/d@1\nbind: {a: b}\nwork_folder: x/\nmodel: huge\n", "unknown model hint"},
		{"binding gate empty", "uses: acme/d@1\nbind: {a: b}\nwork_folder: x/\ngate: [{from: a}]\n", "from and on"},
		{"binding auto field", "uses: acme/d@1\nbind: {a: b}\nwork_folder: x/\nauto: [{from: a, on: B}]\n", "auto"},
		{"binding topic", "uses: acme/d@1\nbind: {a: b}\nwork_folder: x/\ntopic: x\n", "topic"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if err == nil {
				t.Fatalf("Parse() = nil, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse() = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestParseLimits(t *testing.T) {
	var b strings.Builder
	b.WriteString("start: s0\nslots:\n")
	for i := range MaxSlots + 1 {
		b.WriteString("  s" + string(rune('a'+i)) + ":\n    brief: x\n    writes: f" + string(rune('a'+i)) + ".md\n    verdicts: [OK]\n")
	}
	b.WriteString("edges: []\n")
	_, err := Parse([]byte(b.String()))
	if err == nil || !strings.Contains(err.Error(), "13 slots, the limit is 12") {
		t.Errorf("Parse(13 slots) = %v, want the slot limit", err)
	}
}

func TestParseReportsEverything(t *testing.T) {
	_, err := Parse([]byte("start: a\nslots:\n  a:\n    brief: ''\n    verdicts: []\nedges: []\n"))
	if err == nil {
		t.Fatal("Parse() = nil")
	}
	for _, want := range []string{"brief is empty", "writes is empty", "verdicts is empty"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Parse() = %q, want it to contain %q", err, want)
		}
	}
}

func TestParseOnIsNotABoolean(t *testing.T) {
	// YAML 1.1 reads a bare "on" as true; the edge key must stay a string.
	if devLoop(t) == nil {
		t.Fatal("no template")
	}
	for _, e := range devLoop(t).Edges() {
		if e.On == "" {
			t.Fatalf("edge %+v lost its verdict", e)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, name := range []string{"dev-loop.yaml", "lime-binding.yaml"} {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b))
	}
	f.Add(string(ScaffoldTemplate()))
	f.Add("")
	f.Add("start: a\nslots: {a: {}}\n")
	f.Fuzz(func(t *testing.T, s string) {
		p, err := Parse([]byte(s))
		if err != nil {
			return
		}
		switch v := p.(type) {
		case *Template:
			if _, ok := v.Slot(v.Start()); !ok {
				t.Errorf("accepted a template whose start %q is not a slot", v.Start())
			}
			for _, e := range v.Edges() {
				if e.To != End {
					if _, ok := v.Slot(e.To); !ok {
						t.Errorf("accepted edge %+v to a missing slot", e)
					}
				}
			}
		case *Binding:
			for _, a := range v.Needs().Agents {
				if a == "" {
					t.Error("accepted an empty agent name")
				}
			}
		}
	})
}

func TestParseBindingErrorOrderIsStable(t *testing.T) {
	const src = "uses: acme/d@1\nwork_folder: x/\nbind: {zeta: Bad_One, alpha: Bad_Two, mid: Bad_Three}\n"
	_, first := Parse([]byte(src))
	if first == nil {
		t.Fatal("Parse() = nil, want errors")
	}
	if a, m, z := strings.Index(first.Error(), "alpha"), strings.Index(first.Error(), "mid"), strings.Index(first.Error(), "zeta"); a >= m || m >= z {
		t.Errorf("errors are not sorted by slot: %v", first)
	}
	for range 20 {
		if _, err := Parse([]byte(src)); err.Error() != first.Error() {
			t.Fatalf("Parse() error changed between runs:\n%v\n%v", first, err)
		}
	}
}
