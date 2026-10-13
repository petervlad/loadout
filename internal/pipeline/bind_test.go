package pipeline

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/agent"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file; run with -update to review the change\n--- got ---\n%s", name, got)
	}
}

func mustBinding(t *testing.T, src string) *Binding {
	t.Helper()
	p, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q) = %v", src, err)
	}
	return p.(*Binding)
}

func TestBind(t *testing.T) {
	const head = "uses: acme/dev-loop@1\nwork_folder: x/\n"
	tests := []struct {
		name    string
		binding string
		wantErr string
	}{
		{"complete", read(t, "lime-binding.yaml"), ""},
		{"unbound slots", head + "bind: {plan: a, dev: b}\n", "slot review, test has no agent"},
		{"empty value is unbound", head + "bind:\n  plan: a\n  dev: b\n  review: c\n  test:\n", "slot test has no agent"},
		{"unknown slot", head + "bind: {plan: a, dev: b, review: c, test: d, extra: e}\n", `"extra"`},
		{"gate on a gate edge", head + "bind: {plan: a, dev: b, review: c, test: d}\ngate: [{from: plan, on: READY}]\n", ""},
		{"gate on unknown edge", head + "bind: {plan: a, dev: b, review: c, test: d}\ngate: [{from: plan, on: NOPE}]\n", "plan on NOPE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Bind(mustBinding(t, tc.binding), devLoop(t))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Bind() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Bind() = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestBoundAppliesGatesAndNeverRemovesThem(t *testing.T) {
	tmpl := devLoop(t)
	if e, _ := tmpl.Edge("dev", "DONE"); e.Via != Auto {
		t.Fatalf("fixture: dev DONE is %s, want auto", e.Via)
	}
	bound, err := Bind(lime(t), tmpl)
	if err != nil {
		t.Fatal(err)
	}
	var devEdges []Edge
	for _, s := range bound.Slots() {
		if s.Name == "dev" {
			devEdges = s.Edges
		}
	}
	for _, e := range devEdges {
		if e.Via != Gate {
			t.Errorf("edge %+v after the binding's gate, want gate", e)
		}
	}
	if e, _ := tmpl.Edge("dev", "DONE"); e.Via != Auto {
		t.Error("Bind changed the template it was given")
	}
	if got := strings.Join(bound.Agents(), ","); got != "lime-planner,lime-dev,lime-code-review,lime-test" {
		t.Errorf("Agents() = %s", got)
	}
}

func TestCompatible(t *testing.T) {
	base := read(t, "dev-loop.yaml")
	tests := []struct {
		name    string
		next    string
		wantErr string
	}{
		{"same", base, ""},
		{"brief changed", strings.Replace(base, "Implement the requirement", "Build the requirement", 1), ""},
		{"file renamed", strings.ReplaceAll(base, "test.md", "verify.md"), ""},
		{"via changed", strings.Replace(base, "on: READY,             to: dev,    via: gate", "on: READY,             to: dev,    via: auto", 1), ""},
		{"edge retargeted", strings.Replace(base, "on: PASS,              to: end", "on: PASS,              to: dev", 1), ""},
		{"slot renamed", strings.NewReplacer("  test:", "  verify:", "from: test", "from: verify", "to: test", "to: verify").Replace(base), "slot test was removed"},
		{"slot removed", strings.Replace(strings.Replace(base, "  - {from: test,   on: PASS,              to: end,    via: gate}\n", "", 1), "[PASS, FAIL, INCONCLUSIVE]", "[FAIL, INCONCLUSIVE]", 1), "slot test no longer returns PASS"},
		{"verdict added", strings.Replace(strings.Replace(base, "verdicts: [READY]", "verdicts: [READY, SKIP]", 1), "edges:\n", "edges:\n  - {from: plan, on: SKIP, to: end, via: gate}\n", 1), "slot plan now also returns SKIP"},
		{"slot added", strings.Replace(base, "edges:\n", "  extra:\n    brief: x\n    writes: extra.md\n    verdicts: [OK]\nedges:\n  - {from: extra, on: OK, to: end, via: gate}\n", 1), "slot extra is new"},
	}
	prev := devLoop(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse([]byte(tc.next))
			if err != nil {
				t.Fatalf("fixture does not parse: %v", err)
			}
			err = Compatible(prev, p.(*Template))
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("Compatible() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Compatible() = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestConductorName(t *testing.T) {
	tests := []struct{ layer, name, want string }{
		{"lime", "dev-loop", "lime-dev-loop"},
		{"lime", "lime-dev-loop", "lime-dev-loop"},
		{"lime", "limes", "lime-limes"},
	}
	for _, tc := range tests {
		if got := ConductorName(tc.layer, tc.name); got != tc.want {
			t.Errorf("ConductorName(%q, %q) = %q, want %q", tc.layer, tc.name, got, tc.want)
		}
	}
}

func TestMermaid(t *testing.T) {
	golden(t, "dev-loop.mmd", Mermaid(devLoop(t), nil))
	bound, err := Bind(lime(t), devLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "lime-dev-loop.mmd", bound.Mermaid())
}

func TestScaffold(t *testing.T) {
	out := Scaffold(Ref{"acme", "dev-loop", 1}, devLoop(t))
	golden(t, "scaffold-binding.yaml", string(out))

	b := mustBinding(t, string(out))
	if _, err := Bind(b, devLoop(t)); err == nil || !strings.Contains(err.Error(), "plan, dev, review, test has no agent") {
		t.Errorf("Bind(scaffold) = %v, want every slot reported unbound", err)
	}
	filled := strings.NewReplacer("  plan:\n", "  plan: a\n", "  dev:\n", "  dev: b\n", "  review:\n", "  review: c\n", "  test:\n", "  test: d\n").Replace(string(out))
	if _, err := Bind(mustBinding(t, filled), devLoop(t)); err != nil {
		t.Errorf("Bind(filled scaffold) = %v, want nil", err)
	}
}

func TestScaffoldTemplate(t *testing.T) {
	p, err := Parse(ScaffoldTemplate())
	if err != nil {
		t.Fatalf("Parse(ScaffoldTemplate()) = %v", err)
	}
	if _, ok := p.(*Template); !ok {
		t.Errorf("ScaffoldTemplate() parsed as %T, want *Template", p)
	}
}

func TestAllowed(t *testing.T) {
	bound, err := Bind(lime(t), devLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	def := func(name string, tools ...agent.Tool) agent.Definition {
		return agent.Definition{Name: name, Description: "d", Prompt: "p", Tools: tools}
	}
	tests := []struct {
		name         string
		defs         []agent.Definition
		unrestricted bool
		unknown      string
	}{
		{"all restricted", []agent.Definition{
			def("lime-planner", agent.ToolRead), def("lime-dev", agent.ToolRead, agent.ToolEdit),
			def("lime-code-review", agent.ToolRead), def("lime-test", agent.ToolShell),
		}, false, ""},
		{"one delegates", []agent.Definition{
			def("lime-planner", agent.ToolRead), def("lime-dev", agent.ToolAgent),
			def("lime-code-review", agent.ToolRead), def("lime-test", agent.ToolShell),
		}, true, ""},
		{"no tools list means every tool", []agent.Definition{
			def("lime-planner"), def("lime-dev", agent.ToolRead),
			def("lime-code-review", agent.ToolRead), def("lime-test", agent.ToolShell),
		}, true, ""},
		{"unknown agent", []agent.Definition{
			def("lime-planner", agent.ToolRead), def("lime-dev", agent.ToolRead), def("lime-code-review", agent.ToolRead),
			def("other", agent.ToolAgent),
		}, false, "lime-test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bound.Allowed(tc.defs)
			if got.Unrestricted != tc.unrestricted {
				t.Errorf("Unrestricted = %v, want %v", got.Unrestricted, tc.unrestricted)
			}
			if strings.Join(got.Unknown, ",") != tc.unknown {
				t.Errorf("Unknown = %v, want %q", got.Unknown, tc.unknown)
			}
			if strings.Join(got.Names, ",") != "lime-planner,lime-dev,lime-code-review,lime-test" {
				t.Errorf("Names = %v", got.Names)
			}
		})
	}
}

func TestStartShell(t *testing.T) {
	bound, err := Bind(lime(t), devLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	def := func(tools ...agent.Tool) []agent.Definition {
		return []agent.Definition{{Name: "lime-planner", Description: "d", Prompt: "p", Tools: tools}}
	}
	tests := []struct {
		name         string
		defs         []agent.Definition
		shell, known bool
	}{
		{"edit only", def(agent.ToolRead, agent.ToolEdit), false, true},
		{"shell", def(agent.ToolShell), true, true},
		{"every tool", def(), true, true},
		{"missing", nil, false, false},
	}
	for _, tc := range tests {
		if shell, known := bound.StartShell(tc.defs); shell != tc.shell || known != tc.known {
			t.Errorf("%s: StartShell() = %v, %v, want %v, %v", tc.name, shell, known, tc.shell, tc.known)
		}
	}
}

func TestScaffoldGateExample(t *testing.T) {
	// The example template has only gate edges: its first slot and verdict.
	p, err := Parse(ScaffoldTemplate())
	if err != nil {
		t.Fatal(err)
	}
	out := string(Scaffold(Ref{"acme", "example", 1}, p.(*Template)))
	if !strings.Contains(out, "#   - {from: draft, on: DONE}\n") {
		t.Errorf("no first-slot gate example in:\n%s", out)
	}
	// A template with an auto edge shows that edge.
	auto := strings.Replace(string(ScaffoldTemplate()), "on: DONE,              to: check, via: gate", "on: DONE,              to: check, via: auto", 1)
	q, err := Parse([]byte(strings.Replace(auto, "to: end,   via: gate", "to: end,   via: auto", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if out := string(Scaffold(Ref{"acme", "example", 1}, q.(*Template))); !strings.Contains(out, "#   - {from: draft, on: DONE}\n") || strings.Contains(out, "from: dev") {
		t.Errorf("gate example is not the template's own auto edge:\n%s", out)
	}
}
