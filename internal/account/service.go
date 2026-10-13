package account

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/mail"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/password"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/tier"
)

// Mailer delivers one email.
type Mailer interface {
	Send(ctx context.Context, m mail.Message) error
}

// Registry is what the account service needs from the registry: the
// namespaces people manage, and the tokens it issues and revokes for them.
type Registry interface {
	Namespace(ctx context.Context, name string) (registry.Namespace, error)
	CreateNamespace(ctx context.Context, name, parent string, kind registry.NamespaceKind) (registry.Namespace, error)
	DeleteNamespace(ctx context.Context, name string) error
	PackageFootprint(ctx context.Context, id pack.ID) (registry.PackageFootprint, error)
	DeletePackage(ctx context.Context, id pack.ID) (registry.PackageFootprint, error)
	SetRequireReview(ctx context.Context, name string, on bool) (registry.Namespace, error)
	SetWiki(ctx context.Context, name string, on bool) (registry.Namespace, error)
	SetCourse(ctx context.Context, name string, course pack.ID) (registry.Namespace, error)
	TiersOf(ctx context.Context, name string) (tier.Stack, error)
	SetModelTiers(ctx context.Context, name string, t tier.Table) (tier.Stack, error)
	CreateAccountToken(ctx context.Context, label, userID string) (string, registry.Token, error)
	CreateUserToken(ctx context.Context, team string, scope registry.TokenScope, label, userID string) (string, registry.Token, error)
	CreateMachineToken(ctx context.Context, team string, scope registry.TokenScope, label, createdBy string) (string, registry.Token, error)
	Tokens(ctx context.Context, team string) ([]registry.Token, error)
	RevokeToken(ctx context.Context, id string) error
	RevokeUserTokens(ctx context.Context, userID, team string) (int, error)
}

var _ Registry = (*registry.Service)(nil)

// Snapshots keeps a copy of the database before a change nothing undoes.
type Snapshots = registry.Snapshots

// Deletions keeps the record a delete without review leaves: who deleted
// the package, why and what went, written in the transaction that deletes.
type Deletions interface {
	RecordDelete(ctx context.Context, by registry.Actor, id pack.ID, reason string) (registry.PackageFootprint, error)
}

var _ Deletions = (*registry.DeletionService)(nil)

// Options configures a Service.
type Options struct {
	// PublicURL is the registry's address in a browser, for links in mails
	// and the device approval page. Empty when web login is off.
	PublicURL string
	// Mailer sends codes. Nil turns email sign-up and sign-in off.
	Mailer Mailer
	// Hasher hashes and checks passwords with a cap on how many run at
	// once. Nil uses password.DefaultConcurrent and password.DefaultWait.
	Hasher *password.Hasher
	// Wiki keeps the namespaces' wiki pages. Nil means this registry has
	// no wiki.
	Wiki Wikis
	// Snapshots takes the copy that comes before a package is deleted on
	// the web. Nil turns web deletes off.
	Snapshots Snapshots
	// Deletions keeps the record of every direct delete. Nil deletes
	// without one, for tests; the registry always sets it.
	Deletions Deletions
	// Now is time.Now in production.
	Now func() time.Time
	Log *slog.Logger
}

// Service implements sign-up, sign-in, sessions, roles and the device flow
// on top of a Store.
type Service struct {
	store     Store
	reg       Registry
	roles     *Roles
	flow      *device.Flow
	mailer    Mailer
	hasher    *password.Hasher
	publicURL string
	wiki      Wikis
	snapshots Snapshots
	deletions Deletions
	// deleteMu makes web deletes run one after the other.
	deleteMu sync.Mutex
	now      func() time.Time
	log      *slog.Logger
}

// NewService returns an account service. roles resolves access over the
// same store; flow holds the pending `loadout login` sign-ins.
func NewService(store Store, reg Registry, roles *Roles, flow *device.Flow, o Options) *Service {
	hasher := o.Hasher
	if hasher == nil {
		hasher = password.NewHasher(password.DefaultConcurrent, password.DefaultWait)
	}
	return &Service{
		hasher:    hasher,
		store:     store,
		reg:       reg,
		roles:     roles,
		flow:      flow,
		mailer:    o.Mailer,
		publicURL: strings.TrimRight(o.PublicURL, "/"),
		wiki:      o.Wiki,
		snapshots: o.Snapshots,
		deletions: o.Deletions,
		now:       o.Now,
		log:       o.Log,
	}
}

// Pending is a started email challenge. ChallengeID is not a secret; it
// only says which code the browser is entering.
type Pending struct {
	ChallengeID string
}

// SessionGrant is a new web session. Secret goes into a cookie and is
// never stored or logged.
type SessionGrant struct {
	Secret    string
	ExpiresAt time.Time
	User      User
}

// EmailEnabled reports whether email sign-up and sign-in are on.
func (s *Service) EmailEnabled() bool { return s.mailer != nil }

// Signup starts an account with email and password. The password is set
// only once the address is confirmed with the emailed code or link, so an
// unverified sign-up never changes an account. The answer is the same
// whether or not the address already has an account; an existing owner gets
// a mail saying so instead of a code.
func (s *Service) Signup(ctx context.Context, email, pw string) (Pending, error) {
	if s.mailer == nil {
		return Pending{}, ErrEmailDisabled
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return Pending{}, err
	}
	if err := password.Check(pw, email); err != nil {
		return Pending{}, err
	}
	hash, err := s.hasher.Hash(ctx, pw) // always, so every path costs the same
	if err != nil {
		return Pending{}, err
	}
	u, err := s.userForEmail(ctx, email)
	if err != nil {
		return Pending{}, fmt.Errorf("sign up: %w", err)
	}
	if u.Verified() {
		s.send(ctx, existingAccountMail(email, s.publicURL))
		s.log.InfoContext(ctx, "sign-up for an existing account", "user_id", u.ID)
		return decoy()
	}
	return s.issue(ctx, u, PurposeVerifySignup, hash)
}

// Login checks email and password and, if they are right, mails a code
// that completes the sign-in. Wrong password, unknown email and a user
// without a password all get the same answer after the same work.
func (s *Service) Login(ctx context.Context, email, pw string) (Pending, error) {
	if s.mailer == nil {
		return Pending{}, ErrEmailDisabled
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return Pending{}, err
	}
	u, err := s.store.UserByEmail(ctx, email)
	if errors.Is(err, registry.ErrNotFound) {
		if err := s.hasher.VerifyDummy(ctx, pw); err != nil {
			return Pending{}, err
		}
		return decoy()
	}
	if err != nil {
		return Pending{}, fmt.Errorf("sign in: %w", err)
	}
	if u.PasswordHash == "" {
		if err := s.hasher.VerifyDummy(ctx, pw); err != nil {
			return Pending{}, err
		}
		return decoy()
	}
	ok, err := s.hasher.Verify(ctx, pw, u.PasswordHash)
	if errors.Is(err, password.ErrBusy) {
		return Pending{}, err
	}
	if err != nil {
		return Pending{}, fmt.Errorf("sign in: user %s: %w", u.ID, err)
	}
	if !ok {
		s.log.InfoContext(ctx, "sign-in with a wrong password", "user_id", u.ID)
		return decoy()
	}
	return s.issue(ctx, u, PurposeLogin, "")
}

// VerifyCode completes a challenge with the code from the mail and starts a
// session. Each challenge allows MaxAttempts codes, and each user
// MaxCodeFailures wrong codes per purpose within CodeFailureWindow; after
// that it returns ErrCodeLocked (the mailed link still works).
func (s *Service) VerifyCode(ctx context.Context, challengeID, code string) (SessionGrant, error) {
	code = strings.Join(strings.Fields(code), "")
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" || challengeID == "" {
		return SessionGrant{}, ErrInvalidCode
	}
	c, err := s.store.Challenge(ctx, challengeID)
	if errors.Is(err, registry.ErrNotFound) {
		return SessionGrant{}, ErrInvalidCode
	}
	if err != nil {
		return SessionGrant{}, fmt.Errorf("verify code: %w", err)
	}
	if !s.usable(c) {
		return SessionGrant{}, ErrInvalidCode
	}
	// Wrong codes are counted per user across codes, so asking for new
	// codes does not buy new guesses.
	now := s.now().UTC()
	failed, err := s.store.CodeFailures(ctx, c.UserID, c.Purpose, now.Add(-CodeFailureWindow))
	if err != nil {
		return SessionGrant{}, fmt.Errorf("verify code: %w", err)
	}
	if failed >= MaxCodeFailures {
		s.log.WarnContext(ctx, "code entry locked", "user_id", c.UserID, "purpose", c.Purpose)
		return SessionGrant{}, ErrCodeLocked
	}
	// Count first, then compare: parallel guesses all count.
	n, err := s.store.AddAttempt(ctx, c.ID)
	if err != nil {
		return SessionGrant{}, fmt.Errorf("verify code: %w", err)
	}
	if n > MaxAttempts {
		return SessionGrant{}, ErrInvalidCode
	}
	if !hmac.Equal(codeHash(c.ID, code), c.CodeHash) {
		if err := s.store.AddCodeFailure(ctx, c.UserID, c.Purpose, now); err != nil {
			return SessionGrant{}, fmt.Errorf("verify code: %w", err)
		}
		s.log.InfoContext(ctx, "wrong code", "challenge", c.ID, "attempt", n)
		return SessionGrant{}, ErrInvalidCode
	}
	return s.complete(ctx, c)
}

// VerifyLink completes a challenge with the token from the mailed link.
func (s *Service) VerifyLink(ctx context.Context, token string) (SessionGrant, error) {
	if token == "" {
		return SessionGrant{}, ErrInvalidCode
	}
	c, err := s.store.ChallengeByLink(ctx, sha256Of(token))
	if errors.Is(err, registry.ErrNotFound) {
		return SessionGrant{}, ErrInvalidCode
	}
	if err != nil {
		return SessionGrant{}, fmt.Errorf("verify link: %w", err)
	}
	if !s.usable(c) {
		return SessionGrant{}, ErrInvalidCode
	}
	return s.complete(ctx, c)
}

// GoogleIdentity is what a verified Google ID token says about a person.
type GoogleIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// GoogleSignIn signs in the person Google vouched for. It finds the user by
// Google subject, else links the user with the same verified address, else
// creates one. No code is mailed: Google verified the address.
func (s *Service) GoogleSignIn(ctx context.Context, id GoogleIdentity) (SessionGrant, error) {
	if !id.EmailVerified {
		return SessionGrant{}, ErrGoogleEmailUnverified
	}
	if id.Subject == "" {
		return SessionGrant{}, fmt.Errorf("%w: google identity without a subject", registry.ErrInvalid)
	}
	email, err := NormalizeEmail(id.Email)
	if err != nil {
		return SessionGrant{}, err
	}
	u, err := s.store.UserByGoogle(ctx, id.Subject)
	if err == nil {
		return s.newSession(ctx, u)
	}
	if !errors.Is(err, registry.ErrNotFound) {
		return SessionGrant{}, fmt.Errorf("google sign-in: %w", err)
	}

	u, err = s.userForEmail(ctx, email)
	if err != nil {
		return SessionGrant{}, fmt.Errorf("google sign-in: %w", err)
	}
	if u.GoogleSubject != "" && u.GoogleSubject != id.Subject {
		return SessionGrant{}, ErrGoogleConflict
	}
	err = s.store.LinkGoogle(ctx, u.ID, id.Subject, id.Name)
	if errors.Is(err, registry.ErrExists) {
		return SessionGrant{}, ErrGoogleConflict // linked by a concurrent request
	}
	if err != nil {
		return SessionGrant{}, fmt.Errorf("google sign-in: link user %s: %w", u.ID, err)
	}
	if err := s.store.MarkVerified(ctx, u.ID, s.now().UTC()); err != nil {
		return SessionGrant{}, fmt.Errorf("google sign-in: verify user %s: %w", u.ID, err)
	}
	if err := s.afterVerify(ctx, u.ID); err != nil {
		return SessionGrant{}, fmt.Errorf("google sign-in: %w", err)
	}
	s.log.InfoContext(ctx, "google account linked", "user_id", u.ID)
	if u, err = s.store.UserByID(ctx, u.ID); err != nil {
		return SessionGrant{}, fmt.Errorf("google sign-in: %w", err)
	}
	return s.newSession(ctx, u)
}

// SessionUser returns the user a session secret belongs to.
func (s *Service) SessionUser(ctx context.Context, secret string) (User, error) {
	if secret == "" {
		return User{}, ErrNoSession
	}
	hash := sha256Of(secret)
	sess, err := s.store.SessionByHash(ctx, hash)
	if errors.Is(err, registry.ErrNotFound) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, fmt.Errorf("session: %w", err)
	}
	if !s.now().Before(sess.ExpiresAt) {
		if err := s.store.DeleteSession(ctx, hash); err != nil {
			return User{}, fmt.Errorf("session: %w", err)
		}
		return User{}, ErrNoSession
	}
	u, err := s.store.UserByID(ctx, sess.UserID)
	if errors.Is(err, registry.ErrNotFound) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, fmt.Errorf("session: %w", err)
	}
	return u, nil
}

// Logout revokes a session.
func (s *Service) Logout(ctx context.Context, secret string) error {
	if secret == "" {
		return nil
	}
	if err := s.store.DeleteSession(ctx, sha256Of(secret)); err != nil {
		return fmt.Errorf("sign out: %w", err)
	}
	return nil
}

// SignOutEverywhere ends every web session of the user and revokes every
// token `loadout login` issued them, for when a device or a token may be
// lost. Machine tokens they made stay; maintainers revoke those. It
// returns how many sessions and tokens ended.
func (s *Service) SignOutEverywhere(ctx context.Context, userID string) (sessions, tokens int, err error) {
	if sessions, err = s.store.DeleteUserSessions(ctx, userID); err != nil {
		return 0, 0, fmt.Errorf("sign out everywhere: %w", err)
	}
	if tokens, err = s.reg.RevokeUserTokens(ctx, userID, ""); err != nil {
		return sessions, 0, fmt.Errorf("sign out everywhere: %w", err)
	}
	s.log.InfoContext(ctx, "signed out everywhere", "user_id", userID, "sessions", sessions, "tokens", tokens)
	return sessions, tokens, nil
}

// Memberships lists a user's memberships, sorted by namespace.
func (s *Service) Memberships(ctx context.Context, userID string) ([]Membership, error) {
	ms, err := s.store.UserMemberships(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("memberships of %s: %w", userID, err)
	}
	return ms, nil
}

// userForEmail returns the user with the address, creating one without a
// password or verification if there is none.
func (s *Service) userForEmail(ctx context.Context, email string) (User, error) {
	u, err := s.store.UserByEmail(ctx, email)
	if err == nil || !errors.Is(err, registry.ErrNotFound) {
		return u, err
	}
	id, err := randomHex(16)
	if err != nil {
		return User{}, err
	}
	u = User{ID: id, Email: email, CreatedAt: s.now().UTC()}
	err = s.store.CreateUser(ctx, u)
	if errors.Is(err, registry.ErrExists) {
		return s.store.UserByEmail(ctx, email) // created by a concurrent request
	}
	if err != nil {
		return User{}, err
	}
	s.log.InfoContext(ctx, "user created", "user_id", u.ID)
	return u, nil
}

// issue stores a new challenge for u and mails its code and link.
func (s *Service) issue(ctx context.Context, u User, purpose Purpose, pwHash string) (Pending, error) {
	now := s.now().UTC()
	if _, err := s.store.DeleteExpiredChallenges(ctx, now); err != nil {
		return Pending{}, fmt.Errorf("challenge: %w", err)
	}
	if _, err := s.store.DeleteCodeFailures(ctx, now.Add(-CodeFailureWindow)); err != nil {
		return Pending{}, fmt.Errorf("challenge: %w", err)
	}
	// Only the newest code works: a fresh code must not mean fresh guesses.
	if _, err := s.store.CancelChallenges(ctx, u.ID, purpose); err != nil {
		return Pending{}, fmt.Errorf("challenge: %w", err)
	}
	id, err := randomHex(16)
	if err != nil {
		return Pending{}, fmt.Errorf("challenge: %w", err)
	}
	code, err := sixDigits()
	if err != nil {
		return Pending{}, fmt.Errorf("challenge: %w", err)
	}
	link, err := randomToken()
	if err != nil {
		return Pending{}, fmt.Errorf("challenge: %w", err)
	}
	c := Challenge{
		ID:           id,
		UserID:       u.ID,
		Purpose:      purpose,
		CodeHash:     codeHash(id, code),
		LinkHash:     sha256Of(link),
		PasswordHash: pwHash,
		CreatedAt:    now,
		ExpiresAt:    now.Add(ChallengeTTL),
	}
	if err := s.store.CreateChallenge(ctx, c); err != nil {
		return Pending{}, fmt.Errorf("challenge: %w", err)
	}
	linkURL := s.publicURL + "/auth/verify?" + url.Values{"token": {link}}.Encode()
	s.send(ctx, codeMail(u.Email, purpose, code, linkURL))
	s.log.InfoContext(ctx, "code sent", "user_id", u.ID, "purpose", purpose, "challenge", id)
	return Pending{ChallengeID: id}, nil
}

// send hands a mail to the mailer. A failure is logged, never shown: the
// answer must not depend on whether a mail went out.
func (s *Service) send(ctx context.Context, m mail.Message) {
	if err := s.mailer.Send(ctx, m); err != nil {
		s.log.ErrorContext(ctx, "queue login email", "to", mail.Redact(m.To), "err", err)
	}
}

// usable reports whether a challenge may still be completed.
func (s *Service) usable(c Challenge) bool {
	return !c.Used && c.Attempts < MaxAttempts && s.now().Before(c.ExpiresAt) && c.Purpose.Valid()
}

// complete marks a challenge used (once, atomically), applies what it
// confirms and starts a session.
func (s *Service) complete(ctx context.Context, c Challenge) (SessionGrant, error) {
	err := s.store.UseChallenge(ctx, c.ID)
	if errors.Is(err, registry.ErrNotFound) {
		return SessionGrant{}, ErrInvalidCode // completed by a concurrent request
	}
	if err != nil {
		return SessionGrant{}, fmt.Errorf("complete challenge: %w", err)
	}
	u, err := s.store.UserByID(ctx, c.UserID)
	if errors.Is(err, registry.ErrNotFound) {
		return SessionGrant{}, ErrInvalidCode
	}
	if err != nil {
		return SessionGrant{}, fmt.Errorf("complete challenge: %w", err)
	}
	if c.Purpose == PurposeVerifySignup {
		// A sign-up only ever sets the password of an unverified account.
		// Once the address is confirmed (by another code, or Google), an
		// older sign-up code changes nothing.
		if u.Verified() {
			s.log.InfoContext(ctx, "sign-up code for a verified account refused", "user_id", u.ID, "challenge", c.ID)
			return SessionGrant{}, ErrInvalidCode
		}
		if err := s.store.SetPassword(ctx, u.ID, c.PasswordHash); err != nil {
			return SessionGrant{}, fmt.Errorf("complete sign-up: %w", err)
		}
		if err := s.store.MarkVerified(ctx, u.ID, s.now().UTC()); err != nil {
			return SessionGrant{}, fmt.Errorf("complete sign-up: %w", err)
		}
		if err := s.afterVerify(ctx, u.ID); err != nil {
			return SessionGrant{}, fmt.Errorf("complete sign-up: %w", err)
		}
		// The password changed: sessions started before are ended.
		if _, err := s.store.DeleteUserSessions(ctx, u.ID); err != nil {
			return SessionGrant{}, fmt.Errorf("complete sign-up: %w", err)
		}
		s.log.InfoContext(ctx, "email verified", "user_id", u.ID)
		if u, err = s.store.UserByID(ctx, u.ID); err != nil {
			return SessionGrant{}, fmt.Errorf("complete sign-up: %w", err)
		}
	}
	return s.newSession(ctx, u)
}

// afterVerify cancels the user's open sign-up codes once the address is
// confirmed, so none of them can set a password later.
func (s *Service) afterVerify(ctx context.Context, userID string) error {
	if _, err := s.store.CancelChallenges(ctx, userID, PurposeVerifySignup); err != nil {
		return err
	}
	return nil
}

func (s *Service) newSession(ctx context.Context, u User) (SessionGrant, error) {
	now := s.now().UTC()
	if _, err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		return SessionGrant{}, fmt.Errorf("new session: %w", err)
	}
	secret, err := randomToken()
	if err != nil {
		return SessionGrant{}, fmt.Errorf("new session: %w", err)
	}
	sess := Session{UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(SessionTTL)}
	if err := s.store.CreateSession(ctx, sess, sha256Of(secret)); err != nil {
		return SessionGrant{}, fmt.Errorf("new session: %w", err)
	}
	s.log.InfoContext(ctx, "signed in", "user_id", u.ID)
	return SessionGrant{Secret: secret, ExpiresAt: sess.ExpiresAt, User: u}, nil
}

// decoy answers like a started challenge that no code will ever complete.
func decoy() (Pending, error) {
	id, err := randomHex(16)
	if err != nil {
		return Pending{}, fmt.Errorf("challenge: %w", err)
	}
	return Pending{ChallengeID: id}, nil
}

// codeHash binds a code to its challenge, so equal codes of two challenges
// hash differently.
func codeHash(challengeID, code string) []byte {
	return sha256Of(challengeID + ":" + code)
}

func sha256Of(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func sixDigits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// randomToken returns 256 random bits, base64url.
func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// namespace validates a namespace name and returns the namespace.
func (s *Service) namespace(ctx context.Context, name string) (registry.Namespace, error) {
	if err := ident.Check("namespace", name); err != nil {
		return registry.Namespace{}, fmt.Errorf("%w: %w", registry.ErrInvalid, err)
	}
	return s.reg.Namespace(ctx, name)
}

// access is what actor may do in namespace; the operator may do anything.
// A user acting through a login token gets their role only inside the
// token's namespace and below, only with a publish token, and never as an
// admin: admin actions need the web or the registry host.
func (s *Service) access(ctx context.Context, actor Actor, namespace string) (Access, error) {
	if actor.Operator {
		return Access{Role: RoleAdmin, Governs: true}, nil
	}
	if actor.UserID == "" {
		return Access{}, nil
	}
	if !actor.viaToken {
		return s.roles.Access(ctx, actor.UserID, namespace)
	}
	if err := s.tokenCovers(ctx, actor, namespace); err != nil {
		return Access{}, err
	}
	return s.roles.accessAsMember(ctx, actor.UserID, namespace)
}

// tokenCovers checks that a login token may manage namespace: a publish
// token of namespace or of a namespace above it.
func (s *Service) tokenCovers(ctx context.Context, actor Actor, namespace string) error {
	switch {
	case namespace == "":
		return forbidden("this needs an admin signed in on the web, or the registry host; a login token cannot do it")
	case actor.tokenNS == registry.AccountNamespace:
		return nil // an account token manages what its user's roles allow
	case actor.tokenScope != registry.ScopePublish:
		return forbidden("managing " + namespace + " needs a publish token (a maintainer's); this login token of " + actor.tokenNS + " only reads")
	}
	ok, err := s.roles.within(ctx, namespace, actor.tokenNS)
	if err != nil {
		return err
	}
	if !ok {
		return forbidden(fmt.Sprintf("this login token is for %s only; run loadout login again to get one for your whole account", actor.tokenNS))
	}
	return nil
}
