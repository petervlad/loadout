package registry

import (
	"context"
	"time"

	"github.com/petervlad/loadout/internal/pack"
)

// Counts sums up what the whole registry holds, for the server page only
// admins see.
type Counts struct {
	Namespaces map[NamespaceKind]int
	// Packages counts packages by the kind of their latest release.
	Packages map[pack.Kind]int
	Releases int
	// ReleaseBytes is the size of every stored release bundle.
	ReleaseBytes int64
	Proposals    map[ProposalStatus]int
	// ProposalBytes is the size of every stored candidate bundle.
	ProposalBytes int64
	Reviews       int
	// ReviewBytes is the size of the reports attached to proposals, text
	// and data.
	ReviewBytes int64
	// MachineTokens are tokens without a user; LoginTokens are those from
	// `loadout login` that have not expired.
	MachineTokens int
	LoginTokens   int
}

// CountStore counts what a store holds across every namespace. Only the
// server page asks it, so it is kept apart from the Store interfaces the
// request paths use.
type CountStore interface {
	// Counts counts at now, which decides which login tokens have expired.
	Counts(ctx context.Context, now time.Time) (Counts, error)
}
