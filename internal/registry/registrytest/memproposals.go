package registrytest

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

type storedProposal struct {
	p      registry.Proposal
	bundle []byte
	texts  [][]byte // review texts, by Seq-1
	datas  [][]byte // review data, by Seq-1; nil for text only
}

var _ registry.ProposalStore = (*MemStore)(nil)

func cloneProposal(p registry.Proposal) registry.Proposal {
	p.Prompts = slices.Clone(p.Prompts)
	p.RemovedPrompts = slices.Clone(p.RemovedPrompts)
	p.Reviews = slices.Clone(p.Reviews)
	return p
}

// find returns the stored proposal with id, or nil. m.mu must be held.
func (m *MemStore) find(id string) *storedProposal {
	for i := range m.proposals {
		if m.proposals[i].p.ID == id {
			return &m.proposals[i]
		}
	}
	return nil
}

// CreateProposal implements registry.ProposalStore.
func (m *MemStore) CreateProposal(_ context.Context, p registry.Proposal, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.proposals {
		if s.p.ID == p.ID {
			return fmt.Errorf("proposal %s already exists", p.ID)
		}
		if s.p.Status == registry.ProposalOpen && s.p.Package == p.Package && s.p.Version.Compare(p.Version) == 0 {
			return fmt.Errorf("%w: %s@%s already has open proposal %s", registry.ErrVersionConflict, p.Package, p.Version, s.p.ID)
		}
	}
	p = newStored(p)
	m.proposals = append(m.proposals, storedProposal{p: p, bundle: bytes.Clone(b)})
	return nil
}

// ReplaceProposal implements registry.ProposalStore.
func (m *MemStore) ReplaceProposal(_ context.Context, old string, p registry.Proposal, b []byte, d registry.Decision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.Status != registry.ProposalWithdrawn {
		return fmt.Errorf("%w: the old one closes as withdrawn, not %q", registry.ErrInvalid, d.Status)
	}
	s, err := m.openProposal(old)
	if err != nil {
		return err
	}
	// Check the new one before closing the old one, as one transaction
	// would roll both back.
	for _, o := range m.proposals {
		if o.p.ID == p.ID {
			return fmt.Errorf("proposal %s already exists", p.ID)
		}
		if o.p.ID != old && o.p.Status == registry.ProposalOpen && o.p.Package == p.Package && o.p.Version.Compare(p.Version) == 0 {
			return fmt.Errorf("%w: %s@%s already has open proposal %s", registry.ErrVersionConflict, p.Package, p.Version, o.p.ID)
		}
	}
	closeWith(s, d)
	s.p.ReplacedBy = p.ID
	p = newStored(p)
	p.Replaces = old
	m.proposals = append(m.proposals, storedProposal{p: p, bundle: bytes.Clone(b)})
	return nil
}

// newStored is p as a store keeps a new proposal: prompts numbered from 1,
// nothing a decision or a replacement sets.
func newStored(p registry.Proposal) registry.Proposal {
	p = cloneProposal(p)
	p.Reviews, p.RemovedPrompts = nil, nil
	p.ApprovedOver, p.Replaces, p.ReplacedBy = registry.Base{}, "", ""
	for i := range p.Prompts {
		p.Prompts[i].Seq = i + 1
	}
	return p
}

// Proposal implements registry.ProposalStore.
func (m *MemStore) Proposal(_ context.Context, id string) (registry.Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.find(id)
	if s == nil {
		return registry.Proposal{}, registry.ErrNotFound
	}
	return cloneProposal(s.p), nil
}

// ProposalBundle implements registry.ProposalStore.
func (m *MemStore) ProposalBundle(_ context.Context, id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.find(id)
	if s == nil {
		return nil, registry.ErrNotFound
	}
	return bytes.Clone(s.bundle), nil
}

// Proposals implements registry.ProposalStore.
func (m *MemStore) Proposals(_ context.Context, f registry.ProposalFilter) ([]registry.Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []registry.Proposal
	for _, s := range slices.Backward(m.proposals) { // newest first
		p := s.p
		if p.Package.Team() != f.Team || (!f.Package.IsZero() && p.Package != f.Package) || (f.Status != "" && p.Status != f.Status) {
			continue
		}
		out = append(out, cloneProposal(p))
		if len(out) == f.Limit {
			break
		}
	}
	return out, nil
}

// AddReview implements registry.ProposalStore.
func (m *MemStore) AddReview(_ context.Context, id string, r registry.Review, text, data []byte, limit int) (registry.Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.find(id)
	switch {
	case s == nil:
		return registry.Review{}, registry.ErrNotFound
	case s.p.Status != registry.ProposalOpen:
		return registry.Review{}, fmt.Errorf("%w: %s is %s", registry.ErrProposalClosed, id, s.p.Status)
	case len(s.p.Reviews) >= limit:
		return registry.Review{}, fmt.Errorf("%w: proposal %s already has %d reviews", registry.ErrInvalid, id, limit)
	}
	r.Seq = len(s.p.Reviews) + 1
	s.p.Reviews = append(s.p.Reviews, r)
	s.texts = append(s.texts, bytes.Clone(text))
	s.datas = append(s.datas, bytes.Clone(data))
	return r, nil
}

// AddPrompt implements registry.ProposalStore.
func (m *MemStore) AddPrompt(_ context.Context, id string, p registry.Prompt, limit, maxRows int) (registry.Prompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.openProposal(id)
	if err != nil {
		return registry.Prompt{}, err
	}
	rows := len(s.p.Prompts) + len(s.p.RemovedPrompts)
	switch {
	case len(s.p.Prompts) >= limit:
		return registry.Prompt{}, fmt.Errorf("%w: proposal %s already has %d prompts; remove one first", registry.ErrInvalid, id, limit)
	case rows >= maxRows:
		return registry.Prompt{}, fmt.Errorf("%w: proposal %s has had %d prompts, the most it can have", registry.ErrInvalid, id, maxRows)
	}
	// Rows are never deleted, so the next number is one past the count.
	p.Seq = rows + 1
	p.EditedAt, p.RemovedAt, p.RemovedBy = time.Time{}, time.Time{}, registry.Actor{}
	s.p.Prompts = append(s.p.Prompts, p)
	return p, nil
}

// EditPrompt implements registry.ProposalStore.
func (m *MemStore) EditPrompt(_ context.Context, id string, seq int, text string, at time.Time) (registry.Prompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, i, err := m.activePrompt(id, seq)
	if err != nil {
		return registry.Prompt{}, err
	}
	s.p.Prompts[i].Text = text
	s.p.Prompts[i].EditedAt = at
	return s.p.Prompts[i], nil
}

// RemovePrompt implements registry.ProposalStore.
func (m *MemStore) RemovePrompt(_ context.Context, id string, seq int, by registry.Actor, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, i, err := m.activePrompt(id, seq)
	if err != nil {
		return err
	}
	if len(s.p.Prompts) == 1 {
		return fmt.Errorf("%w: a proposal keeps at least one prompt", registry.ErrInvalid)
	}
	p := s.p.Prompts[i]
	p.RemovedAt, p.RemovedBy = at, by
	s.p.Prompts = slices.Delete(s.p.Prompts, i, i+1)
	s.p.RemovedPrompts = append(s.p.RemovedPrompts, p)
	slices.SortFunc(s.p.RemovedPrompts, func(a, b registry.Prompt) int { return a.Seq - b.Seq })
	return nil
}

// activePrompt returns the open proposal id and the index of its active
// prompt seq. m.mu must be held.
func (m *MemStore) activePrompt(id string, seq int) (*storedProposal, int, error) {
	s, err := m.openProposal(id)
	if err != nil {
		return nil, 0, err
	}
	i := slices.IndexFunc(s.p.Prompts, func(p registry.Prompt) bool { return p.Seq == seq })
	if i < 0 {
		return nil, 0, fmt.Errorf("%w: proposal %s has no active prompt %d", registry.ErrNotFound, id, seq)
	}
	return s, i, nil
}

// ReviewText implements registry.ProposalStore.
func (m *MemStore) ReviewText(_ context.Context, id string, seq int) (registry.Review, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.find(id)
	if s == nil || seq < 1 || seq > len(s.texts) {
		return registry.Review{}, nil, registry.ErrNotFound
	}
	return s.p.Reviews[seq-1], bytes.Clone(s.texts[seq-1]), nil
}

// ReviewData implements registry.ProposalStore.
func (m *MemStore) ReviewData(_ context.Context, id string, seq int) (registry.Review, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.find(id)
	if s == nil || seq < 1 || seq > len(s.datas) || len(s.datas[seq-1]) == 0 {
		return registry.Review{}, nil, registry.ErrNotFound
	}
	return s.p.Reviews[seq-1], bytes.Clone(s.datas[seq-1]), nil
}

// DecideProposal implements registry.ProposalStore.
func (m *MemStore) DecideProposal(_ context.Context, id string, d registry.Decision) (registry.Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.openProposal(id)
	if err != nil {
		return registry.Proposal{}, err
	}
	if d.Status != registry.ProposalRejected && d.Status != registry.ProposalWithdrawn {
		return registry.Proposal{}, fmt.Errorf("%w: cannot close a proposal as %q", registry.ErrInvalid, d.Status)
	}
	closeWith(s, d)
	return cloneProposal(s.p), nil
}

// ApproveProposal implements registry.ProposalStore.
func (m *MemStore) ApproveProposal(_ context.Context, id string, d registry.Decision) (registry.Proposal, registry.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.openProposal(id)
	if err != nil {
		return registry.Proposal{}, registry.Release{}, err
	}
	if err := m.checkAboveLatest(s.p.Package, s.p.Version); err != nil {
		return registry.Proposal{}, registry.Release{}, err
	}
	if latest := m.latest(s.p.Package); !latest.Equal(d.Latest) {
		return registry.Proposal{}, registry.Release{}, fmt.Errorf("%w: the latest release of %s is %q, not %q", registry.ErrStale, s.p.Package, latest, d.Latest)
	}
	m.seq++
	rel := registry.Release{
		ID:           s.p.Package,
		Version:      s.p.Version,
		Digest:       s.p.Digest,
		Description:  s.p.Description,
		Kind:         s.p.Kind,
		Size:         s.p.Size,
		PublishedAt:  d.At,
		Seq:          m.seq,
		Proposal:     s.p.ID,
		SelfApproved: d.SelfApproved,
		PublishedBy:  d.By,
	}
	m.releases = append(m.releases, stored{rel: rel, bundle: bytes.Clone(s.bundle)})
	d.Status = registry.ProposalApproved
	closeWith(s, d)
	return cloneProposal(s.p), rel, nil
}

// openProposal returns the open proposal with id. m.mu must be held.
func (m *MemStore) openProposal(id string) (*storedProposal, error) {
	s := m.find(id)
	if s == nil {
		return nil, registry.ErrNotFound
	}
	if s.p.Status != registry.ProposalOpen {
		return nil, fmt.Errorf("%w: %s is %s", registry.ErrProposalClosed, id, s.p.Status)
	}
	return s, nil
}

// latest returns the latest release of id, none without one. m.mu must be
// held.
func (m *MemStore) latest(id pack.ID) registry.Base {
	var out registry.Base
	for _, s := range m.releases {
		if s.rel.ID == id {
			if v, ok := out.Version(); !ok || s.rel.Version.Compare(v) > 0 {
				out = registry.BaseAt(s.rel.Version)
			}
		}
	}
	return out
}

func closeWith(s *storedProposal, d registry.Decision) {
	s.p.Status = d.Status
	s.p.DecidedAt = d.At
	s.p.DecidedBy = d.By
	s.p.DecisionNote = d.Note
	s.p.SelfApproved = d.SelfApproved
	s.p.ApprovedOver = d.Over
}
