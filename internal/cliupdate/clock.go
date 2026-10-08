package cliupdate

// clock names who was told, each counted on its own: a person at a
// terminal (scripts share it), agents (commands they run and the session
// hook share it), and the push notifier.
type clock int

const (
	clockPerson clock = iota + 1
	clockAgent
	clockPush
)

var clockNames = map[clock]string{
	clockPerson: "person",
	clockAgent:  "agent",
	clockPush:   "push",
}

func (c clock) String() string { return clockNames[c] }

func clockNamed(name string) (clock, bool) {
	for c, n := range clockNames {
		if n == name {
			return c, true
		}
	}
	return 0, false
}

// lastTold returns when c last heard of a version.
func (st State) lastTold(c clock) (Told, bool) {
	t, ok := st.told[c]
	return t, ok
}

// tell records that c heard of t.Version at t.At.
func (st *State) tell(c clock, t Told) {
	if st.told == nil {
		st.told = map[clock]Told{}
	}
	st.told[c] = t
}
