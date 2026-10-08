package registry_test

import (
	"errors"
	"testing"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/tier"
)

func TestResolvedTiersInheritPerValue(t *testing.T) {
	s := newService(t)
	tree(t, s)
	deep := tier.Key{Tool: "copilot", Hint: agent.ModelDeep}
	project, err := tier.New(map[tier.Key]tier.Choice{
		deep:                                     {Model: "Claude Sonnet 5", Effort: tier.EffortHigh},
		{Tool: "copilot", Hint: agent.ModelFast}: {Model: "Claude Haiku 4.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	team, err := tier.New(map[tier.Key]tier.Choice{deep: {Model: "Claude Opus 4.7"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetModelTiers(t.Context(), "acme", project); err != nil {
		t.Fatal(err)
	}
	st, err := s.SetModelTiers(t.Context(), "lime", team)
	if err != nil {
		t.Fatal(err)
	}
	if st.Namespace() != "lime" || st.Own().Digest() != team.Digest() {
		t.Errorf("SetModelTiers() stack = %s %+v, want lime's own table", st.Namespace(), st.Own().Cells())
	}

	pr := registry.Principal{Team: "lime", Scope: registry.ScopeRead, Layers: []registry.Layer{
		{Namespace: "lime", Kind: registry.KindTeam}, {Namespace: "acme", Kind: registry.KindProject, Depth: 1},
	}}
	got, err := s.ResolvedTiers(t.Context(), pr)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := got.Resolve().Get("copilot", agent.ModelDeep)
	if c != (tier.Choice{Model: "Claude Opus 4.7", Effort: tier.EffortHigh}) {
		t.Errorf("lime's copilot deep = %+v, want its own model with acme's effort", c)
	}
	if m, e := got.Origin("copilot", agent.ModelDeep); m != "lime" || e != "acme" {
		t.Errorf("Origin() = %q, %q; want lime, acme", m, e)
	}
	if c, _ := got.Resolve().Get("copilot", agent.ModelFast); c.Model != "Claude Haiku 4.5" {
		t.Errorf("lime's copilot fast = %+v, want acme's", c)
	}

	// A sibling sees only the project's table.
	sib, err := s.TiersOf(t.Context(), "koala")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := sib.Resolve().Get("copilot", agent.ModelDeep); c.Model != "Claude Sonnet 5" {
		t.Errorf("koala's copilot deep = %+v, want acme's", c)
	}
}

func TestResolvedTiersNeedAView(t *testing.T) {
	s := newService(t)
	if _, err := s.ResolvedTiers(t.Context(), registry.Principal{Account: true}); err == nil {
		t.Error("ResolvedTiers() without a view: error = nil")
	}
	// A token made before namespaces were stored has no record and no table.
	st, err := s.ResolvedTiers(t.Context(), registry.Principal{Team: "old", Scope: registry.ScopeRead})
	if err != nil || !st.Resolve().Empty() {
		t.Errorf("ResolvedTiers() of an unrecorded namespace = %+v, %v; want empty", st.Resolve().Cells(), err)
	}
	if _, err := s.TiersOf(t.Context(), "ghost"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("TiersOf(ghost) error = %v, want ErrNotFound", err)
	}
	if _, err := s.SetModelTiers(t.Context(), "ghost", tier.Table{}); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("SetModelTiers(ghost) error = %v, want ErrNotFound", err)
	}
}
