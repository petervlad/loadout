package scripts_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestNoticesUpToDate runs the same check CI runs: THIRD_PARTY_NOTICES.md
// matches the modules, vendored libraries and borrowed skills, every borrowed
// skill has its LICENSE, and apple-design is not in the tree.
func TestNoticesUpToDate(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	cmd := exec.CommandContext(t.Context(), "sh", "notices.sh", "--check")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("notices.sh --check: %v\n%s", err, out)
	}
}

func TestNoticesUsage(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "sh", "notices.sh", "--bogus").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "usage") {
		t.Errorf("notices.sh --bogus = %v, %q; want a usage error", err, out)
	}
}

// TestReleaseArchivesCarryTheLicence: the archives must ship the licence and
// the notices, as the licences of what is linked in require.
func TestReleaseArchivesCarryTheLicence(t *testing.T) {
	cfg, err := os.ReadFile("../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- LICENSE\n", "- THIRD_PARTY_NOTICES.md\n"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf(".goreleaser.yaml archives do not list %q", strings.TrimSpace(want))
		}
	}
}
