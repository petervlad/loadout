package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/petervlad/loadout/internal/target"
)

func TestSessionOutput(t *testing.T) {
	msg := target.SessionMessage{
		Person: "loadout: 1 update is held back by your edits (payments/planner 1.3.0). Run loadout status.",
		Agent:  "loadout could not update payments/planner to 1.3.0 because the user edited the installed files.",
	}
	got, err := New().SessionOutput(msg)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "session.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("SessionOutput() =\n%s\nwant\n%s", got, want)
	}
	if out, err := New().SessionOutput(target.SessionMessage{}); err != nil || out != nil {
		t.Errorf("SessionOutput(nothing) = %q, %v; want nil", out, err)
	}
}
