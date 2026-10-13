// Package memorytest has contract suites every memory store must pass and
// in-memory stores for tests of code that needs one.
package memorytest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// MemStore is a memory.Store held in memory. It matches a query word by word
// on lowercase words, so unlike SQLite's porter stemmer "hosts" does not match
// "host". Ranking is a rough weight (name 10, description 5, body 1), which
// only sqlitestore tests exactly.
type MemStore struct {
	mu      sync.Mutex
	next    int64
	entries map[int64]*record
	purges  []memory.Purged
}

type record struct {
	entry    memory.Entry
	versions []memory.Version
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore { return &MemStore{entries: map[int64]*record{}} }

var _ memory.Store = (*MemStore)(nil)

func (r *record) copy() memory.Entry {
	e := r.entry
	e.Links = slices.Clone(e.Links)
	return e
}

func (s *MemStore) get(id int64) (*record, error) {
	r, ok := s.entries[id]
	if !ok {
		return nil, fmt.Errorf("memory %d: %w", id, registry.ErrNotFound)
	}
	return r, nil
}

// push appends the next version of r, built from its current entry, and
// moves updated to at.
func (r *record) push(action memory.Action, by memory.Caller, note string, at time.Time) {
	e := &r.entry
	e.Version++
	e.UpdatedAt = at
	if action != memory.ActionConfirm {
		e.EditedBy = ""
		if by.UserID != e.AuthorID {
			e.EditedBy = by.Label
		}
	}
	r.versions = append(r.versions, memory.Version{
		EntryID: e.ID, Number: e.Version, Action: action, Namespace: e.Namespace, Type: e.Type,
		Name: e.Name, Description: e.Description, Body: e.Body, Links: slices.Clone(e.Links),
		By: by, Note: note, At: at,
	})
}

func (s *MemStore) checkLinks(ids []int64) error {
	for _, id := range ids {
		if _, ok := s.entries[id]; !ok {
			return fmt.Errorf("%w: a linked memory does not exist", registry.ErrInvalid)
		}
	}
	return nil
}

// Insert implements memory.Store.
func (s *MemStore) Insert(ctx context.Context, d memory.Draft, by memory.Caller, at time.Time, opts ...memory.WriteOption) (memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return memory.Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLimit(by.UserID, opts); err != nil {
		return memory.Entry{}, err
	}
	if err := s.checkLinks(d.Links()); err != nil {
		return memory.Entry{}, err
	}
	s.next++
	at = at.UTC()
	r := &record{entry: memory.Entry{
		ID: s.next, Namespace: d.Namespace(), Type: d.Type(), Name: d.Name(), Description: d.Description(),
		Body: d.Body(), Links: d.Links(), AuthorID: by.UserID, AuthorLabel: by.Label,
		CreatedAt: at, ConfirmedAt: at, Status: memory.StatusActive,
	}}
	r.push(memory.ActionSave, by, "", at)
	s.entries[r.entry.ID] = r
	return r.copy(), nil
}

// Get implements memory.Store.
func (s *MemStore) Get(ctx context.Context, id int64) (memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return memory.Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return memory.Entry{}, err
	}
	return r.copy(), nil
}

// GetMany implements memory.Store.
func (s *MemStore) GetMany(ctx context.Context, ids []int64) ([]memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []memory.Entry
	for _, id := range ids {
		if r, ok := s.entries[id]; ok {
			out = append(out, r.copy())
		}
	}
	return out, nil
}

// CountUse implements memory.Store.
func (s *MemStore) CountUse(ctx context.Context, ids ...int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if r, ok := s.entries[id]; ok {
			r.entry.Uses++
		}
	}
	return nil
}

// History implements memory.Store.
func (s *MemStore) History(ctx context.Context, id int64) ([]memory.Version, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(r.versions)
	for i := range out {
		out[i].Links = slices.Clone(out[i].Links)
	}
	return out, nil
}

// Update implements memory.Store.
func (s *MemStore) Update(ctx context.Context, id int64, base int, rev memory.Revision, by memory.Caller, at time.Time, opts ...memory.WriteOption) (memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return memory.Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLimit(by.UserID, opts); err != nil {
		return memory.Entry{}, err
	}
	r, err := s.get(id)
	if err != nil {
		return memory.Entry{}, err
	}
	if r.entry.Version != base {
		return memory.Entry{}, fmt.Errorf("memory %d: %w", id, memory.ErrStale)
	}
	if r.entry.Status != memory.StatusActive {
		return memory.Entry{}, fmt.Errorf("%w: memory %d is retracted", registry.ErrInvalid, id)
	}
	if err := s.checkLinks(rev.Links()); err != nil {
		return memory.Entry{}, err
	}
	e := &r.entry
	e.Type, e.Name, e.Description, e.Body = rev.Type(), rev.Name(), rev.Description(), rev.Body()
	e.Links, e.ConfirmedAt = rev.Links(), rev.ConfirmedAt().UTC()
	r.push(rev.Action(), by, rev.Note(), at.UTC())
	return r.copy(), nil
}

// Retract implements memory.Store.
func (s *MemStore) Retract(ctx context.Context, id int64, reason string, by memory.Caller, at time.Time) (memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return memory.Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return memory.Entry{}, err
	}
	if r.entry.Status == memory.StatusRetracted {
		return r.copy(), nil
	}
	r.entry.Status = memory.StatusRetracted
	r.push(memory.ActionRetract, by, reason, at.UTC())
	return r.copy(), nil
}

// Move implements memory.Store.
func (s *MemStore) Move(ctx context.Context, id int64, to string, by memory.Caller, at time.Time) (memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return memory.Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return memory.Entry{}, err
	}
	if r.entry.Status != memory.StatusActive || r.entry.Namespace == to || strings.TrimSpace(to) == "" {
		return memory.Entry{}, fmt.Errorf("%w: memory %d cannot be moved there", registry.ErrInvalid, id)
	}
	r.entry.Namespace = to
	r.push(memory.ActionMove, by, "", at.UTC())
	return r.copy(), nil
}

// Purge implements memory.Store.
func (s *MemStore) Purge(ctx context.Context, id int64, at time.Time) (memory.Purged, error) {
	if err := ctx.Err(); err != nil {
		return memory.Purged{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return memory.Purged{}, err
	}
	p := memory.Purged{EntryID: id, Namespace: r.entry.Namespace, Versions: len(r.versions), CreatedAt: r.entry.CreatedAt, At: at.UTC()}
	delete(s.entries, id)
	for _, o := range s.entries {
		o.entry.Links = slices.DeleteFunc(o.entry.Links, func(l int64) bool { return l == id })
	}
	s.purges = append(s.purges, p)
	return p, nil
}

// RemoveNamespace implements memory.Store.
func (s *MemStore) RemoveNamespace(ctx context.Context, namespace string) (memory.Removed, error) {
	if err := ctx.Err(); err != nil {
		return memory.Removed{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out memory.Removed
	gone := map[int64]bool{}
	for id, r := range s.entries {
		if r.entry.Namespace == namespace {
			gone[id] = true
			out.Entries++
			out.Versions += len(r.versions)
			out.Links += len(r.entry.Links)
		}
	}
	for id, r := range s.entries {
		if gone[id] {
			continue
		}
		for _, l := range r.entry.Links {
			if gone[l] {
				out.Links++
			}
		}
		r.entry.Links = slices.DeleteFunc(r.entry.Links, func(l int64) bool { return gone[l] })
	}
	for id := range gone {
		delete(s.entries, id)
	}
	return out, nil
}

// Scrub implements memory.Store. Nothing is kept in files, so there is
// nothing to scrub.
func (s *MemStore) Scrub(ctx context.Context) error { return ctx.Err() }

// Purges implements memory.Store.
func (s *MemStore) Purges(ctx context.Context) ([]memory.Purged, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.purges), nil
}

// active returns the active entries of the namespaces.
func (s *MemStore) active(namespaces []string) []*record {
	var out []*record
	for _, r := range s.entries {
		if r.entry.Status == memory.StatusActive && slices.Contains(namespaces, r.entry.Namespace) {
			out = append(out, r)
		}
	}
	return out
}

// score is how well a record matches words: the sum of the weights of the
// fields each word is in, or -1 if matchAll is set and a word is missing, or
// 0 if none is present.
func score(e memory.Entry, words []string, matchAll bool) int {
	name, desc, body := wordSet(e.Name), wordSet(e.Description), wordSet(e.Body)
	total, missing := 0, false
	for _, w := range words {
		got := 0
		if name[w] {
			got += 10
		}
		if desc[w] {
			got += 5
		}
		if body[w] {
			got++
		}
		if got == 0 {
			missing = true
		}
		total += got
	}
	if matchAll && missing {
		return 0
	}
	return total
}

func wordSet(text string) map[string]bool {
	m := map[string]bool{}
	for _, w := range memory.Terms(text) {
		m[w] = true
	}
	return m
}

// ranked sorts best score first, then newest update, then id.
func ranked(rs []*record, scores map[int64]int, limit int) []memory.Entry {
	slices.SortFunc(rs, func(a, b *record) int {
		if c := scores[b.entry.ID] - scores[a.entry.ID]; c != 0 {
			return c
		}
		if c := b.entry.UpdatedAt.Compare(a.entry.UpdatedAt); c != 0 {
			return c
		}
		return int(b.entry.ID - a.entry.ID)
	})
	return take(rs, limit)
}

func take(rs []*record, limit int) []memory.Entry {
	if limit > 0 && len(rs) > limit {
		rs = rs[:limit]
	}
	out := make([]memory.Entry, len(rs))
	for i, r := range rs {
		out[i] = r.copy()
	}
	return out
}

func (s *MemStore) match(namespaces []string, typ memory.Type, words []string, all bool) map[int64]int {
	scores := map[int64]int{}
	for _, r := range s.active(namespaces) {
		if typ != "" && r.entry.Type != typ {
			continue
		}
		if sc := score(r.entry, words, all); sc > 0 {
			scores[r.entry.ID] = sc
		}
	}
	return scores
}

func (s *MemStore) hits(scores map[int64]int, limit int) []memory.Entry {
	rs := make([]*record, 0, len(scores))
	for id := range scores {
		rs = append(rs, s.entries[id])
	}
	return ranked(rs, scores, limit)
}

// Search implements memory.Store.
func (s *MemStore) Search(ctx context.Context, q memory.Query) ([]memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	words := memory.Words(q.Text)
	if len(words) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scores := s.match(q.Namespaces, q.Type, words, true)
	if len(scores) == 0 {
		scores = s.match(q.Namespaces, q.Type, words, false)
	}
	return s.hits(scores, q.Limit), nil
}

// Candidates implements memory.Store.
func (s *MemStore) Candidates(ctx context.Context, namespaces []string, words []string, limit int) ([]memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var lower []string
	for _, w := range words {
		lower = append(lower, memory.Words(w)...)
	}
	if len(lower) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits(s.match(namespaces, "", lower, false), limit), nil
}

// Recent implements memory.Store.
func (s *MemStore) Recent(ctx context.Context, namespaces []string, since time.Time, limit int) ([]memory.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rs []*record
	for _, r := range s.active(namespaces) {
		if !r.entry.UpdatedAt.Before(since) {
			rs = append(rs, r)
		}
	}
	return ranked(rs, nil, limit), nil
}

// Index implements memory.Store.
func (s *MemStore) Index(ctx context.Context, namespaces []string, now time.Time, limit int) ([]memory.Entry, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rs := s.active(namespaces)
	fresh := func(r *record) bool { return !r.entry.UpdatedAt.Before(now.Add(-memory.IndexFresh)) }
	slices.SortFunc(rs, func(a, b *record) int {
		fa, fb := fresh(a), fresh(b)
		switch {
		case fa && !fb:
			return -1
		case fb && !fa:
			return 1
		case !fa && a.entry.Uses != b.entry.Uses:
			return b.entry.Uses - a.entry.Uses
		}
		if c := b.entry.UpdatedAt.Compare(a.entry.UpdatedAt); c != 0 {
			return c
		}
		return int(b.entry.ID - a.entry.ID)
	})
	return take(rs, limit), len(rs), nil
}

// WritesSince implements memory.Store.
func (s *MemStore) WritesSince(ctx context.Context, userID string, since time.Time) (memory.Writes, error) {
	if err := ctx.Err(); err != nil {
		return memory.Writes{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes(userID, since), nil
}

// writes counts the save and update versions of a person after since. Only
// those are writes: a retract, a move and a confirm are not.
func (s *MemStore) writes(userID string, since time.Time) memory.Writes {
	var w memory.Writes
	for _, r := range s.entries {
		for _, v := range r.versions {
			if v.By.UserID != userID || !v.At.After(since) || (v.Action != memory.ActionSave && v.Action != memory.ActionUpdate) {
				continue
			}
			w.Count++
			if w.Oldest.IsZero() || v.At.Before(w.Oldest) {
				w.Oldest = v.At
			}
		}
	}
	return w
}

// checkLimit is the daily limit of Within, checked under the store's lock.
func (s *MemStore) checkLimit(userID string, opts []memory.WriteOption) error {
	l, ok := memory.LimitOf(opts)
	if !ok {
		return nil
	}
	if w := s.writes(userID, l.Since); w.Count >= l.Max {
		return &memory.WriteLimitError{Oldest: w.Oldest}
	}
	return nil
}
