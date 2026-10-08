package registry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/pack"
)

// replaceable returns the proposal id when pr may replace it with a new
// candidate of pkg: readable, of pkg, open, and pr's own. Anyone else
// proposes their own.
func (s *ProposalService) replaceable(ctx context.Context, pr Principal, pkg pack.ID, id string) (*Proposal, error) {
	old, err := s.proposals.Proposal(ctx, id)
	if err != nil || !pr.CanRead(old.Package.Team()) {
		if err == nil || errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("replace proposal %s: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("replace proposal %s: %w", id, err)
	}
	switch {
	case old.Package != pkg:
		return nil, refusal(ErrInvalid, "proposal %s is for %s, not %s; propose without replacing it.", id, old.Package, pkg)
	case old.Status != ProposalOpen:
		return nil, fmt.Errorf("replace proposal %s: %w", id, refusal(ErrProposalClosed, "it is %s; propose without replacing it.", old.Status))
	case !actorOf(pr).same(old.Author):
		return nil, refusal(ErrForbidden, "only the author updates a proposal; propose your own.")
	}
	return &old, nil
}

// promptsFor is the prompts of a new proposal: the active prompts of the
// one it replaces as they are (who added them and when kept), then the
// given ones not already there, by author at now.
func promptsFor(old *Proposal, given []string, author Actor, now time.Time) ([]Prompt, error) {
	var carried []Prompt
	if old != nil {
		carried = slices.Clone(old.Prompts)
	}
	out := carried
	for _, text := range given {
		text = strings.TrimSpace(text)
		if slices.ContainsFunc(carried, func(p Prompt) bool { return p.Text == text }) {
			continue
		}
		out = append(out, Prompt{Text: text, Author: author, CreatedAt: now})
	}
	if len(out) > MaxPrompts {
		return nil, refusal(ErrInvalid, "%d prompts with those of the proposal it replaces, limit %d; give fewer.", len(out), MaxPrompts)
	}
	for i := range out {
		out[i].Seq = i + 1
	}
	return out, nil
}

// replace stores prop in place of old in one step: old closes as
// withdrawn by the author, saying what replaced it.
func (s *ProposalService) replace(ctx context.Context, pr Principal, old, prop Proposal, data []byte) (Proposal, error) {
	d := Decision{Status: ProposalWithdrawn, By: prop.Author, At: prop.CreatedAt, Note: "replaced by " + prop.ID}
	if err := s.proposals.ReplaceProposal(ctx, old.ID, prop, data, d); err != nil {
		return Proposal{}, fmt.Errorf("propose %s@%s in place of %s: %w", prop.Package, prop.Version, old.ID, err)
	}
	prop.Replaces = old.ID
	s.log.Info("proposal replaced", "proposal", prop.ID, "replaces", old.ID, "id", prop.Package, "version", prop.Version, "base", prop.Base.String(), "token_id", pr.TokenID)
	return prop, nil
}
