// Package scripts_test runs the shell scripts in this folder.
package scripts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runDenylist runs denylist-check.sh over dir with the given list ("" for
// none) and returns its output and exit code.
func runDenylist(t *testing.T, list, dir string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sh", "denylist-check.sh", dir)
	cmd.Env = append(os.Environ(), "LOADOUT_DENYLIST="+list)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("running denylist-check.sh: %v", err)
	return "", -1
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDenylistCheck(t *testing.T) {
	tree := t.TempDir()
	gitIn(t, tree, "init", "-q")
	write(t, filepath.Join(tree, "a.md"), "nothing to see\n")
	write(t, filepath.Join(tree, ".gitignore"), "ignored.txt\n")
	write(t, filepath.Join(tree, "ignored.txt"), "Secretco is here\n")
	gitIn(t, tree, "add", ".")

	list := filepath.Join(t.TempDir(), "denylist.txt")
	write(t, list, "# comment\n\nsecretco\n(^|[^a-z])acmecorp([^a-z]|$)\n")

	tests := []struct {
		name     string
		list     string
		change   func()
		wantCode int
		wantOut  string
	}{
		{name: "no list set is skipped", list: "", wantCode: 0, wantOut: "skipped"},
		{name: "clean tree", list: list, wantCode: 0, wantOut: "clean"},
		{name: "missing list file", list: filepath.Join(t.TempDir(), "nope"), wantCode: 2, wantOut: "no such file"},
		{name: "match in a file, case-insensitive", list: list, change: func() { write(t, filepath.Join(tree, "docs", "b.md"), "we run at SecretCo\n") }, wantCode: 1, wantOut: "docs/b.md"},
		{name: "match in a file name", list: list, change: func() { write(t, filepath.Join(tree, "secretco-plan.md"), "plain\n") }, wantCode: 1, wantOut: "file name: secretco-plan.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.change != nil {
				tc.change()
				defer func() {
					_ = os.RemoveAll(filepath.Join(tree, "docs"))
					_ = os.Remove(filepath.Join(tree, "secretco-plan.md"))
				}()
			}
			out, code := runDenylist(t, tc.list, tree)
			if code != tc.wantCode || !strings.Contains(out, tc.wantOut) {
				t.Errorf("exit %d, output %q; want exit %d containing %q", code, out, tc.wantCode, tc.wantOut)
			}
			if strings.Contains(out, "acmecorp") {
				t.Errorf("output repeats the denylist: %q", out)
			}
		})
	}
}

// TestPublicTreeAgainstDenylist checks this checkout when the maintainer
// points LOADOUT_DENYLIST at their list; everyone else skips it.
func TestPublicTreeAgainstDenylist(t *testing.T) {
	list := os.Getenv("LOADOUT_DENYLIST")
	if list == "" {
		t.Skip("LOADOUT_DENYLIST is not set")
	}
	out, code := runDenylist(t, list, "..")
	if code != 0 {
		t.Errorf("the tree names something on the denylist (exit %d):\n%s", code, out)
	}
}
