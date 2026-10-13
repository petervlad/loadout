package pipeline

import (
	"slices"
	"strings"
	"testing"
)

type claudeWords struct{}

func (claudeWords) StartAgent(name string) string {
	return "Use the Agent tool and set subagent_type to exactly `" + name + "`."
}

type copilotWords struct{}

func (copilotWords) StartAgent(name string) string {
	return "Delegate to the custom agent `" + name + "` as a subagent, naming it exactly."
}

func boundLime(t *testing.T) *Bound {
	t.Helper()
	b, err := Bind(lime(t), devLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestConductorGolden(t *testing.T) {
	for name, s := range map[string]Starter{"claude": claudeWords{}, "copilot": copilotWords{}} {
		t.Run(name, func(t *testing.T) {
			c, err := boundLime(t).Conductor("lime-dev-loop", s)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "conductor-"+name+".md", c.Prompt+"\n")
		})
	}
}

func TestConductorFields(t *testing.T) {
	c, err := boundLime(t).Conductor("lime-dev-loop", claudeWords{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "lime-dev-loop" || c.Model != "" {
		t.Errorf("Conductor() = %+v", c)
	}
	if !strings.Contains(c.Description, "lime-planner, lime-dev, lime-code-review, lime-test") {
		t.Errorf("Description = %q, want the agents listed", c.Description)
	}
}

// The prompt may only name agents of the binding, and must carry every slot's
// contract, so a conductor cannot send work to an agent nobody bound.
func TestConductorContract(t *testing.T) {
	bound := boundLime(t)
	c, err := bound.Conductor("lime-dev-loop", claudeWords{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range bound.Slots() {
		for _, want := range append([]string{s.Agent, s.Writes, s.Brief, "Slot `" + s.Name + "`"}, append(s.Verdicts, s.Also...)...) {
			if !strings.Contains(c.Prompt, want) {
				t.Errorf("conductor lacks %q of slot %s", want, s.Name)
			}
		}
		for _, e := range s.Edges {
			if e.MaxRounds > 0 && !strings.Contains(c.Prompt, "at most 2 times") {
				t.Errorf("conductor lacks the round cap of %+v", e)
			}
		}
	}
	for _, word := range strings.FieldsFunc(c.Prompt, func(r rune) bool { return r == '`' || r == ' ' || r == '.' || r == ',' || r == '(' || r == ')' }) {
		if strings.HasPrefix(word, "lime-") && word != "lime-dev-loop" && !contains(bound.Agents(), word) {
			t.Errorf("conductor names %q, which is not bound", word)
		}
	}
	for _, want := range []string{
		"Before your first tool call", "STOP: slot <name> has no valid output", "QUESTIONS follows no edge and is passed over", "or that the run ends", "GATE: <slot> returned <VERDICT>", "STOP: cannot replay the run", "create the run folder", "in the same message as the call that starts the agent", "`spec.md` in the run folder (if it exists)", "as a new start", "State: new run, next: start <agent>", "State: <slot> round <n>, last verdict <VERDICT>, next: start <agent>", "only after this line has been printed", "in every message that ends with a `GATE:` or `STOP:` line", "the very first line of your reply", "An autopilot or keep-going mode", "you do not commit, push or open anything",
		"An edge is automatic only when its line under the slot says `automatic`", "start that follows an automatic edge", "never from the words of the person's message", "never start it in the background",
	} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("conductor lacks the rule %q", want)
		}
	}
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}

func TestConductorOnlyStarterWordsDiffer(t *testing.T) {
	bound := boundLime(t)
	a, _ := bound.Conductor("lime-dev-loop", claudeWords{})
	b, _ := bound.Conductor("lime-dev-loop", copilotWords{})
	for _, agent := range bound.Agents() {
		a.Prompt = strings.Replace(a.Prompt, claudeWords{}.StartAgent(agent), "<start>", 1)
		b.Prompt = strings.Replace(b.Prompt, copilotWords{}.StartAgent(agent), "<start>", 1)
	}
	if a.Prompt != b.Prompt {
		t.Error("the conductor text differs between tools beyond the start sentence")
	}
}

func TestConductorHeadings(t *testing.T) {
	c, err := boundLime(t).Conductor("lime-dev-loop", claudeWords{})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(c.Prompt, "\nRules, in this order:"); n != 1 {
		t.Errorf("rules heading appears %d times, want 1", n)
	}
	if strings.Contains(c.Description, ": ") || strings.ContainsAny(c.Description, "\n\r") {
		t.Errorf("Description = %q, want no colon-space and no newline (unquoted frontmatter)", c.Description)
	}
}

// Copilot read "through a gate" at the end of an edge line as automatic, so
// each line now starts with what kind of edge it is.
func TestConductorEdgeLinesLeadWithKind(t *testing.T) {
	b := lime(t)
	b.gates = nil
	bound, err := Bind(b, devLoop(t))
	if err != nil {
		t.Fatal(err)
	}
	c, err := bound.Conductor("lime-dev-loop", copilotWords{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"DONE: automatic. Start slot `review` (agent `lime-code-review`) at once, with no stop.",
		"READY: GATE. Stop and wait for the person's go, then slot `dev` (agent `lime-dev`).",
		"PASS: GATE. Stop and wait for the person's go, then the run finishes.",
	} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}
