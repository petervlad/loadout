package registry_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/registry"
)

func TestPromptRules(t *testing.T) {
	var (
		author     = person("u-alice", registry.ScopeRead, limeLayers)
		laptop     = registry.Principal{TokenID: "tok-alice-2", Team: "lime", Scope: registry.ScopeRead, Layers: limeLayers, Label: "alice laptop", UserID: "u-alice"}
		reviewer   = person("u-bob", registry.ScopeRead, limeLayers)
		maintainer = person("u-maint", registry.ScopePublish, limeLayers)
		machine    = registry.Principal{TokenID: "ci", Team: "lime", Scope: registry.ScopePublish, Layers: limeLayers, Label: "ci", CreatedBy: "u-carol"}
		outsider   = person("u-zed", registry.ScopePublish, []registry.Layer{{Namespace: "zeta", Kind: registry.KindTeam}})
	)
	_, props := newOwnerFixture(nil)
	ctx := t.Context()
	p, err := props.Propose(ctx, author, bundleFor(t, "lime", "0.2.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("add", func(t *testing.T) {
		tests := []struct {
			name    string
			pr      registry.Principal
			text    string
			wantErr error
		}{
			{"author", author, "plan INFOTM-3", nil},
			{"member", reviewer, " plan INFOTM-4 \n", nil},
			{"maintainer", maintainer, "plan INFOTM-5", nil},
			{"machine token", machine, "plan INFOTM-6", nil},
			{"other team", outsider, "plan", registry.ErrNotFound},
			{"blank", reviewer, " \n", registry.ErrInvalid},
			{"too long", reviewer, strings.Repeat("x", registry.MaxPromptSize+1), registry.ErrInvalid},
			{"not UTF-8", reviewer, "\xff", registry.ErrInvalid},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got, err := props.AddPrompt(ctx, tc.pr, p.ID, tc.text)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("AddPrompt() error = %v, want %v", err, tc.wantErr)
				}
				if err == nil && (got.Text != strings.TrimSpace(tc.text) || got.Author.TokenID != tc.pr.TokenID || got.Seq < 3) {
					t.Errorf("AddPrompt() = %+v, want the trimmed text by %s", got, tc.pr.TokenID)
				}
			})
		}
		after, err := props.Proposal(ctx, author, p.ID)
		if err != nil || len(after.Prompts) != 6 {
			t.Errorf("Proposal() after adding = %d prompts, %v; want 6", len(after.Prompts), err)
		}
	})

	byBob, err := props.AddPrompt(ctx, reviewer, p.ID, "bob's question")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("edit and remove only by the one who added it", func(t *testing.T) {
		tests := []struct {
			name    string
			pr      registry.Principal
			seq     int
			wantErr error
		}{
			// In order: a success also removes the prompt.
			{"author, reviewer's", author, byBob.Seq, registry.ErrForbidden},
			{"maintainer, reviewer's", maintainer, byBob.Seq, registry.ErrForbidden},
			{"reviewer, author's", reviewer, 1, registry.ErrForbidden},
			{"machine token, author's", machine, 1, registry.ErrForbidden},
			{"reviewer, own", reviewer, byBob.Seq, nil},
			{"author, own", author, 1, nil},
			{"author's other token, own", laptop, 2, nil},
			{"other team", outsider, 1, registry.ErrNotFound},
			{"no such prompt", author, 99, registry.ErrNotFound},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got, err := props.EditPrompt(ctx, tc.pr, p.ID, tc.seq, "  reworded ")
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("EditPrompt() error = %v, want %v", err, tc.wantErr)
				}
				if err == nil && (got.Text != "reworded" || got.EditedAt.IsZero()) {
					t.Errorf("EditPrompt() = %+v, want reworded with EditedAt", got)
				}
				if err := props.RemovePrompt(ctx, tc.pr, p.ID, tc.seq); !errors.Is(err, tc.wantErr) {
					t.Fatalf("RemovePrompt() error = %v, want %v", err, tc.wantErr)
				}
			})
		}
		if _, err := props.EditPrompt(ctx, reviewer, p.ID, byBob.Seq, "again"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("EditPrompt(removed) error = %v, want ErrNotFound", err)
		}
		if _, err := props.EditPrompt(ctx, author, p.ID, 3, " "); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("EditPrompt(blank) error = %v, want ErrInvalid", err)
		}
	})

	t.Run("limit of active prompts", func(t *testing.T) {
		q, err := props.Propose(ctx, author, bundleFor(t, "lime", "0.3.0"), registry.ProposalInput{Prompts: []string{"one"}})
		if err != nil {
			t.Fatal(err)
		}
		for range registry.MaxPrompts - 1 {
			if _, err := props.AddPrompt(ctx, reviewer, q.ID, "more"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := props.AddPrompt(ctx, reviewer, q.ID, "one too many"); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("AddPrompt() over %d error = %v, want ErrInvalid", registry.MaxPrompts, err)
		}
	})

	t.Run("the last prompt stays", func(t *testing.T) {
		q, err := props.Propose(ctx, author, bundleFor(t, "lime", "0.4.0"), registry.ProposalInput{Prompts: []string{"one"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := props.RemovePrompt(ctx, author, q.ID, 1); !errors.Is(err, registry.ErrInvalid) || !strings.Contains(err.Error(), "at least one prompt") {
			t.Errorf("RemovePrompt(last) error = %v, want ErrInvalid", err)
		}
	})

	t.Run("closed proposal", func(t *testing.T) {
		if _, err := props.Withdraw(ctx, author, p.ID, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := props.AddPrompt(ctx, reviewer, p.ID, "late"); !errors.Is(err, registry.ErrProposalClosed) {
			t.Errorf("AddPrompt(closed) error = %v, want ErrProposalClosed", err)
		}
		if _, err := props.EditPrompt(ctx, author, p.ID, 3, "late"); !errors.Is(err, registry.ErrProposalClosed) {
			t.Errorf("EditPrompt(closed) error = %v, want ErrProposalClosed", err)
		}
		if err := props.RemovePrompt(ctx, author, p.ID, 3); !errors.Is(err, registry.ErrProposalClosed) {
			t.Errorf("RemovePrompt(closed) error = %v, want ErrProposalClosed", err)
		}
	})
}

// TestPromptFromATeamBelow: a member of a team may add a prompt to a
// proposal of the project above, which they can read and propose to.
func TestPromptFromATeamBelow(t *testing.T) {
	_, props := newOwnerFixture(nil)
	ctx := t.Context()
	projectAuthor := person("u-owner", registry.ScopePublish, acmeLayers)
	teamMember := person("u-member", registry.ScopeRead, limeLayers)
	p, err := props.Propose(ctx, projectAuthor, bundleFor(t, "acme", "1.0.0"), twoPrompts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := props.AddPrompt(ctx, teamMember, p.ID, "does it still plan lime tickets?")
	if err != nil || got.Seq != 3 || got.Author.UserID != "u-member" {
		t.Fatalf("AddPrompt(team below) = %+v, %v; want prompt 3 by u-member", got, err)
	}
	if _, err := props.EditPrompt(ctx, projectAuthor, p.ID, got.Seq, "x"); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("EditPrompt(project author, member's prompt) error = %v, want ErrForbidden", err)
	}
	if err := props.RemovePrompt(ctx, teamMember, p.ID, got.Seq); err != nil {
		t.Errorf("RemovePrompt(own, team below) error: %v", err)
	}
}
