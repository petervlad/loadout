// Package registry is the server side of loadout: it stores published
// package versions per namespace and decides who may read or publish them.
//
// Namespaces form a tree: a token reads its own namespace and every
// ancestor, and publishes only to its own.
//
// Storage is behind small interfaces, Store, TokenStore and NamespaceStore,
// so the same
// service runs on a LAN box (SQLite) and in the cloud (later: S3 + Postgres).
package registry

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// Errors callers match with errors.Is. The HTTP layer maps each to a status.
var (
	ErrNotFound        = errors.New("not found")
	ErrVersionConflict = errors.New("version conflict")
	ErrUnauthorized    = errors.New("missing or invalid token")
	ErrForbidden       = errors.New("token does not allow this")
	ErrInvalid         = errors.New("invalid request")
	// ErrExists means the thing being created (a token, a namespace) is
	// already there.
	ErrExists = errors.New("already exists")
)

// Release is one published version of a package.
type Release struct {
	ID          pack.ID
	Version     semver.Version
	Digest      pack.Digest
	Description string
	// Kind is what the package contains: agent, skill or kb.
	Kind pack.Kind
	// Size is the bundle size in bytes.
	Size        int
	PublishedAt time.Time
	// Seq orders releases across the whole registry; it only grows. Clients
	// use it to ask for changes since their last sync.
	Seq int64
	// Proposal is the id of the proposal this release was approved from,
	// empty for a direct publish.
	Proposal string
	// SelfApproved marks a release its author approved as owner, without
	// a second person's review.
	SelfApproved bool
	// PublishedBy is who released it: the publisher of a direct publish,
	// the approver of a release from a proposal. The zero Actor means
	// nobody is known, as for every direct publish from before it was kept.
	PublishedBy Actor
}

// Store keeps releases and their bundles. Implementations must be safe for
// concurrent use.
type Store interface {
	// PutRelease stores a release, its PublishedBy included, and its bundle
	// atomically and returns it with Seq set. It returns ErrVersionConflict unless the version is
	// higher than every release of the package; the check and the insert
	// are one atomic step, so concurrent publishes cannot both pass it.
	PutRelease(ctx context.Context, rel Release, bundle []byte) (Release, error)
	// Release returns one release and its bundle, or ErrNotFound.
	Release(ctx context.Context, id pack.ID, v semver.Version) (Release, []byte, error)
	// Releases returns every release of a package, lowest version first, or
	// ErrNotFound if the package has none.
	Releases(ctx context.Context, id pack.ID) ([]Release, error)
	// Packages returns the latest release of each package of a team, sorted
	// by package name.
	Packages(ctx context.Context, team string) ([]Release, error)
	// Changes returns up to limit releases of a team with Seq greater than
	// since, lowest Seq first.
	Changes(ctx context.Context, team string, since int64, limit int) ([]Release, error)
	// PackageFootprint returns what DeletePackage would remove, or
	// ErrNotFound when the package has no release and no proposal. It sits
	// on Store, not ProposalStore, because the subject is the package; both
	// implementations keep proposals in the same database.
	PackageFootprint(ctx context.Context, id pack.ID) (PackageFootprint, error)
	// DeletePackage removes every release and proposal of id, the
	// proposals' reviews and prompts, and clears id as any namespace's
	// course, and closes its open deletion requests as gone at at, in one
	// transaction, and returns what it removed. It returns ErrNotFound when
	// the package has no release and no proposal.
	DeletePackage(ctx context.Context, id pack.ID, at time.Time) (PackageFootprint, error)
}

// TokenScope is what a token allows.
type TokenScope string

// Token scopes. Publish includes read.
const (
	ScopeRead    TokenScope = "read"
	ScopePublish TokenScope = "publish"
)

// Valid reports whether s is a known scope.
func (s TokenScope) Valid() bool { return s == ScopeRead || s == ScopePublish }

// AccountNamespace is the namespace of an account token: a `loadout login`
// token that acts for its user in every namespace they belong to, with
// their role there at the time of each request.
const AccountNamespace = "*"

// Token describes an access token. The secret itself is never stored, only
// its hash.
type Token struct {
	ID        string
	Team      string
	Scope     TokenScope
	Label     string
	CreatedAt time.Time
	// UserID is set for a token issued to a user account by `loadout
	// login`. Such a token works only while the user is a member of Team,
	// or, for an account token (Team is AccountNamespace), wherever the
	// user is a member.
	UserID string
	// CreatedBy is the user who made a machine token (one without UserID,
	// for CI or a shared machine) through their account. Such a token counts
	// as that user in the four-eyes check. Empty for login tokens and for
	// tokens made on the registry host with `loadout token create`.
	CreatedBy string
	// ExpiresAt is when the token stops working; zero means never. Login
	// tokens expire after LoginTokenTTL, and `loadout login` gets a new one.
	ExpiresAt time.Time
}

// TokenStore keeps token records keyed by the hash of their secret.
// Implementations must be safe for concurrent use.
type TokenStore interface {
	// CreateToken stores t under hash, or returns ErrExists if the id or
	// the hash is taken.
	CreateToken(ctx context.Context, t Token, hash []byte) error
	// TokenByHash returns the token with that hash, or ErrNotFound.
	TokenByHash(ctx context.Context, hash []byte) (Token, error)
	// Tokens lists a team's tokens, oldest first.
	Tokens(ctx context.Context, team string) ([]Token, error)
	// RevokeToken deletes a token by id, or returns ErrNotFound.
	RevokeToken(ctx context.Context, id string) error
	// RevokeUserTokens deletes the tokens issued to a user for namespace
	// team, or in every namespace when team is empty, and returns how many
	// it deleted.
	RevokeUserTokens(ctx context.Context, userID, team string) (int, error)
}

// Memberships tells which users belong to which namespace. The registry
// asks it on every request made with a token issued to a user.
type Memberships interface {
	// MemberScope returns the user's role in namespace, and false if the
	// user is not a member (or does not exist).
	MemberScope(ctx context.Context, userID, namespace string) (TokenScope, bool, error)
}

// Owners tells whether a user owns a namespace through their memberships:
// an owner of it or of a project above it. The admin flag does not count.
type Owners interface {
	Owns(ctx context.Context, userID, namespace string) (bool, error)
}

// Principal is who a request acts as: one namespace, with one scope.
//
// An account token's principal starts with no namespace (Account is true,
// Team empty) and is narrowed to one with Service.In, from the namespace
// the request is about.
type Principal struct {
	TokenID string
	// Team is the namespace the token was issued for, or the one an
	// account principal was narrowed to.
	Team  string
	Scope TokenScope
	// Layers is Team followed by its ancestors, closest first, as resolved
	// when the token was authenticated. Empty means Team alone.
	Layers []Layer
	// Label is the token's label. It is informational: anyone who can
	// create tokens chooses it, so rules key on TokenID.
	Label string
	// UserID is the user a login token was issued to, empty for other
	// tokens.
	UserID string
	// CreatedBy is the user who made a machine token through their
	// account; see Token.CreatedBy.
	CreatedBy string
	// Account marks a principal of an account token.
	Account bool
}

// Narrowed reports whether p acts in one namespace: always for a
// namespace token, after Service.In for an account token.
func (p Principal) Narrowed() bool { return p.Team != "" }

// Machine reports whether p acts through a machine token a user made: CI
// or a shared machine, never a person deciding.
func (p Principal) Machine() bool { return p.UserID == "" && p.CreatedBy != "" }

// layers returns the namespaces p reads, closest first.
func (p Principal) layers() []Layer {
	if len(p.Layers) == 0 {
		return []Layer{{Namespace: p.Team, Kind: KindTeam}}
	}
	return slices.Clone(p.Layers)
}

// Depth returns how far namespace ns is from p's own namespace (0 for its
// own, 1 for the parent, ...), and false if p cannot read it.
func (p Principal) Depth(ns string) (int, bool) {
	for _, l := range p.layers() {
		if l.Namespace == ns {
			return l.Depth, true
		}
	}
	return 0, false
}

// CanRead reports whether p may read ns's packages: its own namespace and
// every ancestor.
func (p Principal) CanRead(ns string) bool {
	if !p.Narrowed() {
		return false
	}
	_, ok := p.Depth(ns)
	return ok && p.Scope.Valid()
}

// CanPublish reports whether p may publish to ns: only its own namespace,
// never an ancestor.
func (p Principal) CanPublish(ns string) bool {
	return p.Narrowed() && p.Team == ns && p.Scope == ScopePublish
}
