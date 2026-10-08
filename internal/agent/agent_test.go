package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const validFile = `---
name: planner
description: Turn a ticket into an implementation plan.
model: deep
tools: [read, search, shell]
---

You are the team's planner.
`

func TestParseValid(t *testing.T) {
	got, err := Parse([]byte(validFile))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	want := Definition{
		Name:        "planner",
		Description: "Turn a ticket into an implementation plan.",
		Model:       ModelDeep,
		Tools:       []Tool{ToolRead, ToolSearch, ToolShell},
		Prompt:      "You are the team's planner.",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Parse() mismatch (-want +got):\n%s", diff)
	}
}

func TestParseCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(validFile, "\n", "\r\n")
	if _, err := Parse([]byte(crlf)); err != nil {
		t.Errorf("Parse(CRLF file) error: %v", err)
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantMsg string
	}{
		{"no frontmatter", "You are a planner.", "must start with"},
		{"unclosed frontmatter", "---\nname: planner\n", "not closed"},
		{"unknown field", "---\nname: planner\ndescription: d\ncolour: red\n---\nbody", "colour"},
		{"bad name", "---\nname: Planner\ndescription: d\n---\nbody", "lowercase"},
		{"no description", "---\nname: planner\n---\nbody", "description is empty"},
		{"unknown tool", "---\nname: planner\ndescription: d\ntools: [teleport]\n---\nbody", "teleport"},
		{"vendor model id", "---\nname: planner\ndescription: d\nmodel: opus\n---\nbody", "model hint"},
		{"empty prompt", "---\nname: planner\ndescription: d\n---\n\n", "prompt"},
		{"empty tools list", "---\nname: planner\ndescription: d\ntools: []\n---\nbody", "every tool"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if err == nil {
				t.Fatalf("Parse(%q) = nil error, want one containing %q", tc.in, tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("Parse(%q) error = %q, want it to contain %q", tc.in, err, tc.wantMsg)
			}
		})
	}
}

func TestParseReportsAllProblems(t *testing.T) {
	_, err := Parse([]byte("---\nname: Bad\nmodel: huge\n---\n"))
	for _, want := range []string{"lowercase", "description", "model hint", "prompt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestNoFrontmatterIsSentinel(t *testing.T) {
	_, err := Parse([]byte("plain text"))
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Errorf("Parse(plain text) error = %v, want ErrNoFrontmatter", err)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	want, err := Parse([]byte(validFile))
	if err != nil {
		t.Fatal(err)
	}
	b, err := want.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse(Marshal()) error: %v\n%s", err, b)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}
