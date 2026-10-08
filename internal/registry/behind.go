package registry

import (
	"context"
	"errors"
	"fmt"
)

// ErrBehind means a proposal was made from an older release than the
// package's latest, so approving it as it is would drop the newer
// release's changes. The approver names that release to go ahead
// (ApproveInput.Over), or the author brings the proposal up to date.
var ErrBehind = errors.New("proposal is behind the latest release")

// standing reads where p stands against its package's latest release now.
func (s *ProposalService) standing(ctx context.Context, p Proposal) (Standing, error) {
	rels, err := s.releases.Releases(ctx, p.Package)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Standing{}, fmt.Errorf("approve %s: releases of %s: %w", p.ID, p.Package, err)
	}
	st := Standing{Base: p.Base}
	if n := len(rels); n > 0 {
		st.Latest = BaseAt(rels[n-1].Version)
	}
	return st, nil
}

// checkOver lets a proposal be approved as it stands: one whose version
// was released already never (ErrVersionConflict), one that is behind
// only over the latest release named exactly (ErrBehind), any other one
// whatever over says.
func checkOver(p Proposal, st Standing, over string) error {
	switch {
	case st.Blocks(p.Version):
		return fmt.Errorf("approve %s: %w", p.ID, refusal(ErrVersionConflict, "%s@%s is not higher than the latest release %s. %s",
			p.Package, p.Version, st.Latest, rebaseHint(p.ID)))
	case !st.Behind() || over == st.Latest.String():
		return nil
	}
	return behindError(p, st, over)
}

// behindError says why a behind proposal was not approved and both ways
// out, with the command that approves it over the latest release.
func behindError(p Proposal, st Standing, over string) error {
	made := "is based on " + p.Base.String()
	if p.Base.IsZero() {
		made = "was made before the first release"
	}
	latest := st.Latest.String()
	wrong := ""
	if over != "" {
		wrong = fmt.Sprintf("You asked to approve it over %s, but the latest release is %s. ", over, latest)
	}
	// Every sentence ends with a full stop, the commands inside them, and
	// a CLI from before --over learns it needs an update.
	return refusal(ErrBehind, "proposal %s %s and %s was released since, so approving it releases %s without %s's changes. %sThe author can bring it up to date with loadout rebase --proposal %s. To release it as it is, approve it over %s with loadout review approve %s --over %s (update loadout if it does not know --over).",
		p.ID, made, latest, p.Version, latest, wrong, p.ID, latest, p.ID, latest)
}

// refusal is sentinel with sentences a person reads, each ending with a
// full stop as user-facing text does (Go's convention for error strings
// is for the wrapping prefixes, not for these).
func refusal(sentinel error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, args...))
}

// rebaseHint tells the author of a proposal whose version was released how
// to bring it up to date: a rebase raises its version.
func rebaseHint(id string) string {
	return "The author brings it up to date with loadout rebase --proposal " + id + ", which raises its version, then proposes again."
}

// landedMeanwhile turns the store's ErrStale, a release that landed
// between reading the latest release and approving, into the refusal the
// approver would have had with the release in view.
func (s *ProposalService) landedMeanwhile(ctx context.Context, p Proposal, over string, stale error) error {
	st, err := s.standing(ctx, p)
	if err != nil {
		return errors.Join(stale, err)
	}
	if err := checkOver(p, st, over); err != nil {
		return err
	}
	// Still approvable as asked (a release and its removal cannot both
	// happen); say what happened rather than nothing.
	return fmt.Errorf("approve %s: %w", p.ID, refusal(ErrBehind, "a release came out while approving; look again and approve."))
}
