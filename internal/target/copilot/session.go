package copilot

import (
	"github.com/petervlad/loadout/internal/jsonedit"
	"github.com/petervlad/loadout/internal/target"
)

var _ target.SessionHook = (*Adapter)(nil)

// sessionOutput is what a sessionStart hook prints for Copilot. Copilot CLI
// reads only a top-level additionalContext, VS Code reads it under
// hookSpecificOutput, so it is written both ways. VS Code shows a
// top-level systemMessage to the person; Copilot CLI shows nothing, so the
// agent gets the text and is asked to mention it. (Copilot CLI's progress
// lines would show, but VS Code reads stdout as one JSON object.)
// Sources: https://docs.github.com/en/copilot/reference/hooks-reference,
// https://code.visualstudio.com/docs/agent-customization/hooks
type sessionOutput struct {
	AdditionalContext  string             `json:"additionalContext"`
	SystemMessage      string             `json:"systemMessage,omitempty"`
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// SessionOutput returns the hook's JSON line for msg, or nil when there is
// nothing for the agent to pass on.
func (*Adapter) SessionOutput(msg target.SessionMessage) ([]byte, error) {
	if msg.Agent == "" {
		return nil, nil
	}
	data, err := jsonedit.Marshal(sessionOutput{
		AdditionalContext:  msg.Agent,
		SystemMessage:      msg.Person,
		HookSpecificOutput: hookSpecificOutput{HookEventName: "SessionStart", AdditionalContext: msg.Agent},
	}, "", "")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
