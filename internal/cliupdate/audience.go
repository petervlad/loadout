package cliupdate

// Audience is who will read what loadout prints on stderr.
type Audience int

const (
	// Person is someone at a terminal.
	Person Audience = iota + 1
	// Agent is Claude Code, Copilot or another agent running loadout.
	Agent
	// Script is neither a terminal nor an agent: cron, CI, a script.
	Script
)

// clock is the clock that counts what a's readers heard: scripts share the
// person's, so cron and the terminal do not both say it on one day.
func (a Audience) clock() clock {
	if a == Agent {
		return clockAgent
	}
	return clockPerson
}

// agentSignals are the variables agents set in the commands they run; any
// one names an agent. A new tool is a row.
var agentSignals = []struct {
	name  string
	match func(string) bool
}{
	{"CLAUDECODE", func(v string) bool { return v == "1" }},              // Claude Code (2.1.287 checked)
	{"COPILOT_AGENT_SESSION_ID", func(v string) bool { return v != "" }}, // Copilot CLI (documented)
	{"AI_AGENT", func(v string) bool { return v != "" }},                 // a convention several tools follow
}

// Detect decides the audience from the environment and whether stderr is
// a terminal. Agent signals win over the terminal: VS Code's agent runs
// commands in one and sets nothing of its own.
func Detect(getenv func(string) string, stderrTerminal bool) Audience {
	for _, s := range agentSignals {
		if s.match(getenv(s.name)) {
			return Agent
		}
	}
	if stderrTerminal {
		return Person
	}
	return Script
}
