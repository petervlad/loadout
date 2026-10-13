package registry_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

type proposalFixture struct {
	svc   *registry.Service
	props *registry.ProposalService
}

func newProposalFixture(t *testing.T) proposalFixture {
	t.Helper()
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	return proposalFixture{
		svc:   registry.NewService(store, store, store, now, log),
		props: registry.NewProposalService(store, store, now, log),
	}
}

func (f proposalFixture) principal(t *testing.T, team string, scope registry.TokenScope, label string) registry.Principal {
	t.Helper()
	secret, _, err := f.svc.CreateToken(t.Context(), team, scope, label)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := f.svc.Authenticate(t.Context(), secret)
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

var twoPrompts = registry.ProposalInput{Note: "sharper plans", Prompts: []string{"plan TRK-1", "plan TRK-2"}}

func TestProposeRules(t *testing.T) {
	f := newProposalFixture(t)
	pub := f.principal(t, "lime", registry.ScopePublish, "alice")
	read := f.principal(t, "lime", registry.ScopeRead, "carol")
	if _, err := f.svc.Publish(t.Context(), pub, bundleFor(t, "lime", "0.1.0")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		pr      registry.Principal
		data    []byte
		in      registry.ProposalInput
		wantErr error
	}{
		{"read token may propose", read, bundleFor(t, "lime", "0.2.0"), twoPrompts, nil},
		{"same version as latest", pub, bundleFor(t, "lime", "0.1.0"), twoPrompts, registry.ErrVersionConflict},
		{"already open", pub, bundleFor(t, "lime", "0.2.0"), twoPrompts, registry.ErrVersionConflict},
		{"other team", pub, bundleFor(t, "other", "1.0.0"), twoPrompts, registry.ErrForbidden},
		{"no prompts", pub, bundleFor(t, "lime", "0.3.0"), registry.ProposalInput{}, registry.ErrInvalid},
		{"blank prompt", pub, bundleFor(t, "lime", "0.3.0"), registry.ProposalInput{Prompts: []string{" "}}, registry.ErrInvalid},
		{"too many prompts", pub, bundleFor(t, "lime", "0.3.0"), registry.ProposalInput{Prompts: make([]string, registry.MaxPrompts+1)}, registry.ErrInvalid},
		{"prompt too long", pub, bundleFor(t, "lime", "0.3.0"), registry.ProposalInput{Prompts: []string{strings.Repeat("x", registry.MaxPromptSize+1)}}, registry.ErrInvalid},
		{"note too long", pub, bundleFor(t, "lime", "0.3.0"), registry.ProposalInput{Note: strings.Repeat("x", registry.MaxNoteSize+1), Prompts: []string{"p"}}, registry.ErrInvalid},
		{"garbage", pub, []byte("nope"), twoPrompts, registry.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := f.props.Propose(t.Context(), tc.pr, tc.data, tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Propose() error = %v, want %v", err, tc.wantErr)
			}
			if err == nil && (p.Status != registry.ProposalOpen || p.Author.TokenID != tc.pr.TokenID || p.Author.Label != tc.pr.Label || len(p.Prompts) != 2) {
				t.Errorf("Propose() = %+v, want open by %s with 2 prompts", p, tc.pr.TokenID)
			}
		})
	}

	// A proposal is not a release.
	id, _ := pack.NewID("lime", "planner")
	if rels, err := f.svc.Releases(t.Context(), read, id); err != nil || len(rels) != 1 {
		t.Errorf("Releases() after proposing = %d, %v; want only 0.1.0", len(rels), err)
	}
}

func TestApproveFourEyes(t *testing.T) {
	f := newProposalFixture(t)
	author := f.principal(t, "lime", registry.ScopePublish, "alice")
	p, err := f.props.Propose(t.Context(), author, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		pr      registry.Principal
		wantErr error
	}{
		{"author", author, registry.ErrForbidden},
		{"same label, other token", f.principal(t, "lime", registry.ScopePublish, " Alice "), registry.ErrForbidden},
		{"read token", f.principal(t, "lime", registry.ScopeRead, "carol"), registry.ErrForbidden},
		{"other team", f.principal(t, "other", registry.ScopePublish, "dave"), registry.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := f.props.Approve(t.Context(), tc.pr, p.ID, registry.ApproveInput{}); !errors.Is(err, tc.wantErr) {
				t.Errorf("Approve() error = %v, want %v", err, tc.wantErr)
			}
			if _, err := f.props.Reject(t.Context(), tc.pr, p.ID, ""); !errors.Is(err, tc.wantErr) {
				t.Errorf("Reject() error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	bob := f.principal(t, "lime", registry.ScopePublish, "bob")
	got, rel, err := f.props.Approve(t.Context(), bob, p.ID, registry.ApproveInput{Note: "looks good"})
	if err != nil {
		t.Fatalf("Approve() by a teammate error: %v", err)
	}
	if got.Status != registry.ProposalApproved || got.DecidedBy.TokenID != bob.TokenID || got.DecisionNote != "looks good" {
		t.Errorf("Approve() proposal = %+v, want approved by bob", got)
	}
	if rel.PublishedBy.TokenID != bob.TokenID || rel.PublishedBy.Label != "bob" {
		t.Errorf("Approve() release PublishedBy = %+v, want bob, who approved it", rel.PublishedBy)
	}
	fetched, _, err := f.svc.Fetch(t.Context(), bob, rel.ID, "latest")
	if err != nil || fetched.Version.String() != "0.2.0" || fetched.Digest != p.Digest {
		t.Errorf("Fetch(latest) after approve = %+v, %v; want the candidate 0.2.0", fetched, err)
	}
	if _, _, err := f.props.Approve(t.Context(), bob, p.ID, registry.ApproveInput{}); !errors.Is(err, registry.ErrProposalClosed) {
		t.Errorf("second Approve() error = %v, want ErrProposalClosed", err)
	}
}

func TestEmptyLabelsDoNotMatch(t *testing.T) {
	f := newProposalFixture(t)
	author := f.principal(t, "lime", registry.ScopePublish, "")
	p, err := f.props.Propose(t.Context(), author, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.props.Approve(t.Context(), f.principal(t, "lime", registry.ScopePublish, ""), p.ID, registry.ApproveInput{}); err != nil {
		t.Errorf("Approve() with two unlabelled tokens error = %v, want nil", err)
	}
}

func TestApproveAfterAPublishConflicts(t *testing.T) {
	f := newProposalFixture(t)
	author := f.principal(t, "lime", registry.ScopeRead, "alice")
	bob := f.principal(t, "lime", registry.ScopePublish, "bob")
	p, err := f.props.Propose(t.Context(), author, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Publish(t.Context(), bob, bundleFor(t, "lime", "0.2.0")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.props.Approve(t.Context(), bob, p.ID, registry.ApproveInput{}); !errors.Is(err, registry.ErrVersionConflict) {
		t.Fatalf("Approve() after a publish error = %v, want ErrVersionConflict", err)
	}
	if got, err := f.props.Proposal(t.Context(), author, p.ID); err != nil || got.Status != registry.ProposalOpen {
		t.Errorf("Proposal() after the conflict = %+v, %v; want still open", got, err)
	}
}

func TestWithdrawAndReject(t *testing.T) {
	f := newProposalFixture(t)
	author := f.principal(t, "lime", registry.ScopeRead, "alice")
	bob := f.principal(t, "lime", registry.ScopePublish, "bob")

	p, err := f.props.Propose(t.Context(), author, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.props.Withdraw(t.Context(), bob, p.ID, ""); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Withdraw() by a non-author error = %v, want ErrForbidden", err)
	}
	got, err := f.props.Withdraw(t.Context(), author, p.ID, "rethinking")
	if err != nil || got.Status != registry.ProposalWithdrawn || got.DecisionNote != "rethinking" {
		t.Fatalf("Withdraw() = %+v, %v; want withdrawn", got, err)
	}
	if _, err := f.props.Reject(t.Context(), bob, p.ID, ""); !errors.Is(err, registry.ErrProposalClosed) {
		t.Errorf("Reject() after withdraw error = %v, want ErrProposalClosed", err)
	}

	p, err = f.props.Propose(t.Context(), author, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatalf("Propose() again after withdrawing error: %v", err)
	}
	got, err = f.props.Reject(t.Context(), bob, p.ID, "too vague")
	if err != nil || got.Status != registry.ProposalRejected || got.DecidedBy.Label != "bob" {
		t.Errorf("Reject() = %+v, %v; want rejected by bob", got, err)
	}

	open, err := f.props.Proposals(t.Context(), bob, pack.ID{}, registry.ProposalOpen)
	if err != nil || len(open) != 0 {
		t.Errorf("Proposals(open) = %v, %v; want none", open, err)
	}
	all, err := f.props.Proposals(t.Context(), bob, pack.ID{}, "")
	if err != nil || len(all) != 2 || all[0].ID != p.ID {
		t.Errorf("Proposals(any) = %v, %v; want 2, newest first", all, err)
	}
	if _, err := f.props.Proposals(t.Context(), bob, pack.ID{}, "maybe"); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Proposals(bad status) error = %v, want ErrInvalid", err)
	}
}

func TestReviews(t *testing.T) {
	f := newProposalFixture(t)
	author := f.principal(t, "lime", registry.ScopeRead, "alice")
	reader := f.principal(t, "lime", registry.ScopeRead, "carol")
	outsider := f.principal(t, "other", registry.ScopeRead, "eve")
	p, err := f.props.Propose(t.Context(), author, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		pr      registry.Principal
		text    string
		data    []byte
		wantErr error
	}{
		{"teammate", reader, "# report", nil, nil},
		{"with data", reader, "# report 2", []byte(`{"prompts":[]}`), nil},
		{"empty", reader, "  \n", nil, registry.ErrInvalid},
		{"too large", reader, strings.Repeat("x", registry.MaxReviewSize+1), nil, registry.ErrInvalid},
		{"not utf-8", reader, "\xff", nil, registry.ErrInvalid},
		{"data not json", reader, "# report", []byte("{nope"), registry.ErrInvalid},
		{"data too large", reader, "# report", []byte(`"` + strings.Repeat("x", registry.MaxReviewSize) + `"`), registry.ErrInvalid},
		{"other team", outsider, "hi", nil, registry.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.props.AddReview(t.Context(), tc.pr, p.ID, []byte(tc.text), tc.data); !errors.Is(err, tc.wantErr) {
				t.Errorf("AddReview() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
	r, text, err := f.props.ReviewText(t.Context(), author, p.ID, 1)
	if err != nil || string(text) != "# report" || r.Author.Label != "carol" {
		t.Errorf("ReviewText(1) = %+v, %q, %v", r, text, err)
	}
	if _, _, err := f.props.ReviewText(t.Context(), outsider, p.ID, 1); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("ReviewText() by another team error = %v, want ErrNotFound", err)
	}
	if r, data, err := f.props.ReviewData(t.Context(), author, p.ID, 2); err != nil || string(data) != `{"prompts":[]}` || r.DataSize != len(data) {
		t.Errorf("ReviewData(2) = %+v, %q, %v", r, data, err)
	}
	if _, _, err := f.props.ReviewData(t.Context(), author, p.ID, 1); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("ReviewData() of a text-only review error = %v, want ErrNotFound", err)
	}
	if _, _, err := f.props.ReviewData(t.Context(), outsider, p.ID, 2); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("ReviewData() by another team error = %v, want ErrNotFound", err)
	}
}

func TestProposeToALayerAbove(t *testing.T) {
	f := newProposalFixture(t)
	if _, err := f.svc.CreateNamespace(t.Context(), "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateNamespace(t.Context(), "lime", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	owner := f.principal(t, "acme", registry.ScopePublish, "olga")
	member := f.principal(t, "lime", registry.ScopePublish, "alice")
	if _, err := f.svc.Publish(t.Context(), owner, bundleFor(t, "acme", "1.0.0")); err != nil {
		t.Fatal(err)
	}

	p, err := f.props.Propose(t.Context(), member, bundleFor(t, "acme", "1.1.0"), twoPrompts)
	if err != nil {
		t.Fatalf("a team member proposing to its parent layer: %v", err)
	}
	list, err := f.props.Proposals(t.Context(), member, p.Package, "")
	if err != nil || len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("Proposals(acme/planner) for the author = %+v, %v; want the proposal", list, err)
	}
	if _, _, err := f.props.Approve(t.Context(), member, p.ID, registry.ApproveInput{}); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("the author's team approving a parent layer's package: error %v, want ErrForbidden", err)
	}
	if _, rel, err := f.props.Approve(t.Context(), owner, p.ID, registry.ApproveInput{Note: "ok"}); err != nil || rel.Version.String() != "1.1.0" {
		t.Errorf("the layer's publisher approving: release %+v, %v; want 1.1.0", rel, err)
	}

	// The layer owner cannot see or propose into the team below it.
	if _, err := f.props.Propose(t.Context(), owner, bundleFor(t, "lime", "1.0.0"), twoPrompts); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("a parent layer proposing into a child: error %v, want ErrForbidden", err)
	}
}

func TestProposeRecordsTheBase(t *testing.T) {
	f := newProposalFixture(t)
	pub := f.principal(t, "lime", registry.ScopePublish, "alice")
	for _, v := range []string{"0.1.0", "0.2.0"} {
		if _, err := f.svc.Publish(t.Context(), pub, bundleFor(t, "lime", v)); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name    string
		data    []byte
		base    string
		want    string
		wantErr error
	}{
		{"defaults to the latest release", bundleFor(t, "lime", "0.3.0"), "", "0.2.0", nil},
		{"an older release is accepted", bundleFor(t, "lime", "0.4.0"), "0.1.0", "0.1.0", nil},
		{"the latest release", bundleFor(t, "lime", "0.5.0"), "0.2.0", "0.2.0", nil},
		{"not a release", bundleFor(t, "lime", "0.6.0"), "0.1.5", "", registry.ErrInvalid},
		{"not a version", bundleFor(t, "lime", "0.6.0"), "latest", "", registry.ErrInvalid},
		{"no release yet is none", bundleNamed(t, "lime", "reviewer", "0.1.0"), "", "", nil},
		{"no release yet, a base given", bundleNamed(t, "lime", "reviewer", "0.2.0"), "0.1.0", "", registry.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := twoPrompts
			in.Base = tc.base
			p, err := f.props.Propose(t.Context(), pub, tc.data, in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Propose(base %q) error = %v, want %v", tc.base, err, tc.wantErr)
			}
			if err != nil {
				return
			}
			got, err := f.props.Proposal(t.Context(), pub, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if p.Base.String() != tc.want || got.Base.String() != tc.want {
				t.Errorf("Propose(base %q) base = %q, stored %q; want %q", tc.base, p.Base, got.Base, tc.want)
			}
		})
	}
}

func TestStandings(t *testing.T) {
	f := newProposalFixture(t)
	pub := f.principal(t, "lime", registry.ScopePublish, "alice")
	read := f.principal(t, "lime", registry.ScopeRead, "carol")
	publish := func(name, version string) {
		t.Helper()
		if _, err := f.svc.Publish(t.Context(), pub, bundleNamed(t, "lime", name, version)); err != nil {
			t.Fatal(err)
		}
	}
	propose := func(name, version string) string {
		t.Helper()
		p, err := f.props.Propose(t.Context(), read, bundleNamed(t, "lime", name, version), twoPrompts)
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	publish("planner", "0.1.0")
	publish("writer", "1.0.0")
	blocked := propose("planner", "0.2.0")
	behind := propose("planner", "0.4.0")
	upToDate := propose("writer", "1.1.0")
	first := propose("reviewer", "0.1.0")
	publish("planner", "0.2.0") // after both planner proposals were made from 0.1.0

	props, err := f.props.Proposals(t.Context(), read, pack.ID{}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.props.Standings(t.Context(), read, props)
	if err != nil {
		t.Fatalf("Standings() error: %v", err)
	}
	if len(got) != len(props) {
		t.Fatalf("Standings() = %d standings for %d proposals", len(got), len(props))
	}
	type want struct {
		base, latest   string
		behind, blocks bool
	}
	wants := map[string]want{
		blocked:  {"0.1.0", "0.2.0", true, true},
		behind:   {"0.1.0", "0.2.0", true, false},
		upToDate: {"1.0.0", "1.0.0", false, false},
		first:    {"", "", false, false},
	}
	for i, p := range props {
		s, w := got[i], wants[p.ID]
		if s.Base.String() != w.base || s.Latest.String() != w.latest || s.Behind() != w.behind || s.Blocks(p.Version) != w.blocks {
			t.Errorf("Standings() for %s@%s = base %q, latest %q, behind %v, blocks %v; want %+v",
				p.Package, p.Version, s.Base, s.Latest, s.Behind(), s.Blocks(p.Version), w)
		}
	}

	if got, err := f.props.Standings(t.Context(), read, nil); err != nil || len(got) != 0 {
		t.Errorf("Standings(none) = %+v, %v; want none", got, err)
	}
	other := f.principal(t, "other", registry.ScopePublish, "dave")
	if _, err := f.props.Standings(t.Context(), other, props); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Standings() by another team error = %v, want ErrForbidden", err)
	}
}
