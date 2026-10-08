package registry_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

func TestApproveBehind(t *testing.T) {
	own := owners{"u-owner": {"acme", "lime"}}
	var (
		member     = person("u-member", registry.ScopeRead, limeLayers)
		maintainer = person("u-maint", registry.ScopePublish, limeLayers)
		owner      = person("u-owner", registry.ScopePublish, limeLayers)
	)
	tests := []struct {
		name      string
		author    registry.Principal
		approver  registry.Principal
		released  []string // after the proposal was made from 0.1.0
		in        registry.ApproveInput
		wantErr   error
		wantInErr []string
		wantOver  string
	}{
		{"not behind, over is ignored", member, maintainer, nil, registry.ApproveInput{Over: "9.9.9"}, nil, nil, ""},
		{
			"behind, without over", member, maintainer,
			[]string{"0.2.0"},
			registry.ApproveInput{},
			registry.ErrBehind,
			[]string{"is based on 0.1.0 and 0.2.0 was released since", "loadout review approve", "--over 0.2.0 (update loadout if it does not know --over)."},
			"",
		},
		{
			"behind, over an older release", member, maintainer,
			[]string{"0.2.0"},
			registry.ApproveInput{Over: "0.1.0"},
			registry.ErrBehind,
			[]string{"You asked to approve it over 0.1.0, but the latest release is 0.2.0."},
			"",
		},
		{
			"two behind, over the one before the latest", member, maintainer,
			[]string{"0.2.0", "0.2.1"},
			registry.ApproveInput{Over: "0.2.0"},
			registry.ErrBehind,
			[]string{"--over 0.2.1"},
			"",
		},
		{"behind, over the latest", member, maintainer, []string{"0.2.0"}, registry.ApproveInput{Over: "0.2.0"}, nil, nil, "0.2.0"},
		{"owner, own, as owner and over", owner, owner, []string{"0.2.0"}, registry.ApproveInput{Note: "hotfix redone", AsOwner: true, Over: "0.2.0"}, nil, nil, "0.2.0"},
		{"owner, own, as owner without over", owner, owner, []string{"0.2.0"}, registry.ApproveInput{Note: "hotfix redone", AsOwner: true}, registry.ErrBehind, nil, ""},
		{
			"its version was released", member, maintainer,
			[]string{"0.3.0"},
			registry.ApproveInput{Over: "0.3.0"},
			registry.ErrVersionConflict,
			[]string{"The author brings it up to date with loadout rebase --proposal", "which raises its version, then proposes again."},
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, props := newOwnerFixture(own)
			if _, err := svc.Publish(t.Context(), maintainer, bundleFor(t, "lime", "0.1.0")); err != nil {
				t.Fatal(err)
			}
			p, err := props.Propose(t.Context(), tc.author, bundleFor(t, "lime", "0.3.0"), twoPrompts)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range tc.released {
				if _, err := svc.Publish(t.Context(), maintainer, bundleFor(t, "lime", v)); err != nil {
					t.Fatal(err)
				}
			}
			got, rel, err := props.Approve(t.Context(), tc.approver, p.ID, tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Approve() error = %v, want %v", err, tc.wantErr)
			}
			for _, want := range tc.wantInErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Approve() error = %q, want it to contain %q", err, want)
				}
			}
			if err != nil {
				if open, err := props.Proposal(t.Context(), tc.approver, p.ID); err != nil || open.Status != registry.ProposalOpen {
					t.Errorf("after a refused approve Proposal() = %+v, %v; want still open", open, err)
				}
				return
			}
			if got.ApprovedOver.String() != tc.wantOver || rel.Version.String() != "0.3.0" {
				t.Errorf("Approve() = approved over %q, released %s; want over %q, 0.3.0", got.ApprovedOver, rel.Version, tc.wantOver)
			}
			if stored, err := props.Proposal(t.Context(), tc.approver, p.ID); err != nil || stored.ApprovedOver.String() != tc.wantOver {
				t.Errorf("Proposal() after approve = over %q, %v; want %q", stored.ApprovedOver, err, tc.wantOver)
			}
		})
	}
}

// landing publishes a release just before each approval reaches the store,
// as a teammate publishing while the approver clicks would.
type landing struct {
	*registrytest.MemStore
	release func()
}

func (l landing) ApproveProposal(ctx context.Context, id string, d registry.Decision) (registry.Proposal, registry.Release, error) {
	l.release()
	return l.MemStore.ApproveProposal(ctx, id, d)
}

func TestApproveWhileAReleaseLands(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	maintainer := person("u-maint", registry.ScopePublish, limeLayers)
	member := person("u-member", registry.ScopeRead, limeLayers)
	publish := func(v string) {
		t.Helper()
		if _, err := svc.Publish(t.Context(), maintainer, bundleFor(t, "lime", v)); err != nil {
			t.Fatal(err)
		}
	}
	publish("0.1.0")
	props := registry.NewProposalService(store, landing{MemStore: store, release: func() { publish("0.2.0") }}, now, log)
	p, err := props.Propose(t.Context(), member, bundleFor(t, "lime", "0.3.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = props.Approve(t.Context(), maintainer, p.ID, registry.ApproveInput{})
	if !errors.Is(err, registry.ErrBehind) || !strings.Contains(err.Error(), "--over 0.2.0") {
		t.Fatalf("Approve() while 0.2.0 lands error = %v, want ErrBehind naming 0.2.0", err)
	}
}
