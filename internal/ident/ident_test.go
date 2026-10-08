package ident

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		in    string
		valid bool
	}{
		{"planner", true},
		{"code-review", true},
		{"lime2", true},
		{"", false},
		{"Planner", false},
		{"-planner", false},
		{"planner-", false},
		{"code--review", false},
		{"../etc", false},
		{"a/b", false},
		{"with space", false},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
	}
	for _, tc := range tests {
		err := Check("agent", tc.in)
		if got := err == nil; got != tc.valid {
			t.Errorf("Check(%q) valid = %v, want %v (err: %v)", tc.in, got, tc.valid, err)
		}
	}
}
