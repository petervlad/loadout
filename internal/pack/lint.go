package pack

import (
	"fmt"
	"strings"
)

// Finding is a warning about a package. It never stops a publish.
type Finding struct {
	// Path is the file, Line the line in it (from 1), Text the line.
	Path string
	Line int
	Text string
	// Rule names the check, such as "handoff".
	Rule string
	// Advice tells the author what to do instead.
	Advice string
}

// String words the finding as a warning for a person.
func (f Finding) String() string {
	return fmt.Sprintf("warning: %s:%d %s", f.Path, f.Line, f.Advice)
}

// Lint returns the warnings for p, none for a kind with no checks.
func (p *Pack) Lint() []Finding {
	if lint := kinds[p.Kind()].lint; lint != nil {
		return lint(p)
	}
	return nil
}

// lintAgent finds handoffs in an agent's prompt. The agent keeps its prompt
// without the frontmatter, so the lines are moved to where they sit in
// agent.md.
func lintAgent(p *Pack) []Finding {
	def, ok := p.Agent()
	if !ok {
		return nil
	}
	file := strings.ReplaceAll(string(p.files[AgentFile]), "\r\n", "\n")
	offset := promptOffset(file, def.Prompt)
	var out []Finding
	for _, h := range def.Handoffs() {
		out = append(out, Finding{
			Path: AgentFile, Line: offset + h.Line, Text: h.Text, Rule: RuleHandoff,
			Advice: fmt.Sprintf("names the next agent: %s. Handoffs live in pipelines, so end with a Verdict line and let the pipeline decide the next step. A later version refuses this.", h.Text),
		})
	}
	return out
}

// RuleHandoff is the Rule of a finding about an agent naming its successor.
const RuleHandoff = "handoff"

// promptOffset counts the lines before the prompt in file, looking for it
// after the closing --- of the frontmatter so a header that repeats the
// text is not mistaken for it.
func promptOffset(file, prompt string) int {
	start := 0
	if _, rest, ok := strings.Cut(file, "\n"); ok {
		for _, line := range strings.SplitAfter(rest, "\n") {
			start += len(line)
			if strings.TrimRight(line, " \t\n") == "---" {
				break
			}
		}
		start += len(file) - len(rest)
	}
	i := strings.Index(file[start:], prompt)
	if i < 0 {
		return 0
	}
	return strings.Count(file[:start+i], "\n")
}
