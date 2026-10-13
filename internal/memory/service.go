package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

// ErrNoMemory is what every request gets for a namespace the person cannot
// read, one that does not exist and one whose switch is off. It is one fixed
// sentence, so a 404 never says whether a team exists. It wraps
// registry.ErrNotFound.
var ErrNoMemory error = noMemoryError{}

type noMemoryError struct{}

func (noMemoryError) Error() string { return "No team memory here." }
func (noMemoryError) Unwrap() error { return registry.ErrNotFound }

// Search limits: a Query with no Limit gets DefaultSearchLimit, and none gets
// more than MaxSearchLimit.
const (
	DefaultSearchLimit = 10
	MaxSearchLimit     = 50
)

// Service holds the rules of team memory: rights, the per-namespace switch,
// limits and the duplicate check. Stores only keep data. It is safe for
// concurrent use.
type Service struct {
	store    Store
	switches SwitchStore
	roles    Roles
	now      func() time.Time
	log      *slog.Logger
}

// NewService returns a service. A nil clock reads time.Now and a nil logger
// discards. The store, the switches and the roles are required.
func NewService(store Store, switches SwitchStore, roles Roles, now func() time.Time, log *slog.Logger) (*Service, error) {
	if store == nil || switches == nil || roles == nil {
		return nil, errors.New("memory service needs a store, a switch store and roles")
	}
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: store, switches: switches, roles: roles, now: now, log: log}, nil
}

// Now is the service's clock, so callers mark unconfirmed entries against the
// same time the service used.
func (s *Service) Now() time.Time { return s.now() }

// access is what a person may do in one namespace now: their rights, and the
// layers they can read, the namespace first. A layer above is only listed
// while its own switch is on.
type access struct {
	Rights
	readable []string
	// projectOn is whether the project above has memory on.
	projectOn bool
}

// resolve answers ErrNoMemory unless the person reads ns and ns has memory on.
func (s *Service) resolve(ctx context.Context, c Caller, ns string) (access, error) {
	r, err := s.roles.MemoryRights(ctx, c.UserID, ns)
	if err != nil {
		return access{}, fmt.Errorf("memory rights: %w", err)
	}
	if !r.Read {
		return access{}, ErrNoMemory
	}
	on, err := s.enabled(ctx, ns)
	if err != nil {
		return access{}, err
	}
	if !on {
		return access{}, ErrNoMemory
	}
	a := access{Rights: r, readable: []string{ns}}
	for _, l := range r.Layers {
		if l == ns {
			continue
		}
		if on, err := s.enabled(ctx, l); err != nil {
			return access{}, err
		} else if on {
			a.readable = append(a.readable, l)
			a.projectOn = true
		}
	}
	return a, nil
}

func (s *Service) enabled(ctx context.Context, ns string) (bool, error) {
	sp, err := s.switches.Get(ctx, ns)
	if err != nil {
		return false, fmt.Errorf("memory switch of %s: %w", ns, err)
	}
	return sp.Enabled, nil
}

// entryOf returns the entry with this id when it lives in a namespace the
// person can use: a missing entry and a hidden one look the same.
func (s *Service) entryOf(ctx context.Context, c Caller, id int64) (Entry, access, error) {
	e, err := s.store.Get(ctx, id)
	if errors.Is(err, registry.ErrNotFound) {
		return Entry{}, access{}, ErrNoMemory
	}
	if err != nil {
		return Entry{}, access{}, fmt.Errorf("memory %d: %w", id, err)
	}
	a, err := s.resolve(ctx, c, e.Namespace)
	if err != nil {
		return Entry{}, access{}, err
	}
	return e, a, nil
}

// Search finds active entries in the layers the person reads (ns, and its
// project while that has memory on). q.Namespaces, if set, narrows them.
// It does not count a use.
func (s *Service) Search(ctx context.Context, c Caller, ns string, q Query) ([]Entry, error) {
	a, err := s.resolve(ctx, c, ns)
	if err != nil {
		return nil, err
	}
	if len(Words(q.Text)) == 0 {
		return nil, nil
	}
	q.Namespaces = narrow(a.readable, q.Namespaces)
	if q.Limit <= 0 {
		q.Limit = DefaultSearchLimit
	}
	q.Limit = min(q.Limit, MaxSearchLimit)
	return s.store.Search(ctx, q)
}

// narrow keeps the layers asked for. A filter that names none of them reads
// nothing.
func narrow(layers, want []string) []string {
	if len(want) == 0 {
		return layers
	}
	return slices.DeleteFunc(slices.Clone(layers), func(l string) bool { return !slices.Contains(want, l) })
}

// Get returns one active entry of the layers the person reads and counts one
// use. A retracted entry is hidden like a missing one.
func (s *Service) Get(ctx context.Context, c Caller, ns string, id int64) (Entry, error) {
	a, err := s.resolve(ctx, c, ns)
	if err != nil {
		return Entry{}, err
	}
	e, err := s.store.Get(ctx, id)
	if errors.Is(err, registry.ErrNotFound) {
		return Entry{}, ErrNoMemory
	}
	if err != nil {
		return Entry{}, fmt.Errorf("memory %d: %w", id, err)
	}
	if !e.Active() || !slices.Contains(a.readable, e.Namespace) {
		return Entry{}, ErrNoMemory
	}
	if err := s.store.CountUse(ctx, id); err != nil {
		return Entry{}, fmt.Errorf("count use of memory %d: %w", id, err)
	}
	e.Uses++
	return e, nil
}

// Recent returns the entries whose last version is at or after since, newest
// first, from the layers the person reads. limit <= 0 means DefaultSearchLimit.
func (s *Service) Recent(ctx context.Context, c Caller, ns string, since time.Time, limit int) ([]Entry, error) {
	a, err := s.resolve(ctx, c, ns)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	return s.store.Recent(ctx, a.readable, since, min(limit, MaxSearchLimit))
}

// Index returns the entries for the index in the server instructions (at most
// IndexLines, best first) and how many active entries the layers hold.
func (s *Service) Index(ctx context.Context, c Caller, ns string) ([]Entry, int, error) {
	a, err := s.resolve(ctx, c, ns)
	if err != nil {
		return nil, 0, err
	}
	return s.store.Index(ctx, a.readable, s.now(), IndexLines)
}

// SetEnabled turns team memory on or off for ns. Only those with the Switch
// right may. A person who cannot read ns gets ErrNoMemory.
func (s *Service) SetEnabled(ctx context.Context, c Caller, ns string, on bool) error {
	r, err := s.roles.MemoryRights(ctx, c.UserID, ns)
	if err != nil {
		return fmt.Errorf("memory rights: %w", err)
	}
	if !r.Read {
		return ErrNoMemory
	}
	if !r.Switch {
		return fmt.Errorf("%w: only owners and maintainers turn team memory on or off", registry.ErrForbidden)
	}
	if err := s.switches.Set(ctx, ns, on, c.UserID, s.now()); err != nil {
		return fmt.Errorf("set memory switch of %s: %w", ns, err)
	}
	s.log.InfoContext(ctx, "memory switch set", "namespace", ns, "user", c.UserID, "enabled", on)
	return nil
}

// Inspect returns an entry, retracted or not, and how many versions it has,
// for the operator to look at before a purge. Like Purge it checks no rights.
func (s *Service) Inspect(ctx context.Context, id int64) (Entry, int, error) {
	e, err := s.store.Get(ctx, id)
	if err != nil {
		return Entry{}, 0, fmt.Errorf("memory %d: %w", id, err)
	}
	h, err := s.store.History(ctx, id)
	if err != nil {
		return Entry{}, 0, fmt.Errorf("history of memory %d: %w", id, err)
	}
	return e, len(h), nil
}

// RemoveNamespace deletes the memory of a namespace that is being deleted:
// the switch, every entry, its history and links. It checks no rights, so
// only the code that deletes the namespace calls it. A team created later
// under the same name starts with memory off and no entries. The log names the
// namespace and the counts, never text.
func (s *Service) RemoveNamespace(ctx context.Context, ns string) (Removed, error) {
	// The switch first: if the entries then fail to go, they are at least
	// unreachable.
	if err := s.switches.Remove(ctx, ns); err != nil {
		return Removed{}, fmt.Errorf("remove the memory switch of %s: %w", ns, err)
	}
	r, err := s.store.RemoveNamespace(ctx, ns)
	if err != nil {
		return Removed{}, fmt.Errorf("remove the memory of %s: %w", ns, err)
	}
	s.log.InfoContext(ctx, "memory removed with namespace", "namespace", ns,
		"entries", r.Entries, "versions", r.Versions, "links", r.Links)
	return r, nil
}

// Purge removes an entry and its history for good and records that it
// happened. It is for the operator on the host: it takes no Caller and checks
// no rights, so only code that runs there may call it.
//
// It can be run again for an id it already purged: the entry is gone, so it
// only repeats the scrub of the files and reports success. That is the way
// out when the write-ahead log was busy the first time. The Purged it returns
// then has no CreatedAt, since the record keeps no dates of the entry.
func (s *Service) Purge(ctx context.Context, id int64) (Purged, error) {
	p, err := s.store.Purge(ctx, id, s.now())
	if errors.Is(err, registry.ErrNotFound) {
		prev, ok, perr := s.WasPurged(ctx, id)
		if perr != nil {
			return Purged{}, perr
		}
		if ok {
			if err := s.store.Scrub(ctx); err != nil {
				return Purged{}, fmt.Errorf("scrub after the purge of memory %d: %w", id, err)
			}
			s.log.InfoContext(ctx, "memory entry scrubbed again", "id", id, "namespace", prev.Namespace)
			return prev, nil
		}
	}
	if err != nil {
		return Purged{}, fmt.Errorf("purge memory %d: %w", id, err)
	}
	s.log.InfoContext(ctx, "memory entry purged", "id", id, "namespace", p.Namespace, "versions", p.Versions)
	return p, nil
}

// WasPurged finds the record of a purge of entry id. Like Purge it checks no
// rights.
func (s *Service) WasPurged(ctx context.Context, id int64) (Purged, bool, error) {
	all, err := s.store.Purges(ctx)
	if err != nil {
		return Purged{}, false, fmt.Errorf("list purges: %w", err)
	}
	for _, p := range all {
		if p.EntryID == id {
			return p, true, nil
		}
	}
	return Purged{}, false, nil
}
