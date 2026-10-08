package server

import (
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/registry"
)

// toAPI converts a registry release to its wire form.
func toAPI(r registry.Release) api.Release {
	return api.Release{
		ID:           r.ID.String(),
		Version:      r.Version.String(),
		Digest:       string(r.Digest),
		Description:  r.Description,
		Kind:         string(r.Kind),
		Size:         r.Size,
		PublishedAt:  r.PublishedAt,
		Seq:          r.Seq,
		Proposal:     r.Proposal,
		SelfApproved: r.SelfApproved,
		PublishedBy:  toAPIActorOrNil(r.PublishedBy),
	}
}

// toAPIActorOrNil is toAPIActor, or nil when nobody is known.
func toAPIActorOrNil(a registry.Actor) *api.Actor {
	if a == (registry.Actor{}) {
		return nil
	}
	out := toAPIActor(a)
	return &out
}

// toAPIAll converts a list of releases. The result is never nil, so it
// encodes as a JSON array rather than null.
func toAPIAll(rs []registry.Release) []api.Release {
	out := make([]api.Release, 0, len(rs))
	for _, r := range rs {
		out = append(out, toAPI(r))
	}
	return out
}

func toAPIActor(a registry.Actor) api.Actor {
	return api.Actor{TokenID: a.TokenID, Label: a.Label}
}

func toAPIReview(r registry.Review) api.Review {
	return api.Review{Seq: r.Seq, Author: toAPIActor(r.Author), CreatedAt: r.CreatedAt, Size: r.Size, ReportSize: r.DataSize}
}

// toAPIProposal converts a proposal to its wire form. Slices are never nil,
// so they encode as JSON arrays.
func toAPIProposal(p registry.Proposal, pr registry.Principal) api.Proposal {
	out := api.Proposal{
		ID:           p.ID,
		Package:      p.Package.String(),
		Version:      p.Version.String(),
		Digest:       string(p.Digest),
		Description:  p.Description,
		Kind:         string(p.Kind),
		Size:         p.Size,
		Author:       toAPIActor(p.Author),
		Note:         p.Note,
		Prompts:      p.PromptTexts(),
		PromptList:   make([]api.Prompt, 0, len(p.Prompts)),
		Status:       string(p.Status),
		CreatedAt:    p.CreatedAt,
		DecisionNote: p.DecisionNote,
		SelfApproved: p.SelfApproved,
		Reviews:      make([]api.Review, 0, len(p.Reviews)),
		Base:         p.Base.String(),
		ApprovedOver: p.ApprovedOver.String(),
		Replaces:     p.Replaces,
		ReplacedBy:   p.ReplacedBy,
		Yours:        new(p.By(pr)),
	}
	if !p.DecidedAt.IsZero() {
		at := p.DecidedAt
		by := toAPIActor(p.DecidedBy)
		out.DecidedAt, out.DecidedBy = &at, &by
	}
	for _, r := range p.Reviews {
		out.Reviews = append(out.Reviews, toAPIReview(r))
	}
	for _, pr := range p.Prompts {
		out.PromptList = append(out.PromptList, toAPIPrompt(pr))
	}
	return out
}

func toAPIPrompt(p registry.Prompt) api.Prompt {
	out := api.Prompt{Seq: p.Seq, Text: p.Text, Author: toAPIActor(p.Author), CreatedAt: p.CreatedAt}
	if !p.EditedAt.IsZero() {
		at := p.EditedAt
		out.EditedAt = &at
	}
	return out
}

// toAPIProposals converts proposals with where each stands now, in order.
func toAPIProposals(props []registry.Proposal, standings []registry.Standing, pr registry.Principal) []api.Proposal {
	out := make([]api.Proposal, 0, len(props))
	for i, p := range props {
		ap := toAPIProposal(p, pr)
		ap.Latest = standings[i].Latest.String()
		out = append(out, ap)
	}
	return out
}
