package registry

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/tier"
)

// ResolvedTiers returns the model tables of the principal's view, closest
// first: its namespace's own table, then each ancestor's. Stack.Resolve is
// what installs in that view render with. Anyone who reads the view may
// ask; a table holds no secrets, only the names of models.
func (s *Service) ResolvedTiers(ctx context.Context, pr Principal) (tier.Stack, error) {
	if !pr.Narrowed() {
		return tier.Stack{}, errNoView
	}
	return s.stack(ctx, pr.layers())
}

// TiersOf returns the model tables of namespace name and its ancestors,
// closest first, or ErrNotFound. Callers check who may read them.
func (s *Service) TiersOf(ctx context.Context, name string) (tier.Stack, error) {
	chain, err := s.chain(ctx, name)
	if err != nil {
		return tier.Stack{}, fmt.Errorf("model tiers of %s: %w", name, err)
	}
	return s.stack(ctx, chain)
}

// SetModelTiers replaces the own model table of namespace name and returns
// its stack. Callers check who may change it.
func (s *Service) SetModelTiers(ctx context.Context, name string, t tier.Table) (tier.Stack, error) {
	if err := s.namespaces.SetModelTiers(ctx, name, t); err != nil {
		return tier.Stack{}, fmt.Errorf("namespace %s: model tiers: %w", name, err)
	}
	s.log.Info("namespace model tiers changed", "namespace", name, "cells", len(t.Cells()), "digest", t.Digest())
	return s.TiersOf(ctx, name)
}

func (s *Service) stack(ctx context.Context, chain []Layer) (tier.Stack, error) {
	layers := make([]tier.Layer, 0, len(chain))
	for _, l := range chain {
		t, err := s.namespaces.ModelTiers(ctx, l.Namespace)
		// A token made before namespaces were stored reads a namespace
		// with no record; it has no table.
		if err != nil && !errors.Is(err, ErrNotFound) {
			return tier.Stack{}, fmt.Errorf("model tiers of %s: %w", l.Namespace, err)
		}
		layers = append(layers, tier.Layer{Namespace: l.Namespace, Table: t})
	}
	return tier.NewStack(layers...), nil
}
