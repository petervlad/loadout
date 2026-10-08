package install

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

// fakeRenderer writes "<dir>/<agent>.txt" containing the prompt and version,
// and skills under "<dir>/skills".
type fakeRenderer struct{ name, dir string }

func (r fakeRenderer) Name() string { return r.name }

func (r fakeRenderer) SkillsDir(target.ScopeKind) (string, error) {
	return path.Join(r.dir, "skills"), nil
}

func (r fakeRenderer) RenderAgent(def agent.Definition, run tier.Choice, _ target.Scope, stamp target.Stamp) (target.File, error) {
	data := stamp.String() + "\n" + def.Prompt + "\n"
	if run.Model != "" {
		data += "model: " + run.Model + "\n"
	}
	return target.File{Path: path.Join(r.dir, def.Name+".txt"), Data: []byte(data)}, nil
}

// memLocks is an in-memory LockStore.
type memLocks struct {
	mu   sync.Mutex
	lock *lock.Lock
}

func (m *memLocks) Load() (*lock.Lock, error) {
	l := &lock.Lock{}
	if m.lock != nil {
		for _, e := range m.lock.Entries() {
			l.Put(e)
		}
	}
	return l, nil
}

func (m *memLocks) Update(_ context.Context, fn func(*lock.Lock) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.Load()
	if err != nil {
		return err
	}
	if err := fn(l); err != nil {
		return err
	}
	m.lock = l
	return nil
}

type fixture struct {
	in    *Installer
	locks *memLocks
	scope target.Scope
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	locks := &memLocks{}
	log := slog.New(slog.DiscardHandler)
	in := New(locks, log, fakeRenderer{"alpha", "alpha"}, fakeRenderer{"beta", "beta"})
	return fixture{in: in, locks: locks, scope: target.Scope{Kind: target.ScopeUser, Root: t.TempDir()}}
}

func (f fixture) path(rel string) string { return filepath.Join(f.scope.Root, rel) }

func makePack(t *testing.T, version, prompt string, targets ...string) *pack.Pack {
	t.Helper()
	manifest := "name: lime/planner\nkind: agent\nversion: " + version + "\ndescription: Plans.\n"
	if len(targets) > 0 {
		manifest += "targets: [" + strings.Join(targets, ", ") + "]\n"
	}
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte(manifest),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: Plans.\n---\n" + prompt + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallWritesEveryTargetAndLocks(t *testing.T) {
	f := newFixture(t)
	res, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope, Source: "/src/planner"})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}
	for _, p := range []string{"alpha/planner.txt", "beta/planner.txt"} {
		if got := readFile(t, f.path(p)); !strings.Contains(got, "lime/planner@1.0.0") {
			t.Errorf("%s = %q, want the stamp", p, got)
		}
	}
	if res.Unchanged {
		t.Error("first install reported Unchanged")
	}
	if res.Entry.Policy != lock.DefaultPolicy {
		t.Errorf("Policy = %q, want %q", res.Entry.Policy, lock.DefaultPolicy)
	}
	if got := len(f.locks.lock.Entries()); got != 1 {
		t.Errorf("lock has %d entries, want 1", got)
	}
}

func TestInstallTargetSelection(t *testing.T) {
	tests := []struct {
		name      string
		supported []string
		requested []string
		wantFiles []string
		wantErr   string
	}{
		{name: "all by default", wantFiles: []string{"alpha", "beta"}},
		{name: "package targets", supported: []string{"beta"}, wantFiles: []string{"beta"}},
		{name: "requested", requested: []string{"alpha"}, wantFiles: []string{"alpha"}},
		{name: "unknown", requested: []string{"gamma"}, wantErr: "unknown target"},
		{name: "unsupported", supported: []string{"beta"}, requested: []string{"alpha"}, wantErr: "does not support"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1", tc.supported...), Options{Scope: f.scope, Targets: tc.requested})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Install() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Install() error: %v", err)
			}
			for _, dir := range []string{"alpha", "beta"} {
				_, statErr := os.Stat(f.path(dir + "/planner.txt"))
				want := strings.Contains(strings.Join(tc.wantFiles, ","), dir)
				if got := statErr == nil; got != want {
					t.Errorf("%s written = %v, want %v", dir, got, want)
				}
			}
		})
	}
}

func TestReinstallSameVersionIsUnchanged(t *testing.T) {
	f := newFixture(t)
	p := makePack(t, "1.0.0", "v1")
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	res, err := f.in.Install(t.Context(), p, Options{Scope: f.scope, Policy: lock.PolicyAuto})
	if err != nil {
		t.Fatalf("second Install() error: %v", err)
	}
	if !res.Unchanged {
		t.Error("reinstall of the same version was not Unchanged")
	}
	if e, _ := f.locks.lock.Find(p.ID(), f.scope); e.Policy != lock.PolicyAuto {
		t.Errorf("policy after reinstall = %q, want auto", e.Policy)
	}
}

func TestUpgradeKeepsPolicyAndOverwritesOwnFiles(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope, Policy: lock.PolicyAuto}); err != nil {
		t.Fatal(err)
	}
	res, err := f.in.Install(t.Context(), makePack(t, "1.1.0", "v2"), Options{Scope: f.scope})
	if err != nil {
		t.Fatalf("upgrade error: %v", err)
	}
	if got := readFile(t, f.path("alpha/planner.txt")); !strings.Contains(got, "v2") {
		t.Errorf("after upgrade file = %q, want v2", got)
	}
	if res.Entry.Policy != lock.PolicyAuto {
		t.Errorf("policy after upgrade = %q, want auto (kept)", res.Entry.Policy)
	}
}

func TestConflicts(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, f fixture)
		wantReason Reason
	}{
		{
			name: "unmanaged file",
			setup: func(t *testing.T, f fixture) {
				t.Helper()
				if err := os.MkdirAll(f.path("alpha"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.path("alpha/planner.txt"), []byte("hand written"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: ReasonUnmanaged,
		},
		{
			name: "edited after install",
			setup: func(t *testing.T, f fixture) {
				t.Helper()
				if _, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope}); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.path("alpha/planner.txt"), []byte("my tweak"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: ReasonModified,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)

			_, err := f.in.Install(t.Context(), makePack(t, "2.0.0", "v2"), Options{Scope: f.scope})
			ce, ok := errors.AsType[*ConflictError](err)
			if !ok {
				t.Fatalf("Install() error = %v, want *ConflictError", err)
			}
			if len(ce.Conflicts) != 1 || ce.Conflicts[0].Reason != tc.wantReason {
				t.Errorf("conflicts = %+v, want one %q", ce.Conflicts, tc.wantReason)
			}
			if got, _ := os.ReadFile(f.path("beta/planner.txt")); strings.Contains(string(got), "v2") {
				t.Error("a conflict still let other files be written; install must be all or nothing")
			}

			if _, err := f.in.Install(t.Context(), makePack(t, "2.0.0", "v2"), Options{Scope: f.scope, Force: true}); err != nil {
				t.Fatalf("Install(Force) error: %v", err)
			}
			if got := readFile(t, f.path("alpha/planner.txt")); !strings.Contains(got, "v2") {
				t.Errorf("after --force file = %q, want v2", got)
			}
		})
	}
}

func TestIdenticalUnmanagedFileIsNotAConflict(t *testing.T) {
	f := newFixture(t)
	p := makePack(t, "1.0.0", "v1")
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	f.locks.lock = nil // lock lost, files still on disk
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
		t.Errorf("reinstall over identical files error: %v", err)
	}
}

func TestStatus(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path("alpha/planner.txt"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.path("beta/planner.txt")); err != nil {
		t.Fatal(err)
	}

	statuses, err := f.in.Status(t.Context())
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(Status()) = %d, want 1", len(statuses))
	}
	got := map[string]State{}
	for _, fs := range statuses[0].Files {
		got[fs.Target] = fs.State
	}
	if got["alpha"] != StateModified || got["beta"] != StateMissing {
		t.Errorf("file states = %v, want alpha modified, beta missing", got)
	}
	if statuses[0].State() != StateMissing {
		t.Errorf("State() = %q, want missing (the worst)", statuses[0].State())
	}
}

func TestInstallRejectsBadOptions(t *testing.T) {
	f := newFixture(t)
	p := makePack(t, "1.0.0", "v1")
	bad := []Options{
		{Scope: target.Scope{Kind: "global", Root: f.scope.Root}},
		{Scope: target.Scope{Kind: target.ScopeUser, Root: "relative/path"}},
		{Scope: f.scope, Policy: "sometimes"},
	}
	for _, opts := range bad {
		if _, err := f.in.Install(t.Context(), p, opts); err == nil {
			t.Errorf("Install(%+v) = nil error, want error", opts)
		}
	}
}

func TestSetPolicy(t *testing.T) {
	f := newFixture(t)
	p := makePack(t, "1.0.0", "v1")
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	if n, err := f.in.SetPolicy(t.Context(), p.ID(), "", lock.PolicyPin); err != nil || n != 1 {
		t.Fatalf("SetPolicy() = %d, %v; want 1, nil", n, err)
	}
	if e, _ := f.locks.lock.Find(p.ID(), f.scope); e.Policy != lock.PolicyPin {
		t.Errorf("policy = %q, want pin", e.Policy)
	}
	other, err := pack.NewID("lime", "ghost")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.SetPolicy(t.Context(), other, "", lock.PolicyPin); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("SetPolicy(unknown) error = %v, want ErrNotInstalled", err)
	}
	if _, err := f.in.SetPolicy(t.Context(), p.ID(), "", "sometimes"); err == nil {
		t.Error("SetPolicy(invalid) succeeded")
	}
}

func TestSymlinkedFolders(t *testing.T) {
	tests := []struct {
		name string
		link func(t *testing.T, home, elsewhere string)
	}{
		{"tool folder is a symlink", func(t *testing.T, home, elsewhere string) {
			t.Helper()
			if err := os.Symlink(elsewhere, filepath.Join(home, "alpha")); err != nil {
				t.Fatal(err)
			}
		}},
		{"both tool folders are symlinks", func(t *testing.T, home, elsewhere string) {
			t.Helper()
			for _, dir := range []string{"alpha", "beta"} {
				target := filepath.Join(elsewhere, dir)
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(home, dir)); err != nil {
					t.Fatal(err)
				}
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			elsewhere := t.TempDir() // e.g. a dotfiles repo
			tc.link(t, f.scope.Root, elsewhere)
			if _, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope}); err != nil {
				t.Fatalf("Install() through a symlinked folder error: %v", err)
			}
			statuses, err := f.in.Status(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got := statuses[0].State(); got != StateOK {
				t.Errorf("State() = %q, want ok", got)
			}
		})
	}
}

func TestSymlinkedAgentFileIsNeverFollowed(t *testing.T) {
	f := newFixture(t)
	mine := filepath.Join(t.TempDir(), "my-planner.md") // the user's own agent in their toolkit repo
	if err := os.WriteFile(mine, []byte("my own agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.path("alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mine, f.path("alpha/planner.txt")); err != nil {
		t.Fatal(err)
	}

	_, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope})
	ce, ok := errors.AsType[*ConflictError](err)
	if !ok || len(ce.Conflicts) != 1 || ce.Conflicts[0].Reason != ReasonSymlink {
		t.Fatalf("Install() over a symlink error = %v, want a symlink conflict", err)
	}

	if _, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope, Force: true}); err != nil {
		t.Fatalf("Install(Force) error: %v", err)
	}
	if got := readFile(t, mine); got != "my own agent" {
		t.Errorf("the symlink target was overwritten: %q", got)
	}
	info, err := os.Lstat(f.path("alpha/planner.txt"))
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("after --force the path is still a symlink (%v)", err)
	}
}

func TestScopeRootIsASymlink(t *testing.T) {
	f := newFixture(t)
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(f.scope.Root, link); err != nil {
		t.Fatal(err)
	}
	scope := target.Scope{Kind: target.ScopeUser, Root: link}
	if _, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: scope}); err != nil {
		t.Fatalf("Install() with a symlinked scope root error: %v", err)
	}
	if got := readFile(t, f.path("alpha/planner.txt")); !strings.Contains(got, "v1") {
		t.Errorf("file not written through the symlinked root: %q", got)
	}
}

func TestUninstall(t *testing.T) {
	f := newFixture(t)
	p := makePack(t, "1.0.0", "v1")
	if _, err := f.in.Install(t.Context(), p, Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path("beta/planner.txt"), []byte("my edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := f.in.Uninstall(t.Context(), p.ID(), "", false)
	if err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}
	if _, err := os.Stat(f.path("alpha/planner.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unedited file still there: %v", err)
	}
	if got := readFile(t, f.path("beta/planner.txt")); got != "my edit" {
		t.Errorf("edited file = %q, want it kept", got)
	}
	if len(removed) != 1 || len(removed[0].Kept) != 1 {
		t.Errorf("Uninstall() = %+v, want one scope with one kept file", removed)
	}
	if _, ok := f.locks.lock.Find(p.ID(), f.scope); ok {
		t.Error("lock entry still present")
	}
	if _, err := f.in.Uninstall(t.Context(), p.ID(), "", false); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("second Uninstall() error = %v, want ErrNotInstalled", err)
	}
}

func TestForceNeverTakesOverAnotherPackage(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), makePack(t, "1.0.0", "v1"), Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	other, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: other/planner\nkind: agent\nversion: 1.0.0\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: d\n---\nsomeone else\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.in.Install(t.Context(), other, Options{Scope: f.scope, Force: true})
	ce, ok := errors.AsType[*ConflictError](err)
	if !ok || ce.Conflicts[0].Reason != ReasonOtherPackage {
		t.Fatalf("Install(other team, Force) error = %v, want an other-package conflict", err)
	}
}

func makeSkill(t *testing.T, id string) *pack.Pack {
	t.Helper()
	_, name, _ := strings.Cut(id, "/")
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile:     []byte("name: " + id + "\nkind: skill\nversion: 1.0.0\ndescription: Checks.\n"),
		"SKILL.md":            []byte("---\nname: " + name + "\ndescription: Checks.\n---\nSteps.\n"),
		"references/extra.md": []byte("# Extra\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStatusNamesTheSkillFolder(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(t.Context(), makeSkill(t, "lime/checklist"), Options{Scope: f.scope}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(t.Context(), makeSkill(t, "base/checklist"), Options{Scope: f.scope, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	statuses, err := f.in.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("len(Status()) = %d, want 2", len(statuses))
	}
	for _, st := range statuses {
		if len(st.Files) != 4 {
			t.Errorf("%s: %d files, want 2 per target", st.Entry.ID, len(st.Files))
		}
		for _, fs := range st.Files {
			want := f.path(filepath.Join(fs.Target, "skills", "checklist"))
			if fs.Unit != want {
				t.Errorf("%s %s: Unit = %q, want %q", st.Entry.ID, fs.Path, fs.Unit, want)
			}
		}
	}
}
