package agent

import (
	"regexp"
	"strings"
)

// Handoff is a line of an agent's prompt that hands control to a named
// next agent. Handoffs belong in a pipeline, so the registry's tools warn
// about them.
type Handoff struct {
	// Line counts from 1 at the first line of the prompt.
	Line int
	// Text is the line, trimmed.
	Text string
}

var (
	// A "Next:" line (bold or a list item once the markup is stripped) that
	// says run, call, start, use, invoke or hand off to a name. The name must
	// look like an agent (hyphenated or in backticks), so "Next: run pytest"
	// and "Next: use Docker" stay silent.
	reNext = regexp.MustCompile("(?i)^next:\\s*(?:run|call|start|use|invoke|hand\\s+off\\s+to)\\s+(?:the\\s+)?(?:`[a-z][a-z0-9_-]*`|[a-z][a-z0-9_]*(?:-[a-z0-9_]+)+)")
	// The phrases that name a next agent anywhere in a line. The name must
	// look like one (hyphenated or in backticks), so "then run the tests"
	// and "hand off to a subagent" stay silent.
	reInline = regexp.MustCompile("(?i)\\b(?:hand\\s+off\\s+to|then\\s+run)\\s+(?:`[a-z][a-z0-9_]*(?:-[a-z0-9_]+)+`|[a-z][a-z0-9_]*(?:-[a-z0-9_]+)+)")
	// A list marker or quote at the start of a line.
	reLead = regexp.MustCompile(`^(?:>\s*)*(?:(?:[-+]|\d+[.)])\s+)?`)
)

// Handoffs returns the prompt lines that hand control to a named next
// agent. It only reads text, so it can miss a handoff worded another way.
func (d Definition) Handoffs() []Handoff {
	var out []Handoff
	fenced := false
	for i, line := range strings.Split(d.Prompt, "\n") {
		// Example output inside a code block is not an instruction.
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if !fenced && isHandoff(line) {
			out = append(out, Handoff{Line: i + 1, Text: strings.TrimSpace(line)})
		}
	}
	return out
}

func isHandoff(line string) bool {
	s := strings.TrimSpace(line)
	s = strings.TrimSpace(reLead.ReplaceAllString(s, ""))
	s = strings.ReplaceAll(s, "*", "")
	return reNext.MatchString(s) || reInline.MatchString(s)
}
