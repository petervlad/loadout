package registry_test

import (
	"testing"

	"github.com/petervlad/loadout/internal/registry"
)

// Proposal.By decides the "yours" a client reads, so it decides whose
// proposal a propose replaces: the author's own token, or any token of the
// same person, and nobody else's. Two tokens without a user (made on the
// registry host) are two different authors.
func TestProposalBy(t *testing.T) {
	byLogin := registry.Proposal{Author: registry.Actor{TokenID: "login-a", Label: "alice@example.com via loadout login", UserID: "u-alice"}}
	byHostToken := registry.Proposal{Author: registry.Actor{TokenID: "host-1", Label: "ci"}}
	tests := []struct {
		name string
		p    registry.Proposal
		pr   registry.Principal
		want bool
	}{
		{"the same login token", byLogin, registry.Principal{TokenID: "login-a", UserID: "u-alice"}, true},
		{"another login token of the same user", byLogin, registry.Principal{TokenID: "login-b", UserID: "u-alice"}, true},
		{"a machine token the user made", byLogin, registry.Principal{TokenID: "ci-a", CreatedBy: "u-alice"}, true},
		{"another user's login token", byLogin, registry.Principal{TokenID: "login-c", UserID: "u-carol"}, false},
		{"a machine token another user made", byLogin, registry.Principal{TokenID: "ci-c", CreatedBy: "u-carol"}, false},
		{"a host token with no user", byLogin, registry.Principal{TokenID: "host-2"}, false},
		{"the same host token", byHostToken, registry.Principal{TokenID: "host-1"}, true},
		{"another host token, even with the same label", byHostToken, registry.Principal{TokenID: "host-2", Label: "ci"}, false},
		{"a login token against a host token's proposal", byHostToken, registry.Principal{TokenID: "login-a", UserID: "u-alice"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.By(tc.pr); got != tc.want {
				t.Errorf("By() = %v, want %v", got, tc.want)
			}
		})
	}
}
