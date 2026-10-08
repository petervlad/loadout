package syncstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/textdiff"
)

var cmpOpts = cmp.AllowUnexported(pack.ID{}, semver.Version{})

func user(home string) target.Scope { return target.Scope{Kind: target.ScopeUser, Root: home} }

func project(root string) target.Scope { return target.Scope{Kind: target.ScopeProject, Root: root} }

func behind(t *testing.T, name string, scope target.Scope, held ...string) Behind {
	t.Helper()
	id, err := pack.NewID("lime", name)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	b := Behind{ID: id, Scope: scope, Version: semver.New(1, 2, 0), Latest: semver.New(1, 3, 0), Since: at, Checked: at}
	for _, p := range held {
		b.Held = append(b.Held, Blocker{Path: p, Reason: "edited since loadout wrote it"})
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "state", "syncstate.yaml"), "/home/a")
	models := behind(t, "reviewer", project("/src/repo"))
	models.Latest, models.Models, models.Told = models.Version, "sha256:abc", true
	want := []Behind{behind(t, "planner", user("/home/a"), ".claude/agents/planner.md"), models}
	err := s.Update(t.Context(), func(st *State) error {
		for _, b := range want {
			st.Put(b)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if diff := cmp.Diff(want, got.All(), cmpOpts); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file mode = %o, want 600", perm)
	}
}

func TestLoadMissingOrEmptyIsEmpty(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.yaml"), empty} {
		st, err := NewStore(path, "/home/a").Load()
		if err != nil || len(st.All()) != 0 {
			t.Errorf("Load(%s) = %d records, %v; want empty", filepath.Base(path), len(st.All()), err)
		}
	}
}

func TestDamagedFileSaysWhatToDo(t *testing.T) {
	for _, content := range []string{"version: 2\n", "{{{ not yaml", "version: 1\nbehind:\n  - idx: 3\n", "version: 1\nunknown: true\n"} {
		path := filepath.Join(t.TempDir(), "syncstate.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		s := NewStore(path, "/home/a")
		if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), "is damaged") || !strings.Contains(err.Error(), "next loadout sync writes it again") {
			t.Errorf("Load(%q) error = %v, want a damaged-file message with the remedy", content, err)
		}
		// The next sync starts afresh rather than failing.
		if err := s.Update(t.Context(), func(st *State) error { st.Put(behind(t, "planner", user("/home/a"))); return nil }); err != nil {
			t.Errorf("Update over %q: %v", content, err)
		}
		if st, err := s.Load(); err != nil || len(st.All()) != 1 {
			t.Errorf("after Update over %q: %d records, %v", content, len(st.All()), err)
		}
	}
}

func TestUserScopeFollowsTheCurrentHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syncstate.yaml")
	if err := NewStore(path, "/home/old").Update(t.Context(), func(st *State) error {
		st.Put(behind(t, "planner", user("/home/old")))
		st.Put(behind(t, "reviewer", project("/src/repo")))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/home/old") {
		t.Errorf("the state file stores the user home:\n%s", data)
	}
	st, err := NewStore(path, "/home/new").Load()
	if err != nil {
		t.Fatal(err)
	}
	id := behind(t, "planner", user("/home/new")).ID
	if _, ok := st.Find(Key{ID: id, Scope: user("/home/new")}); !ok {
		t.Errorf("user-scope record not found under the new home: %+v", st.All())
	}
}

func TestPutReplacesByKeyAndDropRemoves(t *testing.T) {
	var st State
	first := behind(t, "planner", user("/home/a"))
	st.Put(first)
	newer := first
	newer.Latest = semver.New(1, 4, 0)
	st.Put(newer)
	st.Put(behind(t, "planner", project("/home/a"))) // same root, other kind
	if n := len(st.All()); n != 2 {
		t.Fatalf("len(All()) = %d, want 2", n)
	}
	if got, ok := st.Find(first.Key()); !ok || got.Latest.String() != "1.4.0" {
		t.Errorf("Find() = %v, %v; want latest 1.4.0", got.Latest, ok)
	}
	if !st.Drop(first.Key()) || st.Drop(first.Key()) {
		t.Error("Drop() should report true once, then false")
	}
	if _, ok := st.Find(Key{ID: first.ID, Scope: project("/home/a")}); !ok {
		t.Error("Drop(user) removed the project record")
	}
}

func TestRecordsAreCopies(t *testing.T) {
	var st State
	st.Put(behind(t, "planner", user("/home/a"), "a.md"))
	st.All()[0].Held[0].Path = "changed"
	got, _ := st.Find(behind(t, "planner", user("/home/a")).Key())
	if got.Held[0].Path != "a.md" {
		t.Error("changing a record from All() changed the state")
	}
}

func TestBehindHelpers(t *testing.T) {
	b := behind(t, "planner", user("/home/a"), "a.md", "b.md")
	if !b.IsHeld() || b.ModelsOnly() || !cmp.Equal(b.HeldPaths(), []string{"a.md", "b.md"}) {
		t.Errorf("held update: IsHeld %v ModelsOnly %v HeldPaths %v", b.IsHeld(), b.ModelsOnly(), b.HeldPaths())
	}
	b.Held, b.Latest = nil, b.Version
	if b.IsHeld() || !b.ModelsOnly() {
		t.Errorf("model change: IsHeld %v ModelsOnly %v", b.IsHeld(), b.ModelsOnly())
	}
}

func TestUpdateErrorSavesNothing(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "syncstate.yaml"), "/home/a")
	boom := errors.New("boom")
	if err := s.Update(t.Context(), func(st *State) error { st.Put(behind(t, "planner", user("/home/a"))); return boom }); !errors.Is(err, boom) {
		t.Fatalf("Update() error = %v, want boom", err)
	}
	if st, err := s.Load(); err != nil || len(st.All()) != 0 {
		t.Errorf("after a failed Update: %d records, %v", len(st.All()), err)
	}
}

func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "syncstate.yaml"), "/home/a")
	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			errs <- s.Update(t.Context(), func(st *State) error {
				st.Put(behind(t, fmt.Sprintf("agent-%d", i), user("/home/a")))
				return nil
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Update() error: %v", err)
		}
	}
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(st.All()); got != n {
		t.Errorf("after %d concurrent updates the state has %d records", n, got)
	}
}

func TestMergedRoundTrip(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "syncstate.yaml"), "/home/a")
	b := behind(t, "planner", user("/home/a"))
	m := Merged{
		ID: b.ID, Scope: user("/home/a"), Version: semver.New(1, 3, 0), Paths: []string{".claude/agents/planner.md"},
		Labels: textdiff.Labels{Ours: "yours (edited 1.2.0)", Base: "installed 1.2.0", Theirs: "release 1.3.0"}, At: b.Since,
	}
	if err := s.Update(t.Context(), func(st *State) error { st.PutMerged(m); st.Put(b); return nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/home/a") {
		t.Errorf("the state file stores the user home:\n%s", data)
	}
	st, err := NewStore(s.Path(), "/home/b").Load()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := st.FindMerged(Key{ID: m.ID, Scope: user("/home/b")})
	if !ok || got.Labels != m.Labels || got.Paths[0] != m.Paths[0] || len(st.All()) != 1 {
		t.Errorf("FindMerged() = %+v, %v; want the record under the new home, and the behind record kept", got, ok)
	}
	if !st.DropMerged(got.Key()) || len(st.AllMerged()) != 0 {
		t.Error("DropMerged() did not remove the record")
	}
}
