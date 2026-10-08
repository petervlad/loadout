package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/petervlad/loadout/internal/jsonedit"
	"github.com/petervlad/loadout/internal/target"
)

// hookMarker is in the command of loadout's hook and nobody else's; a
// handler whose command holds it and the word loadout is loadout's, with or
// without a path to the binary.
const hookMarker = "notices --hook " + Name

// hookTimeout is the seconds Claude Code gives the hook; notices answers
// within one.
const hookTimeout = 5

// handler is one hook handler in settings.json.
type handler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Shell   string `json:"shell,omitempty"`
	Timeout int    `json:"timeout"`
}

// matcherGroup is one entry of hooks.SessionStart.
type matcherGroup struct {
	Matcher string    `json:"matcher"`
	Hooks   []handler `json:"hooks"`
}

func handlerFor(cmd target.HookCommand) handler {
	if cmd.Windows {
		return handler{Type: "command", Command: cmd.PowerShell, Shell: "powershell", Timeout: hookTimeout}
	}
	return handler{Type: "command", Command: cmd.POSIX, Timeout: hookTimeout}
}

// HookFile is Claude Code's user settings: there is no folder for user
// hooks, so loadout adds one entry to this file.
// Source: https://code.claude.com/docs/en/hooks
func (*Adapter) HookFile() string { return ".claude/settings.json" }

// WithHook adds one matcher group to hooks.SessionStart, for new sessions
// only ("startup"), running cmd. An older loadout handler is replaced in
// place. Every other byte of cur stays as it is.
func (*Adapter) WithHook(cur []byte, cmd target.HookCommand) ([]byte, error) {
	h := handlerFor(cmd)
	group := matcherGroup{Matcher: "startup", Hooks: []handler{h}}
	if len(bytes.TrimSpace(cur)) == 0 {
		out, err := jsonedit.Marshal(map[string]any{"hooks": map[string]any{"SessionStart": []matcherGroup{group}}}, "", "  ")
		return append(out, '\n'), err
	}
	s, err := parseSettings(cur)
	if err != nil {
		return nil, err
	}
	switch {
	case !s.hasHooks:
		return jsonedit.Append(cur, s.root, "hooks", map[string]any{"SessionStart": []matcherGroup{group}})
	case !s.hasSessionStart:
		return jsonedit.Append(cur, s.hooks, "SessionStart", []matcherGroup{group})
	}
	ours, found := s.ours(cur)
	if !found {
		return jsonedit.Append(cur, s.sessionStart, "", group)
	}
	var have handler
	if err := json.Unmarshal(ours.node.Raw(cur), &have); err == nil && have == h {
		return cur, nil
	}
	return jsonedit.Replace(cur, ours.node, h)
}

// WithoutHook removes loadout's handlers, then a matcher group, the
// SessionStart list and the hooks object each only when that left it
// empty. nil means the file held nothing else.
func (*Adapter) WithoutHook(cur []byte) ([]byte, error) {
	if len(bytes.TrimSpace(cur)) == 0 {
		return cur, nil
	}
	for {
		s, err := parseSettings(cur)
		if err != nil {
			return nil, err
		}
		if !s.hasSessionStart {
			return cur, nil
		}
		ours, found := s.ours(cur)
		if !found {
			return cur, nil
		}
		switch {
		case ours.handlers.Len() > 1:
			cur = jsonedit.Remove(cur, ours.handlers, ours.index)
		case s.sessionStart.Len() > 1:
			cur = jsonedit.Remove(cur, s.sessionStart, ours.group)
		case s.hooks.Len() > 1:
			cur = jsonedit.Remove(cur, s.hooks, s.hooks.Index("SessionStart"))
		case s.root.Len() > 1:
			cur = jsonedit.Remove(cur, s.root, s.root.Index("hooks"))
		default:
			return nil, nil
		}
	}
}

// settings is a parsed settings.json, down to hooks.SessionStart.
type settings struct {
	root, hooks, sessionStart jsonedit.Node
	hasHooks, hasSessionStart bool
}

// errSettings explains a settings.json loadout will not change.
func errSettings(what string) error {
	return fmt.Errorf("%s; loadout leaves the file as it is", what)
}

func parseSettings(data []byte) (settings, error) {
	root, err := jsonedit.Parse(data)
	if err != nil {
		return settings{}, errSettings(fmt.Sprintf("~/.claude/settings.json is %v", err))
	}
	if root.Kind != '{' {
		return settings{}, errSettings("~/.claude/settings.json is not a JSON object")
	}
	s := settings{root: root}
	if s.hooks, s.hasHooks = root.Get("hooks"); !s.hasHooks {
		return s, nil
	}
	if s.hooks.Kind != '{' {
		return settings{}, errSettings(`"hooks" in ~/.claude/settings.json is not an object`)
	}
	if s.sessionStart, s.hasSessionStart = s.hooks.Get("SessionStart"); s.hasSessionStart && s.sessionStart.Kind != '[' {
		return settings{}, errSettings(`"hooks.SessionStart" in ~/.claude/settings.json is not a list`)
	}
	return s, nil
}

// found is where loadout's handler is: matcher group, its handlers and
// the handler's index among them.
type found struct {
	group    int
	handlers jsonedit.Node
	index    int
	node     jsonedit.Node
}

// ours finds the first handler loadout wrote. Groups and handlers of
// another shape are someone else's and are skipped.
func (s settings) ours(data []byte) (found, bool) {
	for gi, g := range s.sessionStart.Elems {
		hs, ok := g.Get("hooks")
		if g.Kind != '{' || !ok || hs.Kind != '[' {
			continue
		}
		for hi, h := range hs.Elems {
			if c, ok := h.Get("command"); ok && isOurs(c.String(data)) {
				return found{group: gi, handlers: hs, index: hi, node: h}, true
			}
		}
	}
	return found{}, false
}

func isOurs(command string) bool {
	return strings.Contains(command, hookMarker) && strings.Contains(command, "loadout")
}
