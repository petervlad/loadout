// Package account adds user accounts to the registry: people sign in on the
// web (email and password plus an emailed code, or Google), get roles in
// namespaces (see Role), and `loadout login` turns their role into a normal
// namespace token through the device flow.
//
// Login is only about identity. Namespace tokens keep working exactly as
// before; a token issued to a user additionally records the user, and the
// registry refuses it once the membership is gone.
package account

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

// Lifetimes and limits.
const (
	SessionTTL   = 30 * 24 * time.Hour
	ChallengeTTL = 15 * time.Minute
	// MaxAttempts is how many codes may be tried against one challenge.
	MaxAttempts = 5
	// MaxCodeFailures is how many wrong codes one user may enter for one
	// purpose within CodeFailureWindow, across all their codes, before
	// code entry is locked for them.
	MaxCodeFailures   = 10
	CodeFailureWindow = 24 * time.Hour
	maxEmailLen       = 254
)

// Errors callers match with errors.Is. Store implementations use
// registry.ErrNotFound and registry.ErrExists.
var (
	// ErrInvalidEmail means the text is not a plain email address.
	ErrInvalidEmail = errors.New("not a valid email address")
	// ErrInvalidCode covers every way a code or link can fail (wrong,
	// expired, used, too many attempts, unknown), so the answer never
	// tells which.
	ErrInvalidCode = errors.New("the code or link is invalid or has expired")
	// ErrCodeLocked means too many wrong codes were entered for the
	// account lately; codes are refused until the oldest ones age out.
	ErrCodeLocked = errors.New("too many wrong codes were entered for this account; wait up to 24 hours, or use the link in the email")
	// ErrNoSession means the session is missing, expired or revoked.
	ErrNoSession = errors.New("not signed in")
	// ErrEmailDisabled means no mailer is configured, so email sign-up and
	// sign-in are off.
	ErrEmailDisabled = errors.New("email sign-in is not enabled on this registry")
	// ErrGoogleEmailUnverified means Google did not vouch for the address.
	ErrGoogleEmailUnverified = errors.New("google has not verified this email address")
	// ErrGoogleConflict means the address belongs to a user linked to a
	// different Google account.
	ErrGoogleConflict = errors.New("this email is linked to another google account")
	// ErrNotMember means the user has no role in the namespace.
	ErrNotMember = errors.New("not a member of this namespace")
)

// User is one person. Email is unique, trimmed and lowercased.
type User struct {
	ID    string
	Email string
	Name  string
	// PasswordHash is an argon2id PHC string, empty without a password.
	PasswordHash string
	// GoogleSubject is Google's id for the person, empty if not linked.
	GoogleSubject string
	// EmailVerifiedAt is zero until the address is confirmed.
	EmailVerifiedAt time.Time
	CreatedAt       time.Time
	// IsAdmin makes the user an admin of the whole registry.
	IsAdmin bool
}

// Verified reports whether the user confirmed their address.
func (u User) Verified() bool { return !u.EmailVerifiedAt.IsZero() }

// Membership gives a user a role in a namespace: member, maintainer, or
// (on a project) owner.
type Membership struct {
	UserID string
	// Email is filled in by the list methods, for display.
	Email     string
	Namespace string
	Role      Role
	CreatedAt time.Time
}

// Session is a signed-in browser. Its id is a secret; stores keep only its
// hash.
type Session struct {
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Purpose says what an emailed code completes. It is stored, so it is a
// string.
type Purpose string

// Purposes.
const (
	PurposeVerifySignup Purpose = "verify-signup"
	PurposeLogin        Purpose = "login"
)

// Valid reports whether p is a known purpose.
func (p Purpose) Valid() bool { return p == PurposeVerifySignup || p == PurposeLogin }

// Challenge is one emailed code and the magic link sent with it. Both are
// stored hashed; either completes the challenge once.
type Challenge struct {
	ID       string
	UserID   string
	Purpose  Purpose
	CodeHash []byte
	LinkHash []byte
	// PasswordHash is the password a signup sets when it is verified.
	PasswordHash string
	Attempts     int
	Used         bool
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// UserStore keeps users.
type UserStore interface {
	// CreateUser stores u, or returns registry.ErrExists if its id, email
	// or Google subject is taken.
	CreateUser(ctx context.Context, u User) error
	// UserByID, UserByEmail and UserByGoogle return one user, or
	// registry.ErrNotFound.
	UserByID(ctx context.Context, id string) (User, error)
	UserByEmail(ctx context.Context, email string) (User, error)
	UserByGoogle(ctx context.Context, subject string) (User, error)
	// The setters change one thing about a user and nothing else, so two
	// changes at once never undo each other. Each returns
	// registry.ErrNotFound for an unknown id.
	//
	// SetPassword sets the password hash.
	SetPassword(ctx context.Context, id, hash string) error
	// MarkVerified records that the address was confirmed at at, unless it
	// already was.
	MarkVerified(ctx context.Context, id string, at time.Time) error
	// LinkGoogle links a Google subject, and sets the name if the user has
	// none. It returns registry.ErrExists if the subject belongs to another
	// user or the user is linked to another subject.
	LinkGoogle(ctx context.Context, id, subject, name string) error
	// SetAdmin sets or clears the admin flag.
	SetAdmin(ctx context.Context, id string, on bool) error
	// DeleteUser removes a user with their memberships, sessions and
	// challenges, or returns registry.ErrNotFound.
	DeleteUser(ctx context.Context, id string) error
	// Admins lists the users with the admin flag, sorted by email.
	Admins(ctx context.Context) ([]User, error)
}

// MemberStore keeps memberships.
type MemberStore interface {
	// PutMember adds a membership or changes its role. It returns
	// registry.ErrNotFound if the user or the namespace does not exist.
	PutMember(ctx context.Context, m Membership) error
	// PutMembers puts every membership of ms like PutMember, all or none:
	// if one fails (an unknown user or namespace), none is written.
	PutMembers(ctx context.Context, ms []Membership) error
	// RemoveMember deletes a membership, or returns registry.ErrNotFound.
	RemoveMember(ctx context.Context, userID, namespace string) error
	// Membership returns one membership, or registry.ErrNotFound.
	Membership(ctx context.Context, userID, namespace string) (Membership, error)
	// Members lists a namespace's members sorted by email, with Email set.
	Members(ctx context.Context, namespace string) ([]Membership, error)
	// UserMemberships lists a user's memberships sorted by namespace, with
	// Email set.
	UserMemberships(ctx context.Context, userID string) ([]Membership, error)
}

// SessionStore keeps web sessions by the hash of their id.
type SessionStore interface {
	// CreateSession stores s under hash, or returns registry.ErrExists.
	CreateSession(ctx context.Context, s Session, hash []byte) error
	// SessionByHash returns a session, expired or not, or
	// registry.ErrNotFound.
	SessionByHash(ctx context.Context, hash []byte) (Session, error)
	// DeleteSession removes a session; a missing one is not an error.
	DeleteSession(ctx context.Context, hash []byte) error
	// DeleteExpiredSessions removes sessions that expired before now.
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error)
	// DeleteUserSessions removes every session of the user and returns
	// how many.
	DeleteUserSessions(ctx context.Context, userID string) (int, error)
}

// ChallengeStore keeps emailed codes.
type ChallengeStore interface {
	// CreateChallenge stores c, or returns registry.ErrExists.
	CreateChallenge(ctx context.Context, c Challenge) error
	// Challenge returns a challenge by id, or registry.ErrNotFound.
	Challenge(ctx context.Context, id string) (Challenge, error)
	// ChallengeByLink returns the challenge whose link has that hash, or
	// registry.ErrNotFound.
	ChallengeByLink(ctx context.Context, linkHash []byte) (Challenge, error)
	// AddAttempt counts one code attempt and returns the new count, in one
	// atomic step so parallel guesses cannot exceed the limit. It returns
	// registry.ErrNotFound for an unknown id.
	AddAttempt(ctx context.Context, id string) (int, error)
	// UseChallenge marks a challenge used. It returns registry.ErrNotFound
	// if it does not exist or was used already, in one atomic step, so a
	// challenge completes at most once.
	UseChallenge(ctx context.Context, id string) error
	// DeleteExpiredChallenges removes challenges that expired before now.
	DeleteExpiredChallenges(ctx context.Context, now time.Time) (int, error)
	// CancelChallenges marks the user's open challenges of purpose used,
	// or of every purpose when purpose is empty, and returns how many.
	CancelChallenges(ctx context.Context, userID string, purpose Purpose) (int, error)
	// AddCodeFailure records one wrong code for the user and purpose.
	AddCodeFailure(ctx context.Context, userID string, purpose Purpose, at time.Time) error
	// CodeFailures counts the user's wrong codes for purpose since since.
	CodeFailures(ctx context.Context, userID string, purpose Purpose, since time.Time) (int, error)
	// DeleteCodeFailures removes wrong codes recorded before before.
	DeleteCodeFailures(ctx context.Context, before time.Time) (int, error)
}

// Store is everything the account service keeps. Implementations must be
// safe for concurrent use.
type Store interface {
	UserStore
	MemberStore
	SessionStore
	ChallengeStore
}

// NormalizeEmail trims and lowercases an address and checks that it is a
// plain address such as alice@example.com.
func NormalizeEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	if e == "" || len(e) > maxEmailLen || strings.ContainsAny(e, " \t\r\n<>\",;") {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e {
		return "", ErrInvalidEmail
	}
	local, domain, ok := strings.Cut(e, "@")
	if !ok || local == "" || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidEmail
	}
	return e, nil
}

// Actor is who asks for a change: the operator at the registry host (with
// the data folder, allowed everything), a user signed in on the web, or a
// user acting through a `loadout login` token.
type Actor struct {
	UserID   string
	Operator bool
	// viaToken marks a user acting through a login token of tokenNS with
	// tokenScope. Such a user manages only tokenNS and the namespaces
	// below it, needs a publish token, and never gets admin rights.
	viaToken   bool
	tokenNS    string
	tokenScope registry.TokenScope
}

// Operator is the actor of the admin commands run on the registry host.
func Operator() Actor { return Actor{Operator: true} }

// UserActor is a user signed in on the web.
func UserActor(userID string) Actor { return Actor{UserID: userID} }

// TokenActor is a user acting through a `loadout login` token issued for
// namespace, with the token's scope as it is now.
func TokenActor(userID, namespace string, scope registry.TokenScope) Actor {
	return Actor{UserID: userID, viaToken: true, tokenNS: namespace, tokenScope: scope}
}

// WholeAccountActor is a user acting through an account token of `loadout
// login`: what their roles allow in each namespace, never admin rights.
func WholeAccountActor(userID string) Actor {
	return Actor{UserID: userID, viaToken: true, tokenNS: registry.AccountNamespace}
}

// forbidden is the error for a denied change.
func forbidden(what string) error {
	return fmt.Errorf("%w: %s", registry.ErrForbidden, what)
}
