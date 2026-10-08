package account_test

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/password"
)

// TestGoogleLinkCancelsSignupCodes: a sign-up code sent before the owner
// linked Google cannot set a password afterwards.
func TestGoogleLinkCancelsSignupCodes(t *testing.T) {
	f := newFixture(t)
	const email = "alice@example.com"
	p, err := f.svc.Signup(t.Context(), email, "attacker picked this")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := f.mailed(t, email, 0)
	if _, err := f.svc.GoogleSignIn(t.Context(), account.GoogleIdentity{Subject: "g-alice", Email: email, EmailVerified: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code); !errors.Is(err, account.ErrInvalidCode) {
		t.Errorf("VerifyCode(sign-up code after Google linked) error = %v, want ErrInvalidCode", err)
	}
	if u, err := f.store.UserByEmail(t.Context(), email); err != nil || u.PasswordHash != "" {
		t.Errorf("user after the old sign-up code = %+v, %v; want no password", u, err)
	}
}

// TestSignupCodeNeverChangesAVerifiedPassword: completing a sign-up code of
// an account that is verified by now changes nothing.
func TestSignupCodeNeverChangesAVerifiedPassword(t *testing.T) {
	f := newFixture(t)
	g := f.signup(t, "alice@example.com")
	evil, err := password.Hash("attacker picked this")
	if err != nil {
		t.Fatal(err)
	}
	link := sha256.Sum256([]byte("stale-link"))
	stale := account.Challenge{
		ID: "stale", UserID: g.User.ID, Purpose: account.PurposeVerifySignup,
		CodeHash: []byte("x"), LinkHash: link[:], PasswordHash: evil,
		CreatedAt: f.clock.t, ExpiresAt: f.clock.t.Add(account.ChallengeTTL),
	}
	if err := f.store.CreateChallenge(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.VerifyLink(t.Context(), "stale-link"); !errors.Is(err, account.ErrInvalidCode) {
		t.Errorf("VerifyLink(sign-up of a verified account) error = %v, want ErrInvalidCode", err)
	}
	u, err := f.store.UserByID(t.Context(), g.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := password.Verify(pw, u.PasswordHash); err != nil || !ok {
		t.Errorf("the password changed: the owner's own password no longer works (%v)", err)
	}
	if _, err := f.svc.SessionUser(t.Context(), g.Secret); err != nil {
		t.Errorf("the owner's session ended: %v", err)
	}
}

// TestSignupEndsOtherSessions: setting the password with a sign-up code
// ends every session started before.
func TestSignupEndsOtherSessions(t *testing.T) {
	f := newFixture(t)
	const email = "alice@example.com"
	p, err := f.svc.Signup(t.Context(), email, pw)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := f.mailed(t, email, 0)
	u, err := f.store.UserByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	old := sha256.Sum256([]byte("older-session"))
	if err := f.store.CreateSession(t.Context(), account.Session{UserID: u.ID, CreatedAt: f.clock.t, ExpiresAt: f.clock.t.Add(account.SessionTTL)}, old[:]); err != nil {
		t.Fatal(err)
	}
	g, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SessionUser(t.Context(), "older-session"); !errors.Is(err, account.ErrNoSession) {
		t.Errorf("SessionUser(session from before the password was set) error = %v, want ErrNoSession", err)
	}
	if _, err := f.svc.SessionUser(t.Context(), g.Secret); err != nil {
		t.Errorf("SessionUser(new session) error: %v", err)
	}
}
