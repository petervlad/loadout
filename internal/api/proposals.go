package api

import (
	"time"

	"github.com/petervlad/loadout/internal/semver"
)

// Proposal routes carry these.
const (
	// CodeProposalClosed is the error code for acting on a proposal that
	// was already approved, rejected or withdrawn.
	CodeProposalClosed = "proposal_closed"

	// CodeBehind is the error code for approving a proposal that a newer
	// release came out after, without naming that release in Over.
	CodeBehind = "behind"

	// ProposalMetadataPart and ProposalBundlePart name the two parts of
	// the multipart/form-data body of POST /v1/proposals: a JSON
	// ProposalRequest and the candidate bundle.
	ProposalMetadataPart = "metadata"
	ProposalBundlePart   = "bundle"

	// ReviewContentType is the media type of an attached review.
	ReviewContentType = "text/markdown; charset=utf-8"

	// ReviewMarkdownPart and ReviewReportPart name the parts of a
	// multipart/form-data POST /v1/proposals/{id}/reviews: the Markdown
	// report and, optionally, the same result as JSON.
	ReviewMarkdownPart = "markdown"
	ReviewReportPart   = "report"
)

// Proposal statuses, as sent in Proposal.Status and the status query.
const (
	StatusOpen      = "open"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusWithdrawn = "withdrawn"
	// StatusAny is only a query value: proposals in any status.
	StatusAny = "any"
)

// ProposalRequest is the metadata part of POST /v1/proposals.
type ProposalRequest struct {
	Note string `json:"note"`
	// Prompts are the prompts the author tested the candidate with.
	Prompts []string `json:"prompts"`
	// Base is the release the candidate was made from; empty means the
	// latest release. A registry older than bases refuses the field, and
	// the client then sends the proposal again without it.
	Base string `json:"base,omitempty"`
	// Replaces is the id of the caller's own open proposal this one
	// replaces in one step, after a rebase; its prompts come along. A
	// registry older than replacing refuses the field, and the client then
	// proposes without it, leaving the old proposal open.
	Replaces string `json:"replaces,omitempty"`
}

// Actor is the token that did something. Label is informational.
type Actor struct {
	TokenID string `json:"token_id"`
	Label   string `json:"label"`
}

// Review describes one review result attached to a proposal. Its text is
// served separately by GET /v1/proposals/{id}/reviews/{seq}.
type Review struct {
	Seq       int       `json:"seq"`
	Author    Actor     `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	Size      int       `json:"size"`
	// ReportSize is the size of the JSON result sent with the text, 0 if
	// there is none.
	ReportSize int `json:"report_size,omitempty"`
}

// Prompt is one active test prompt of a proposal. Its text is untrusted:
// anyone who reads the proposal may add one.
type Prompt struct {
	// Seq numbers the prompts of one proposal from 1; a removed prompt
	// keeps its number, so numbers have gaps.
	Seq       int       `json:"seq"`
	Text      string    `json:"text"`
	Author    Actor     `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	// EditedAt is set once the text was changed.
	EditedAt *time.Time `json:"edited_at,omitempty"`
}

// PromptRequest is the body of POST /v1/proposals/{id}/prompts and of
// PUT /v1/proposals/{id}/prompts/{seq}.
type PromptRequest struct {
	Text string `json:"text"`
}

// PromptResponse is the body of a successful prompt add or edit.
type PromptResponse struct {
	Prompt Prompt `json:"prompt"`
}

// Proposal is a candidate version of a package waiting for review.
type Proposal struct {
	ID string `json:"id"`
	// Package is the package id as "team/name".
	Package     string `json:"package"`
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	Description string `json:"description"`
	// Kind is agent, skill or kb; empty means agent.
	Kind   string `json:"kind,omitempty"`
	Size   int    `json:"size"`
	Author Actor  `json:"author"`
	Note   string `json:"note"`
	// Prompts are the texts of the active prompts, oldest first: what a
	// comparison runs. Every registry sends them.
	Prompts []string `json:"prompts"`
	// PromptList is the same prompts with their numbers and who added
	// each. A registry older than editable prompts leaves it out (nil);
	// clients then read Prompts.
	PromptList []Prompt  `json:"prompt_list"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	// DecidedAt and DecidedBy are set once the proposal is closed.
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	DecidedBy    *Actor     `json:"decided_by,omitempty"`
	DecisionNote string     `json:"decision_note,omitempty"`
	// SelfApproved is true when the author approved it as an owner, so it
	// was released without a second person's review.
	SelfApproved bool     `json:"self_approved,omitempty"`
	Reviews      []Review `json:"reviews"`
	// Base is the release the proposal was made from, empty before the
	// package's first release and from older registries.
	Base string `json:"base,omitempty"`
	// Latest is the package's latest release when the proposal was read,
	// set by GET /v1/proposals and GET /v1/proposals/{id}; older
	// registries leave it out. The proposal is behind (see Behind) when a
	// release came out after its base.
	Latest string `json:"latest,omitempty"`
	// ApprovedOver is the latest release an approver released this proposal
	// over, dropping its changes; empty unless approved while behind.
	ApprovedOver string `json:"approved_over,omitempty"`
	// Replaces is the proposal this one replaced after a rebase, and
	// ReplacedBy the one that replaced it (its status stays withdrawn, so
	// older clients read it).
	Replaces   string `json:"replaces,omitempty"`
	ReplacedBy string `json:"replaced_by,omitempty"`
	// Yours says whether the caller wrote the proposal (its token, or one
	// of the same person's). Registries always send it; nil means an
	// older registry, which says nothing, so a client treats the proposal
	// as someone else's.
	Yours *bool `json:"yours,omitempty"`
}

// ByCaller reports whether the registry said the caller wrote p.
func (p Proposal) ByCaller() bool { return p.Yours != nil && *p.Yours }

// Behind reports whether a release came out after an open proposal's
// base, so approving it as it is would drop that release's changes. Both
// are releases and the base is never above the latest, so any difference
// means behind. A closed proposal is never behind (an approved one is
// behind its own release), and a registry that sends no Latest never says
// behind.
func (p Proposal) Behind() bool {
	return p.Status == StatusOpen && p.Latest != "" && p.Latest != p.Base
}

// Blocked reports whether an open proposal's version was released, or
// passed, after it was made, so it cannot be approved as that version.
func (p Proposal) Blocked() bool {
	if !p.Behind() {
		return false
	}
	latest, err1 := semver.Parse(p.Latest)
	v, err2 := semver.Parse(p.Version)
	return err1 == nil && err2 == nil && latest.Compare(v) >= 0
}

// ProposalResponse is the body of GET /v1/proposals/{id}, of a successful
// POST /v1/proposals, and of reject and withdraw.
type ProposalResponse struct {
	Proposal Proposal `json:"proposal"`
}

// ProposalsResponse is the body of GET /v1/proposals, newest first.
type ProposalsResponse struct {
	Proposals []Proposal `json:"proposals"`
}

// DecisionRequest is the optional JSON body of approve, reject and withdraw.
type DecisionRequest struct {
	Note string `json:"note"`
	// AsOwner, on approve only, says the caller approves their own
	// proposal as an owner of its namespace, without review. Clients send
	// it only when set, so a normal approve works with older registries.
	AsOwner bool `json:"as_owner,omitempty"`
	// Over, on approve only, names the latest release the approver accepts
	// releasing over when the proposal is behind it. Clients send it only
	// when given, so an approve works with older registries.
	Over string `json:"over,omitempty"`
}

// ApproveResponse is the body of a successful approve: the closed proposal
// and the release it became.
type ApproveResponse struct {
	Proposal Proposal `json:"proposal"`
	Release  Release  `json:"release"`
}

// ReviewResponse is the body of a successful POST /v1/proposals/{id}/reviews.
type ReviewResponse struct {
	Review Review `json:"review"`
}
