package install

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
	"github.com/petervlad/loadout/internal/textdiff"
	"github.com/petervlad/loadout/internal/tier"
)

const sixLines = "one\ntwo\nthree\nfour\nfive\nsix"

// agentAt is lime/planner at version with prompt, and model as its hint
// when not empty.
func agentAt(t *testing.T, version, prompt, model string) *pack.Pack {
	t.Helper()
	hint := ""
	if model != "" {
		hint = "model: " + model + "\n"
	}
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: lime/planner\nkind: agent\nversion: " + version + "\ndescription: Plans.\n"),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: Plans.\n" + hint + "---\n" + prompt + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// skillAt is lime/checklist at version with files besides SKILL.md.
func skillAt(t *testing.T, version string, files map[string]string) *pack.Pack {
	t.Helper()
	all := map[string][]byte{
		pack.ManifestFile: []byte("name: lime/checklist\nkind: skill\nversion: " + version + "\ndescription: Checks.\n"),
		"SKILL.md":        []byte("---\nname: checklist\ndescription: Checks.\n---\nSteps.\n"),
	}
	for p, d := range files {
		all[p] = []byte(d)
	}
	p, err := pack.FromFiles(all)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f fixture) edit(t *testing.T, rel string, change func(string) string) {
	t.Helper()
	if err := os.WriteFile(f.path(rel), []byte(change(readFile(t, f.path(rel)))), 0o644); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
}

func (f fixture) install(t *testing.T, p *pack.Pack, models tier.Table) {
	t.Helper()
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope, Models: models}); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) merge(t *testing.T, base, next *pack.Pack, models tier.Table) MergeResult {
	t.Helper()
	res, err := f.in.Merge(t.Context(), base, next, Options{Scope: f.scope, Models: models})
	if err != nil {
		t.Fatalf("Merge() error: %v", err)
	}
	return res
}

func actionOf(res MergeResult, rel string) MergedFile {
	for _, f := range res.Files {
		if f.Rel == rel {
			return f
		}
	}
	return MergedFile{}
}

func TestMergeAgent(t *testing.T) {
	v1 := agentAt(t, "1.0.0", sixLines, "")
	tests := []struct {
		name     string
		edit     func(string) string
		next     *pack.Pack
		action   MergeAction
		contains []string
	}{
		{
			name:     "untouched takes the release",
			next:     agentAt(t, "2.0.0", strings.Replace(sixLines, "one", "ONE", 1), ""),
			action:   MergeTook,
			contains: []string{"lime/planner@2.0.0", "ONE"},
		},
		{
			name:     "both changed, different lines",
			edit:     func(s string) string { return strings.Replace(s, "six", "six, mine", 1) },
			next:     agentAt(t, "2.0.0", strings.Replace(sixLines, "one", "ONE", 1), ""),
			action:   MergeMerged,
			contains: []string{"lime/planner@2.0.0", "ONE", "six, mine"},
		},
		{
			name:   "both changed the same line",
			edit:   func(s string) string { return strings.Replace(s, "one", "uno", 1) },
			next:   agentAt(t, "2.0.0", strings.Replace(sixLines, "one", "ONE", 1), ""),
			action: MergeConflicted,
			contains: []string{
				"<<<<<<< yours (edited 1.0.0)\nlime/planner@1.0.0\nuno\n",
				"||||||| installed 1.0.0\n", "=======\nlime/planner@2.0.0\nONE\n>>>>>>> release 2.0.0\n",
			},
		},
		{
			name:     "you deleted it: it comes back",
			edit:     nil,
			next:     agentAt(t, "2.0.0", sixLines, ""),
			action:   MergeTook,
			contains: []string{"lime/planner@2.0.0"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.install(t, v1, tier.Table{})
			switch {
			case tc.edit != nil:
				f.edit(t, "alpha/planner.txt", tc.edit)
			case strings.HasPrefix(tc.name, "you deleted"):
				if err := os.Remove(f.path("alpha/planner.txt")); err != nil {
					t.Fatal(err)
				}
			}
			res := f.merge(t, v1, tc.next, tier.Table{})
			if got := actionOf(res, "alpha/planner.txt"); got.Action != tc.action {
				t.Errorf("alpha action = %+v, want %q", got, tc.action)
			}
			if got := actionOf(res, "beta/planner.txt"); got.Action != MergeTook {
				t.Errorf("beta (never edited) action = %q, want took the release", got.Action)
			}
			got := readFile(t, f.path("alpha/planner.txt"))
			for _, w := range tc.contains {
				if !strings.Contains(got, w) {
					t.Errorf("alpha file lacks %q:\n%s", w, got)
				}
			}
			if res.Entry.Version.String() != "2.0.0" {
				t.Errorf("entry version = %s, want 2.0.0", res.Entry.Version)
			}
		})
	}
}

func TestMergeSkillFiles(t *testing.T) {
	f := newFixture(t)
	v1 := skillAt(t, "1.0.0", map[string]string{"references/extra.md": "# Extra\n", "references/old.md": "# Old\n"})
	f.install(t, v1, tier.Table{})
	f.edit(t, "alpha/skills/checklist/references/extra.md", func(s string) string { return s + "mine\n" })
	f.edit(t, "alpha/skills/checklist/references/old.md", func(s string) string { return s + "mine\n" })
	v2 := skillAt(t, "2.0.0", map[string]string{"references/extra.md": "# Extra\n"})
	res := f.merge(t, v1, v2, tier.Table{})

	if got := actionOf(res, "alpha/skills/checklist/references/extra.md"); got.Action != MergeKept {
		t.Errorf("edited, unchanged in the release: %+v, want kept yours", got)
	}
	if got := readFile(t, f.path("alpha/skills/checklist/references/extra.md")); got != "# Extra\nmine\n" {
		t.Errorf("extra.md = %q, want the edit", got)
	}
	if len(res.Kept) != 1 || !strings.HasSuffix(res.Kept[0], filepath.FromSlash("alpha/skills/checklist/references/old.md")) {
		t.Errorf("Kept = %v, want the edited old.md the release dropped", res.Kept)
	}
	if !strings.Contains(readFile(t, f.path("alpha/skills/checklist/references/old.md")), "mine") {
		t.Error("the edited file the release dropped was deleted")
	}
	if _, err := os.Stat(f.path("beta/skills/checklist/references/old.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the unedited file the release dropped is still there")
	}
	if e, _ := f.locks.lock.Find(v2.ID(), f.scope); len(e.Files) != 4 {
		t.Errorf("lock has %d files, want next's 4 (old.md no longer managed)", len(e.Files))
	}
}

func TestMergeRefusals(t *testing.T) {
	v1 := skillAt(t, "1.0.0", nil)
	v2 := skillAt(t, "2.0.0", map[string]string{"references/new.md": "# New\n"})
	f := newFixture(t)
	f.install(t, v1, tier.Table{})
	if err := os.MkdirAll(f.path("alpha/skills/checklist/references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path("alpha/skills/checklist/references/new.md"), []byte("my own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.in.Merge(t.Context(), v1, v2, Options{Scope: f.scope})
	if ce, ok := errors.AsType[*ConflictError](err); !ok || ce.Conflicts[0].Reason != ReasonUnmanaged {
		t.Errorf("Merge() over a file loadout did not write = %v, want a conflict", err)
	}
	if got := readFile(t, f.path("alpha/skills/checklist/references/new.md")); got != "my own\n" {
		t.Error("a refused merge changed a file")
	}
	if _, err := f.in.Merge(t.Context(), v1, v2, Options{Scope: f.scope, Force: true}); !errors.Is(err, ErrMergeForce) {
		t.Errorf("Merge(--force) = %v, want ErrMergeForce", err)
	}
	g := newFixture(t)
	if _, err := g.in.Merge(t.Context(), v1, v2, Options{Scope: g.scope}); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Merge() of nothing installed = %v, want ErrNotInstalled", err)
	}
	g.install(t, v2, tier.Table{})
	if _, err := g.in.Merge(t.Context(), v1, skillAt(t, "3.0.0", nil), Options{Scope: g.scope}); err == nil || !strings.Contains(err.Error(), "2.0.0 is installed, not 1.0.0") {
		t.Errorf("Merge() from the wrong base = %v", err)
	}
}

func TestMergeBaseCheck(t *testing.T) {
	v1 := agentAt(t, "1.0.0", sixLines, "deep")
	v2 := agentAt(t, "2.0.0", sixLines, "deep")
	edit := func(s string) string { return strings.Replace(s, "three", "three, mine", 1) }

	// Installed without a table, merged with one: the base renders with
	// the empty table and the merge is clean.
	f := newFixture(t)
	f.install(t, v1, tier.Table{})
	f.edit(t, "alpha/planner.txt", edit)
	res := f.merge(t, v1, v2, alphaDeep(t, "big"))
	if got := actionOf(res, "alpha/planner.txt"); got.Action != MergeMerged || got.Whole {
		t.Errorf("alpha = %+v, want merged against the empty table's base", got)
	}
	if got := readFile(t, f.path("alpha/planner.txt")); !strings.Contains(got, "three, mine") || !strings.Contains(got, "model: big") {
		t.Errorf("alpha file:\n%s", got)
	}

	// Installed with one table, merged with another: no base is known, so
	// the file holds both versions whole, never a guess.
	g := newFixture(t)
	g.install(t, v1, alphaDeep(t, "small"))
	g.edit(t, "alpha/planner.txt", edit)
	res = g.merge(t, v1, v2, alphaDeep(t, "big"))
	if got := actionOf(res, "alpha/planner.txt"); got.Action != MergeConflicted || !got.Whole || got.Conflicts != 1 {
		t.Errorf("alpha = %+v, want one whole-file conflict", got)
	}
}

func TestMergeTooLongIsWhole(t *testing.T) {
	var long strings.Builder
	for i := range 5001 {
		fmt.Fprintf(&long, "line %d\n", i)
	}
	v1 := skillAt(t, "1.0.0", map[string]string{"references/long.md": long.String()})
	v2 := skillAt(t, "2.0.0", map[string]string{"references/long.md": long.String() + "more\n"})
	f := newFixture(t)
	f.install(t, v1, tier.Table{})
	f.edit(t, "alpha/skills/checklist/references/long.md", func(s string) string { return "mine\n" + s })
	res := f.merge(t, v1, v2, tier.Table{})
	if got := actionOf(res, "alpha/skills/checklist/references/long.md"); !got.Whole || got.Action != MergeConflicted {
		t.Errorf("long file = %+v, want whole", got)
	}
}

func TestMergeSharedFileOnce(t *testing.T) {
	scope := target.Scope{Kind: target.ScopeProject, Root: t.TempDir()}
	in := New(&memLocks{}, slog.New(slog.DiscardHandler), fakeRenderer{"alpha", "shared"}, fakeRenderer{"beta", "shared"})
	v1 := skillAt(t, "1.0.0", map[string]string{"references/extra.md": "a\nb\nc\nd\n"})
	if _, err := in.Install(t.Context(), v1, Options{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(scope.Root, "shared", "skills", "checklist", "references", "extra.md")
	if err := os.WriteFile(extra, []byte("a\nb\nc\nd, mine\n"), 0o644); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
	res, err := in.Merge(t.Context(), v1, skillAt(t, "2.0.0", map[string]string{"references/extra.md": "A\nb\nc\nd\n"}), Options{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(res.Files); n != 2 {
		t.Errorf("Merge() touched %d files, want SKILL.md and extra.md once each", n)
	}
	if got := actionOf(res, "shared/skills/checklist/references/extra.md"); got.Action != MergeMerged || got.Target != "alpha,beta" {
		t.Errorf("extra.md = %+v, want merged once for both", got)
	}
	if got := readFile(t, extra); got != "A\nb\nc\nd, mine\n" {
		t.Errorf("extra.md = %q", got)
	}
}

func TestMergeRealAdapters(t *testing.T) {
	scope := target.Scope{Kind: target.ScopeProject, Root: t.TempDir()}
	in := New(&memLocks{}, slog.New(slog.DiscardHandler), claudecode.New(), copilot.New())
	v1 := agentAt(t, "1.0.0", sixLines, "")
	if _, err := in.Install(t.Context(), v1, Options{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".claude/agents/planner.md", ".github/agents/planner.agent.md"} {
		path := filepath.Join(scope.Root, filepath.FromSlash(p))
		if err := os.WriteFile(path, []byte(strings.Replace(readFile(t, path), "five", "five, mine", 1)), 0o644); err != nil { //nolint:gosec // test temp dir
			t.Fatal(err)
		}
	}
	res, err := in.Merge(t.Context(), v1, agentAt(t, "2.0.0", strings.Replace(sixLines, "two", "TWO", 1), ""), Options{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".claude/agents/planner.md", ".github/agents/planner.agent.md"} {
		if got := actionOf(res, p); got.Action != MergeMerged {
			t.Errorf("%s = %+v, want merged with its own tool's base", p, got)
		}
		got := readFile(t, filepath.Join(scope.Root, filepath.FromSlash(p)))
		if !strings.Contains(got, "TWO") || !strings.Contains(got, "five, mine") || !strings.Contains(got, "lime/planner@2.0.0") {
			t.Errorf("%s:\n%s", p, got)
		}
	}
}

func TestMergeLockAndRerun(t *testing.T) {
	v1 := agentAt(t, "1.0.0", sixLines, "")
	v2 := agentAt(t, "2.0.0", strings.Replace(sixLines, "one", "ONE", 1), "")
	edit := func(s string) string { return strings.Replace(s, "six", "six, mine", 1) }

	f := newFixture(t)
	f.install(t, v1, tier.Table{})
	f.edit(t, "alpha/planner.txt", edit)
	f.merge(t, v1, v2, tier.Table{})
	merged := readFile(t, f.path("alpha/planner.txt"))

	// The merged file counts as an edit on 2.0.0.
	statuses, err := f.in.Status(t.Context())
	if err != nil || statuses[0].Entry.Version.String() != "2.0.0" || statuses[0].State() != StateModified {
		t.Errorf("Status() after merge = %+v, %v; want 2.0.0, modified", statuses, err)
	}
	if again, err := f.in.Rerenders(t.Context(), v2, Options{Scope: f.scope}); err != nil || again {
		t.Errorf("Rerenders() after merge = %v, %v; want false", again, err)
	}
	if edited, _ := f.in.Edited(t.Context(), statuses[0].Entry, nil); len(edited) != 1 || edited[0] != "alpha/planner.txt" {
		t.Errorf("Edited() after merge = %v, want the merged file", edited)
	}

	// A merge that wrote one file and stopped before the lock: running it
	// again gives the same result.
	g := newFixture(t)
	g.install(t, v1, tier.Table{})
	g.edit(t, "alpha/planner.txt", edit)
	g.edit(t, "beta/planner.txt", func(string) string { return readFile(t, f.path("beta/planner.txt")) })
	g.merge(t, v1, v2, tier.Table{})
	if got := readFile(t, g.path("alpha/planner.txt")); got != merged {
		t.Errorf("rerun after a partial write:\n%s\nwant\n%s", got, merged)
	}
}

func TestMarked(t *testing.T) {
	f := newFixture(t)
	v1 := agentAt(t, "1.0.0", sixLines, "")
	f.install(t, v1, tier.Table{})
	f.edit(t, "alpha/planner.txt", func(s string) string { return strings.Replace(s, "one", "uno", 1) })
	res := f.merge(t, v1, agentAt(t, "2.0.0", strings.Replace(sixLines, "one", "ONE", 1), ""), tier.Table{})
	paths := res.Conflicted()
	if got, err := f.in.Marked(t.Context(), res.Entry, paths, res.Labels); err != nil || len(got) != 1 {
		t.Fatalf("Marked() = %v, %v; want the conflicted file", got, err)
	}
	f.edit(t, "alpha/planner.txt", func(string) string { return "resolved\n" })
	if got, _ := f.in.Marked(t.Context(), res.Entry, paths, res.Labels); len(got) != 0 {
		t.Errorf("Marked() after resolving = %v", got)
	}
	if got, _ := f.in.Marked(t.Context(), res.Entry, paths, textdiff.Labels{Ours: "x", Base: "y", Theirs: "z"}); len(got) != 0 {
		t.Errorf("Marked() with other labels = %v", got)
	}
}
