package target

// SessionHook is an Adapter whose tool runs a command when a session
// starts, so loadout can tell the person, and their agent, about updates
// their edits hold back. Each adapter knows its tool's output format, so a
// third tool is a third adapter, not a switch. Adapters stay pure: they
// turn values into bytes.
type SessionHook interface {
	// HookFile is the file, relative to the home folder and
	// slash-separated, that holds the tool's user-level hooks.
	HookFile() string
	// WithHook returns cur (nil when the file does not exist) with
	// loadout's hook running cmd, or cur itself when it is already there.
	// Everything else in cur is kept as it is; cur that cannot be read
	// safely is refused, never repaired.
	WithHook(cur []byte, cmd HookCommand) ([]byte, error)
	// WithoutHook returns cur without loadout's hook, or cur itself when
	// it has none; nil means the file held nothing else and goes.
	WithoutHook(cur []byte) ([]byte, error)
	// SessionOutput is what the hook prints for msg; nil prints nothing.
	SessionOutput(msg SessionMessage) ([]byte, error)
}

// HookCommand is the command line loadout's hook runs, for each shell. A
// tool with one command field takes PowerShell on Windows.
type HookCommand struct {
	POSIX      string
	PowerShell string
	// Windows is set when the hook runs on Windows.
	Windows bool
}

// SessionMessage is what to tell at session start: a line for the person,
// and a line for the agent. Both empty means there is nothing to say.
type SessionMessage struct {
	Person string
	Agent  string
}

// Empty reports whether there is nothing to say.
func (m SessionMessage) Empty() bool { return m.Person == "" && m.Agent == "" }

// Join returns m followed by o, each line joined with a space, so one hook
// output carries both.
func (m SessionMessage) Join(o SessionMessage) SessionMessage {
	join := func(a, b string) string {
		if a == "" || b == "" {
			return a + b
		}
		return a + " " + b
	}
	return SessionMessage{Person: join(m.Person, o.Person), Agent: join(m.Agent, o.Agent)}
}
