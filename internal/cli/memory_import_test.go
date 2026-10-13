package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/memimport"
	"github.com/petervlad/loadout/internal/secretscan"
)

func TestChooseInteractively(t *testing.T) {
	list := memimport.Listing{Entries: []memimport.Entry{
		{Number: 1, Selected: true},
		{Number: 2},
		{Number: 3, Refusal: &secretscan.Finding{Rule: "jwt", Field: "body", Line: 1, Column: 1}},
	}}
	tests := []struct {
		name  string
		input string
		want  []int
		said  string
	}{
		{"enter keeps the pre-selection", "\n", []int{1}, ""},
		{"toggle on and off", "2\n1 2 2\n\n", []int{2}, ""},
		{"add one", "2\n\n", []int{1, 2}, ""},
		{"a secret cannot be chosen", "3\n\n", []int{1}, "Entry 3 holds a secret"},
		{"unknown number", "7\n\n", []int{1}, "There is no entry 7."},
		{"not a number", "x\n\n", []int{1}, "Type entry numbers"},
		{"q saves nothing", "2\nq\n", nil, ""},
		{"closed input saves nothing", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			got, err := chooseInteractively(strings.NewReader(tt.input), &out, list)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) || (len(got) > 0 && !slices.Equal(got, tt.want)) {
				t.Errorf("chose %v, want %v", got, tt.want)
			}
			if !strings.Contains(out.String(), tt.said) {
				t.Errorf("output lacks %q:\n%s", tt.said, out.String())
			}
		})
	}
}

func TestParseSelection(t *testing.T) {
	if got, err := parseSelection("3, 1 2"); err != nil || !slices.Equal(got, []int{3, 1, 2}) {
		t.Errorf("parseSelection = %v, %v", got, err)
	}
	if got, err := parseSelection(""); err != nil || got != nil {
		t.Errorf("empty = %v, %v, want nil", got, err)
	}
	for _, bad := range []string{"0", "-1", "a", "1,x"} {
		if _, err := parseSelection(bad); err == nil {
			t.Errorf("parseSelection(%q) = nil error", bad)
		}
	}
}
