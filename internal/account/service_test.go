package account_test

import (
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/mail/mailtest"
	"github.com/petervlad/loadout/internal/password"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/tier"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type fixture struct {
	svc    *account.Service
	reg    *registry.Service
	store  *accounttest.MemStore
	roles  *account.Roles
	mails  *mailtest.Recorder
	clock  *clock
	regMem *registrytest.MemStore
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.DiscardHandler)
	regMem := registrytest.NewMemStore()
	store := accounttest.NewMemStore(regMem)
	roles := account.NewRoles(store, regMem)
	reg := registry.NewService(regMem, regMem, regMem, c.now, log, registry.WithMemberships(roles))
	mails := &mailtest.Recorder{}
	svc := account.NewService(store, reg, roles, device.New(c.now), account.Options{
		PublicURL: "https://registry.example.com",
		Mailer:    mails,
		Now:       c.now,
		Log:       log,
	})
	if _, err := reg.CreateNamespace(t.Context(), "lime", "", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	return &fixture{svc: svc, reg: reg, store: store, roles: roles, mails: mails, clock: c, regMem: regMem}
}

var (
	codeRE = regexp.MustCompile(`\b(\d{6})\b`)
	linkRE = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)
)

// mailed returns the code and link token of the nth mail to addr.
func (f *fixture) mailed(t *testing.T, addr string, n int) (code, token string) {
	t.Helper()
	m := f.mails.WaitFor(t, addr, n)
	c := codeRE.FindStringSubmatch(m.Text)
	l := linkRE.FindStringSubmatch(m.Text)
	if c == nil || l == nil {
		t.Fatalf("mail to %s has no code or link:\n%s", addr, m.Text)
	}
	return c[1], l[1]
}

const pw = "tangerine kettle river"

// signup creates a verified user with pw.
func (f *fixture) signup(t *testing.T, email string) account.SessionGrant {
	t.Helper()
	n := f.mails.Count(email)
	p, err := f.svc.Signup(t.Context(), email, pw)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := f.mailed(t, email, n)
	g, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code)
	if err != nil {
		t.Fatalf("VerifyCode(signup) error: %v", err)
	}
	return g
}

func TestSignupVerifyAndLogin(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Signup(t.Context(), "  Alice@Example.com ", pw)
	if err != nil {
		t.Fatal(err)
	}
	m := f.mails.WaitFor(t, "alice@example.com", 0)
	if !strings.Contains(m.Subject, "Confirm") || !strings.Contains(m.Text, "https://registry.example.com/auth/verify?token=") {
		t.Errorf("signup mail = %+v, want a confirmation with a link to the registry", m)
	}
	u, err := f.store.UserByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.Verified() || u.PasswordHash != "" {
		t.Errorf("user before verification = %+v, want unverified without a password", u)
	}
	if _, err := f.svc.Login(t.Context(), "alice@example.com", pw); err != nil {
		t.Fatal(err)
	}
	if n := f.mails.Count("alice@example.com"); n != 1 {
		t.Errorf("login before verification sent %d mails in total, want only the signup mail", n)
	}

	code, _ := f.mailed(t, "alice@example.com", 0)
	g, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code)
	if err != nil {
		t.Fatalf("VerifyCode() error: %v", err)
	}
	if !g.User.Verified() || g.User.Email != "alice@example.com" || !g.ExpiresAt.Equal(f.clock.t.Add(account.SessionTTL)) {
		t.Errorf("grant = %+v, want a verified user and a 30-day session", g)
	}
	if got, err := f.svc.SessionUser(t.Context(), g.Secret); err != nil || got.ID != u.ID {
		t.Errorf("SessionUser() = %+v, %v; want %s", got, err, u.ID)
	}

	// Log in: password first, then the mailed code.
	lp, err := f.svc.Login(t.Context(), "ALICE@example.com", pw)
	if err != nil {
		t.Fatal(err)
	}
	lm := f.mails.WaitFor(t, "alice@example.com", 1)
	if !strings.Contains(lm.Subject, "sign-in code") {
		t.Errorf("login mail subject = %q, want a sign-in code", lm.Subject)
	}
	code, _ = f.mailed(t, "alice@example.com", 1)
	if _, err := f.svc.VerifyCode(t.Context(), lp.ChallengeID, code); err != nil {
		t.Fatalf("VerifyCode(login) error: %v", err)
	}
}

func TestLoginDoesNotRevealAccounts(t *testing.T) {
	f := newFixture(t)
	f.signup(t, "alice@example.com")
	before := len(f.mails.Sent())

	for name, tc := range map[string]struct{ email, pw string }{
		"wrong password": {"alice@example.com", "wrong password here"},
		"unknown email":  {"nobody@example.com", pw},
	} {
		p, err := f.svc.Login(t.Context(), tc.email, tc.pw)
		if err != nil || p.ChallengeID == "" {
			t.Errorf("Login(%s) = %+v, %v; want a normal-looking answer", name, p, err)
		}
		if _, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, "000000"); !errors.Is(err, account.ErrInvalidCode) {
			t.Errorf("VerifyCode(%s) error = %v, want ErrInvalidCode", name, err)
		}
	}
	if n := len(f.mails.Sent()); n != before {
		t.Errorf("failed logins sent %d mails, want none", n-before)
	}
}

func TestSignupOfAnExistingAccount(t *testing.T) {
	f := newFixture(t)
	f.signup(t, "alice@example.com")
	u, err := f.store.UserByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}

	p, err := f.svc.Signup(t.Context(), "alice@example.com", "another good password")
	if err != nil || p.ChallengeID == "" {
		t.Fatalf("Signup(existing) = %+v, %v; want the usual answer", p, err)
	}
	m := f.mails.WaitFor(t, "alice@example.com", 1)
	if !strings.Contains(m.Subject, "already have") || codeRE.MatchString(m.Text) {
		t.Errorf("mail = %+v, want a notice without a code", m)
	}
	after, err := f.store.UserByID(t.Context(), u.ID)
	if err != nil || after.PasswordHash != u.PasswordHash {
		t.Error("signing up again changed the password")
	}
}

func TestUnverifiedSignupNeverSetsThePassword(t *testing.T) {
	f := newFixture(t)
	// Mallory signs up with Alice's address first, then Alice does.
	if _, err := f.svc.Signup(t.Context(), "alice@example.com", "mallory's password!"); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Signup(t.Context(), "alice@example.com", pw)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := f.mailed(t, "alice@example.com", 1)
	g, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := password.Verify(pw, g.User.PasswordHash); err != nil || !ok {
		t.Error("Alice's own password is not the one set")
	}
}

func TestCodeRules(t *testing.T) {
	t.Run("wrong format", func(t *testing.T) {
		f := newFixture(t)
		p, err := f.svc.Signup(t.Context(), "a@example.com", pw)
		if err != nil {
			t.Fatal(err)
		}
		for _, code := range []string{"", "12345", "1234567", "abcdef"} {
			if _, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code); !errors.Is(err, account.ErrInvalidCode) {
				t.Errorf("VerifyCode(%q) error = %v, want ErrInvalidCode", code, err)
			}
		}
		// Spaces are ignored: people copy "123 456".
		code, _ := f.mailed(t, "a@example.com", 0)
		if _, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code[:3]+" "+code[3:]); err != nil {
			t.Errorf("VerifyCode(with a space) error: %v", err)
		}
	})

	t.Run("expires after 15 minutes", func(t *testing.T) {
		f := newFixture(t)
		p, err := f.svc.Signup(t.Context(), "a@example.com", pw)
		if err != nil {
			t.Fatal(err)
		}
		code, token := f.mailed(t, "a@example.com", 0)
		f.clock.advance(account.ChallengeTTL)
		if _, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code); !errors.Is(err, account.ErrInvalidCode) {
			t.Errorf("VerifyCode(expired) error = %v, want ErrInvalidCode", err)
		}
		if _, err := f.svc.VerifyLink(t.Context(), token); !errors.Is(err, account.ErrInvalidCode) {
			t.Errorf("VerifyLink(expired) error = %v, want ErrInvalidCode", err)
		}
	})

	t.Run("five attempts", func(t *testing.T) {
		f := newFixture(t)
		p, err := f.svc.Signup(t.Context(), "a@example.com", pw)
		if err != nil {
			t.Fatal(err)
		}
		code, token := f.mailed(t, "a@example.com", 0)
		wrong := "000000"
		if code == wrong {
			wrong = "111111"
		}
		for range account.MaxAttempts {
			if _, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, wrong); !errors.Is(err, account.ErrInvalidCode) {
				t.Fatalf("VerifyCode(wrong) error = %v, want ErrInvalidCode", err)
			}
		}
		if _, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code); !errors.Is(err, account.ErrInvalidCode) {
			t.Errorf("VerifyCode(right, after %d wrong) error = %v, want ErrInvalidCode", account.MaxAttempts, err)
		}
		if _, err := f.svc.VerifyLink(t.Context(), token); !errors.Is(err, account.ErrInvalidCode) {
			t.Errorf("VerifyLink() after the attempts ran out error = %v, want ErrInvalidCode", err)
		}
	})

	t.Run("single use", func(t *testing.T) {
		f := newFixture(t)
		p, err := f.svc.Signup(t.Context(), "a@example.com", pw)
		if err != nil {
			t.Fatal(err)
		}
		code, token := f.mailed(t, "a@example.com", 0)
		if _, err := f.svc.VerifyLink(t.Context(), token); err != nil {
			t.Fatalf("VerifyLink() error: %v", err)
		}
		if _, err := f.svc.VerifyLink(t.Context(), token); !errors.Is(err, account.ErrInvalidCode) {
			t.Errorf("second VerifyLink() error = %v, want ErrInvalidCode", err)
		}
		if _, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code); !errors.Is(err, account.ErrInvalidCode) {
			t.Errorf("VerifyCode() after the link was used error = %v, want ErrInvalidCode", err)
		}
	})
}

func TestSignupRules(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Signup(t.Context(), "not an email", pw); !errors.Is(err, account.ErrInvalidEmail) {
		t.Errorf("Signup(bad email) error = %v, want ErrInvalidEmail", err)
	}
	if _, err := f.svc.Signup(t.Context(), "a@example.com", "password1234"); err == nil {
		t.Error("Signup(common password) succeeded")
	}
	off := account.NewService(f.store, f.reg, f.roles, device.New(f.clock.now), account.Options{Now: f.clock.now, Log: slog.New(slog.DiscardHandler)})
	if off.EmailEnabled() {
		t.Error("EmailEnabled() without a mailer = true")
	}
	if _, err := off.Signup(t.Context(), "a@example.com", pw); !errors.Is(err, account.ErrEmailDisabled) {
		t.Errorf("Signup() without a mailer error = %v, want ErrEmailDisabled", err)
	}
	if _, err := off.Login(t.Context(), "a@example.com", pw); !errors.Is(err, account.ErrEmailDisabled) {
		t.Errorf("Login() without a mailer error = %v, want ErrEmailDisabled", err)
	}
}

func TestSessions(t *testing.T) {
	f := newFixture(t)
	g := f.signup(t, "alice@example.com")
	if _, err := f.svc.SessionUser(t.Context(), "made-up"); !errors.Is(err, account.ErrNoSession) {
		t.Errorf("SessionUser(made up) error = %v, want ErrNoSession", err)
	}
	if err := f.svc.Logout(t.Context(), g.Secret); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SessionUser(t.Context(), g.Secret); !errors.Is(err, account.ErrNoSession) {
		t.Errorf("SessionUser() after logout error = %v, want ErrNoSession", err)
	}

	g = f.login(t, "alice@example.com")
	f.clock.advance(account.SessionTTL)
	if _, err := f.svc.SessionUser(t.Context(), g.Secret); !errors.Is(err, account.ErrNoSession) {
		t.Errorf("SessionUser() after 30 days error = %v, want ErrNoSession", err)
	}
}

// login signs an existing user in with password and code.
func (f *fixture) login(t *testing.T, email string) account.SessionGrant {
	t.Helper()
	n := f.mails.Count(email)
	p, err := f.svc.Login(t.Context(), email, pw)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := f.mailed(t, email, n)
	g, err := f.svc.VerifyCode(t.Context(), p.ChallengeID, code)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGoogleSignIn(t *testing.T) {
	f := newFixture(t)
	id := account.GoogleIdentity{Subject: "g-1", Email: "Alice@Example.com", EmailVerified: true, Name: "Alice"}

	if _, err := f.svc.GoogleSignIn(t.Context(), account.GoogleIdentity{Subject: "g-1", Email: "a@example.com"}); !errors.Is(err, account.ErrGoogleEmailUnverified) {
		t.Errorf("GoogleSignIn(unverified email) error = %v, want ErrGoogleEmailUnverified", err)
	}

	g, err := f.svc.GoogleSignIn(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if g.User.Email != "alice@example.com" || g.User.GoogleSubject != "g-1" || !g.User.Verified() || g.User.Name != "Alice" {
		t.Errorf("new Google user = %+v", g.User)
	}
	if len(f.mails.Sent()) != 0 {
		t.Error("Google sign-in sent a mail; Google already verified the address")
	}

	// Same subject, new address at Google: still the same user.
	again, err := f.svc.GoogleSignIn(t.Context(), account.GoogleIdentity{Subject: "g-1", Email: "new@example.com", EmailVerified: true})
	if err != nil || again.User.ID != g.User.ID {
		t.Errorf("GoogleSignIn(same subject) = %+v, %v; want user %s", again.User, err, g.User.ID)
	}

	// A password user is linked by verified email.
	bob := f.signup(t, "bob@example.com")
	linked, err := f.svc.GoogleSignIn(t.Context(), account.GoogleIdentity{Subject: "g-2", Email: "bob@example.com", EmailVerified: true})
	if err != nil || linked.User.ID != bob.User.ID || linked.User.GoogleSubject != "g-2" || linked.User.PasswordHash == "" {
		t.Errorf("GoogleSignIn(bob) = %+v, %v; want bob linked, password kept", linked.User, err)
	}

	// Another Google account with Bob's address is refused.
	if _, err := f.svc.GoogleSignIn(t.Context(), account.GoogleIdentity{Subject: "g-3", Email: "bob@example.com", EmailVerified: true}); !errors.Is(err, account.ErrGoogleConflict) {
		t.Errorf("GoogleSignIn(other subject, bob's email) error = %v, want ErrGoogleConflict", err)
	}
}

func TestMembers(t *testing.T) {
	f := newFixture(t)
	op := account.Operator()
	m, created, err := f.svc.AddMember(t.Context(), op, "lime", "Carol@Example.com", account.RoleMaintainer)
	if err != nil {
		t.Fatal(err)
	}
	if !created || m.Email != "carol@example.com" || m.Role != account.RoleMaintainer {
		t.Errorf("AddMember() = %+v, created %v; want a new user who maintains lime", m, created)
	}
	if _, created, err := f.svc.AddMember(t.Context(), op, "lime", "carol@example.com", account.RoleMember); err != nil || created {
		t.Errorf("AddMember(again, member) created %v, %v; want the existing user", created, err)
	}
	if _, _, err := f.svc.AddMember(t.Context(), op, "ghost", "carol@example.com", account.RoleMember); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("AddMember(missing namespace) error = %v, want ErrNotFound", err)
	}
	if _, _, err := f.svc.AddMember(t.Context(), op, "lime", "carol@example.com", "publish"); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("AddMember(bad role) error = %v, want ErrInvalid", err)
	}
	if _, _, err := f.svc.AddMember(t.Context(), op, "lime", "carol@example.com", account.RoleOwner); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("AddMember(owner of a team) error = %v, want ErrInvalid", err)
	}
	if _, err := f.svc.SetMember(t.Context(), op, "lime", "dave@example.com", account.RoleMember); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("SetMember(not a member) error = %v, want ErrNotFound", err)
	}
	ms, err := f.svc.Members(t.Context(), op, "lime")
	if err != nil || len(ms) != 1 || ms[0].Role != account.RoleMember {
		t.Errorf("Members() = %+v, %v; want carol as member", ms, err)
	}
	if _, err := f.svc.RemoveMember(t.Context(), op, "lime", "nobody@example.com"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("RemoveMember(unknown user) error = %v, want ErrNotFound", err)
	}
}

// team sets up acme (project) with lime (team) below and one user per role,
// and returns each user's actor.
func (f *fixture) team(t *testing.T) map[string]account.Actor {
	t.Helper()
	ctx, op := t.Context(), account.Operator()
	if _, err := f.reg.CreateNamespace(ctx, "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.CreateNamespace(ctx, "web", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	actors := map[string]account.Actor{}
	for _, m := range []struct {
		name, ns string
		role     account.Role
	}{
		{"owner", "acme", account.RoleOwner},
		{"maintainer", "web", account.RoleMaintainer},
		{"member", "web", account.RoleMember},
		{"other", "web", account.RoleMember},
		{"stranger", "lime", account.RoleMember},
	} {
		ms, _, err := f.svc.AddMember(ctx, op, m.ns, m.name+"@example.com", m.role)
		if err != nil {
			t.Fatal(err)
		}
		actors[m.name] = account.UserActor(ms.UserID)
	}
	admin, err := f.svc.SetAdmin(ctx, op, "admin@example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	actors["admin"] = account.UserActor(admin.ID)
	return actors
}

// TestManagementMatrix checks who may do what through the service, with
// real memberships: team web under project acme.
func TestManagementMatrix(t *testing.T) {
	type check struct {
		name string
		do   func(f *fixture, a account.Actor) error
	}
	ctx := t.Context()
	checks := []check{
		{"add member to web", func(f *fixture, a account.Actor) error {
			_, _, err := f.svc.AddMember(ctx, a, "web", "new@example.com", account.RoleMember)
			return err
		}},
		{"add maintainer to web", func(f *fixture, a account.Actor) error {
			_, _, err := f.svc.AddMember(ctx, a, "web", "new@example.com", account.RoleMaintainer)
			return err
		}},
		{"add owner to acme", func(f *fixture, a account.Actor) error {
			_, _, err := f.svc.AddMember(ctx, a, "acme", "new@example.com", account.RoleOwner)
			return err
		}},
		{"demote maintainer of web", func(f *fixture, a account.Actor) error {
			_, err := f.svc.SetMember(ctx, a, "web", "maintainer@example.com", account.RoleMember)
			return err
		}},
		{"remove member of web", func(f *fixture, a account.Actor) error {
			_, err := f.svc.RemoveMember(ctx, a, "web", "other@example.com")
			return err
		}},
		{"list members of web", func(f *fixture, a account.Actor) error {
			_, err := f.svc.Members(ctx, a, "web")
			return err
		}},
		{"create team in acme", func(f *fixture, a account.Actor) error {
			_, err := f.svc.CreateNamespace(ctx, a, "mobile", "acme", registry.KindTeam)
			return err
		}},
		{"create project", func(f *fixture, a account.Actor) error {
			_, err := f.svc.CreateNamespace(ctx, a, "globex", "", registry.KindProject)
			return err
		}},
		{"require review in web", func(f *fixture, a account.Actor) error {
			_, err := f.svc.SetRequireReview(ctx, a, "web", true)
			return err
		}},
		{"set model tiers of web", func(f *fixture, a account.Actor) error {
			_, err := f.svc.SetModelTiers(ctx, a, "web", tier.Table{})
			return err
		}},
		{"set model tiers of acme", func(f *fixture, a account.Actor) error {
			_, err := f.svc.SetModelTiers(ctx, a, "acme", tier.Table{})
			return err
		}},
		{"read model tiers of web", func(f *fixture, a account.Actor) error {
			_, err := f.svc.ModelTiers(ctx, a, "web")
			return err
		}},
		{"create machine token in web", func(f *fixture, a account.Actor) error {
			_, _, err := f.svc.CreateMachineToken(ctx, a, "web", registry.ScopeRead, "ci")
			return err
		}},
		{"delete namespace", func(f *fixture, a account.Actor) error {
			if _, err := f.reg.CreateNamespace(ctx, "empty", "acme", registry.KindTeam); err != nil {
				return err
			}
			return f.svc.DeleteNamespace(ctx, a, "empty")
		}},
	}
	allowed := map[string][]string{
		"admin":      {"add member to web", "add maintainer to web", "add owner to acme", "demote maintainer of web", "remove member of web", "list members of web", "create team in acme", "create project", "require review in web", "set model tiers of web", "set model tiers of acme", "read model tiers of web", "create machine token in web", "delete namespace"},
		"owner":      {"add member to web", "add maintainer to web", "demote maintainer of web", "remove member of web", "list members of web", "create team in acme", "require review in web", "set model tiers of web", "set model tiers of acme", "read model tiers of web", "create machine token in web"},
		"maintainer": {"add member to web", "remove member of web", "list members of web", "set model tiers of web", "read model tiers of web", "create machine token in web"},
		"member":     {"list members of web", "read model tiers of web"},
		"stranger":   {},
	}
	for who, ok := range allowed {
		for _, c := range checks {
			t.Run(who+"/"+c.name, func(t *testing.T) {
				f := newFixture(t)
				actors := f.team(t)
				err := c.do(f, actors[who])
				want := false
				for _, name := range ok {
					want = want || name == c.name
				}
				if want && err != nil {
					t.Errorf("error: %v, want allowed", err)
				}
				if !want && !errors.Is(err, registry.ErrForbidden) {
					t.Errorf("error = %v, want ErrForbidden", err)
				}
			})
		}
	}
}

func TestMachineTokens(t *testing.T) {
	f := newFixture(t)
	actors := f.team(t)
	secret, tok, err := f.svc.CreateMachineToken(t.Context(), actors["maintainer"], "web", registry.ScopePublish, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.Authenticate(t.Context(), secret); err != nil {
		t.Errorf("machine token does not work: %v", err)
	}
	// A login token of the same team is not a machine token.
	if _, _, err := f.reg.CreateUserToken(t.Context(), "web", registry.ScopeRead, "x", actors["member"].UserID); err != nil {
		t.Fatal(err)
	}
	toks, err := f.svc.MachineTokens(t.Context(), actors["maintainer"], "web")
	if err != nil || len(toks) != 1 || toks[0].ID != tok.ID {
		t.Errorf("MachineTokens() = %+v, %v; want only the ci token", toks, err)
	}
	if err := f.svc.RevokeMachineToken(t.Context(), actors["maintainer"], "lime", tok.ID); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("RevokeMachineToken(another team) error = %v, want ErrForbidden", err)
	}
	if err := f.svc.RevokeMachineToken(t.Context(), actors["maintainer"], "web", tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.Authenticate(t.Context(), secret); !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("revoked machine token still works: %v", err)
	}
}

func TestAdmins(t *testing.T) {
	f := newFixture(t)
	op := account.Operator()
	u, err := f.svc.SetAdmin(t.Context(), op, "Root@Example.com", true)
	if err != nil || !u.IsAdmin || u.Email != "root@example.com" {
		t.Fatalf("SetAdmin() = %+v, %v", u, err)
	}
	if _, err := f.svc.SetAdmin(t.Context(), account.UserActor(u.ID), "x@example.com", true); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("SetAdmin() by a user error = %v, want ErrForbidden: admins are made on the host", err)
	}
	admins, err := f.svc.Admins(t.Context(), op)
	if err != nil || len(admins) != 1 {
		t.Errorf("Admins() = %+v, %v", admins, err)
	}
	if _, err := f.svc.SetAdmin(t.Context(), op, "root@example.com", false); err != nil {
		t.Fatal(err)
	}
	if admins, err := f.svc.Admins(t.Context(), op); err != nil || len(admins) != 0 {
		t.Errorf("Admins() after revoke = %+v, %v", admins, err)
	}
	if _, err := f.svc.SetAdmin(t.Context(), op, "nobody@example.com", false); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("SetAdmin(unknown, false) error = %v, want ErrNotFound", err)
	}
}

// deviceLogin runs the device flow for u (approving for lime) and returns
// the poll that carried the token.
func (f *fixture) deviceLogin(t *testing.T, u account.User, namespace string) account.DevicePoll {
	t.Helper()
	start, err := f.svc.StartDevice(t.Context(), namespace, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if start.VerificationURI != "https://registry.example.com/auth/device" || !strings.HasSuffix(start.VerificationURIComplete, "?code="+start.UserCode) {
		t.Errorf("StartDevice() URIs = %s, %s", start.VerificationURI, start.VerificationURIComplete)
	}
	if poll, err := f.svc.PollDevice(t.Context(), start.DeviceCode); err != nil || poll.Status != device.StatusPending {
		t.Fatalf("PollDevice() before approval = %+v, %v; want pending", poll, err)
	}
	if err := f.svc.ApproveDevice(t.Context(), u, start.UserCode, "lime"); err != nil {
		t.Fatalf("ApproveDevice() error: %v", err)
	}
	f.clock.advance(start.Interval)
	poll, err := f.svc.PollDevice(t.Context(), start.DeviceCode)
	if err != nil || poll.Status != device.StatusApproved || poll.Token == "" {
		t.Fatalf("PollDevice() after approval = %+v, %v; want a token", poll, err)
	}
	return poll
}

func TestDeviceLogin(t *testing.T) {
	f := newFixture(t)
	g := f.signup(t, "alice@example.com")

	// Not a member yet: the request shows no choices and cannot be approved.
	start, err := f.svc.StartDevice(t.Context(), "lime", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.svc.DeviceRequest(t.Context(), g.User, strings.ToLower(start.UserCode))
	if err != nil || req.Namespace != "lime" || len(req.Choices) != 0 {
		t.Errorf("DeviceRequest() = %+v, %v; want lime with no choices", req, err)
	}
	if err := f.svc.ApproveDevice(t.Context(), g.User, start.UserCode, "lime"); !errors.Is(err, account.ErrNotMember) {
		t.Errorf("ApproveDevice(non-member) error = %v, want ErrNotMember", err)
	}
	if _, err := f.svc.DeviceRequest(t.Context(), g.User, "BCDF-GHJK"); !errors.Is(err, account.ErrInvalidCode) {
		t.Errorf("DeviceRequest(unknown code) error = %v, want ErrInvalidCode", err)
	}

	if _, _, err := f.svc.AddMember(t.Context(), account.Operator(), "lime", "alice@example.com", account.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	poll := f.deviceLogin(t, g.User, "lime")
	if poll.Namespace != "lime" || poll.Scope != registry.ScopePublish {
		t.Errorf("token for %s/%s, want lime/publish", poll.Namespace, poll.Scope)
	}
	pr, err := f.reg.Authenticate(t.Context(), poll.Token)
	if err != nil || pr.Team != "lime" || pr.UserID != g.User.ID || pr.Label != "alice@example.com via loadout login" {
		t.Fatalf("Authenticate(login token) = %+v, %v", pr, err)
	}

	// Demoted to member: the token reads only.
	if _, err := f.svc.SetMember(t.Context(), account.Operator(), "lime", "alice@example.com", account.RoleMember); err != nil {
		t.Fatal(err)
	}
	if pr, err := f.reg.Authenticate(t.Context(), poll.Token); err != nil || pr.Scope != registry.ScopeRead {
		t.Errorf("Authenticate() after demotion = %+v, %v; want read", pr, err)
	}

	// Removing the membership revokes the token.
	n, err := f.svc.RemoveMember(t.Context(), account.Operator(), "lime", "alice@example.com")
	if err != nil || n != 1 {
		t.Errorf("RemoveMember() = %d, %v; want 1 token revoked", n, err)
	}
	if _, err := f.reg.Authenticate(t.Context(), poll.Token); !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("Authenticate() after removal error = %v, want ErrUnauthorized", err)
	}
}

func TestDeviceLoginChoosesNamespace(t *testing.T) {
	f := newFixture(t)
	g := f.signup(t, "alice@example.com")
	if _, _, err := f.svc.AddMember(t.Context(), account.Operator(), "lime", "alice@example.com", account.RoleMember); err != nil {
		t.Fatal(err)
	}
	start, err := f.svc.StartDevice(t.Context(), "", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.svc.DeviceRequest(t.Context(), g.User, start.UserCode)
	if err != nil || req.Namespace != "" || len(req.Choices) != 1 || req.Choices[0].Namespace != "lime" {
		t.Fatalf("DeviceRequest() = %+v, %v; want lime as the only choice", req, err)
	}
	poll := f.deviceLogin(t, g.User, "")
	if poll.Namespace != "lime" || poll.Scope != registry.ScopeRead {
		t.Errorf("token for %s/%s, want lime/read", poll.Namespace, poll.Scope)
	}
}

func TestDeleteUserRevokesEverything(t *testing.T) {
	f := newFixture(t)
	g := f.signup(t, "alice@example.com")
	if _, _, err := f.svc.AddMember(t.Context(), account.Operator(), "lime", "alice@example.com", account.RoleMember); err != nil {
		t.Fatal(err)
	}
	poll := f.deviceLogin(t, g.User, "lime")
	n, err := f.svc.DeleteUser(t.Context(), account.Operator(), "alice@example.com")
	if err != nil || n != 1 {
		t.Errorf("DeleteUser() = %d, %v; want 1 token revoked", n, err)
	}
	if _, err := f.reg.Authenticate(t.Context(), poll.Token); !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("token of a deleted user still works: %v", err)
	}
	if _, err := f.svc.SessionUser(t.Context(), g.Secret); !errors.Is(err, account.ErrNoSession) {
		t.Errorf("session of a deleted user still works: %v", err)
	}
}

func TestNormalizeEmail(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"\tAlice@Example.COM ", "alice@example.com"},
		{"a.b+tag@sub.example.org", "a.b+tag@sub.example.org"},
	} {
		if got, err := account.NormalizeEmail(tt.in); err != nil || got != tt.want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "alice", "alice@", "@example.com", "Alice <a@example.com>", "a@b@example.com", "a@localhost", "a@example.com\r\nBcc: x@y.z", "a b@example.com"} {
		if _, err := account.NormalizeEmail(in); !errors.Is(err, account.ErrInvalidEmail) {
			t.Errorf("NormalizeEmail(%q) error = %v, want ErrInvalidEmail", in, err)
		}
	}
}

// Mails carry a code and a link, never the password.
func TestMailContent(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Signup(t.Context(), "alice@example.com", pw); err != nil {
		t.Fatal(err)
	}
	m := f.mails.WaitFor(t, "alice@example.com", 0)
	if strings.Contains(m.Text, pw) || strings.Contains(m.HTML, pw) {
		t.Error("the mail contains the password")
	}
	if err := m.Validate(); err != nil {
		t.Errorf("mail is not valid: %v", err)
	}
	if !strings.Contains(m.HTML, "<a href=\"https://registry.example.com/auth/verify?token=") {
		t.Errorf("HTML body has no link:\n%s", m.HTML)
	}
}

// The tree has two layers, whoever asks: projects at the top, teams in
// them.
func TestNamespaceShape(t *testing.T) {
	f := newFixture(t)
	admin := f.team(t)["admin"]
	for _, c := range []struct {
		name, parent string
		kind         registry.NamespaceKind
	}{
		{"loose", "", registry.KindTeam},
		{"sub", "web", registry.KindTeam},
		{"inner", "acme", registry.KindProject},
	} {
		if _, err := f.svc.CreateNamespace(t.Context(), admin, c.name, c.parent, c.kind); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("create %s %s under %q: error = %v, want ErrInvalid", c.kind, c.name, c.parent, err)
		}
	}
	if _, err := f.svc.CreateNamespace(t.Context(), account.Operator(), "loose", "", registry.KindTeam); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("host creates a team without a project: error = %v, want ErrInvalid", err)
	}
}

// Whoever creates a project owns it; the registry host creates it with no
// owner.
func TestProjectCreatorIsOwner(t *testing.T) {
	f := newFixture(t)
	admin := f.team(t)["admin"]
	if _, err := f.svc.CreateNamespace(t.Context(), admin, "globex", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	m, err := f.store.Membership(t.Context(), admin.UserID, "globex")
	if err != nil || m.Role != account.RoleOwner {
		t.Errorf("creator's membership = %+v, %v; want owner", m, err)
	}
	if _, err := f.svc.CreateNamespace(t.Context(), account.Operator(), "initech", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if ms, err := f.svc.Members(t.Context(), account.Operator(), "initech"); err != nil || len(ms) != 0 {
		t.Errorf("host-made project members = %v, %v; want none", ms, err)
	}
}
