package registry

import (
	"context"
	"time"

	"github.com/petervlad/loadout/internal/pack"
)

// DeletionStatus is where a request to delete a package is in its life.
type DeletionStatus string

// Deletion statuses. Only an open request can change. Gone means the
// package was deleted another way (directly, or by the operator) while the
// request was open.
const (
	DeletionOpen      DeletionStatus = "open"
	DeletionApproved  DeletionStatus = "approved"
	DeletionRejected  DeletionStatus = "rejected"
	DeletionWithdrawn DeletionStatus = "withdrawn"
	DeletionGone      DeletionStatus = "gone"
)

// Valid reports whether s is a known status.
func (s DeletionStatus) Valid() bool {
	switch s {
	case DeletionOpen, DeletionApproved, DeletionRejected, DeletionWithdrawn, DeletionGone:
		return true
	}
	return false
}

// Deletion is a request to delete a package, reviewed like a proposal. It
// is kept after the package is gone: the record of who asked, who decided
// and what went.
type Deletion struct {
	// ID looks like del-3f9a1c2b7d4e.
	ID      string
	Package pack.ID
	Reason  string
	// Newest is the package's newest release when it was asked, none for a
	// package that only has proposals. A release after it means the
	// request was made about something else.
	Newest Base
	Author Actor
	Status DeletionStatus
	// CreatedAt is when it was asked; DecidedAt is zero while open.
	CreatedAt    time.Time
	DecidedAt    time.Time
	DecidedBy    Actor
	DecisionNote string
	// SelfApproved marks a request its author approved as an owner of the
	// package's namespace, so it was deleted without a second person.
	SelfApproved bool
	// Removed is what went, set once it is approved.
	Removed PackageFootprint
}

// By reports whether pr asked for it: the author's token, or a token of the
// author's user.
func (d Deletion) By(pr Principal) bool { return actorOf(pr).same(d.Author) }

// DeletionFilter selects the requests of one namespace.
type DeletionFilter struct {
	Team string
	// Status limits the list to one status; empty means any.
	Status DeletionStatus
	// Limit caps the result; the newest requests are kept.
	Limit int
}

// DeletionStore keeps requests to delete packages. Implementations must be
// safe for concurrent use, and delete packages in the same database as the
// releases they remove, so an approval is one transaction.
type DeletionStore interface {
	// CreateDeletion stores an open request, or returns ErrExists when the
	// package already has one.
	CreateDeletion(ctx context.Context, d Deletion) error
	// Deletion returns one request, or ErrNotFound.
	Deletion(ctx context.Context, id string) (Deletion, error)
	// Deletions lists the requests matching f, newest first.
	Deletions(ctx context.Context, f DeletionFilter) ([]Deletion, error)
	// DecideDeletion closes an open request as rejected or withdrawn (the
	// proposal statuses of the same name in d.Status). It returns
	// ErrProposalClosed if the request is not open.
	DecideDeletion(ctx context.Context, id string, d Decision) (Deletion, error)
	// ApproveDeletion deletes the package and closes the request as
	// approved, with what went, in one transaction. It returns
	// ErrProposalClosed if the request is not open, ErrNotFound if the
	// package is already gone, and ErrStale if the package's newest release
	// is not d.Latest.
	ApproveDeletion(ctx context.Context, id string, d Decision) (Deletion, error)
	// RecordDeletion deletes the package and stores a closed, approved
	// request that says who did it and why, in one transaction. It is how a
	// delete without review (an admin's, the operator's) leaves its record.
	// Any open request of the package closes as gone. It returns
	// ErrNotFound when the package has no release and no proposal.
	RecordDeletion(ctx context.Context, d Deletion) (PackageFootprint, error)
}
