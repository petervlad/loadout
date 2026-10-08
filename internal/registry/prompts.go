package registry

import (
	"context"
	"fmt"
	"strings"
)

// AddPrompt adds a test prompt to an open proposal. Anyone who can read the
// proposal may add one, the same people who may attach a review, so a team
// below can ask a project proposal the questions it cares about. The text is
// stored as it is (trimmed) and never run by the registry.
func (s *ProposalService) AddPrompt(ctx context.Context, pr Principal, id, text string) (Prompt, error) {
	p, pr, err := s.proposalAs(ctx, pr, id)
	if err != nil {
		return Prompt{}, err
	}
	if err := checkOpen(p); err != nil {
		return Prompt{}, err
	}
	if err := checkPrompt(text); err != nil {
		return Prompt{}, fmt.Errorf("%w: the prompt %w", ErrInvalid, err)
	}
	in := Prompt{Text: strings.TrimSpace(text), Author: actorOf(pr), CreatedAt: s.now().UTC()}
	out, err := s.proposals.AddPrompt(ctx, id, in, MaxPrompts, maxPromptRows)
	if err != nil {
		return Prompt{}, fmt.Errorf("add a prompt to %s: %w", id, err)
	}
	s.log.Info("prompt added", "proposal", id, "prompt", out.Seq, "bytes", len(out.Text), "token_id", pr.TokenID)
	return out, nil
}

// EditPrompt replaces the text of an active prompt. Only the person who
// added it may: the author edits theirs, a reviewer theirs. Maintainers get
// no override, so nobody's question is reworded under their name.
func (s *ProposalService) EditPrompt(ctx context.Context, pr Principal, id string, seq int, text string) (Prompt, error) {
	pr, err := s.ownPrompt(ctx, pr, id, seq, "edit")
	if err != nil {
		return Prompt{}, err
	}
	if err := checkPrompt(text); err != nil {
		return Prompt{}, fmt.Errorf("%w: the prompt %w", ErrInvalid, err)
	}
	out, err := s.proposals.EditPrompt(ctx, id, seq, strings.TrimSpace(text), s.now().UTC())
	if err != nil {
		return Prompt{}, fmt.Errorf("edit prompt %d of %s: %w", seq, id, err)
	}
	s.log.Info("prompt edited", "proposal", id, "prompt", seq, "bytes", len(out.Text), "token_id", pr.TokenID)
	return out, nil
}

// RemovePrompt removes an active prompt. Like EditPrompt, only the person
// who added it may. The prompt keeps its number and stays in the activity
// list; a proposal keeps at least one prompt.
func (s *ProposalService) RemovePrompt(ctx context.Context, pr Principal, id string, seq int) error {
	pr, err := s.ownPrompt(ctx, pr, id, seq, "remove")
	if err != nil {
		return err
	}
	if err := s.proposals.RemovePrompt(ctx, id, seq, actorOf(pr), s.now().UTC()); err != nil {
		return fmt.Errorf("remove prompt %d of %s: %w", seq, id, err)
	}
	s.log.Info("prompt removed", "proposal", id, "prompt", seq, "token_id", pr.TokenID)
	return nil
}

// ownPrompt checks that prompt seq of an open proposal is active and that
// pr added it, and returns pr narrowed to the proposal's namespace.
func (s *ProposalService) ownPrompt(ctx context.Context, pr Principal, id string, seq int, verb string) (Principal, error) {
	p, pr, err := s.proposalAs(ctx, pr, id)
	if err != nil {
		return Principal{}, err
	}
	if err := checkOpen(p); err != nil {
		return Principal{}, err
	}
	prompt, ok := p.activePrompt(seq)
	if !ok {
		return Principal{}, fmt.Errorf("%w: proposal %s has no prompt %d; it may have been removed", ErrNotFound, id, seq)
	}
	if !actorOf(pr).same(prompt.Author) {
		return Principal{}, fmt.Errorf("%w: only the person who added prompt %d can %s it; add your own prompt instead", ErrForbidden, seq, verb)
	}
	return pr, nil
}

// activePrompt returns the active prompt with seq.
func (p Proposal) activePrompt(seq int) (Prompt, bool) {
	for _, pr := range p.Prompts {
		if pr.Seq == seq {
			return pr, true
		}
	}
	return Prompt{}, false
}

func checkOpen(p Proposal) error {
	if p.Status != ProposalOpen {
		return fmt.Errorf("%w: proposal %s is %s, so its prompts cannot change", ErrProposalClosed, p.ID, p.Status)
	}
	return nil
}
