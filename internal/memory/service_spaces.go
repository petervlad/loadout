package memory

import (
	"context"
	"fmt"

	"github.com/petervlad/loadout/internal/registry"
)

// Enabled reports whether ns has team memory on. It checks no rights, so a
// caller uses it only for a namespace the person may already see, such as
// the one whose Settings page they are on.
func (s *Service) Enabled(ctx context.Context, ns string) (bool, error) { return s.enabled(ctx, ns) }

// Place is a namespace whose team memory a person can use.
type Place struct {
	Namespace string
	Kind      registry.NamespaceKind
}

// Spaces lists the namespaces where the person has memory rights and the
// switch is on, sorted by name. It is what `install` and `sync` use to know
// which servers to set up.
func (s *Service) Spaces(ctx context.Context, c Caller) ([]Place, error) {
	on, err := s.switches.Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("memory switches: %w", err)
	}
	var out []Place
	for _, ns := range on {
		r, err := s.roles.MemoryRights(ctx, c.UserID, ns)
		if err != nil {
			return nil, fmt.Errorf("memory rights: %w", err)
		}
		if r.Read {
			out = append(out, Place{Namespace: ns, Kind: r.Kind})
		}
	}
	return out, nil
}
