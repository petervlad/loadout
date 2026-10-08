package notice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/textdiff"
)

type fakeFiles struct {
	entries []lock.Entry
	edited  []string // paths still edited, in any install
	marked  []string // paths still holding conflict markers
}

func (f fakeFiles) Marked(_ context.Context, _ lock.Entry, paths []string, _ textdiff.Labels) ([]string, error) {
	var out []string
	for _, p := range paths {
		if slices.Contains(f.marked, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f fakeFiles) Entries(context.Context) ([]lock.Entry, error) { return f.entries, nil }

func (f fakeFiles) Edited(_ context.Context, e lock.Entry, only []string) ([]string, error) {
	var out []string
	for _, r := range e.Files {
		if slices.Contains(f.edited, r.Path) && (len(only) == 0 || slices.Contains(only, r.Path)) {
			out = append(out, r.Path)
		}
	}
	return out, nil
}

type fakeMemory struct {
	st  syncstate.State
	err error
}

func (m *fakeMemory) Load() (syncstate.State, error) { return m.st, m.err }

func (m *fakeMemory) Update(_ context.Context, fn func(*syncstate.State) error) error {
	return fn(&m.st)
}

var (
	home    = target.Scope{Kind: target.ScopeUser, Root: "/home/a"}
	billing = target.Scope{Kind: target.ScopeProject, Root: "/src/billing"}
	checked = time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
)

const agentPath = ".claude/agents/planner.md"

func id(t *testing.T) pack.ID {
	t.Helper()
	i, err := pack.ParseID("payments/planner")
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func installed(t *testing.T, version semver.Version, scope target.Scope) lock.Entry {
	t.Helper()
	return lock.Entry{ID: id(t), Version: version, Scope: scope, Files: []lock.FileRecord{{Target: "claude", Path: agentPath}}}
}

func held(t *testing.T, scope target.Scope) syncstate.Behind {
	t.Helper()
	return syncstate.Behind{
		ID: id(t), Scope: scope, Version: semver.New(1, 2, 0), Latest: semver.New(1, 3, 0), Checked: checked,
		Held: []syncstate.Blocker{{Path: agentPath, Reason: string(install.ReasonModified)}},
	}
}

func TestList(t *testing.T) {
	v12, v13 := semver.New(1, 2, 0), semver.New(1, 3, 0)
	models := held(t, home)
	models.Latest, models.Models = models.Version, "sha256:new"
	tests := []struct {
		name    string
		entries []lock.Entry
		record  syncstate.Behind
		edited  []string
		filter  Filter
		want    string // the sentence, or "" for no notice
	}{
		{name: "still held", entries: []lock.Entry{installed(t, v12, home)}, record: held(t, home), edited: []string{agentPath}, want: "1.3.0 held back: you edited planner.md (claude)"},
		{name: "edits reverted: available", entries: []lock.Entry{installed(t, v12, home)}, record: held(t, home), want: "1.3.0 available: loadout install payments/planner"},
		{name: "caught up, or forced", entries: []lock.Entry{installed(t, v13, home)}, record: held(t, home), edited: []string{agentPath}},
		{name: "uninstalled", record: held(t, home), edited: []string{agentPath}},
		{name: "pinned since: the quiet way out", entries: []lock.Entry{func() lock.Entry { e := installed(t, v12, home); e.Policy = lock.PolicyPin; return e }()}, record: held(t, home), edited: []string{agentPath}},
		{name: "model settings held", entries: []lock.Entry{installed(t, v12, home)}, record: models, edited: []string{agentPath}, want: "model settings held back: you edited planner.md (claude)"},
		{name: "model settings applied", entries: []lock.Entry{func() lock.Entry { e := installed(t, v12, home); e.Models = "sha256:new"; return e }()}, record: models},
		{name: "project matched by the folder", entries: []lock.Entry{installed(t, v12, billing)}, record: held(t, billing), edited: []string{agentPath}, filter: Filter{Dir: "/src/billing/cmd"}, want: "1.3.0 held back: you edited planner.md (claude)"},
		{name: "project not matched by another folder", entries: []lock.Entry{installed(t, v12, billing)}, record: held(t, billing), edited: []string{agentPath}, filter: Filter{Dir: "/src/billing-other"}},
		{name: "user scope matches any folder", entries: []lock.Entry{installed(t, v12, home)}, record: held(t, home), edited: []string{agentPath}, filter: Filter{Dir: "/elsewhere"}, want: "1.3.0 held back: you edited planner.md (claude)"},
		{name: "kinds filter", entries: []lock.Entry{installed(t, v12, home)}, record: held(t, home), filter: Filter{Kinds: []Kind{Held}}},
		{name: "available project command", entries: []lock.Entry{installed(t, v12, billing)}, record: held(t, billing), want: "1.3.0 available: loadout install payments/planner --scope project"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var st syncstate.State
			st.Put(tc.record)
			svc := New(&fakeMemory{st: st}, fakeFiles{entries: tc.entries, edited: tc.edited})
			got, err := svc.List(t.Context(), tc.filter)
			if err != nil {
				t.Fatalf("List() error: %v", err)
			}
			switch {
			case tc.want == "" && len(got) != 0:
				t.Errorf("List() = %+v, want nothing", got)
			case tc.want != "" && (len(got) != 1 || got[0].Sentence() != tc.want):
				t.Errorf("List() = %+v, want one notice %q", got, tc.want)
			case tc.want != "" && !got[0].Checked.Equal(checked):
				t.Errorf("Checked = %v, want %v", got[0].Checked, checked)
			}
		})
	}
}

func TestDamagedStateIsEmpty(t *testing.T) {
	svc := New(&fakeMemory{err: errors.New("syncstate.yaml is damaged")}, fakeFiles{})
	if got, err := svc.List(t.Context(), Filter{}); err != nil || len(got) != 0 {
		t.Errorf("List() = %v, %v; want nothing and no error", got, err)
	}
}

func TestMostPressingFirst(t *testing.T) {
	other, err := pack.ParseID("payments/aaa")
	if err != nil {
		t.Fatal(err)
	}
	available := held(t, home)
	available.ID, available.Held = other, nil
	var st syncstate.State
	st.Put(available)
	st.Put(held(t, home))
	a := installed(t, semver.New(1, 2, 0), home)
	b := a
	b.ID = other
	got, err := New(&fakeMemory{st: st}, fakeFiles{entries: []lock.Entry{a, b}, edited: []string{agentPath}}).List(t.Context(), Filter{})
	if err != nil || len(got) != 2 || got[0].Kind != Held || got[1].Kind != Available {
		t.Errorf("List() = %+v, %v; want held before available", got, err)
	}
}

func TestSentences(t *testing.T) {
	n := Notice{
		Kind: Held, ID: id(t), Scope: home, Latest: semver.New(2, 0, 0), Edits: true,
		Files: []string{"SKILL.md (claude, copilot)", "a.md (claude, copilot)", "b.md (claude, copilot)", "c.md (claude, copilot)", "d.md (claude, copilot)"},
	}
	tests := []struct {
		name string
		n    Notice
		want string
	}{
		{"held, many files", n, "2.0.0 held back: you edited SKILL.md (claude, copilot), a.md (claude, copilot), b.md (claude, copilot) and 2 more"},
		{"held by files loadout did not write", Notice{Kind: Held, ID: id(t), Latest: semver.New(2, 0, 0), Files: []string{"notes.md"}}, "2.0.0 held back: notes.md in the way"},
		{"model settings available", Notice{Kind: Available, ID: id(t), Scope: home, Models: true}, "model settings changed: loadout install payments/planner"},
		{"built-in skill", Notice{Kind: Available, ID: id(t), Latest: semver.New(2, 0, 0), Builtin: true}, "2.0.0 available: loadout setup --skills"},
	}
	for _, tc := range tests {
		if got := tc.n.Sentence(); got != tc.want {
			t.Errorf("%s: Sentence() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMessage(t *testing.T) {
	one := Notice{Kind: Held, ID: id(t), Latest: semver.New(1, 3, 0)}
	if m := Message([]Notice{{Kind: Available, ID: id(t)}}); !m.Empty() {
		t.Errorf("Message(available only) = %+v, want empty", m)
	}
	m := Message([]Notice{one})
	if m.Person != "loadout: 1 update is held back by your edits (payments/planner 1.3.0). Run loadout status." {
		t.Errorf("Person = %q", m.Person)
	}
	for _, want := range []string{"could not update payments/planner to 1.3.0", "never run loadout install --force without asking", "loadout status"} {
		if !strings.Contains(m.Agent, want) {
			t.Errorf("Agent = %q, want %q", m.Agent, want)
		}
	}
	runbooks, err := pack.ParseID("payments/runbooks")
	if err != nil {
		t.Fatal(err)
	}
	two := Message([]Notice{one, {Kind: Held, ID: runbooks, Latest: semver.New(2, 0, 0), Models: true}})
	if !strings.Contains(two.Person, "2 updates are held back by your edits (payments/planner 1.3.0 and payments/runbooks model settings)") {
		t.Errorf("Person = %q", two.Person)
	}
}

// entriesCounter counts how often the lock is read.
type entriesCounter struct {
	files fakeFiles
	reads *int
}

func (c entriesCounter) Entries(ctx context.Context) ([]lock.Entry, error) {
	*c.reads++
	return c.files.Entries(ctx)
}

func (c entriesCounter) Marked(ctx context.Context, e lock.Entry, paths []string, l textdiff.Labels) ([]string, error) {
	return c.files.Marked(ctx, e, paths, l)
}

func (c entriesCounter) Edited(ctx context.Context, e lock.Entry, only []string) ([]string, error) {
	return c.files.Edited(ctx, e, only)
}

func TestNothingHeldSkipsTheLock(t *testing.T) {
	available := held(t, home)
	available.Held = nil
	var st syncstate.State
	st.Put(available)
	reads := 0
	svc := New(&fakeMemory{st: st}, entriesCounter{files: fakeFiles{entries: []lock.Entry{installed(t, semver.New(1, 2, 0), home)}}, reads: &reads})
	if got, err := svc.List(t.Context(), Filter{Kinds: []Kind{Held}}); err != nil || len(got) != 0 || reads != 0 {
		t.Errorf("List(held) = %v, %v with %d lock reads; want nothing and no read", got, err, reads)
	}
	if got, err := svc.List(t.Context(), Filter{}); err != nil || len(got) != 1 || reads != 1 {
		t.Errorf("List(all) = %v, %v with %d lock reads; want the available one", got, err, reads)
	}
}

func TestUnresolved(t *testing.T) {
	v13 := semver.New(1, 3, 0)
	labels := textdiff.Labels{Ours: "yours (edited 1.2.0)", Base: "installed 1.2.0", Theirs: "release 1.3.0"}
	mem := &fakeMemory{}
	svc := New(mem, fakeFiles{entries: []lock.Entry{installed(t, v13, home)}, marked: []string{agentPath}})
	m := syncstate.Merged{ID: id(t), Scope: home, Version: v13, Labels: labels, Paths: []string{agentPath}, At: checked}
	if err := svc.RecordMerge(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	got, err := svc.List(t.Context(), Filter{Kinds: []Kind{Held, Unresolved}})
	if err != nil || len(got) != 1 || got[0].Sentence() != "1.3.0 merged: resolve the conflicts in planner.md (claude)" {
		t.Fatalf("List() = %+v, %v", got, err)
	}
	msg := Message(got)
	if !strings.Contains(msg.Person, "merge conflicts to resolve in planner.md (claude) (payments/planner 1.3.0)") || !strings.Contains(msg.Agent, "offer to help the user resolve them") {
		t.Errorf("Message() = %+v", msg)
	}

	// Resolved, or another version installed since: nothing to say.
	resolved := New(mem, fakeFiles{entries: []lock.Entry{installed(t, v13, home)}})
	if got, _ := resolved.List(t.Context(), Filter{}); len(got) != 0 {
		t.Errorf("List() after resolving = %+v", got)
	}
	// Markers still there after another version went in: still reported.
	moved := New(mem, fakeFiles{entries: []lock.Entry{installed(t, semver.New(1, 4, 0), home)}, marked: []string{agentPath}})
	if got, _ := moved.List(t.Context(), Filter{}); len(got) != 1 || got[0].Kind != Unresolved {
		t.Errorf("List() after another install with the markers left = %+v, want them reported", got)
	}
	if left, err := moved.Unresolved(t.Context(), m.ID, home); err != nil || len(left) != 1 {
		t.Errorf("Unresolved() = %v, %v; want planner.md", left, err)
	}

	// A clean merge never forgets markers that are still there...
	clean := m
	clean.Paths = nil
	if err := moved.RecordMerge(t.Context(), clean); err != nil || len(mem.st.AllMerged()) != 1 {
		t.Errorf("RecordMerge(clean) over unresolved markers left %d records, %v; want the record kept", len(mem.st.AllMerged()), err)
	}
	// ...and forgets the record once they are resolved.
	if err := resolved.RecordMerge(t.Context(), clean); err != nil || len(mem.st.AllMerged()) != 0 {
		t.Errorf("RecordMerge(clean) after resolving left %d records, %v", len(mem.st.AllMerged()), err)
	}
}

// TestProjectMatchedThroughALink: a tool may report the session's folder
// through a symbolic link (/tmp and /private/tmp on macOS, a linked work
// folder); the project install it holds still matches, either way round.
func TestProjectMatchedThroughALink(t *testing.T) {
	realRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realRoot, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "work")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, root, dir string }{
		{name: "folder through the link", root: realRoot, dir: filepath.Join(link, "cmd")},
		{name: "project recorded through the link", root: link, dir: filepath.Join(realRoot, "cmd")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope := target.Scope{Kind: target.ScopeProject, Root: tc.root}
			var st syncstate.State
			st.Put(held(t, scope))
			svc := New(&fakeMemory{st: st}, fakeFiles{entries: []lock.Entry{installed(t, semver.New(1, 2, 0), scope)}, edited: []string{agentPath}})
			got, err := svc.List(t.Context(), Filter{Dir: tc.dir})
			if err != nil || len(got) != 1 {
				t.Errorf("List() = %+v, %v; want the project's notice", got, err)
			}
		})
	}
}

// TestMessageOffersMergeOnlyWhereItWorks: install --merge takes a newer
// release over the person's edits; it refuses new model settings (no
// release to merge), loadout's own skills and files loadout did not write,
// so the agent is not told to run it for those.
func TestMessageOffersMergeOnlyWhereItWorks(t *testing.T) {
	tests := []struct {
		name   string
		notice Notice
		merge  bool
	}{
		{name: "edited, newer release", notice: Notice{Kind: Held, ID: id(t), Latest: semver.New(1, 3, 0), Edits: true}, merge: true},
		{name: "new model settings", notice: Notice{Kind: Held, ID: id(t), Latest: semver.New(1, 2, 0), Models: true, Edits: true}},
		{name: "loadout's own skill", notice: Notice{Kind: Held, ID: id(t), Latest: semver.New(1, 3, 0), Edits: true, Builtin: true}},
		{name: "files loadout did not write", notice: Notice{Kind: Held, ID: id(t), Latest: semver.New(1, 3, 0)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Message([]Notice{tc.notice})
			if got := strings.Contains(m.Agent, "--merge"); got != tc.merge {
				t.Errorf("Agent = %q; offers --merge: %v, want %v", m.Agent, got, tc.merge)
			}
		})
	}
}
