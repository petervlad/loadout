package api

import "time"

// Deletion routes carry these. A request to delete a package is reviewed
// like a proposal: ids start with "del-", and closing one reuses the
// proposal error codes.
const (
	// StatusGone is a deletion status only: the package was deleted
	// another way while the request was open.
	StatusGone = "gone"

	// DeletionIDPrefix starts the id of every deletion request, which is
	// how the CLI tells it from a proposal.
	DeletionIDPrefix = "del-"
)

// DeletionRequest is the body of POST /v1/deletions.
type DeletionRequest struct {
	// Package is the package id as "team/name".
	Package string `json:"package"`
	// Reason is required: the record of a delete says why.
	Reason string `json:"reason"`
}

// Removed is what an approved deletion removed.
type Removed struct {
	Releases  []string `json:"releases"`
	Proposals int      `json:"proposals"`
	Reviews   int      `json:"reviews"`
	Prompts   int      `json:"prompts"`
}

// Deletion is a request to delete a package.
type Deletion struct {
	ID      string `json:"id"`
	Package string `json:"package"`
	Reason  string `json:"reason"`
	// Newest is the package's newest release when it was asked, empty for
	// a package that only has proposals.
	Newest string `json:"newest,omitempty"`
	Author Actor  `json:"author"`
	Status string `json:"status"`
	// CreatedAt is when it was asked; DecidedAt and DecidedBy are set once
	// it is closed.
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	DecidedBy    *Actor     `json:"decided_by,omitempty"`
	DecisionNote string     `json:"decision_note,omitempty"`
	// SelfApproved is true when the author approved it as an owner, so the
	// package was deleted without a second person.
	SelfApproved bool `json:"self_approved,omitempty"`
	// Removed is set once it is approved.
	Removed *Removed `json:"removed,omitempty"`
	// Latest is the package's newest release now, and Stale says a release
	// came out after the request, so it cannot be approved. Both are set
	// by GET /v1/deletions/{id} for an open request.
	Latest string `json:"latest,omitempty"`
	Stale  bool   `json:"stale,omitempty"`
	// Yours says whether the caller asked for it (their token, or one of
	// their user's).
	Yours bool `json:"yours"`
}

// DeletionResponse is the body of GET /v1/deletions/{id}, of a successful
// POST /v1/deletions, and of approve, reject and withdraw on a request.
type DeletionResponse struct {
	Deletion Deletion `json:"deletion"`
}

// DeletionsResponse is the body of GET /v1/deletions, newest first.
type DeletionsResponse struct {
	Deletions []Deletion `json:"deletions"`
}
