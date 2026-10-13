package memorytest

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

var (
	at   = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	dana = memory.Caller{UserID: "u-dana", Label: "dana@example.com"}
	lee  = memory.Caller{UserID: "u-lee", Label: "lee@example.com"}
)

// RunStoreContract runs the behaviour every memory.Store must have.
// newStore must return a fresh, empty store for each call.
func RunStoreContract(t *testing.T, newStore func(t *testing.T) memory.Store) {
	t.Helper()
	for _, c := range []struct {
		name string
		run  func(*testing.T, memory.Store)
	}{
		{"insert and get", runInsertGet},
		{"links", runLinks},
		{"update keeps history", runUpdate},
		{"confirm only", runConfirm},
		{"edited by", runEditedBy},
		{"stale update", runStale},
		{"concurrent updates", runConcurrent},
		{"retract", runRetract},
		{"move keeps identity", runMove},
		{"search", runSearch},
		{"search filters", runSearchFilters},
		{"candidates", runCandidates},
		{"recent", runRecent},
		{"index", runIndex},
		{"uses", runUses},
		{"writes since", runWrites},
		{"writes are saves and updates", runWritesCountOnlySavesAndUpdates},
		{"write limit", runWriteLimit},
		{"purge", runPurge},
		{"remove namespace", runRemoveNamespace},
	} {
		t.Run(c.name, func(t *testing.T) { c.run(t, newStore(t)) })
	}
}

func draft(t *testing.T, ns string, typ memory.Type, name, desc, body string, links ...int64) memory.Draft {
	t.Helper()
	d, err := memory.NewDraft(ns, typ, name, desc, body, links)
	if err != nil {
		t.Fatalf("NewDraft(%s) error: %v", name, err)
	}
	return d
}

// save inserts a reference entry.
func save(t *testing.T, s memory.Store, ns, name, desc, body string, by memory.Caller, when time.Time, links ...int64) memory.Entry {
	t.Helper()
	e, err := s.Insert(t.Context(), draft(t, ns, memory.TypeReference, name, desc, body, links...), by, when)
	if err != nil {
		t.Fatalf("Insert(%s) error: %v", name, err)
	}
	return e
}

func revise(t *testing.T, e memory.Entry, c memory.Change, now time.Time) memory.Revision {
	t.Helper()
	r, err := c.Revise(e, now)
	if err != nil {
		t.Fatalf("Revise() error: %v", err)
	}
	return r
}

func ids(es []memory.Entry) []int64 {
	out := make([]int64, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func wantIDs(t *testing.T, what string, got []memory.Entry, want ...int64) {
	t.Helper()
	if g := ids(got); !slices.Equal(g, want) {
		t.Errorf("%s = %v, want %v", what, g, want)
	}
}

func wantIs(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("%s error = %v, want %v", what, err, target)
	}
}

func runInsertGet(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	e, err := s.Insert(ctx, draft(t, "lime", memory.TypeProject, "Release label", "PRs need the release label", "Body\nlines"), dana, at)
	if err != nil {
		t.Fatalf("Insert() error: %v", err)
	}
	if e.ID < 1 || e.Namespace != "lime" || e.Type != memory.TypeProject || e.Name != "Release label" ||
		e.Description != "PRs need the release label" || e.Body != "Body\nlines" || len(e.Links) != 0 ||
		e.AuthorID != dana.UserID || e.AuthorLabel != dana.Label || e.Status != memory.StatusActive ||
		e.Version != 1 || e.Uses != 0 {
		t.Errorf("Insert() = %+v", e)
	}
	for what, got := range map[string]time.Time{"created": e.CreatedAt, "updated": e.UpdatedAt, "confirmed": e.ConfirmedAt} {
		if !got.Equal(at) {
			t.Errorf("Insert() %s = %v, want %v", what, got, at)
		}
	}
	got, err := s.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.ID != e.ID || got.Name != e.Name || got.Body != e.Body || !got.CreatedAt.Equal(at) || got.Version != 1 {
		t.Errorf("Get() = %+v, want what Insert returned", got)
	}
	h, err := s.History(ctx, e.ID)
	if err != nil || len(h) != 1 {
		t.Fatalf("History() = %v, %v, want one version", h, err)
	}
	if v := h[0]; v.Number != 1 || v.Action != memory.ActionSave || v.By != dana || v.Name != e.Name || v.Body != e.Body ||
		v.Namespace != "lime" || v.Type != memory.TypeProject || !v.At.Equal(at) || v.EntryID != e.ID {
		t.Errorf("History()[0] = %+v", v)
	}
	second := save(t, s, "lime", "Second", "d", "", lee, at)
	if second.ID == e.ID {
		t.Error("two entries share an id")
	}
	_, err = s.Get(ctx, 999)
	wantIs(t, "Get(999)", err, registry.ErrNotFound)
	_, err = s.History(ctx, 999)
	wantIs(t, "History(999)", err, registry.ErrNotFound)
}

func runLinks(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	a := save(t, s, "lime", "A", "d", "", dana, at)
	b := save(t, s, "lime", "B", "d", "", dana, at)
	c, err := s.Insert(ctx, draft(t, "lime", memory.TypeReference, "C", "d", "", b.ID, a.ID), dana, at)
	if err != nil {
		t.Fatalf("Insert() with links error: %v", err)
	}
	if !slices.Equal(c.Links, []int64{b.ID, a.ID}) {
		t.Errorf("Links = %v, want %v in the order given", c.Links, []int64{b.ID, a.ID})
	}
	c.Links[0] = 99
	if got, _ := s.Get(ctx, c.ID); !slices.Equal(got.Links, []int64{b.ID, a.ID}) {
		t.Errorf("a caller changed a stored entry: links = %v", got.Links)
	}
	_, err = s.Insert(ctx, draft(t, "lime", memory.TypeReference, "D", "d", "", 999), dana, at)
	wantIs(t, "Insert(link to nothing)", err, registry.ErrInvalid)

	many, err := s.GetMany(ctx, []int64{c.ID, 999, a.ID})
	if err != nil {
		t.Fatalf("GetMany() error: %v", err)
	}
	wantIDs(t, "GetMany", many, c.ID, a.ID)

	r := revise(t, c, memory.Change{Links: &[]int64{a.ID}}, at)
	u, err := s.Update(ctx, c.ID, c.Version, r, dana, at.Add(time.Hour))
	if err != nil || !slices.Equal(u.Links, []int64{a.ID}) {
		t.Errorf("Update() links = %v, %v, want [%d]", u.Links, err, a.ID)
	}
	r = revise(t, u, memory.Change{Links: &[]int64{999}}, at)
	_, err = s.Update(ctx, c.ID, u.Version, r, dana, at.Add(2*time.Hour))
	wantIs(t, "Update(link to nothing)", err, registry.ErrInvalid)
}

func runUpdate(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	e := save(t, s, "lime", "Host", "old desc", "old body", dana, at)
	later := at.Add(48 * time.Hour)
	name, body := "Host v2", "new body"
	typ := memory.TypeFeedback
	r := revise(t, e, memory.Change{Name: &name, Body: &body, Type: &typ, Note: "renamed"}, later)
	u, err := s.Update(ctx, e.ID, e.Version, r, lee, later)
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	if u.Version != 2 || u.Name != name || u.Body != body || u.Type != typ || u.Description != "old desc" ||
		!u.UpdatedAt.Equal(later) || !u.CreatedAt.Equal(at) || !u.ConfirmedAt.Equal(at) ||
		u.AuthorID != dana.UserID || u.Status != memory.StatusActive {
		t.Errorf("Update() = %+v, want version 2 with the author and created date kept", u)
	}
	h, err := s.History(ctx, e.ID)
	if err != nil || len(h) != 2 {
		t.Fatalf("History() = %v, %v, want 2 versions", h, err)
	}
	if h[0].Number != 1 || h[0].Name != "Host" || h[0].Body != "old body" || h[0].Type != memory.TypeReference {
		t.Errorf("History()[0] = %+v, want the first version kept as it was", h[0])
	}
	if v := h[1]; v.Number != 2 || v.Action != memory.ActionUpdate || v.Name != name || v.By != lee || v.Note != "renamed" || !v.At.Equal(later) {
		t.Errorf("History()[1] = %+v", v)
	}
	_, err = s.Update(ctx, 999, 1, r, lee, later)
	wantIs(t, "Update(999)", err, registry.ErrNotFound)
}

func runConfirm(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	e := save(t, s, "lime", "Host", "desc", "body", dana, at)
	later := at.AddDate(0, 7, 0)
	u, err := s.Update(ctx, e.ID, 1, revise(t, e, memory.Change{Confirm: true}, later), lee, later)
	if err != nil {
		t.Fatalf("Update(confirm) error: %v", err)
	}
	if u.Version != 2 || !u.ConfirmedAt.Equal(later) || !u.UpdatedAt.Equal(later) || u.Name != e.Name || u.Body != e.Body ||
		u.Description != e.Description {
		t.Errorf("Update(confirm) = %+v, want only confirmed_at moved", u)
	}
	if u.Unconfirmed(later) {
		t.Error("a just confirmed entry reads as unconfirmed")
	}
	h, _ := s.History(ctx, e.ID)
	if len(h) != 2 || h[1].Action != memory.ActionConfirm || h[1].Body != "body" || h[1].By != lee {
		t.Errorf("History() = %+v, want a confirm version holding the same text", h)
	}
}

func runEditedBy(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	e := save(t, s, "lime", "Host", "desc", "body", dana, at)
	if e.EditedBy != "" {
		t.Errorf("a new entry has EditedBy %q, want none", e.EditedBy)
	}
	step := func(by memory.Caller, c memory.Change, i int) memory.Entry {
		t.Helper()
		now := at.Add(time.Duration(i) * time.Hour)
		cur, err := s.Get(ctx, e.ID)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		u, err := s.Update(ctx, e.ID, cur.Version, revise(t, cur, c, now), by, now)
		if err != nil {
			t.Fatalf("Update() error: %v", err)
		}
		got, _ := s.Get(ctx, e.ID)
		if got.EditedBy != u.EditedBy {
			t.Errorf("Get().EditedBy = %q, Update() returned %q, want the same", got.EditedBy, u.EditedBy)
		}
		return got
	}
	b1, b2 := "by lee", "by dana"
	if got := step(lee, memory.Change{Body: &b1}, 1); got.EditedBy != lee.Label {
		t.Errorf("after lee edited: EditedBy %q, want %q", got.EditedBy, lee.Label)
	}
	if got := step(dana, memory.Change{Confirm: true}, 2); got.EditedBy != lee.Label {
		t.Errorf("after the author confirmed: EditedBy %q, want lee kept", got.EditedBy)
	}
	if got := step(dana, memory.Change{Body: &b2}, 3); got.EditedBy != "" {
		t.Errorf("after the author edited again: EditedBy %q, want none", got.EditedBy)
	}
	b3 := "by lee again"
	step(lee, memory.Change{Body: &b3}, 4)
	if got := step(lee, memory.Change{Confirm: true}, 5); got.EditedBy != lee.Label {
		t.Errorf("EditedBy %q, want lee", got.EditedBy)
	}
}

func runStale(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	e := save(t, s, "lime", "Host", "desc", "body", dana, at)
	a, b := "from a", "from b"
	if _, err := s.Update(ctx, e.ID, 1, revise(t, e, memory.Change{Body: &a}, at), dana, at.Add(time.Hour)); err != nil {
		t.Fatalf("first Update() error: %v", err)
	}
	_, err := s.Update(ctx, e.ID, 1, revise(t, e, memory.Change{Body: &b}, at), lee, at.Add(2*time.Hour))
	wantIs(t, "stale Update()", err, memory.ErrStale)
	got, _ := s.Get(ctx, e.ID)
	h, _ := s.History(ctx, e.ID)
	if got.Body != a || got.Version != 2 || len(h) != 2 {
		t.Errorf("after a stale update: body %q version %d history %d, want the first update kept and no new version", got.Body, got.Version, len(h))
	}
}

func runConcurrent(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	e := save(t, s, "lime", "Host", "desc", "body", dana, at)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			body := fmt.Sprintf("body %d", i)
			r, err := memory.Change{Body: &body}.Revise(e, at)
			if err != nil {
				errs[i] = err
				return
			}
			_, errs[i] = s.Update(ctx, e.ID, 1, r, dana, at.Add(time.Duration(i+1)*time.Minute))
		})
	}
	wg.Wait()
	won, stale := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, memory.ErrStale):
			stale++
		default:
			t.Errorf("Update() error = %v, want nil or ErrStale", err)
		}
	}
	if won != 1 || stale != n-1 {
		t.Errorf("%d updates won and %d were stale, want 1 and %d", won, stale, n-1)
	}
	if h, _ := s.History(ctx, e.ID); len(h) != 2 {
		t.Errorf("History() has %d versions, want 2", len(h))
	}
}

func runRetract(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	ns := []string{"lime"}
	e := save(t, s, "lime", "Host", "desc", "uniqueword", dana, at)
	other := save(t, s, "lime", "Other", "desc", "uniqueword", dana, at)
	later := at.Add(time.Hour)
	r, err := s.Retract(ctx, e.ID, "wrong host", lee, later)
	if err != nil {
		t.Fatalf("Retract() error: %v", err)
	}
	if r.Status != memory.StatusRetracted || r.Version != 2 || !r.UpdatedAt.Equal(later) {
		t.Errorf("Retract() = %+v", r)
	}
	got, err := s.Get(ctx, e.ID)
	if err != nil || got.Status != memory.StatusRetracted || got.Body != "uniqueword" {
		t.Errorf("Get() after retract = %+v, %v, want the entry kept", got, err)
	}
	h, _ := s.History(ctx, e.ID)
	if len(h) != 2 || h[1].Action != memory.ActionRetract || h[1].Note != "wrong host" || h[1].By != lee || h[0].Body != "uniqueword" {
		t.Errorf("History() = %+v, want the history kept with a retract version", h)
	}
	found, _ := s.Search(ctx, memory.Query{Text: "uniqueword", Namespaces: ns})
	wantIDs(t, "Search after retract", found, other.ID)
	cand, _ := s.Candidates(ctx, ns, []string{"uniqueword", "host"}, 10)
	wantIDs(t, "Candidates after retract", cand, other.ID)
	rec, _ := s.Recent(ctx, ns, at.Add(-time.Hour), 10)
	wantIDs(t, "Recent after retract", rec, other.ID)
	idx, total, _ := s.Index(ctx, ns, later, 10)
	wantIDs(t, "Index after retract", idx, other.ID)
	if total != 1 {
		t.Errorf("Index total = %d, want 1", total)
	}

	again, err := s.Retract(ctx, e.ID, "twice", lee, later.Add(time.Hour))
	if err != nil || again.Version != 2 {
		t.Errorf("second Retract() = version %d, %v, want no new version", again.Version, err)
	}
	body := "x"
	_, err = s.Update(ctx, e.ID, 2, revise(t, e, memory.Change{Body: &body}, later), dana, later)
	wantIs(t, "Update(retracted)", err, registry.ErrInvalid)
	_, err = s.Retract(ctx, 999, "x", lee, later)
	wantIs(t, "Retract(999)", err, registry.ErrNotFound)
}

func runMove(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	e := save(t, s, "lime", "Host", "desc", "movable", dana, at)
	later := at.Add(time.Hour)
	m, err := s.Move(ctx, e.ID, "acme", lee, later)
	if err != nil {
		t.Fatalf("Move() error: %v", err)
	}
	if m.ID != e.ID || m.Namespace != "acme" || m.AuthorID != dana.UserID || m.AuthorLabel != dana.Label ||
		m.Version != 2 || m.Name != "Host" || !m.CreatedAt.Equal(at) {
		t.Errorf("Move() = %+v, want the id, author and created date kept", m)
	}
	h, _ := s.History(ctx, e.ID)
	if len(h) != 2 || h[0].Namespace != "lime" || h[1].Namespace != "acme" || h[1].Action != memory.ActionMove || h[1].By != lee {
		t.Errorf("History() = %+v, want the old namespace kept in version 1", h)
	}
	if got, _ := s.Search(t.Context(), memory.Query{Text: "movable", Namespaces: []string{"lime"}}); len(got) != 0 {
		t.Errorf("Search(lime) found the moved entry")
	}
	got, _ := s.Search(t.Context(), memory.Query{Text: "movable", Namespaces: []string{"acme"}})
	wantIDs(t, "Search(acme)", got, e.ID)

	_, err = s.Move(ctx, e.ID, "acme", lee, later)
	wantIs(t, "Move(same namespace)", err, registry.ErrInvalid)
	_, err = s.Move(ctx, 999, "acme", lee, later)
	wantIs(t, "Move(999)", err, registry.ErrNotFound)
	gone := save(t, s, "lime", "Gone", "d", "", dana, at)
	if _, err := s.Retract(ctx, gone.ID, "x", dana, later); err != nil {
		t.Fatalf("Retract() error: %v", err)
	}
	_, err = s.Move(ctx, gone.ID, "acme", lee, later)
	wantIs(t, "Move(retracted)", err, registry.ErrInvalid)
}

func runSearch(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	ns := []string{"lime"}
	tracker := save(t, s, "lime", "TRK Tracker host", "tickets live on tracker cc", "never the atc host", dana, at)
	label := save(t, s, "lime", "Release label", "PRs need a label", "the release label goes on the PR", dana, at.Add(time.Hour))
	inBody := save(t, s, "lime", "CI", "gate pipeline", "regate failures with the tracker link", dana, at.Add(2*time.Hour))

	tests := []struct {
		name, q   string
		want      []int64
		unordered bool // ranking between unlike matches is the engine's
	}{
		{"name word", "TRK", []int64{tracker.ID}, false},
		{"description word", "tickets", []int64{tracker.ID}, false},
		{"body word", "regate", []int64{inBody.ID}, false},
		{"case is ignored", "RELEASE LABEL", []int64{label.ID}, false},
		{"all words must match", "tracker host", []int64{tracker.ID}, false},
		{"name beats body", "tracker", []int64{tracker.ID, inBody.ID}, false},
		{"falls back to any word", "release ci", []int64{inBody.ID, label.ID}, true},
		{"no match", "kubernetes", nil, false},
		{"only punctuation", `"*" ( ) - ^`, nil, false},
		{"empty", "", nil, false},
		{"search syntax is plain words", `tracker OR NEAR(x y) "unbalanced`, []int64{tracker.ID, inBody.ID}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Search(ctx, memory.Query{Text: tt.q, Namespaces: ns, Limit: 10})
			if err != nil {
				t.Fatalf("Search(%q) error: %v", tt.q, err)
			}
			if tt.unordered {
				g := ids(got)
				slices.Sort(g)
				slices.Sort(tt.want)
				if !slices.Equal(g, tt.want) {
					t.Errorf("Search(%q) = %v, want %v in any order", tt.q, g, tt.want)
				}
				return
			}
			wantIDs(t, "Search("+tt.q+")", got, tt.want...)
		})
	}
	got, _ := s.Search(ctx, memory.Query{Text: "tracker", Namespaces: ns, Limit: 1})
	wantIDs(t, "Search(limit 1)", got, tracker.ID)
}

func runSearchFilters(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	mk := func(ns string, typ memory.Type, name string) memory.Entry {
		e, err := s.Insert(ctx, draft(t, ns, typ, name, "shared words here", ""), dana, at)
		if err != nil {
			t.Fatalf("Insert() error: %v", err)
		}
		return e
	}
	limeRef := mk("lime", memory.TypeReference, "alpha")
	limeFb := mk("lime", memory.TypeFeedback, "beta")
	acmeRef := mk("acme", memory.TypeReference, "gamma")
	mk("other", memory.TypeReference, "delta")

	tests := []struct {
		name string
		q    memory.Query
		want []int64
	}{
		{"one namespace", memory.Query{Namespaces: []string{"lime"}}, []int64{limeFb.ID, limeRef.ID}},
		{"two namespaces", memory.Query{Namespaces: []string{"lime", "acme"}}, []int64{acmeRef.ID, limeFb.ID, limeRef.ID}},
		{"type filter", memory.Query{Namespaces: []string{"lime", "acme"}, Type: memory.TypeReference}, []int64{acmeRef.ID, limeRef.ID}},
		{"type with no match", memory.Query{Namespaces: []string{"acme"}, Type: memory.TypeProject}, nil},
		{"no namespaces matches nothing", memory.Query{}, nil},
		{"unknown namespace", memory.Query{Namespaces: []string{"nope"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.q.Text = "shared words"
			got, err := s.Search(ctx, tt.q)
			if err != nil {
				t.Fatalf("Search() error: %v", err)
			}
			// Equal scores: newest first, then the higher id, in both stores.
			slices.Sort(tt.want)
			g := ids(got)
			slices.Sort(g)
			if !slices.Equal(g, tt.want) {
				t.Errorf("Search() = %v, want %v", g, tt.want)
			}
		})
	}
}

func runCandidates(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	a := save(t, s, "lime", "Tracker host", "where tickets live", "", dana, at)
	b := save(t, s, "lime", "Release label", "needed on PRs", "", dana, at)
	p := save(t, s, "acme", "Tracker auth", "tokens", "", dana, at)
	save(t, s, "other", "Tracker elsewhere", "no", "", dana, at)

	got, err := s.Candidates(ctx, []string{"lime", "acme"}, []string{"tracker", "host", "label"}, 10)
	if err != nil {
		t.Fatalf("Candidates() error: %v", err)
	}
	g := ids(got)
	slices.Sort(g)
	if want := []int64{a.ID, b.ID, p.ID}; !slices.Equal(g, want) {
		t.Errorf("Candidates() = %v, want %v (any word, readable namespaces only)", g, want)
	}
	if len(got) > 0 && got[0].ID != a.ID {
		t.Errorf("Candidates()[0] = %d, want %d, which matches two words", got[0].ID, a.ID)
	}
	got, _ = s.Candidates(ctx, []string{"lime", "acme"}, []string{"tracker", "label"}, 2)
	if len(got) != 2 {
		t.Errorf("Candidates(limit 2) returned %d", len(got))
	}
	got, _ = s.Candidates(ctx, []string{"lime"}, nil, 10)
	if len(got) != 0 {
		t.Errorf("Candidates(no words) = %v, want none", ids(got))
	}
	got, _ = s.Candidates(ctx, nil, []string{"tracker"}, 10)
	if len(got) != 0 {
		t.Errorf("Candidates(no namespaces) = %v, want none", ids(got))
	}
}

func runRecent(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	ns := []string{"lime"}
	save(t, s, "lime", "Old", "d", "", dana, at.Add(-10*24*time.Hour))
	mid := save(t, s, "lime", "Mid", "d", "", dana, at.Add(-3*24*time.Hour))
	edited := save(t, s, "lime", "Edited", "d", "", dana, at.Add(-9*24*time.Hour))
	save(t, s, "other", "Elsewhere", "d", "", dana, at)
	body := "x"
	if _, err := s.Update(ctx, edited.ID, 1, revise(t, edited, memory.Change{Body: &body}, at), dana, at.Add(-time.Hour)); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	got, err := s.Recent(ctx, ns, at.Add(-7*24*time.Hour), 10)
	if err != nil {
		t.Fatalf("Recent() error: %v", err)
	}
	wantIDs(t, "Recent(7 days)", got, edited.ID, mid.ID)
	got, _ = s.Recent(ctx, ns, at.Add(-30*24*time.Hour), 2)
	wantIDs(t, "Recent(limit 2)", got, edited.ID, mid.ID)
	got, _ = s.Recent(ctx, ns, mid.UpdatedAt, 10)
	wantIDs(t, "Recent(since is inclusive)", got, edited.ID, mid.ID)
	got, _ = s.Recent(ctx, nil, at.Add(-30*24*time.Hour), 10)
	wantIDs(t, "Recent(no namespaces)", got)
}

func runIndex(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	ns := []string{"lime", "acme"}
	day := 24 * time.Hour
	old1 := save(t, s, "lime", "old1", "d", "", dana, at.Add(-30*day))
	old2 := save(t, s, "lime", "old2", "d", "", dana, at.Add(-20*day))
	old3 := save(t, s, "acme", "old3", "d", "", dana, at.Add(-40*day))
	old4 := save(t, s, "lime", "old4", "d", "", dana, at.Add(-30*day))
	fresh1 := save(t, s, "lime", "fresh1", "d", "", dana, at.Add(-2*day))
	fresh2 := save(t, s, "acme", "fresh2", "d", "", dana, at.Add(-day))
	gone := save(t, s, "lime", "gone", "d", "", dana, at.Add(-day))
	save(t, s, "other", "other", "d", "", dana, at)
	if _, err := s.Retract(ctx, gone.ID, "x", dana, at.Add(-day)); err != nil {
		t.Fatalf("Retract() error: %v", err)
	}
	// Uses only order the entries that are not fresh.
	for id, n := range map[int64]int{old1.ID: 3, old3.ID: 3, old4.ID: 3, old2.ID: 1, fresh1.ID: 9} {
		for range n {
			if err := s.CountUse(ctx, id); err != nil {
				t.Fatalf("CountUse() error: %v", err)
			}
		}
	}
	got, total, err := s.Index(ctx, ns, at, 150)
	if err != nil {
		t.Fatalf("Index() error: %v", err)
	}
	// Fresh first by date (fresh1 has more uses and is still second). Then by
	// uses; old1 and old4 tie on uses and date, so the higher id goes first.
	wantIDs(t, "Index", got, fresh2.ID, fresh1.ID, old4.ID, old1.ID, old3.ID, old2.ID)
	if total != 6 {
		t.Errorf("Index total = %d, want 6 active entries in the two namespaces", total)
	}
	got, total, _ = s.Index(ctx, ns, at, 3)
	wantIDs(t, "Index(limit 3)", got, fresh2.ID, fresh1.ID, old4.ID)
	if total != 6 {
		t.Errorf("Index(limit 3) total = %d, want the count before the cap: 6", total)
	}
	got, total, _ = s.Index(ctx, []string{"acme"}, at, 10)
	wantIDs(t, "Index(acme)", got, fresh2.ID, old3.ID)
	if total != 2 {
		t.Errorf("Index(acme) total = %d, want 2", total)
	}
	got, total, _ = s.Index(ctx, nil, at, 10)
	if len(got) != 0 || total != 0 {
		t.Errorf("Index(no namespaces) = %v, %d, want nothing", ids(got), total)
	}
}

func runUses(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	a := save(t, s, "lime", "A", "d", "", dana, at)
	b := save(t, s, "lime", "B", "d", "", dana, at)
	if err := s.CountUse(ctx, a.ID, a.ID, b.ID, 999); err != nil {
		t.Fatalf("CountUse() error: %v", err)
	}
	if got, _ := s.Get(ctx, a.ID); got.Uses != 2 {
		t.Errorf("Uses = %d, want 2", got.Uses)
	}
	if got, _ := s.Get(ctx, b.ID); got.Uses != 1 {
		t.Errorf("Uses = %d, want 1", got.Uses)
	}
	if got, _ := s.Get(ctx, a.ID); got.Version != 1 || !got.UpdatedAt.Equal(at) {
		t.Errorf("reading changed the entry: version %d updated %v", got.Version, got.UpdatedAt)
	}
}

func runWrites(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	first := at.Add(-30 * time.Hour)
	e := save(t, s, "lime", "A", "d", "", dana, first)
	save(t, s, "acme", "B", "d", "", dana, at.Add(-2*time.Hour))
	save(t, s, "lime", "C", "d", "", lee, at.Add(-time.Hour))
	body := "x"
	if _, err := s.Update(ctx, e.ID, 1, revise(t, e, memory.Change{Body: &body}, at), dana, at.Add(-time.Hour)); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	w, err := s.WritesSince(ctx, dana.UserID, at.Add(-memory.WriteWindow))
	if err != nil {
		t.Fatalf("WritesSince() error: %v", err)
	}
	if w.Count != 2 || !w.Oldest.Equal(at.Add(-2*time.Hour)) {
		t.Errorf("WritesSince(dana, 24h) = %+v, want 2 writes in all namespaces, oldest 2h ago", w)
	}
	w, _ = s.WritesSince(ctx, dana.UserID, first.Add(-time.Hour))
	if w.Count != 3 || !w.Oldest.Equal(first) {
		t.Errorf("WritesSince(dana, earlier) = %+v, want 3", w)
	}
	w, _ = s.WritesSince(ctx, "u-nobody", first.Add(-time.Hour))
	if w.Count != 0 || !w.Oldest.IsZero() {
		t.Errorf("WritesSince(nobody) = %+v, want zero", w)
	}
	w, _ = s.WritesSince(ctx, dana.UserID, at)
	if w.Count != 0 {
		t.Errorf("WritesSince(dana, now) = %+v, want 0", w)
	}
}

// runWritesCountOnlySavesAndUpdates: a confirm, a retract and a move are
// versions but not writes, so they do not use up the daily limit.
func runWritesCountOnlySavesAndUpdates(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	e := save(t, s, "lime", "A", "d", "", dana, at.Add(-3*time.Hour))
	gone := save(t, s, "lime", "B", "d", "", dana, at.Add(-3*time.Hour))
	moved := save(t, s, "lime", "C", "d", "", dana, at.Add(-3*time.Hour))
	since := at.Add(-time.Hour)
	if _, err := s.Update(ctx, e.ID, 1, revise(t, e, memory.Change{Confirm: true}, at), dana, at.Add(-30*time.Minute)); err != nil {
		t.Fatalf("Update(confirm) error: %v", err)
	}
	if _, err := s.Retract(ctx, gone.ID, "wrong", dana, at.Add(-20*time.Minute)); err != nil {
		t.Fatalf("Retract() error: %v", err)
	}
	if _, err := s.Move(ctx, moved.ID, "acme", dana, at.Add(-10*time.Minute)); err != nil {
		t.Fatalf("Move() error: %v", err)
	}
	if w, _ := s.WritesSince(ctx, dana.UserID, since); w.Count != 0 || !w.Oldest.IsZero() {
		t.Errorf("WritesSince after a confirm, a retract and a move = %+v, want none", w)
	}
	body := "changed"
	cur, _ := s.Get(ctx, e.ID)
	if _, err := s.Update(ctx, e.ID, cur.Version, revise(t, cur, memory.Change{Body: &body}, at), dana, at.Add(-5*time.Minute)); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	if w, _ := s.WritesSince(ctx, dana.UserID, since); w.Count != 1 {
		t.Errorf("WritesSince after an update = %+v, want 1", w)
	}
}

// runWriteLimit: Within refuses inside the write, and many writes at once
// cannot all pass the limit.
func runWriteLimit(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	since := at.Add(-time.Hour)
	within := memory.Within(since, 3)
	for i := range 3 {
		if _, err := s.Insert(ctx, draft(t, "lime", memory.TypeReference, fmt.Sprintf("N%d", i), "d", ""), dana, at.Add(-time.Duration(30-i)*time.Minute), within); err != nil {
			t.Fatalf("Insert(%d) under the limit error: %v", i, err)
		}
	}
	_, err := s.Insert(ctx, draft(t, "lime", memory.TypeReference, "Over", "d", ""), dana, at, within)
	var lr *memory.WriteLimitError
	if !errors.As(err, &lr) || !lr.Oldest.Equal(at.Add(-30*time.Minute)) {
		t.Fatalf("Insert over the limit error = %v, want *WriteLimitError with the oldest write", err)
	}
	if found, _ := s.Search(ctx, memory.Query{Text: "Over", Namespaces: []string{"lime"}}); len(found) != 0 {
		t.Error("an insert refused by the limit was saved")
	}
	// The limit is per person.
	if _, err := s.Insert(ctx, draft(t, "lime", memory.TypeReference, "Lees", "d", ""), lee, at, within); err != nil {
		t.Errorf("dana's writes limited lee: %v", err)
	}
	// An update counts and is refused too.
	e := save(t, s, "lime", "Target", "d", "", lee, at.Add(-3*time.Hour))
	body := "x"
	if _, err := s.Update(ctx, e.ID, 1, revise(t, e, memory.Change{Body: &body}, at), dana, at, within); !errors.As(err, &lr) {
		t.Errorf("Update over the limit error = %v, want *WriteLimitError", err)
	}

	// Ten at once with room for two more: exactly two pass.
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	roomy := memory.Within(since, 5)
	for i := range 10 {
		wg.Go(func() {
			_, err := s.Insert(ctx, draft(t, "lime", memory.TypeReference, fmt.Sprintf("Race%d", i), "d", ""), dana, at.Add(time.Minute), roomy)
			if err == nil {
				mu.Lock()
				passed++
				mu.Unlock()
			} else if _, ok := errors.AsType[*memory.WriteLimitError](err); !ok {
				t.Errorf("racing Insert error: %v", err)
			}
		})
	}
	wg.Wait()
	if passed != 2 {
		t.Errorf("%d racing inserts passed a limit with room for 2", passed)
	}
}

func runRemoveNamespace(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	a := save(t, s, "lime", "Host", "desc", "doomedword", dana, at)
	b := save(t, s, "lime", "Other", "desc", "doomedword", dana, at, a.ID)
	keep := save(t, s, "acme", "Shared", "desc", "doomedword", dana, at, a.ID)
	later := at.Add(time.Hour)
	if _, err := s.Update(ctx, b.ID, 1, revise(t, b, memory.Change{Confirm: true}, later), lee, later); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	got, err := s.RemoveNamespace(ctx, "lime")
	if err != nil || got.Entries != 2 || got.Versions != 3 || got.Links != 2 {
		t.Fatalf("RemoveNamespace() = %+v, %v, want 2 entries, 3 versions and 2 links", got, err)
	}
	for _, id := range []int64{a.ID, b.ID} {
		_, err = s.Get(ctx, id)
		wantIs(t, "Get(removed)", err, registry.ErrNotFound)
		_, err = s.History(ctx, id)
		wantIs(t, "History(removed)", err, registry.ErrNotFound)
	}
	found, _ := s.Search(ctx, memory.Query{Text: "doomedword", Namespaces: []string{"lime", "acme"}})
	wantIDs(t, "Search after removal", found, keep.ID)
	if k, err := s.Get(ctx, keep.ID); err != nil || len(k.Links) != 0 {
		t.Errorf("Get(keep) = %+v, %v, want it kept with its link to a removed entry gone", k, err)
	}
	if w, _ := s.WritesSince(ctx, dana.UserID, at.Add(-time.Hour)); w.Count != 1 {
		t.Errorf("WritesSince = %d, want 1: the removed versions are gone", w.Count)
	}
	// Nothing left is not an error.
	if got, err := s.RemoveNamespace(ctx, "lime"); err != nil || got != (memory.Removed{}) {
		t.Errorf("RemoveNamespace(again) = %+v, %v, want nothing", got, err)
	}
	// A removal is not a purge: it leaves no purge record.
	if list, _ := s.Purges(ctx); len(list) != 0 {
		t.Errorf("Purges() = %+v, want none", list)
	}
}

func runPurge(t *testing.T, s memory.Store) {
	t.Helper()
	ctx := t.Context()
	victim := save(t, s, "lime", "Leaked", "d", "marker-text", dana, at)
	keep := save(t, s, "lime", "Keeper", "d", "", dana, at, victim.ID)
	body := "marker-text again"
	if _, err := s.Update(ctx, victim.ID, 1, revise(t, victim, memory.Change{Body: &body}, at), dana, at.Add(time.Hour)); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	when := at.Add(2 * time.Hour)
	p, err := s.Purge(ctx, victim.ID, when)
	if err != nil {
		t.Fatalf("Purge() error: %v", err)
	}
	if p.EntryID != victim.ID || p.Namespace != "lime" || p.Versions != 2 || !p.CreatedAt.Equal(at) || !p.At.Equal(when) {
		t.Errorf("Purge() = %+v", p)
	}
	_, err = s.Get(ctx, victim.ID)
	wantIs(t, "Get(purged)", err, registry.ErrNotFound)
	_, err = s.History(ctx, victim.ID)
	wantIs(t, "History(purged)", err, registry.ErrNotFound)
	if found, _ := s.Search(ctx, memory.Query{Text: "marker", Namespaces: []string{"lime"}}); len(found) != 0 {
		t.Errorf("Search found the purged entry")
	}
	if got, _ := s.Get(ctx, keep.ID); len(got.Links) != 0 {
		t.Errorf("Links = %v, want the link to the purged entry gone", got.Links)
	}
	if w, _ := s.WritesSince(ctx, dana.UserID, at.Add(-time.Hour)); w.Count != 1 {
		t.Errorf("WritesSince = %d, want 1: the purged versions are gone", w.Count)
	}
	list, err := s.Purges(ctx)
	if err != nil || len(list) != 1 || list[0].EntryID != victim.ID || list[0].Versions != 2 {
		t.Errorf("Purges() = %+v, %v, want one record", list, err)
	}
	next := save(t, s, "lime", "Next", "d", "", dana, at)
	if next.ID == victim.ID {
		t.Error("a purged id was handed out again")
	}
	_, err = s.Purge(ctx, victim.ID, when)
	wantIs(t, "Purge(again)", err, registry.ErrNotFound)
}
