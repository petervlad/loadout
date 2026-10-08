package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// session runs loadout commands against a throwaway home directory.
type session struct {
	t    *testing.T
	home string
	wd   string
	// env adds to the environment, which is HOME only otherwise.
	env map[string]string
	// version is the CLI's version; empty is an unknown build, which
	// never hears that a newer loadout is out.
	version string
	// terminal says stderr is a terminal (a person, not a script).
	terminal bool
}

// exec runs one loadout command with the session's environment and build.
func (s *session) exec(stdin string, args ...string) (stdout, stderr string, code int) {
	s.t.Helper()
	var out, errOut bytes.Buffer
	env := map[string]string{"HOME": s.home}
	maps.Copy(env, s.env)
	ext := realExternals()
	ext.version = s.version
	ext.environ = func() []string {
		var kv []string
		for k, v := range env {
			kv = append(kv, k+"="+v)
		}
		return kv
	}
	terminal := s.terminal
	ext.stderrTerminal = func() bool { return terminal }
	code = runWith(s.t.Context(), args, strings.NewReader(stdin), &out, &errOut, func(k string) string { return env[k] }, s.wd, ext)
	// Every older test runs an unknown build: none may start hearing about
	// a newer loadout.
	if heard := errOut.String(); s.version == "" && (strings.Contains(heard, "newer loadout") || strings.Contains(heard, " is out (you have ")) {
		s.t.Errorf("loadout %s told an unknown build about a newer loadout:\n%s", strings.Join(args, " "), errOut.String())
	}
	return out.String(), errOut.String(), code
}

// newSession returns a Claude Code user: their home has a ~/.claude folder.
func newSession(t *testing.T) *session {
	t.Helper()
	s := &session{t: t, home: t.TempDir(), wd: t.TempDir()}
	if err := os.Mkdir(filepath.Join(s.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *session) run(args ...string) (stdout, stderr string, code int) {
	s.t.Helper()
	return s.exec("", args...)
}

func (s *session) mustRun(args ...string) string {
	s.t.Helper()
	out, errOut, code := s.run(args...)
	if code != 0 {
		s.t.Fatalf("loadout %s: exit %d\nstderr: %s", strings.Join(args, " "), code, errOut)
	}
	return out
}

func TestNewInstallStatus(t *testing.T) {
	s := newSession(t)
	s.mustRun("new", "agent", "lime/planner")
	out := s.mustRun("install", "planner")

	installed := filepath.Join(s.home, ".claude", "agents", "planner.md")
	if !strings.Contains(out, installed) {
		t.Errorf("install output = %q, want the installed path %s", out, installed)
	}
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("installed file: %v", err)
	}
	if !strings.Contains(string(data), "managed by loadout: lime/planner@0.1.0") {
		t.Errorf("installed file is missing the stamp:\n%s", data)
	}

	if out := s.mustRun("install", "planner"); !strings.Contains(out, "nothing to do") {
		t.Errorf("second install output = %q, want nothing to do", out)
	}

	status := s.mustRun("status")
	if !strings.Contains(status, "lime/planner") || !strings.Contains(status, " ok ") {
		t.Errorf("status = %q, want lime/planner ok", status)
	}

	if err := os.WriteFile(installed, []byte("my edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := s.mustRun("status"); !strings.Contains(status, "modified") {
		t.Errorf("status after edit = %q, want modified", status)
	}
}

func TestInstallRefusesToClobberAnEditedFile(t *testing.T) {
	s := newSession(t)
	s.mustRun("new", "agent", "lime/planner")
	s.mustRun("install", "planner")
	installed := filepath.Join(s.home, ".claude", "agents", "planner.md")
	if err := os.WriteFile(installed, []byte("my edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A new version of the package, then install again.
	manifest := filepath.Join(s.wd, "planner", "loadout.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, bytes.Replace(data, []byte("0.1.0"), []byte("0.2.0"), 1), 0o644); err != nil { //nolint:gosec // path is inside the test temp dir
		t.Fatal(err)
	}

	_, errOut, code := s.run("install", "planner")
	if code == 0 || !strings.Contains(errOut, "edited since loadout wrote it") {
		t.Fatalf("install over an edited file: exit %d, stderr %q; want a conflict", code, errOut)
	}
	if got, _ := os.ReadFile(installed); string(got) != "my edit" {
		t.Error("the edited file was overwritten")
	}
	s.mustRun("install", "planner", "--force")
	if got, _ := os.ReadFile(installed); !strings.Contains(string(got), "0.2.0") {
		t.Error("--force did not install the new version")
	}
}

func TestProjectScopeUsesGitRoot(t *testing.T) {
	s := newSession(t)
	s.mustRun("new", "agent", "lime/planner")
	pkg := filepath.Join(s.wd, "planner")

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "src", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	s.wd = sub
	s.mustRun("install", pkg, "--scope", "project")

	if _, err := os.Stat(filepath.Join(repo, ".claude", "agents", "planner.md")); err != nil {
		t.Errorf("project install not at the git root: %v", err)
	}
}

func TestLogFileRecordsRuns(t *testing.T) {
	s := newSession(t)
	s.mustRun("new", "agent", "lime/planner")
	s.mustRun("install", "planner")
	data, err := os.ReadFile(filepath.Join(s.home, ".loadout", "loadout.log"))
	if err != nil {
		t.Fatalf("log file: %v", err)
	}
	for _, want := range []string{`"msg":"run started"`, `"msg":"installed"`, `"msg":"run finished"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log file is missing %s", want)
		}
	}
}

func TestErrorsExitNonZero(t *testing.T) {
	s := newSession(t)
	for _, args := range [][]string{
		{"install", "does-not-exist"},
		{"new", "agent", "not-a-valid-id"},
		{"install", "."},
		{"status", "extra-arg"},
	} {
		if _, errOut, code := s.run(args...); code == 0 || !strings.HasPrefix(errOut, "error:") {
			t.Errorf("loadout %v: exit %d, stderr %q; want exit 1 with an error line", args, code, errOut)
		}
	}
}

func TestVerboseShowsDebugOnTerminal(t *testing.T) {
	s := newSession(t)
	_, errOut, _ := s.run("status", "-v")
	if !strings.Contains(errOut, "run finished") {
		t.Errorf("-v stderr = %q, want debug records", errOut)
	}
	_, errOut, _ = s.run("status")
	if strings.Contains(errOut, "run finished") {
		t.Errorf("default stderr = %q, want no debug records", errOut)
	}
}

func TestNoAIToolInstalled(t *testing.T) {
	s := newSession(t)
	if err := os.Remove(filepath.Join(s.home, ".claude")); err != nil {
		t.Fatal(err)
	}
	s.mustRun("new", "agent", "lime/planner")
	if _, errOut, code := s.run("install", "planner"); code == 0 || !strings.Contains(errOut, "no supported AI tool found") {
		t.Errorf("install with no AI tool: exit %d, stderr %q", code, errOut)
	}
	s.mustRun("install", "planner", "--target", "claude")
	if _, err := os.Stat(filepath.Join(s.home, ".claude", "agents", "planner.md")); err != nil {
		t.Errorf("--target claude did not install: %v", err)
	}
}

func TestProjectScopeNeedsAGitRepo(t *testing.T) {
	s := newSession(t)
	s.mustRun("new", "agent", "lime/planner")
	if _, errOut, code := s.run("install", "planner", "--scope", "project"); code == 0 || !strings.Contains(errOut, "git repository") {
		t.Errorf("project install outside git: exit %d, stderr %q", code, errOut)
	}
}

// TestParallelInstallsAreAllTracked is the regression test for parallel
// installs losing lock entries.
func TestParallelInstallsAreAllTracked(t *testing.T) {
	s := newSession(t)
	const n = 10
	for i := range n {
		s.mustRun("new", "agent", fmt.Sprintf("lime/agent-%d", i))
	}
	var wg sync.WaitGroup
	codes := make(chan int, n)
	for i := range n {
		wg.Go(func() {
			_, _, code := s.run("install", fmt.Sprintf("agent-%d", i))
			codes <- code
		})
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 0 {
			t.Errorf("a parallel install exited %d", code)
		}
	}
	out := s.mustRun("status")
	if got := strings.Count(out, "lime/agent-"); got != n {
		t.Errorf("status lists %d agents after %d parallel installs:\n%s", got, n, out)
	}
}

func TestUninstall(t *testing.T) {
	s := newSession(t)
	s.mustRun("new", "agent", "lime/planner")
	s.mustRun("install", "planner")
	if out := s.mustRun("uninstall", "lime/planner"); !strings.Contains(out, "uninstalled lime/planner") {
		t.Errorf("uninstall output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".claude", "agents", "planner.md")); !os.IsNotExist(err) {
		t.Errorf("agent file still there: %v", err)
	}
	if out := s.mustRun("status"); !strings.Contains(out, "nothing installed") {
		t.Errorf("status after uninstall = %q", out)
	}
}

// TestVersionNamesTheRelease: the first line of loadout version names the
// release a build is or was built after, as scripts read it.
func TestVersionNamesTheRelease(t *testing.T) {
	tests := []struct{ version, want string }{
		{"v0.4.0", "loadout 0.4.0"},
		{"v0.4.0-3-gabc1234", "loadout after 0.4.0 (from abc1234)"},
		{"v0.4.1-0.20261004192155-b42ddb9b6aba", "loadout after 0.4.0 (built 4 Oct 2026 from b42ddb9)"},
		{"dev", "loadout dev"},
	}
	for _, tc := range tests {
		home := t.TempDir()
		var out, errOut bytes.Buffer
		getenv := func(k string) string { return map[string]string{"HOME": home}[k] }
		code := runWith(t.Context(), []string{"version"}, strings.NewReader(""), &out, &errOut, getenv, t.TempDir(), externals{version: tc.version})
		if first, _, _ := strings.Cut(out.String(), "\n"); code != 0 || first != tc.want {
			t.Errorf("version %s: exit %d, first line %q, want %q (stderr %q)", tc.version, code, first, tc.want, errOut.String())
		}
	}
}
