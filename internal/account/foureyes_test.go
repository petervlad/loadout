package account_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

func bundleOf(t *testing.T, team, version string) []byte {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + team + "/planner\nkind: agent\nversion: " + version + "\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: d\n---\nprompt\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := bundle.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// authAs authenticates a token secret or fails the test.
func (f *fixture) authAs(t *testing.T, secret string) registry.Principal {
	t.Helper()
	pr, err := f.reg.Authenticate(t.Context(), secret)
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

// loginToken issues a `loadout login` token of userID in namespace.
func (f *fixture) loginToken(t *testing.T, namespace, userID string) registry.Principal {
	t.Helper()
	secret, _, err := f.reg.CreateUserToken(t.Context(), namespace, registry.ScopePublish, userID+" via loadout login", userID)
	if err != nil {
		t.Fatal(err)
	}
	return f.authAs(t, secret)
}

// TestMachineTokenCannotBypassFourEyes: a maintainer can make a publish
// machine token, but it can neither approve their own proposal nor
// propose something they then approve themselves.
func TestMachineTokenCannotBypassFourEyes(t *testing.T) {
	f := newFixture(t)
	ctx, op := t.Context(), account.Operator()
	if _, err := f.reg.SetRequireReview(ctx, "lime", true); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, name := range []string{"alice", "bob"} {
		m, _, err := f.svc.AddMember(ctx, op, "lime", name+"@example.com", account.RoleMaintainer)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = m.UserID
	}
	alice, bob := f.loginToken(t, "lime", ids["alice"]), f.loginToken(t, "lime", ids["bob"])
	secret, _, err := f.svc.CreateMachineToken(ctx, account.UserActor(ids["alice"]), "lime", registry.ScopePublish, "ci")
	if err != nil {
		t.Fatal(err)
	}
	machine := f.authAs(t, secret)
	props := registry.NewProposalService(f.regMem, f.regMem, f.clock.now, slog.New(slog.DiscardHandler))
	in := registry.ProposalInput{Prompts: []string{"plan it"}}

	own, err := props.Propose(ctx, alice, bundleOf(t, "lime", "0.1.0"), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := props.Approve(ctx, machine, own.ID, registry.ApproveInput{}); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Approve(alice's proposal, alice's machine token) error = %v, want ErrForbidden", err)
	}
	if _, err := props.Reject(ctx, machine, own.ID, ""); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Reject(by a machine token) error = %v, want ErrForbidden", err)
	}

	byCI, err := props.Propose(ctx, machine, bundleOf(t, "lime", "0.2.0"), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := props.Approve(ctx, alice, byCI.ID, registry.ApproveInput{}); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Approve(proposal of alice's machine token, alice) error = %v, want ErrForbidden", err)
	}
	if _, _, err := props.Approve(ctx, bob, byCI.ID, registry.ApproveInput{}); err != nil {
		t.Errorf("Approve(proposal of alice's machine token, bob) error: %v", err)
	}
}
