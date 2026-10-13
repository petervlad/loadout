package pipeline

import (
	"fmt"
	"strings"
)

// Rounds writes a round count with its noun: "1 round", "2 rounds".
func Rounds(n int) string {
	if n == 1 {
		return "1 round"
	}
	return fmt.Sprintf("%d rounds", n)
}

// Mermaid draws t as a flowchart. Gate edges are dashed, auto edges solid, and
// a back edge's label carries its max_rounds. When agents maps slot names to
// agent names, each node shows its agent under the slot name. Slot names,
// verdicts and agent names are restricted alphabets, so nothing needs escaping.
func Mermaid(t *Template, agents map[string]string) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	b.WriteString("  begin([start])\n")
	for i, s := range t.slots {
		label := s.Name
		if a := agents[s.Name]; a != "" {
			label += "<br/>" + a
		}
		fmt.Fprintf(&b, "  s%d[\"%s\"]\n", i, label)
	}
	b.WriteString("  done([end])\n")
	fmt.Fprintf(&b, "  begin --> s%d\n", t.index(t.start))
	for _, e := range t.edges {
		label := e.On
		if e.MaxRounds > 0 {
			label += ", max " + Rounds(e.MaxRounds)
		}
		to := "done"
		if e.To != End {
			to = fmt.Sprintf("s%d", t.index(e.To))
		}
		arrow := "-->"
		if e.Via == Gate {
			arrow = "-.->"
		}
		fmt.Fprintf(&b, "  s%d %s|\"%s\"| %s\n", t.index(e.From), arrow, label, to)
	}
	return b.String()
}
