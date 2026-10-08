package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
)

func testEntry(t *testing.T, name string, scope target.Scope) Entry {
	t.Helper()
	id, err := pack.NewID("lime", name)
	if err != nil {
		t.Fatal(err)
	}
	return Entry{
		ID:      id,
		Version: semver.New(1, 0, 0),
		Digest:  "sha256:abc",
		Source:  "/src/" + name,
		Policy:  PolicyNotify,
		Scope:   scope,
		Files:   []FileRecord{{Target: "claude", Path: ".claude/agents/" + name + ".md", SHA256: "def"}},
	}
}

func user(home string) target.Scope { return target.Scope{Kind: target.ScopeUser, Root: home} }

func project(root string) target.Scope { return target.Scope{Kind: target.ScopeProject, Root: root} }

var cmpOpts = cmp.AllowUnexported(pack.ID{}, semver.Version{})

func TestUpdateRoundTrip(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "state", "lock.yaml"), "/home/a")
	want := []Entry{testEntry(t, "planner", user("/home/a")), testEntry(t, "reviewer", project("/src/repo"))}
	err := s.Update(t.Context(), func(l *Lock) error {
		for _, e := range want {
			l.Put(e)
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
	if diff := cmp.Diff(want, got.Entries(), cmpOpts); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateErrorSavesNothing(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "lock.yaml"), "/home/a")
	boom := errors.New("boom")
	err := s.Update(t.Context(), func(l *Lock) error {
		l.Put(testEntry(t, "planner", user("/home/a")))
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update() error = %v, want boom", err)
	}
	l, err := s.Load()
	if err != nil || len(l.Entries()) != 0 {
		t.Errorf("after a failed Update the lock has %d entries (%v), want 0", len(l.Entries()), err)
	}
}

// TestConcurrentUpdatesLoseNothing is the regression test for parallel
// installs dropping lock entries. Each goroutine opens its own lock file
// descriptor, as separate processes would.
func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "lock.yaml"), "/home/a")
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			errs <- s.Update(t.Context(), func(l *Lock) error {
				l.Put(testEntry(t, fmt.Sprintf("agent-%d", i), user("/home/a")))
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
	l, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(l.Entries()); got != n {
		t.Errorf("after %d concurrent updates the lock has %d entries", n, got)
	}
}

// TestUserScopeFollowsTheCurrentHome is the regression test for a copied
// ~/.loadout writing into the old home.
func TestUserScopeFollowsTheCurrentHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.yaml")
	if err := NewStore(path, "/home/old").Update(t.Context(), func(l *Lock) error {
		l.Put(testEntry(t, "planner", user("/home/old")))
		l.Put(testEntry(t, "reviewer", project("/src/repo")))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/home/old") {
		t.Errorf("the lock file stores the user home:\n%s", data)
	}

	l, err := NewStore(path, "/home/new").Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries() {
		want := "/home/new"
		if e.Scope.Kind == target.ScopeProject {
			want = "/src/repo"
		}
		if e.Scope.Root != want {
			t.Errorf("%s root = %q, want %q", e.ID, e.Scope.Root, want)
		}
	}
}

func TestLoadMissingOrEmptyIsEmpty(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.yaml"), empty} {
		l, err := NewStore(path, "/home/a").Load()
		if err != nil || len(l.Entries()) != 0 {
			t.Errorf("Load(%s) = %d entries, %v; want empty", filepath.Base(path), len(l.Entries()), err)
		}
	}
}

func TestDamagedLockExplainsRecovery(t *testing.T) {
	for _, content := range []string{"version: 99\nentries: []\n", "{{{ not yaml", "version: 1\nentries:\n  - idx: 3\n"} {
		path := filepath.Join(t.TempDir(), "lock.yaml")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := NewStore(path, "/home/a").Load()
		if err == nil || !strings.Contains(err.Error(), "is damaged") || !strings.Contains(err.Error(), "move it aside") {
			t.Errorf("Load(%q) error = %v, want a damaged-file message with a recovery hint", content, err)
		}
	}
}

func TestPutFindRemove(t *testing.T) {
	l := &Lock{}
	l.Put(testEntry(t, "planner", user("/home/a")))
	updated := testEntry(t, "planner", user("/home/a"))
	updated.Version = semver.New(2, 0, 0)
	l.Put(updated)
	// Same root, different kind: a project scope in a home that is a git repo.
	l.Put(testEntry(t, "planner", project("/home/a")))

	if n := len(l.Entries()); n != 2 {
		t.Fatalf("len(Entries()) = %d, want 2 (user and project are separate)", n)
	}
	if got, ok := l.Find(updated.ID, user("/home/a")); !ok || got.Version.String() != "2.0.0" {
		t.Errorf("Find(user) = %v, %v, want version 2.0.0", got.Version, ok)
	}
	if !l.Remove(updated.ID, user("/home/a")) {
		t.Error("Remove() = false, want true")
	}
	if _, ok := l.Find(updated.ID, user("/home/a")); ok {
		t.Error("entry still found after Remove")
	}
	if _, ok := l.Find(updated.ID, project("/home/a")); !ok {
		t.Error("Remove(user) also removed the project entry")
	}
}

func TestOwner(t *testing.T) {
	l := &Lock{}
	e := testEntry(t, "planner", user("/home/a"))
	l.Put(e)
	if got, ok := l.Owner("/home/a", ".claude/agents/planner.md"); !ok || got.ID != e.ID {
		t.Errorf("Owner() = %v, %v, want %v", got.ID, ok, e.ID)
	}
	if _, ok := l.Owner("/other", ".claude/agents/planner.md"); ok {
		t.Error("Owner() found a file under a different root")
	}
}

func TestEntriesAreCopies(t *testing.T) {
	l := &Lock{}
	l.Put(testEntry(t, "planner", user("/home/a")))
	l.Entries()[0].Files[0].SHA256 = "changed"
	if l.Entries()[0].Files[0].SHA256 == "changed" {
		t.Error("mutating Entries() changed the lock")
	}
}

func TestLayerFieldsRoundTripAndStayOptional(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.yaml")
	s := NewStore(path, "/home/a")
	plain := testEntry(t, "planner", project("/src/a"))
	layered := testEntry(t, "planner", project("/src/b"))
	layered.Depth = 1
	layered.Shadowed = []Shadow{{Target: "copilot", Path: ".github/agents/planner.md", By: plain.ID}}
	layered.Files = nil
	err := s.Update(t.Context(), func(l *Lock) error {
		l.Put(plain)
		l.Put(layered)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]Entry{plain, layered}, got.Entries(), cmpOpts, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "depth:"); n != 1 {
		t.Errorf("lock has %d depth fields, want 1 (omitted at depth 0):\n%s", n, data)
	}
}

func TestEntryTargets(t *testing.T) {
	id, err := pack.NewID("lime", "planner")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		entry        Entry
		want         []string
		wantShadowed bool
	}{
		{"none", Entry{}, nil, false},
		{"files only", Entry{Files: []FileRecord{{Target: "claude"}, {Target: "copilot"}}}, []string{"claude", "copilot"}, false},
		{"files and shadows", Entry{Files: []FileRecord{{Target: "copilot"}}, Shadowed: []Shadow{{Target: "claude", By: id}}}, []string{"copilot", "claude"}, false},
		{"shadows only", Entry{Shadowed: []Shadow{{Target: "claude", By: id}, {Target: "claude", By: id}}}, []string{"claude"}, true},
		{"shared file", Entry{Files: []FileRecord{{Target: "claude,copilot"}, {Target: "claude,copilot"}}}, []string{"claude", "copilot"}, false},
		{"shared and single", Entry{Files: []FileRecord{{Target: "copilot"}, {Target: "claude,copilot"}}}, []string{"copilot", "claude"}, false},
		{"shared shadow", Entry{Shadowed: []Shadow{{Target: "claude,copilot", By: id}}}, []string{"claude", "copilot"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.Targets(); !cmp.Equal(got, tc.want) {
				t.Errorf("Targets() = %v, want %v", got, tc.want)
			}
			if got := tc.entry.FullyShadowed(); got != tc.wantShadowed {
				t.Errorf("FullyShadowed() = %v, want %v", got, tc.wantShadowed)
			}
		})
	}
}

func TestSkillUnitRoundTripsAndStaysOptional(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.yaml")
	s := NewStore(path, "/home/a")
	agent := testEntry(t, "planner", project("/src/a"))
	skill := testEntry(t, "notes", project("/src/a"))
	skill.Files = []FileRecord{
		{Target: "claude,copilot", Path: ".claude/skills/notes/SKILL.md", SHA256: "a", Unit: ".claude/skills/notes"},
		{Target: "claude,copilot", Path: ".claude/skills/notes/references/b.md", SHA256: "b", Unit: ".claude/skills/notes"},
	}
	err := s.Update(t.Context(), func(l *Lock) error {
		l.Put(agent)
		l.Put(skill)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]Entry{skill, agent}, got.Entries(), cmpOpts); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "unit:"); n != 2 {
		t.Errorf("lock has %d unit fields, want 2 (omitted for agent files):\n%s", n, data)
	}
}
