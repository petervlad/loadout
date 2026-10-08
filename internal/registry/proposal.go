package registry

import (
	"context"
	"errors"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// ErrProposalClosed means a proposal was already approved, rejected or
// withdrawn, so it cannot change any more.
var ErrProposalClosed = errors.New("proposal is not open")

// Limits on what a proposal carries. The registry stores all of it, so
// every field is bounded.
const (
	// MaxPrompts is how many test prompts a proposal may carry.
	MaxPrompts = 20
	// MaxPromptSize is the size limit of one prompt in bytes.
	MaxPromptSize = 4 << 10
	// maxPromptRows caps the prompts a proposal ever had, removed ones
	// included, so adding and removing cannot grow the table without end.
	maxPromptRows = 60
	// MaxNoteSize is the size limit of a proposal or decision note in bytes.
	MaxNoteSize = 4 << 10
	// MaxReviewSize is the size limit of one attached review in bytes.
	MaxReviewSize = 256 << 10
	// MaxReviews is how many reviews one proposal may collect.
	MaxReviews = 20
	// maxListed caps one Proposals listing.
	maxListed = 200
)

// ProposalStatus is where a proposal is in its life.
type ProposalStatus string

// Proposal statuses. Only an open proposal can change.
const (
	ProposalOpen      ProposalStatus = "open"
	ProposalApproved  ProposalStatus = "approved"
	ProposalRejected  ProposalStatus = "rejected"
	ProposalWithdrawn ProposalStatus = "withdrawn"
)

// Valid reports whether s is a known status.
func (s ProposalStatus) Valid() bool {
	switch s {
	case ProposalOpen, ProposalApproved, ProposalRejected, ProposalWithdrawn:
		return true
	}
	return false
}

// Actor is the token that did something, with its label for display, and
// the user behind it: the user a `loadout login` token was issued to, or
// the user who made a machine token through their account.
type Actor struct {
	TokenID string
	Label   string
	UserID  string
}

// same reports whether a and b are one token, or tokens of one user.
func (a Actor) same(b Actor) bool {
	return a.TokenID == b.TokenID || (a.UserID != "" && a.UserID == b.UserID)
}

// Proposal is a candidate version of a package waiting for review. It is
// not a release: sync never sees it until someone approves it.
type Proposal struct {
	ID          string
	Package     pack.ID
	Version     semver.Version
	Digest      pack.Digest
	Description string
	// Kind is what the package contains: agent, skill or kb.
	Kind pack.Kind
	// Size is the bundle size in bytes.
	Size   int
	Author Actor
	Note   string
	// Prompts are the active prompts, oldest first: the author's from
	// propose, then those anyone reading the proposal added.
	Prompts []Prompt
	// RemovedPrompts are the prompts people removed, by Seq, kept for the
	// activity list.
	RemovedPrompts []Prompt
	Status         ProposalStatus
	// CreatedAt is when it was proposed; DecidedAt is zero while open.
	CreatedAt    time.Time
	DecidedAt    time.Time
	DecidedBy    Actor
	DecisionNote string
	// SelfApproved marks a proposal its author approved as an owner of
	// its namespace, so it was released without a second person's review.
	SelfApproved bool
	// Reviews are the attached review results, oldest first, without text.
	Reviews []Review
	// Base is the release the proposal was made from, none before the
	// package's first release. Where that stands now is a Standing.
	Base Base
	// ApprovedOver is the latest release the approver accepted releasing
	// over, dropping its changes; none unless approved while behind.
	ApprovedOver Base
	// Replaces is the proposal this one replaced after a rebase, and
	// ReplacedBy the one that replaced it; empty when there is none.
	Replaces   string
	ReplacedBy string
}

// By reports whether pr wrote p: the author's token, or a token of the
// author's user (a machine token counting as the user who made it).
func (p Proposal) By(pr Principal) bool { return actorOf(pr).same(p.Author) }

// PromptTexts returns the texts of the active prompts, oldest first, for
// callers that only run or count them.
func (p Proposal) PromptTexts() []string {
	out := make([]string, len(p.Prompts))
	for i, pr := range p.Prompts {
		out[i] = pr.Text
	}
	return out
}

// Prompt is one test prompt of a proposal. Its text is untrusted: anyone
// who reads the proposal may add one, and only a reviewer's own command
// sends it to their own AI tool.
type Prompt struct {
	// Seq numbers the prompts of one proposal from 1. A removed prompt
	// keeps its number, so a stale form never edits a newer prompt.
	Seq       int
	Text      string
	Author    Actor
	CreatedAt time.Time
	// EditedAt is zero until the text is changed.
	EditedAt time.Time
	// RemovedAt is zero while the prompt is active; RemovedBy is who
	// removed it.
	RemovedAt time.Time
	RemovedBy Actor
}

// Review describes one attached review result, such as a comparison report.
type Review struct {
	// Seq numbers the reviews of one proposal from 1.
	Seq       int
	Author    Actor
	CreatedAt time.Time
	// Size is the length of the text in bytes.
	Size int
	// DataSize is the length of the structured result (JSON) attached with
	// the text, 0 when there is none.
	DataSize int
}

// Decision closes a proposal.
type Decision struct {
	Status ProposalStatus
	By     Actor
	At     time.Time
	Note   string
	// SelfApproved is set when the author approves as owner; the store
	// records it on the proposal and on the release it becomes.
	SelfApproved bool
	// Latest is the package's latest release the approver saw, on approval
	// only; the store refuses with ErrStale if another release landed
	// since. Over is the latest release approved over (recorded as the
	// proposal's ApprovedOver), none unless the proposal was behind it.
	Latest Base
	Over   Base
}

// ProposalFilter selects proposals of one team.
type ProposalFilter struct {
	Team string
	// Package limits the list to one package; zero means every package.
	Package pack.ID
	// Status limits the list to one status; empty means any.
	Status ProposalStatus
	// Limit caps the result; the newest proposals are kept.
	Limit int
}

// ProposalStore keeps proposals, their candidate bundles and their reviews.
// Implementations must be safe for concurrent use.
type ProposalStore interface {
	// CreateProposal stores an open proposal, its bundle, its Base and its
	// prompts, numbered from 1 in order. ApprovedOver, Replaces and
	// ReplacedBy are left empty: only a decision or a replacement sets them.
	// It returns ErrVersionConflict if the package already has an open
	// proposal for that version.
	CreateProposal(ctx context.Context, p Proposal, bundle []byte) error
	// Proposal returns one proposal with its prompts and review summaries,
	// or ErrNotFound.
	Proposal(ctx context.Context, id string) (Proposal, error)
	// ProposalBundle returns a proposal's candidate bundle, or ErrNotFound.
	ProposalBundle(ctx context.Context, id string) ([]byte, error)
	// Proposals lists the proposals matching f, newest first, with their
	// prompts and review summaries.
	Proposals(ctx context.Context, f ProposalFilter) ([]Proposal, error)
	// AddReview attaches text, and optionally its structured data, to an
	// open proposal and returns the review with Seq set. It returns
	// ErrProposalClosed if the proposal is closed and ErrInvalid if it
	// already has limit reviews.
	AddReview(ctx context.Context, id string, r Review, text, data []byte, limit int) (Review, error)
	// ReviewText returns one review and its text, or ErrNotFound.
	ReviewText(ctx context.Context, id string, seq int) (Review, []byte, error)
	// ReviewData returns one review and its structured data, or ErrNotFound
	// if the review does not exist or was attached without data.
	ReviewData(ctx context.Context, id string, seq int) (Review, []byte, error)
	// DecideProposal closes an open proposal as rejected or withdrawn. It
	// returns ErrProposalClosed if the proposal is not open.
	DecideProposal(ctx context.Context, id string, d Decision) (Proposal, error)
	// AddPrompt appends p to an open proposal and returns it with Seq
	// set. It returns ErrProposalClosed if the proposal is closed and
	// ErrInvalid if it already has limit active prompts or maxRows
	// prompts in all.
	AddPrompt(ctx context.Context, id string, p Prompt, limit, maxRows int) (Prompt, error)
	// EditPrompt replaces the text of an active prompt of an open proposal
	// and sets its EditedAt. It returns ErrNotFound if the prompt does not
	// exist or was removed.
	EditPrompt(ctx context.Context, id string, seq int, text string, at time.Time) (Prompt, error)
	// RemovePrompt marks an active prompt of an open proposal removed; its
	// Seq is never reused. It returns ErrInvalid if it is the last active
	// prompt, since a proposal keeps at least one.
	RemovePrompt(ctx context.Context, id string, seq int, by Actor, at time.Time) error
	// ApproveProposal turns an open proposal into a release in one
	// transaction: the release is stored with PublishedAt d.At, Proposal
	// id, d.SelfApproved and PublishedBy d.By (the approver released it),
	// and the proposal is marked approved (with d.SelfApproved too), or
	// neither happens. d.Over is recorded as ApprovedOver. It returns
	// ErrProposalClosed if the proposal is not open, ErrVersionConflict
	// if the package has a release at or above the proposed version, and
	// ErrStale if the package's latest release is not d.Latest (checked in
	// the same transaction, so nobody approves over a release they never
	// saw).
	ApproveProposal(ctx context.Context, id string, d Decision) (Proposal, Release, error)
	// ReplaceProposal closes the open proposal old as withdrawn by d.By
	// with d.Note, links the two (old's ReplacedBy, p's Replaces) and
	// stores p with its bundle and prompts (each with its author and
	// times), in one transaction: old is closed first, so p may keep old's
	// version. It returns ErrProposalClosed if old is not open (approved,
	// rejected or replaced in the meantime) and ErrVersionConflict like
	// CreateProposal.
	ReplaceProposal(ctx context.Context, old string, p Proposal, bundle []byte, d Decision) error
}
