package syncstate

import (
	"path/filepath"

	"github.com/petervlad/loadout/internal/install"
)

// Blockers turns the conflicts that refused an install into the files that
// hold its update back, as paths relative to the scope root.
func Blockers(root string, ce *install.ConflictError) []Blocker {
	out := make([]Blocker, len(ce.Conflicts))
	for i, c := range ce.Conflicts {
		p := c.Path
		if rel, err := filepath.Rel(root, c.Path); err == nil {
			p = filepath.ToSlash(rel)
		}
		out[i] = Blocker{Path: p, Reason: string(c.Reason)}
		if c.Reason == install.ReasonOtherPackage {
			out[i].Owner = c.Owner.String()
		}
	}
	return out
}
