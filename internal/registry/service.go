package registry

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// TokenPrefix starts every token secret, so leaked tokens are easy to spot
// and scan for.
const TokenPrefix = "ldt_"

// maxChanges caps one Changes response.
const maxChanges = 500

// LoginTokenTTL is how long a token from `loadout login` works. Signing in
// again issues a fresh one.
const LoginTokenTTL = 90 * 24 * time.Hour

// Service implements the registry's rules on top of its stores.
type Service struct {
	store      Store
	tokens     TokenStore
	namespaces NamespaceStore
	members    Memberships
	// owners, if set, lets Publish tell an owner refused by review how to
	// release anyway.
	owners Owners
	now    func() time.Time
	log    *slog.Logger
}

// Option configures a Service.
type Option func(*Service)

// WithMemberships checks tokens issued to users against m on every request.
// Without it, a user token is refused.
func WithMemberships(m Memberships) Option { return func(s *Service) { s.members = m } }

// WithOwnerHints makes the refusal to publish into a namespace that
// requires review tell its owners they may propose and approve as owner.
func WithOwnerHints(o Owners) Option { return func(s *Service) { s.owners = o } }

// NewService returns a registry service. now is time.Now in production.
func NewService(store Store, tokens TokenStore, namespaces NamespaceStore, now func() time.Time, log *slog.Logger, opts ...Option) *Service {
	s := &Service{store: store, tokens: tokens, namespaces: namespaces, now: now, log: log}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateToken makes a new token for namespace team and returns its secret,
// which is shown once and never stored. A namespace that does not exist yet
// is created as a root team.
func (s *Service) CreateToken(ctx context.Context, team string, scope TokenScope, label string) (string, Token, error) {
	return s.CreateUserToken(ctx, team, scope, label, "")
}

// CreateUserToken is CreateToken for a token issued to a user account.
// The token stops working when the user leaves the namespace, and after
// LoginTokenTTL.
func (s *Service) CreateUserToken(ctx context.Context, team string, scope TokenScope, label, userID string) (string, Token, error) {
	t := Token{Team: team, Scope: scope, Label: label, UserID: userID}
	if userID != "" {
		t.ExpiresAt = s.now().UTC().Add(LoginTokenTTL)
	}
	return s.createToken(ctx, t)
}

// CreateAccountToken makes a `loadout login` token for user userID that
// acts in every namespace the user belongs to (see AccountNamespace). It
// expires after LoginTokenTTL.
func (s *Service) CreateAccountToken(ctx context.Context, label, userID string) (string, Token, error) {
	if userID == "" {
		return "", Token{}, fmt.Errorf("%w: an account token needs a user", ErrInvalid)
	}
	return s.createToken(ctx, Token{Team: AccountNamespace, Scope: ScopeRead, Label: label, UserID: userID, ExpiresAt: s.now().UTC().Add(LoginTokenTTL)})
}

// CreateMachineToken is CreateToken for a machine token (CI, a shared
// machine) that user createdBy made through their account. It never acts
// for a person, but four eyes treat it as its creator.
func (s *Service) CreateMachineToken(ctx context.Context, team string, scope TokenScope, label, createdBy string) (string, Token, error) {
	return s.createToken(ctx, Token{Team: team, Scope: scope, Label: label, CreatedBy: createdBy})
}

func (s *Service) createToken(ctx context.Context, t Token) (string, Token, error) {
	account := t.Team == AccountNamespace && t.UserID != ""
	if !account {
		if err := ident.Check("team", t.Team); err != nil {
			return "", Token{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	if !t.Scope.Valid() {
		return "", Token{}, fmt.Errorf("%w: scope %q: want read or publish", ErrInvalid, t.Scope)
	}
	if !account {
		if err := s.ensureNamespace(ctx, t.Team); err != nil {
			return "", Token{}, fmt.Errorf("create token: %w", err)
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Token{}, fmt.Errorf("create token: %w", err)
	}
	secret := TokenPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	idRaw := make([]byte, 6)
	if _, err := rand.Read(idRaw); err != nil {
		return "", Token{}, fmt.Errorf("create token: %w", err)
	}
	t.ID = fmt.Sprintf("%x", idRaw)
	t.CreatedAt = s.now().UTC()
	if err := s.tokens.CreateToken(ctx, t, hashSecret(secret)); err != nil {
		return "", Token{}, fmt.Errorf("create token: %w", err)
	}
	s.log.Info("token created", "token_id", t.ID, "team", t.Team, "scope", t.Scope, "label", t.Label, "user_id", t.UserID, "created_by", t.CreatedBy)
	return secret, t, nil
}

// Authenticate resolves a token secret to the principal it acts as.
func (s *Service) Authenticate(ctx context.Context, secret string) (Principal, error) {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return Principal{}, ErrUnauthorized
	}
	t, err := s.tokens.TokenByHash(ctx, hashSecret(secret))
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate: %w", err)
	}
	if !t.ExpiresAt.IsZero() && !s.now().Before(t.ExpiresAt) {
		return Principal{}, ErrUnauthorized
	}
	if t.Team == AccountNamespace {
		if t.UserID == "" || s.members == nil {
			return Principal{}, ErrUnauthorized
		}
		// The namespace, and the user's role in it, come with the request.
		return Principal{TokenID: t.ID, Label: t.Label, UserID: t.UserID, Account: true}, nil
	}
	scope := t.Scope
	if t.UserID != "" {
		// A login token is only as good as the membership behind it: gone
		// means refused, downgraded to read means read.
		if s.members == nil {
			return Principal{}, ErrUnauthorized
		}
		role, ok, err := s.members.MemberScope(ctx, t.UserID, t.Team)
		if err != nil {
			return Principal{}, fmt.Errorf("authenticate: %w", err)
		}
		if !ok {
			return Principal{}, ErrUnauthorized
		}
		if role == ScopeRead {
			scope = ScopeRead
		}
	}
	layers, err := s.readChain(ctx, t.Team)
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate: %w", err)
	}
	return Principal{TokenID: t.ID, Team: t.Team, Scope: scope, Layers: layers, Label: t.Label, UserID: t.UserID, CreatedBy: t.CreatedBy}, nil
}

// In narrows an account principal to namespace ns: the user's role there
// now, and ns's chain of layers. A namespace token, or an empty ns, leaves
// pr as it is. A user who is not a member of ns is refused.
func (s *Service) In(ctx context.Context, pr Principal, ns string) (Principal, error) {
	if !pr.Account || ns == "" || ns == pr.Team {
		return pr, nil
	}
	if err := ident.Check("namespace", ns); err != nil {
		return Principal{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if s.members == nil {
		return Principal{}, ErrUnauthorized
	}
	scope, ok, err := s.members.MemberScope(ctx, pr.UserID, ns)
	if err != nil {
		return Principal{}, fmt.Errorf("access to %s: %w", ns, err)
	}
	if !ok {
		return Principal{}, fmt.Errorf("%w: you are not a member of %s", ErrForbidden, ns)
	}
	layers, err := s.readChain(ctx, ns)
	if err != nil {
		return Principal{}, fmt.Errorf("access to %s: %w", ns, err)
	}
	pr.Team, pr.Scope, pr.Layers = ns, scope, layers
	return pr, nil
}

// reading returns pr narrowed so it can read ns, when pr is an account
// principal that cannot yet: an operation on one package or proposal acts
// in that package's namespace, whatever view the request named.
func (s *Service) reading(ctx context.Context, pr Principal, ns string) (Principal, error) {
	if !pr.Account || pr.CanRead(ns) {
		return pr, nil
	}
	pr.Team = ""
	return s.In(ctx, pr, ns)
}

// errNoView is the error for a listing an account principal asked for
// without saying which namespace it is about.
var errNoView = fmt.Errorf("%w: this token is for your whole account; say which namespace (the X-Loadout-Namespace header, or update loadout)", ErrInvalid)

// Publish stores a new version from bundle bytes. The version must be higher
// than every published version of the package.
func (s *Service) Publish(ctx context.Context, pr Principal, data []byte) (Release, error) {
	p, err := bundle.Decode(data)
	if err != nil {
		return Release{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if pr.Account && pr.Team != p.ID().Team() {
		pr.Team = ""
		if pr, err = s.In(ctx, pr, p.ID().Team()); err != nil {
			return Release{}, err
		}
	}
	if !pr.CanPublish(p.ID().Team()) {
		if pr.Account {
			return Release{}, fmt.Errorf("%w: publishing to %s needs the maintainer role there", ErrForbidden, p.ID().Team())
		}
		return Release{}, fmt.Errorf("%w: publish to namespace %s (a token publishes only to its own namespace, %s)", ErrForbidden, p.ID().Team(), pr.Team)
	}
	ns, err := s.namespaces.Namespace(ctx, p.ID().Team())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Release{}, fmt.Errorf("publish %s: %w", p.ID(), err)
	}
	if ns.RequireReview {
		err := fmt.Errorf("%w: namespace %s requires review; propose the version instead (loadout propose)", ErrForbidden, ns.Name)
		if s.ownsForHint(ctx, pr, ns.Name) {
			err = fmt.Errorf("%w, then, as an owner of %s, approve it yourself: loadout review approve ID --as-owner --note WHY", err, ns.Name)
		}
		return Release{}, err
	}

	if err := (checker{store: s.store, namespaces: s.namespaces}).check(ctx, p); err != nil {
		return Release{}, err
	}

	// Checked early for a clear message; PutRelease checks again atomically.
	existing, err := s.store.Releases(ctx, p.ID())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Release{}, fmt.Errorf("publish %s: %w", p.ID(), err)
	}
	if n := len(existing); n > 0 {
		if latest := existing[n-1].Version; p.Version().Compare(latest) <= 0 {
			return Release{}, fmt.Errorf("%w: %s@%s must be higher than the latest version %s", ErrVersionConflict, p.ID(), p.Version(), latest)
		}
	}

	rel, err := s.store.PutRelease(ctx, Release{
		ID:          p.ID(),
		Version:     p.Version(),
		Digest:      p.Digest(),
		Description: p.Manifest().Description,
		Kind:        p.Kind(),
		Size:        len(data),
		PublishedAt: s.now().UTC(),
		PublishedBy: actorOf(pr),
	}, data)
	if err != nil {
		return Release{}, fmt.Errorf("publish %s@%s: %w", p.ID(), p.Version(), err)
	}
	s.log.Info("published", "id", rel.ID, "version", rel.Version, "digest", rel.Digest.Short(), "seq", rel.Seq, "token_id", pr.TokenID)
	return rel, nil
}

// ownsForHint reports whether pr is a person who owns ns. It only adds a
// hint to a refusal, so a failed lookup means no hint rather than a
// different error.
func (s *Service) ownsForHint(ctx context.Context, pr Principal, ns string) bool {
	if s.owners == nil || pr.UserID == "" {
		return false
	}
	owns, err := s.owners.Owns(ctx, pr.UserID, ns)
	if err != nil {
		s.log.Warn("ownership for a hint", "namespace", ns, "err", err)
	}
	return err == nil && owns
}

// Fetch returns one release and its bundle. version is a version number or
// "latest".
func (s *Service) Fetch(ctx context.Context, pr Principal, id pack.ID, version string) (Release, []byte, error) {
	pr, err := s.reading(ctx, pr, id.Team())
	if err != nil {
		return Release{}, nil, err
	}
	if !pr.CanRead(id.Team()) {
		return Release{}, nil, fmt.Errorf("%w: read namespace %s", ErrForbidden, id.Team())
	}
	var v semver.Version
	if version == "latest" {
		rels, err := s.store.Releases(ctx, id)
		if err != nil {
			return Release{}, nil, fmt.Errorf("fetch %s: %w", id, err)
		}
		if len(rels) == 0 { // a Store must return ErrNotFound; do not trust it to
			return Release{}, nil, fmt.Errorf("fetch %s: %w", id, ErrNotFound)
		}
		v = rels[len(rels)-1].Version
	} else {
		parsed, err := semver.Parse(version)
		if err != nil {
			return Release{}, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		v = parsed
	}
	rel, data, err := s.store.Release(ctx, id, v)
	if err != nil {
		return Release{}, nil, fmt.Errorf("fetch %s@%s: %w", id, v, err)
	}
	return rel, data, nil
}

// Releases lists every version of a package, lowest first.
func (s *Service) Releases(ctx context.Context, pr Principal, id pack.ID) ([]Release, error) {
	pr, err := s.reading(ctx, pr, id.Team())
	if err != nil {
		return nil, err
	}
	if !pr.CanRead(id.Team()) {
		return nil, fmt.Errorf("%w: read namespace %s", ErrForbidden, id.Team())
	}
	rels, err := s.store.Releases(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("releases of %s: %w", id, err)
	}
	return rels, nil
}

// Packages lists the latest release of each package the principal can
// read: its own namespace first, then each ancestor, by name within one.
func (s *Service) Packages(ctx context.Context, pr Principal) ([]Release, error) {
	if !pr.Narrowed() {
		return nil, errNoView
	}
	var out []Release
	for _, l := range pr.layers() {
		rels, err := s.store.Packages(ctx, l.Namespace)
		if err != nil {
			return nil, fmt.Errorf("packages of %s: %w", l.Namespace, err)
		}
		out = append(out, rels...)
	}
	return out, nil
}

// Changes lists releases in the principal's namespaces published after seq
// since, lowest Seq first. Seq is global, so asking each namespace for its
// first maxChanges and keeping the lowest maxChanges overall is exact.
func (s *Service) Changes(ctx context.Context, pr Principal, since int64) ([]Release, error) {
	if !pr.Narrowed() {
		return nil, errNoView
	}
	var out []Release
	for _, l := range pr.layers() {
		rels, err := s.store.Changes(ctx, l.Namespace, since, maxChanges)
		if err != nil {
			return nil, fmt.Errorf("changes of %s: %w", l.Namespace, err)
		}
		out = append(out, rels...)
	}
	slices.SortFunc(out, func(a, b Release) int { return cmp.Compare(a.Seq, b.Seq) })
	if len(out) > maxChanges {
		out = out[:maxChanges]
	}
	return out, nil
}

func hashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Tokens lists a team's tokens, without secrets.
func (s *Service) Tokens(ctx context.Context, team string) ([]Token, error) {
	toks, err := s.tokens.Tokens(ctx, team)
	if err != nil {
		return nil, fmt.Errorf("tokens of %s: %w", team, err)
	}
	return toks, nil
}

// RevokeToken deletes a token; requests using it fail from then on.
func (s *Service) RevokeToken(ctx context.Context, id string) error {
	if err := s.tokens.RevokeToken(ctx, id); err != nil {
		return fmt.Errorf("revoke token %s: %w", id, err)
	}
	s.log.Info("token revoked", "token_id", id)
	return nil
}

// RevokeUserTokens deletes the tokens issued to a user for namespace team,
// or in every namespace when team is empty.
func (s *Service) RevokeUserTokens(ctx context.Context, userID, team string) (int, error) {
	n, err := s.tokens.RevokeUserTokens(ctx, userID, team)
	if err != nil {
		return 0, fmt.Errorf("revoke tokens of user %s: %w", userID, err)
	}
	if n > 0 {
		s.log.Info("user tokens revoked", "user_id", userID, "team", team, "count", n)
	}
	return n, nil
}
