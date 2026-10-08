package registry_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

// members is a settable registry.Memberships.
type members map[string]registry.TokenScope // "user/namespace" -> role

func (m members) MemberScope(_ context.Context, userID, namespace string) (registry.TokenScope, bool, error) {
	role, ok := m[userID+"/"+namespace]
	return role, ok, nil
}

func TestUserTokenFollowsMembership(t *testing.T) {
	store := registrytest.NewMemStore()
	m := members{"u1/lime": registry.ScopePublish}
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	s := registry.NewService(store, store, store, now, slog.New(slog.DiscardHandler), registry.WithMemberships(m))

	secret, tok, err := s.CreateUserToken(t.Context(), "lime", registry.ScopePublish, "alice@example.com via loadout login", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.UserID != "u1" {
		t.Errorf("CreateUserToken() token = %+v, want UserID u1", tok)
	}
	pr, err := s.Authenticate(t.Context(), secret)
	if err != nil || pr.Scope != registry.ScopePublish || pr.UserID != "u1" {
		t.Fatalf("Authenticate() = %+v, %v; want publish for u1", pr, err)
	}

	// Downgraded to read: the token reads only.
	m["u1/lime"] = registry.ScopeRead
	if pr, err := s.Authenticate(t.Context(), secret); err != nil || pr.Scope != registry.ScopeRead {
		t.Errorf("Authenticate() after downgrade = %+v, %v; want read", pr, err)
	}

	// Removed: the token is refused, even before it is revoked.
	delete(m, "u1/lime")
	if _, err := s.Authenticate(t.Context(), secret); !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("Authenticate() after removal error = %v, want ErrUnauthorized", err)
	}

	// Plain tokens do not ask about memberships.
	plain, _, err := s.CreateToken(t.Context(), "lime", registry.ScopeRead, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(t.Context(), plain); err != nil {
		t.Errorf("Authenticate(plain token) error: %v", err)
	}

	if n, err := s.RevokeUserTokens(t.Context(), "u1", "lime"); err != nil || n != 1 {
		t.Errorf("RevokeUserTokens() = %d, %v; want 1", n, err)
	}
	if toks, err := s.Tokens(t.Context(), "lime"); err != nil || len(toks) != 1 || toks[0].Label != "ci" {
		t.Errorf("Tokens() after revoke = %+v, %v; want only the ci token", toks, err)
	}
}

func TestUserTokenRefusedWithoutMemberships(t *testing.T) {
	s := newService(t) // no WithMemberships
	secret, _, err := s.CreateUserToken(t.Context(), "lime", registry.ScopeRead, "x", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(t.Context(), secret); !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("Authenticate(user token, no memberships) error = %v, want ErrUnauthorized", err)
	}
}

func TestRequireReviewRefusesDirectPublish(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	s := registry.NewService(store, store, store, now, log)
	props := registry.NewProposalService(store, store, now, log)
	pr := principal(t, s, "lime", registry.ScopePublish)
	if _, err := s.Publish(t.Context(), pr, bundleFor(t, "lime", "0.1.0")); err != nil {
		t.Fatalf("Publish() before require review error: %v", err)
	}
	ns, err := s.SetRequireReview(t.Context(), "lime", true)
	if err != nil || !ns.RequireReview {
		t.Fatalf("SetRequireReview() = %+v, %v", ns, err)
	}
	if _, err := s.Publish(t.Context(), pr, bundleFor(t, "lime", "0.2.0")); !errors.Is(err, registry.ErrForbidden) || !strings.Contains(err.Error(), "requires review") {
		t.Errorf("Publish() with review required error = %v, want ErrForbidden", err)
	}
	// Proposing and approving still work.
	secret, _, err := s.CreateToken(t.Context(), "lime", registry.ScopeRead, "author")
	if err != nil {
		t.Fatal(err)
	}
	author, err := s.Authenticate(t.Context(), secret)
	if err != nil {
		t.Fatal(err)
	}
	p, err := props.Propose(t.Context(), author, bundleFor(t, "lime", "0.2.0"), registry.ProposalInput{Prompts: []string{"plan"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, rel, err := props.Approve(t.Context(), pr, p.ID, registry.ApproveInput{}); err != nil || rel.Version.String() != "0.2.0" {
		t.Errorf("Approve() with review required = %+v, %v; want the release", rel, err)
	}
}

func TestFourEyesComparesUsers(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	m := members{"alice/lime": registry.ScopePublish, "bob/lime": registry.ScopePublish}
	s := registry.NewService(store, store, store, now, log, registry.WithMemberships(m))
	props := registry.NewProposalService(store, store, now, log)
	login := func(user string) registry.Principal {
		t.Helper()
		secret, _, err := s.CreateUserToken(t.Context(), "lime", registry.ScopePublish, user+" via loadout login", user)
		if err != nil {
			t.Fatal(err)
		}
		pr, err := s.Authenticate(t.Context(), secret)
		if err != nil {
			t.Fatal(err)
		}
		return pr
	}
	laptop, desktop, bob := login("alice"), login("alice"), login("bob")
	p, err := props.Propose(t.Context(), laptop, bundleFor(t, "lime", "0.1.0"), registry.ProposalInput{Prompts: []string{"plan"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Author.UserID != "alice" {
		t.Errorf("proposal author = %+v, want user alice", p.Author)
	}
	// Alice's second token is still Alice.
	if _, _, err := props.Approve(t.Context(), desktop, p.ID, registry.ApproveInput{}); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Approve() by the author's other token error = %v, want ErrForbidden", err)
	}
	if _, err := props.Reject(t.Context(), desktop, p.ID, ""); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Reject() by the author's other token error = %v, want ErrForbidden", err)
	}
	if _, rel, err := props.Approve(t.Context(), bob, p.ID, registry.ApproveInput{}); err != nil || rel.Version.String() != "0.1.0" {
		t.Errorf("Approve() by bob = %+v, %v", rel, err)
	}
	// The author withdraws with any of their tokens.
	p2, err := props.Propose(t.Context(), laptop, bundleFor(t, "lime", "0.2.0"), registry.ProposalInput{Prompts: []string{"plan"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := props.Withdraw(t.Context(), desktop, p2.ID, ""); err != nil {
		t.Errorf("Withdraw() by the author's other token error: %v", err)
	}
}

func TestDeleteNamespace(t *testing.T) {
	s := newService(t)
	if _, err := s.CreateNamespace(t.Context(), "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNamespace(t.Context(), "lime", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNamespace(t.Context(), "acme"); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("DeleteNamespace(with a child) error = %v, want ErrInvalid", err)
	}
	pr := principal(t, s, "lime", registry.ScopePublish)
	if _, err := s.Publish(t.Context(), pr, bundleFor(t, "lime", "0.1.0")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNamespace(t.Context(), "lime"); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("DeleteNamespace(with releases) error = %v, want ErrInvalid", err)
	}
	if err := s.DeleteNamespace(t.Context(), "ghost"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("DeleteNamespace(ghost) error = %v, want ErrNotFound", err)
	}
}

func TestLoginTokenExpires(t *testing.T) {
	store := registrytest.NewMemStore()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := registry.NewService(store, store, store, clock, slog.New(slog.DiscardHandler), registry.WithMemberships(members{"u1/lime": registry.ScopePublish}))
	login, _, err := s.CreateUserToken(t.Context(), "lime", registry.ScopePublish, "alice via loadout login", "u1")
	if err != nil {
		t.Fatal(err)
	}
	machine, _, err := s.CreateToken(t.Context(), "lime", registry.ScopeRead, "ci")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(90*24*time.Hour - time.Second)
	if _, err := s.Authenticate(t.Context(), login); err != nil {
		t.Errorf("Authenticate(login token, just before expiry) error: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := s.Authenticate(t.Context(), login); !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("Authenticate(login token after 90 days) error = %v, want ErrUnauthorized", err)
	}
	if _, err := s.Authenticate(t.Context(), machine); err != nil {
		t.Errorf("Authenticate(host token after 90 days) error: %v, want it to keep working", err)
	}
}

// One account token acts in every namespace its user belongs to, with the
// role they have there at the time of each request.
func TestAccountToken(t *testing.T) {
	store := registrytest.NewMemStore()
	m := members{"u1/lime": registry.ScopePublish, "u1/acme": registry.ScopeRead, "u2/acme": registry.ScopePublish}
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	s := registry.NewService(store, store, store, now, log, registry.WithMemberships(m))
	props := registry.NewProposalService(store, store, now, log, registry.WithAccess(s))
	ctx := t.Context()
	if _, err := s.CreateNamespace(ctx, "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNamespace(ctx, "lime", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	account := func(user string) registry.Principal {
		t.Helper()
		secret, tok, err := s.CreateAccountToken(ctx, user+" via loadout login", user)
		if err != nil {
			t.Fatal(err)
		}
		if tok.Team != registry.AccountNamespace || tok.ExpiresAt.IsZero() {
			t.Fatalf("CreateAccountToken() = %+v, want an expiring token of %s", tok, registry.AccountNamespace)
		}
		pr, err := s.Authenticate(ctx, secret)
		if err != nil || !pr.Account || pr.Narrowed() {
			t.Fatalf("Authenticate(account token) = %+v, %v; want an account principal with no namespace yet", pr, err)
		}
		return pr
	}
	u1 := account("u1")

	// A listing needs a namespace.
	if _, err := s.Packages(ctx, u1); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Packages(no namespace) error = %v, want ErrInvalid", err)
	}
	lime, err := s.In(ctx, u1, "lime")
	if err != nil || lime.Team != "lime" || lime.Scope != registry.ScopePublish || !lime.CanRead("acme") {
		t.Fatalf("In(lime) = %+v, %v; want publish in lime, reading acme", lime, err)
	}
	if _, err := s.In(ctx, u1, "other"); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("In(other) error = %v, want ErrForbidden", err)
	}

	// Publishing goes by the package's namespace, whatever the view.
	if _, err := s.Publish(ctx, u1, bundleFor(t, "lime", "0.1.0")); err != nil {
		t.Errorf("Publish(lime) error: %v", err)
	}
	acmeView, err := s.In(ctx, u1, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(ctx, acmeView, bundleFor(t, "lime", "0.2.0")); err != nil {
		t.Errorf("Publish(lime) from the acme view error: %v", err)
	}
	if _, err := s.Publish(ctx, lime, bundleFor(t, "acme", "0.1.0")); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Publish(acme) as a reader there error = %v, want ErrForbidden", err)
	}

	// Reading one package works without a view.
	if _, _, err := s.Fetch(ctx, u1, mustID(t, "lime/planner"), "latest"); err != nil {
		t.Errorf("Fetch() without a view error: %v", err)
	}

	// u1 proposes to acme; u2, a maintainer there, approves.
	p, err := props.Propose(ctx, u1, bundleFor(t, "acme", "1.0.0"), registry.ProposalInput{Prompts: []string{"plan"}})
	if err != nil {
		t.Fatalf("Propose(acme) error: %v", err)
	}
	if _, _, err := props.Approve(ctx, u1, p.ID, registry.ApproveInput{}); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Approve() by the author error = %v, want ErrForbidden", err)
	}
	if _, rel, err := props.Approve(ctx, account("u2"), p.ID, registry.ApproveInput{}); err != nil || rel.Version.String() != "1.0.0" {
		t.Errorf("Approve() by u2 = %+v, %v; want the release", rel, err)
	}

	// Losing the membership takes the access away at once.
	delete(m, "u1/lime")
	if _, err := s.In(ctx, u1, "lime"); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("In(lime) after removal error = %v, want ErrForbidden", err)
	}
}

func mustID(t *testing.T, s string) pack.ID {
	t.Helper()
	id, err := pack.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
