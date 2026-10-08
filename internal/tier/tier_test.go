package tier_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/tier"
)

func key(tool string, h agent.Model) tier.Key { return tier.Key{Tool: tool, Hint: h} }

func mustTable(t *testing.T, cells map[tier.Key]tier.Choice) tier.Table {
	t.Helper()
	tb, err := tier.New(cells)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func TestNewValidates(t *testing.T) {
	deep := key("copilot", agent.ModelDeep)
	tests := []struct {
		name    string
		cells   map[tier.Key]tier.Choice
		wantErr string
	}{
		{"model and effort", map[tier.Key]tier.Choice{deep: {Model: "Claude Sonnet 5", Effort: tier.EffortHigh}}, ""},
		{"effort only", map[tier.Key]tier.Choice{deep: {Effort: tier.EffortMax}}, ""},
		{"unicode model", map[tier.Key]tier.Choice{deep: {Model: "Modèle 5 (vendor)"}}, ""},
		{"80 runes", map[tier.Key]tier.Choice{deep: {Model: strings.Repeat("é", 80)}}, ""},
		{"81 runes", map[tier.Key]tier.Choice{deep: {Model: strings.Repeat("a", 81)}}, "longer than 80"},
		{"newline", map[tier.Key]tier.Choice{deep: {Model: "opus\nmodel: evil"}}, "not printable"},
		{"tab", map[tier.Key]tier.Choice{deep: {Model: "opus\t1"}}, "not printable"},
		{"leading space", map[tier.Key]tier.Choice{deep: {Model: " opus"}}, "space"},
		{"trailing space", map[tier.Key]tier.Choice{deep: {Model: "opus "}}, "space"},
		{"unknown effort", map[tier.Key]tier.Choice{deep: {Effort: "extreme"}}, `effort "extreme"`},
		{"unset hint", map[tier.Key]tier.Choice{key("copilot", agent.ModelUnset): {Model: "x"}}, "hint"},
		{"unknown hint", map[tier.Key]tier.Choice{key("copilot", "huge"): {Model: "x"}}, "hint"},
		{"bad tool", map[tier.Key]tier.Choice{key("Co Pilot", agent.ModelDeep): {Model: "x"}}, "tool name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tier.New(tc.cells)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("New() error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("New() error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestNewLimitsTools(t *testing.T) {
	cells := map[tier.Key]tier.Choice{}
	for i := range tier.MaxTools + 1 {
		cells[key("tool"+string(rune('a'+i)), agent.ModelFast)] = tier.Choice{Model: "m"}
	}
	if _, err := tier.New(cells); err == nil {
		t.Error("New() with 9 tools: error = nil")
	}
}

func TestNewDropsEmptyCells(t *testing.T) {
	tb := mustTable(t, map[tier.Key]tier.Choice{key("claude", agent.ModelFast): {}})
	if !tb.Empty() || tb.Digest() != "" {
		t.Errorf("a cell that sets nothing was kept: %+v", tb.Cells())
	}
}

func TestOverPerValue(t *testing.T) {
	deep, fast := key("copilot", agent.ModelDeep), key("copilot", agent.ModelFast)
	project := mustTable(t, map[tier.Key]tier.Choice{
		deep: {Model: "Claude Opus 4.7", Effort: tier.EffortHigh},
		fast: {Model: "Claude Haiku 4.5"},
	})
	team := mustTable(t, map[tier.Key]tier.Choice{
		deep:                           {Effort: tier.EffortMax},
		key("claude", agent.ModelDeep): {Model: "opus[1m]"},
	})
	got := team.Over(project).Cells()
	want := []tier.Cell{
		{Key: key("claude", agent.ModelDeep), Model: "opus[1m]"},
		{Key: fast, Model: "Claude Haiku 4.5"},
		{Key: deep, Model: "Claude Opus 4.7", Effort: tier.EffortMax},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Over() mismatch (-want +got):\n%s", diff)
	}
	if c, _ := project.Get("copilot", agent.ModelDeep); c.Effort != tier.EffortHigh {
		t.Errorf("Over() changed the farther table: %+v", c)
	}
}

func TestDigest(t *testing.T) {
	a := mustTable(t, map[tier.Key]tier.Choice{
		key("copilot", agent.ModelDeep): {Model: "Claude Sonnet 5"},
		key("claude", agent.ModelFast):  {Model: "haiku"},
	})
	b, err := tier.FromCells(a.Cells())
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() == "" || a.Digest() != b.Digest() {
		t.Errorf("Digest() = %q and %q, want equal and set for the same cells", a.Digest(), b.Digest())
	}
	c := mustTable(t, map[tier.Key]tier.Choice{
		key("copilot", agent.ModelDeep): {Model: "Claude Sonnet 5", Effort: tier.EffortLow},
		key("claude", agent.ModelFast):  {Model: "haiku"},
	})
	if c.Digest() == a.Digest() {
		t.Error("Digest() did not change with an effort")
	}
	if (tier.Table{}).Digest() != "" {
		t.Error("the empty table has a digest")
	}
}

func TestFromCellsRefusesDuplicates(t *testing.T) {
	k := key("claude", agent.ModelFast)
	if _, err := tier.FromCells([]tier.Cell{{Key: k, Model: "a"}, {Key: k, Model: "b"}}); err == nil {
		t.Error("FromCells() with a key twice: error = nil")
	}
}

func TestStack(t *testing.T) {
	deep := key("copilot", agent.ModelDeep)
	s := tier.NewStack(
		tier.Layer{Namespace: "lime", Table: mustTable(t, map[tier.Key]tier.Choice{deep: {Effort: tier.EffortMax}})},
		tier.Layer{Namespace: "acme", Table: mustTable(t, map[tier.Key]tier.Choice{deep: {Model: "Claude Sonnet 5", Effort: tier.EffortLow}})},
	)
	if c, _ := s.Resolve().Get("copilot", agent.ModelDeep); c != (tier.Choice{Model: "Claude Sonnet 5", Effort: tier.EffortMax}) {
		t.Errorf("Resolve() = %+v", c)
	}
	if m, e := s.Origin("copilot", agent.ModelDeep); m != "acme" || e != "lime" {
		t.Errorf("Origin() = %q, %q; want acme, lime", m, e)
	}
	if m, e := s.Origin("claude", agent.ModelDeep); m != "" || e != "" {
		t.Errorf("Origin() of an unset cell = %q, %q", m, e)
	}
	if c, _ := s.Above().Resolve().Get("copilot", agent.ModelDeep); c.Effort != tier.EffortLow {
		t.Errorf("Above() = %+v, want only acme", c)
	}
	if s.Namespace() != "lime" || len(s.Own().Cells()) != 1 {
		t.Errorf("Namespace() = %q, Own() = %+v", s.Namespace(), s.Own().Cells())
	}
	if !(tier.Stack{}).Resolve().Empty() {
		t.Error("an empty stack resolves to cells")
	}
}

func TestParseEditsAndApply(t *testing.T) {
	start := mustTable(t, map[tier.Key]tier.Choice{
		key("copilot", agent.ModelFast): {Model: "Claude Haiku 4.5"},
	})
	edits, err := tier.ParseEdits(
		[]string{"copilot:deep=Claude Sonnet 5", "copilot:fast="},
		[]string{"copilot:deep=high"},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := start.Apply(edits)
	if err != nil {
		t.Fatal(err)
	}
	want := []tier.Cell{{Key: key("copilot", agent.ModelDeep), Model: "Claude Sonnet 5", Effort: tier.EffortHigh}}
	if diff := cmp.Diff(want, got.Cells()); diff != "" {
		t.Errorf("Apply() mismatch (-want +got):\n%s", diff)
	}
	if len(start.Cells()) != 1 {
		t.Error("Apply() changed the table it was called on")
	}
}

func TestParseEditsErrors(t *testing.T) {
	for _, tc := range []struct{ models, efforts []string }{
		{models: []string{"copilot=opus"}},
		{models: []string{"copilot:deep"}},
		{models: []string{"copilot:huge=x"}},
		{efforts: []string{":deep=high"}},
	} {
		if _, err := tier.ParseEdits(tc.models, tc.efforts); err == nil {
			t.Errorf("ParseEdits(%v, %v): error = nil", tc.models, tc.efforts)
		}
	}
	edits, err := tier.ParseEdits(nil, []string{"copilot:deep=extreme"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (tier.Table{}).Apply(edits); err == nil {
		t.Error("Apply() of an unknown effort: error = nil")
	}
}
