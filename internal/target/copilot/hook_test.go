package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/petervlad/loadout/internal/target"
)

func TestHookFile(t *testing.T) {
	cmd := target.HookCommand{
		POSIX:      "loadout notices --hook copilot 2>/dev/null || true",
		PowerShell: "if (Get-Command loadout -ErrorAction SilentlyContinue) { loadout notices --hook copilot 2>$null }; exit 0",
	}
	got, err := New().WithHook(nil, cmd)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "hook.golden.json")
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
		t.Errorf("WithHook() =\n%s\nwant\n%s", got, want)
	}
	if again, err := New().WithHook(got, cmd); err != nil || string(again) != string(got) {
		t.Errorf("WithHook() over itself = %q, %v; want the same bytes", again, err)
	}
	if out, err := New().WithoutHook(got); err != nil || out != nil {
		t.Errorf("WithoutHook() = %q, %v; want nil: the file is loadout's", out, err)
	}
	if New().HookFile() != ".copilot/hooks/loadout.json" {
		t.Errorf("HookFile() = %q", New().HookFile())
	}
}
