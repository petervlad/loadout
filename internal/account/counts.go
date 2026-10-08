package account

import (
	"context"
	"time"
)

// Counts sums up the accounts of the whole registry, for the server page
// only admins see.
type Counts struct {
	Users    int
	Verified int
	Admins   int
	Members  map[Role]int
	// Sessions are web sessions that have not expired.
	Sessions int
}

// CountStore counts accounts across the registry. Only the server page asks
// it, so it is kept apart from Store.
type CountStore interface {
	// AccountCounts counts at now, which decides which sessions expired.
	AccountCounts(ctx context.Context, now time.Time) (Counts, error)
}
