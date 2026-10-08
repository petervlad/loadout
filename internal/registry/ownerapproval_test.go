package registry_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

// owners is a fake registry.Owners: the namespaces each user owns, with
// inheritance already worked out. A user named "broken" fails the lookup.
type owners map[string][]string

var errOwnersDown = errors.New("owners are down")

func (o owners) Owns(_ context.Context, userID, namespace string) (bool, error) {
	if userID == "broken" {
		return false, errOwnersDown
	}
	return slices.Contains(o[userID], namespace), nil
}

// lime is a team under the project acme.
var (
	limeLayers = []registry.Layer{{Namespace: "lime", Kind: registry.KindTeam}, {Namespace: "acme", Kind: registry.KindProject, Depth: 1}}
	acmeLayers = []registry.Layer{{Namespace: "acme", Kind: registry.KindProject}}
)

// person is a login principal of user in the namespace of layers.
func person(user string, scope registry.TokenScope, layers []registry.Layer) registry.Principal {
	return registry.Principal{TokenID: "tok-" + user, Team: layers[0].Namespace, Scope: scope, Layers: layers, Label: user + "@example.com", UserID: user}
}

func newOwnerFixture(o registry.Owners) (*registry.Service, *registry.ProposalService) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	opts := []registry.ProposalOption{}
	if o != nil {
		opts = append(opts, registry.WithOwners(o))
	}
	return registry.NewService(store, store, store, now, log, registry.WithOwnerHints(o)),
		registry.NewProposalService(store, store, now, log, opts...)
}

func TestApproveOwnAsOwner(t *testing.T) {
	own := owners{
		"u-owner":      {"acme", "lime"},
		"u-admin-own":  {"acme", "lime"},
		"u-zeta-owner": {"zeta"},
	}
	var (
		member      = person("u-member", registry.ScopeRead, limeLayers)
		maintainer  = person("u-maint", registry.ScopePublish, limeLayers)
		owner       = person("u-owner", registry.ScopePublish, limeLayers)
		ownerAcme   = person("u-owner", registry.ScopePublish, acmeLayers)
		otherOwner  = person("u-zeta-owner", registry.ScopePublish, []registry.Layer{{Namespace: "zeta", Kind: registry.KindProject}})
		admin       = person("u-admin", registry.ScopePublish, limeLayers)
		adminOwner  = person("u-admin-own", registry.ScopePublish, limeLayers)
		machine     = registry.Principal{TokenID: "ci", Team: "lime", Scope: registry.ScopePublish, Layers: limeLayers, Label: "ci", CreatedBy: "u-owner"}
		hostToken   = registry.Principal{TokenID: "host", Team: "lime", Scope: registry.ScopePublish, Layers: limeLayers, Label: "olga"}
		withWhy     = registry.ApproveInput{Note: "hotfix, nobody else is around", AsOwner: true}
		asOwnerOnly = registry.ApproveInput{AsOwner: true}
		plain       = registry.ApproveInput{Note: "looks good"}
	)
	tests := []struct {
		name     string
		author   registry.Principal
		approver registry.Principal
		team     string
		in       registry.ApproveInput
		wantErr  error
		// wantHint is part of the refusal an owner gets without AsOwner.
		wantHint string
		wantSelf bool
	}{
		{"member, own", member, member, "lime", withWhy, registry.ErrForbidden, "", false},
		{"maintainer, own", maintainer, maintainer, "lime", withWhy, registry.ErrForbidden, "", false},
		{"maintainer, someone else's, as owner is ignored", member, maintainer, "lime", withWhy, nil, "", false},
		{"project owner, own team proposal", owner, owner, "lime", withWhy, nil, "", true},
		{"project owner, own project proposal", ownerAcme, ownerAcme, "acme", withWhy, nil, "", true},
		{"project owner, own, without as owner", owner, owner, "lime", plain, registry.ErrForbidden, "--as-owner", false},
		{"project owner, own, without a note", owner, owner, "lime", asOwnerOnly, registry.ErrInvalid, "", false},
		{"project owner, own, blank note", owner, owner, "lime", registry.ApproveInput{Note: "  ", AsOwner: true}, registry.ErrInvalid, "", false},
		{"project owner, someone else's", maintainer, owner, "lime", plain, nil, "", false},
		{"owner of another project", maintainer, otherOwner, "lime", withWhy, registry.ErrNotFound, "", false},
		{"admin without membership, own", admin, admin, "lime", withWhy, registry.ErrForbidden, "", false},
		{"admin who owns, own", adminOwner, adminOwner, "lime", withWhy, nil, "", true},
		{"machine token made by an owner", owner, machine, "lime", withWhy, registry.ErrForbidden, "", false},
		{"host token, own", hostToken, hostToken, "lime", withWhy, registry.ErrForbidden, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, props := newOwnerFixture(own)
			p, err := props.Propose(t.Context(), tc.author, bundleFor(t, tc.team, "0.2.0"), twoPrompts)
			if err != nil {
				t.Fatal(err)
			}
			got, rel, err := props.Approve(t.Context(), tc.approver, p.ID, tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Approve() error = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				if tc.wantHint != "" && !strings.Contains(err.Error(), tc.wantHint) {
					t.Errorf("Approve() error = %q, want a hint with %q", err, tc.wantHint)
				}
				if tc.wantHint == "" && strings.Contains(err.Error(), "--as-owner") {
					t.Errorf("Approve() error = %q offers --as-owner to someone who may not use it", err)
				}
				return
			}
			if got.SelfApproved != tc.wantSelf || rel.SelfApproved != tc.wantSelf || rel.Proposal != p.ID {
				t.Errorf("Approve() = proposal self %v, release %+v; want self %v from %s", got.SelfApproved, rel, tc.wantSelf, p.ID)
			}
			if tc.wantSelf && got.DecisionNote != strings.TrimSpace(tc.in.Note) {
				t.Errorf("Approve() note = %q, want %q", got.DecisionNote, tc.in.Note)
			}
		})
	}
}

func TestApproveOwnWithoutOwners(t *testing.T) {
	_, props := newOwnerFixture(nil)
	owner := person("u-owner", registry.ScopePublish, limeLayers)
	p, err := props.Propose(t.Context(), owner, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = props.Approve(t.Context(), owner, p.ID, registry.ApproveInput{Note: "why", AsOwner: true})
	if !errors.Is(err, registry.ErrForbidden) || strings.Contains(err.Error(), "--as-owner") {
		t.Errorf("Approve() on a registry without accounts error = %v, want the plain four-eyes refusal", err)
	}
}

func TestApproveOwnOwnersFail(t *testing.T) {
	_, props := newOwnerFixture(owners{})
	broken := person("broken", registry.ScopePublish, limeLayers)
	p, err := props.Propose(t.Context(), broken, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := props.Approve(t.Context(), broken, p.ID, registry.ApproveInput{Note: "why", AsOwner: true}); !errors.Is(err, errOwnersDown) {
		t.Errorf("Approve() when ownership cannot be read error = %v, want it wrapped", err)
	}
	// A teammate's approval never asks.
	if _, _, err := props.Approve(t.Context(), person("u-maint", registry.ScopePublish, limeLayers), p.ID, registry.ApproveInput{}); err != nil {
		t.Errorf("Approve() by a teammate error = %v, want nil", err)
	}
}

func TestApproveOwnVersionConflictWins(t *testing.T) {
	svc, props := newOwnerFixture(owners{"u-owner": {"acme", "lime"}})
	owner := person("u-owner", registry.ScopePublish, limeLayers)
	p, err := props.Propose(t.Context(), owner, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(t.Context(), owner, bundleFor(t, "lime", "0.2.0")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := props.Approve(t.Context(), owner, p.ID, registry.ApproveInput{Note: "why", AsOwner: true}); !errors.Is(err, registry.ErrVersionConflict) {
		t.Errorf("Approve() as owner after a publish error = %v, want ErrVersionConflict", err)
	}
}

func TestPublishUnderReviewHintsOwners(t *testing.T) {
	svc, _ := newOwnerFixture(owners{"u-owner": {"acme", "lime"}})
	if _, err := svc.CreateNamespace(t.Context(), "lime", "", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetRequireReview(t.Context(), "lime", true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		pr   registry.Principal
		hint bool
	}{
		{"owner", person("u-owner", registry.ScopePublish, limeLayers), true},
		{"maintainer", person("u-maint", registry.ScopePublish, limeLayers), false},
		{"machine token of the owner", registry.Principal{TokenID: "ci", Team: "lime", Scope: registry.ScopePublish, Layers: limeLayers, CreatedBy: "u-owner"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Publish(t.Context(), tc.pr, bundleFor(t, "lime", "0.1.0"))
			if !errors.Is(err, registry.ErrForbidden) {
				t.Fatalf("Publish() under review error = %v, want ErrForbidden", err)
			}
			if got := strings.Contains(err.Error(), "--as-owner"); got != tc.hint {
				t.Errorf("Publish() error = %q; hint to approve as owner: %v, want %v", err, got, tc.hint)
			}
		})
	}
}
