package agent

import (
	"slices"
	"testing"
)

func TestHandoffs(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   []int // lines found
	}{
		{"next run", "Do the work.\n\nNext: run lime-dev", []int{3}},
		{"bold next call", "**Next:** call `x`", []int{1}},
		{"bold with colon inside", "**Next: ** call lime-test", []int{1}},
		{"list item", "- Next: invoke `lime-code-review`", []int{1}},
		{"quoted", "> Next: hand off to lime-test", []int{1}},
		{"lower case", "next: start lime-test", []int{1}},
		{"then run", "When done, then run lime-test.", []int{1}},
		{"then run in backticks", "Then run `lime-test` on it.", []int{1}},
		{"hand off to", "You hand off to lime-dev afterwards.", []int{1}},
		{"next with the", "Next: run the lime-dev agent", []int{1}},
		{"two", "Next: run a-b\nok\nthen run c-d", []int{1, 3}},

		{"next steps", "Next steps are listed below.\nNext steps: read the plan.", nil},
		{"next without a name", "Next: summarise the findings.\nNext:\n**Next:** wait", nil},
		{"next run a tool", "Next: run pytest\nNext: run npm install\nNext: use grep to find it\nNext: use Docker\nNext: call out risks", nil},
		{"fenced example", "```\nNext: run lime-dev\n```\n~~~\nthen run lime-test\n~~~\nNext: run lime-dev", []int{7}},
		{"next run the tests", "Next: run the tests", nil},
		{"next use it", "Next: use it", nil},
		{"then run a command", "then run the tests, then run `make check`", nil},
		{"delegating", "You may delegate to the explorer subagent.\nHand off to a subagent for search.\nHand off to them.", nil},
		{"unrelated word", "The next agent in the list is irrelevant.", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []int
			for _, h := range (Definition{Prompt: tt.prompt}).Handoffs() {
				got = append(got, h.Line)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Handoffs() lines = %v, want %v", got, tt.want)
			}
		})
	}
}
