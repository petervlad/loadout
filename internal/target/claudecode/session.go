package claudecode

import (
	"github.com/petervlad/loadout/internal/jsonedit"
	"github.com/petervlad/loadout/internal/target"
)

var _ target.SessionHook = (*Adapter)(nil)

// sessionOutput is what a SessionStart hook prints for Claude Code:
// systemMessage is shown to the person, additionalContext goes to Claude.
// Source: https://code.claude.com/docs/en/hooks
type sessionOutput struct {
	SystemMessage      string             `json:"systemMessage,omitempty"`
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// SessionOutput returns the hook's JSON line for msg, or nil when there is
// nothing to say.
func (*Adapter) SessionOutput(msg target.SessionMessage) ([]byte, error) {
	if msg.Empty() {
		return nil, nil
	}
	data, err := jsonedit.Marshal(sessionOutput{
		SystemMessage:      msg.Person,
		HookSpecificOutput: hookSpecificOutput{HookEventName: "SessionStart", AdditionalContext: msg.Agent},
	}, "", "")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
