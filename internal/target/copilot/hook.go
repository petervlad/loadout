package copilot

import (
	"bytes"

	"github.com/petervlad/loadout/internal/jsonedit"
	"github.com/petervlad/loadout/internal/target"
)

// hookTimeout is the seconds Copilot gives the hook; notices answers
// within one, and a timeout fails open.
const hookTimeout = 5

// hookFile is loadout's own file of user-level hooks. Copilot CLI loads
// every *.json in ~/.copilot/hooks, and VS Code reads that folder too, so
// a file of loadout's own touches nothing of the person's.
// Sources: https://docs.github.com/en/copilot/reference/hooks-reference,
// https://code.visualstudio.com/docs/agent-customization/hooks
type hookFile struct {
	Version int                   `json:"version"`
	Hooks   map[string][]hookSpec `json:"hooks"`
}

type hookSpec struct {
	Type       string `json:"type"`
	Bash       string `json:"bash"`
	PowerShell string `json:"powershell"`
	TimeoutSec int    `json:"timeoutSec"`
}

// HookFile is the file loadout owns in Copilot's user hooks folder.
func (*Adapter) HookFile() string { return ".copilot/hooks/loadout.json" }

// WithHook returns the whole file running cmd: loadout owns it.
func (*Adapter) WithHook(cur []byte, cmd target.HookCommand) ([]byte, error) {
	out, err := jsonedit.Marshal(hookFile{Version: 1, Hooks: map[string][]hookSpec{
		"sessionStart": {{Type: "command", Bash: cmd.POSIX, PowerShell: cmd.PowerShell, TimeoutSec: hookTimeout}},
	}}, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	if bytes.Equal(cur, out) {
		return cur, nil
	}
	return out, nil
}

// WithoutHook returns nil: the file holds only loadout's hook.
func (*Adapter) WithoutHook([]byte) ([]byte, error) { return nil, nil }
