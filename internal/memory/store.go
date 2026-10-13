package memory

import (
	"context"
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

// The sentinels are the registry's, so server.writeError maps their statuses
// unchanged: ErrNotFound is 404, ErrInvalid 400, ErrExists, ErrStale 409 and
// ErrUnauthorized 401.
var (
	// ErrStale means an update was based on a version that is no longer the
	// newest.
	ErrStale = registry.ErrStale
	// ErrUnauthorized means a memory token is missing, wrong or expired.
	ErrUnauthorized = registry.ErrUnauthorized
)

// Query is a search over the entries of some namespaces.
type Query struct {
	Text string // user text: only its Words are used
	// Namespaces to search. Empty matches nothing, so a caller that forgot
	// its layers reads nothing rather than everything.
	Namespaces []string
	Type       Type // empty matches every type
	Limit      int  // at most this many, best first
}

// Store keeps entries and their history. The service holds the rules (rights,
// the switch, limits, duplicate check); a store only keeps data and answers
// the questions below. Every method takes the time it needs, so a store reads
// no clock. A missing entry is registry.ErrNotFound.
//
// Search, Candidates, Recent and Index see active entries only.
type Store interface {
	// Insert saves a new entry as version 1 with action save. created,
	// updated and confirmed are all at. A link to an entry that does not
	// exist is registry.ErrInvalid.
	// With Within the daily write count and the insert happen in one
	// transaction, so requests that arrive together cannot all pass the
	// limit. Past it the error is a *WriteLimitError and nothing is saved.
	Insert(ctx context.Context, d Draft, by Caller, at time.Time, opts ...WriteOption) (Entry, error)
	// Get returns an entry, retracted or not. It does not count a use.
	Get(ctx context.Context, id int64) (Entry, error)
	// GetMany returns the entries that exist, in the order of ids.
	GetMany(ctx context.Context, ids []int64) ([]Entry, error)
	// CountUse adds one use to each entry. Unknown ids are ignored.
	CountUse(ctx context.Context, ids ...int64) error
	// History returns every version, oldest first.
	History(ctx context.Context, id int64) ([]Version, error)

	// Update writes the next version. base is the version the caller read:
	// if the entry has moved on, it is ErrStale and nothing changes. A
	// retracted entry is registry.ErrInvalid.
	// Within works as it does for Insert.
	Update(ctx context.Context, id int64, base int, r Revision, by Caller, at time.Time, opts ...WriteOption) (Entry, error)
	// Retract hides an entry and records the reason. Retracting a retracted
	// entry changes nothing and returns it.
	Retract(ctx context.Context, id int64, reason string, by Caller, at time.Time) (Entry, error)
	// Move changes the namespace and keeps the id, author and history. To the
	// namespace it is already in, or a retracted entry, is registry.ErrInvalid.
	Move(ctx context.Context, id int64, to string, by Caller, at time.Time) (Entry, error)
	// Purge removes an entry, its versions and its links for good and records
	// that it happened.
	Purge(ctx context.Context, id int64, at time.Time) (Purged, error)
	// Purges lists the records Purge made, oldest first.
	Purges(ctx context.Context) ([]Purged, error)
	// Scrub does again what Purge does after its delete: it merges the
	// search index and empties the write-ahead log, so deleted text leaves
	// the files. It can be run any number of times. A store that keeps no
	// files does nothing.
	Scrub(ctx context.Context) error
	// RemoveNamespace deletes every entry of a namespace with its versions
	// and links for good, and says how many there were. It is for a
	// namespace that is itself deleted, so it leaves no purge record. A
	// namespace with no entries is not an error.
	RemoveNamespace(ctx context.Context, namespace string) (Removed, error)

	// Search finds entries by the words of the query: all of them, and if
	// none match, any of them. Best match first.
	Search(ctx context.Context, q Query) ([]Entry, error)
	// Candidates returns up to limit entries matching any of the words, for
	// the duplicate check, best first.
	Candidates(ctx context.Context, namespaces []string, words []string, limit int) ([]Entry, error)
	// Recent returns entries whose last version is at or after since, newest
	// first.
	Recent(ctx context.Context, namespaces []string, since time.Time, limit int) ([]Entry, error)
	// Index returns up to limit entries for the index and the number of
	// active entries there are. Order: updated within IndexFresh of now
	// first, newest first, then most used, then most recently updated.
	Index(ctx context.Context, namespaces []string, now time.Time, limit int) ([]Entry, int, error)
	// WritesSince counts the save and update versions a person wrote after
	// since in every namespace (a retract, move or confirm is not a write),
	// and when the oldest of them was made (zero if none).
	WritesSince(ctx context.Context, userID string, since time.Time) (Writes, error)
}

// Removed is what RemoveNamespace deleted. It holds counts only.
type Removed struct {
	Entries  int
	Versions int
	Links    int // links from or to a removed entry
}

// WriteLimit is at most Max writes by the author since Since.
type WriteLimit struct {
	Since time.Time
	Max   int
}

// WriteOption adjusts an Insert or an Update.
type WriteOption func(*WriteLimit)

// Within makes a store refuse the write, inside the transaction that makes
// it, when the author already has limit writes after since.
func Within(since time.Time, limit int) WriteOption {
	return func(l *WriteLimit) { *l = WriteLimit{Since: since, Max: limit} }
}

// LimitOf reads the limit the options set, if any. Stores use it.
func LimitOf(opts []WriteOption) (WriteLimit, bool) {
	var l WriteLimit
	for _, o := range opts {
		o(&l)
	}
	return l, l.Max > 0
}

// WriteLimitError is the error of a write refused by Within. Oldest is when the
// oldest write in the window was made.
type WriteLimitError struct{ Oldest time.Time }

func (*WriteLimitError) Error() string { return "daily write limit reached" }

// Writes is the count behind the daily limit.
type Writes struct {
	Count  int
	Oldest time.Time
}

// Token is a memory token without its secret.
type Token struct {
	ID        string
	UserID    string
	Label     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// TokenStore keeps memory tokens by the hash of their secret. Whether a token
// has expired is for the caller: ByHash returns it either way.
type TokenStore interface {
	// Create stores a token. A repeated id or hash is registry.ErrExists.
	Create(ctx context.Context, t Token, hash []byte) error
	// ByHash finds the token for a secret's hash, or registry.ErrNotFound.
	ByHash(ctx context.Context, hash []byte) (Token, error)
	// List returns a person's tokens, newest first.
	List(ctx context.Context, userID string) ([]Token, error)
	// Revoke removes one of a person's own tokens. Another person's token is
	// registry.ErrNotFound.
	Revoke(ctx context.Context, userID, id string) error
	// RevokeAll removes every token of a person and says how many.
	RevokeAll(ctx context.Context, userID string) (int, error)
}

// Space is whether a namespace has team memory on.
type Space struct {
	Namespace string
	Enabled   bool
	ChangedBy string
	ChangedAt time.Time
}

// SwitchStore keeps the per-namespace switch. It does not know which
// namespaces exist.
type SwitchStore interface {
	// Get returns the switch. A namespace never set reads as off.
	Get(ctx context.Context, namespace string) (Space, error)
	// Set turns memory on or off.
	Set(ctx context.Context, namespace string, on bool, by string, at time.Time) error
	// Enabled lists the namespaces that have memory on, sorted.
	Enabled(ctx context.Context) ([]string, error)
	// Remove forgets a namespace's row, so it reads as never set. Removing
	// a namespace with no row is not an error.
	Remove(ctx context.Context, namespace string) error
}
