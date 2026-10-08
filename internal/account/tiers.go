package account

import (
	"context"

	"github.com/petervlad/loadout/internal/tier"
)

// CanSetTiers reports whether the user may change the namespace's model
// table: its maintainers, and those who govern it (owners of it or of a
// project above, and admins). Members only read it, through sync.
func (a Access) CanSetTiers() bool { return a.Governs || a.Role == RoleMaintainer }

// ModelTiers returns the model tables of namespace and the layers above
// it, closest first, for anyone who reads it.
func (s *Service) ModelTiers(ctx context.Context, actor Actor, namespace string) (tier.Stack, error) {
	if _, err := s.namespace(ctx, namespace); err != nil {
		return tier.Stack{}, err
	}
	a, err := s.access(ctx, actor, namespace)
	if err != nil {
		return tier.Stack{}, err
	}
	if !a.CanRead() {
		return tier.Stack{}, forbidden("the model settings of " + namespace + " are for its members")
	}
	return s.reg.TiersOf(ctx, namespace)
}

// SetModelTiers replaces the namespace's own model table; its maintainers,
// owners (of it or a project above it) and admins may. Teams below inherit it value by value.
func (s *Service) SetModelTiers(ctx context.Context, actor Actor, namespace string, t tier.Table) (tier.Stack, error) {
	if _, err := s.namespace(ctx, namespace); err != nil {
		return tier.Stack{}, err
	}
	a, err := s.access(ctx, actor, namespace)
	if err != nil {
		return tier.Stack{}, err
	}
	if !a.CanSetTiers() {
		return tier.Stack{}, forbidden("changing the model settings of " + namespace + " needs a maintainer or an owner")
	}
	st, err := s.reg.SetModelTiers(ctx, namespace, t)
	if err != nil {
		return tier.Stack{}, err
	}
	s.log.InfoContext(ctx, "model tiers set on request", "namespace", namespace, "by", actorLog(actor))
	return st, nil
}
