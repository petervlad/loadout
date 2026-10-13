package memory

import (
	"context"
	"slices"
)

// Layers returns the namespaces the person reads through ns, ns first and the
// project after it while the project has memory on. It is the access check of
// an endpoint: ErrNoMemory for a namespace the person cannot read, one that
// does not exist and one whose switch is off.
func (s *Service) Layers(ctx context.Context, c Caller, ns string) ([]string, error) {
	a, err := s.resolve(ctx, c, ns)
	if err != nil {
		return nil, err
	}
	return slices.Clone(a.readable), nil
}

// Linked returns, in the order of ids, the active entries among ids that live
// in the layers the person reads through ns. Others are left out, so a link
// to a hidden entry shows nothing. It counts no use.
func (s *Service) Linked(ctx context.Context, c Caller, ns string, ids []int64) ([]Entry, error) {
	a, err := s.resolve(ctx, c, ns)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	found, err := s.store.GetMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(found, func(e Entry) bool {
		return !e.Active() || !slices.Contains(a.readable, e.Namespace)
	}), nil
}
