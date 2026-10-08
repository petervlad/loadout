package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/skill"
)

func TestQuoteFields(t *testing.T) {
	const note = `quoted the description: it holds ": ", which YAML refuses unquoted (the source is unchanged)`
	tests := []struct {
		name        string
		in          string
		want        string
		wantNotices []string
	}{
		{"valid header is untouched", "---\nname: a\ndescription: \"x: y\"\n---\nb\n", "---\nname: a\ndescription: \"x: y\"\n---\nb\n", nil},
		{"description", "---\nname: a\ndescription: Use for Jira: tickets.\n---\nb\n", "---\nname: a\ndescription: \"Use for Jira: tickets.\"\n---\nb\n", []string{note}},
		{
			"name and description", "---\nname: a: b\ndescription: x: y\n---\nb\n", "---\nname: \"a: b\"\ndescription: \"x: y\"\n---\nb\n",
			[]string{`quoted the name: it holds ": ", which YAML refuses unquoted (the source is unchanged)`, note},
		},
		{"crlf kept", "---\r\nname: a\r\ndescription: x: \"y\"\r\n---\r\nb\r\n", "---\r\nname: a\r\ndescription: \"x: \\\"y\\\"\"\r\n---\r\nb\r\n", []string{note}},
		{"a line in the body stays", "---\nname: a\ndescription: x: y\n---\ndescription: x: y\n", "---\nname: a\ndescription: \"x: y\"\n---\ndescription: x: y\n", []string{note}},
		{"no frontmatter is left to the parser", "just text: here\n", "just text: here\n", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, notices, err := quoteFields([]byte(tc.in))
			if err != nil {
				t.Fatalf("quoteFields() error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("quoteFields() =\n%q\nwant\n%q", got, tc.want)
			}
			if diff := cmp.Diff(tc.wantNotices, notices); diff != "" {
				t.Errorf("quoteFields() notices mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestQuoteFieldsRefusesOtherErrors(t *testing.T) {
	tests := []struct {
		name, in string
	}{
		{"another field", "---\nname: a\ndescription: d\nsummary: x: y\n---\nb\n"},
		{"a multi-line description", "---\nname: a\ndescription: x: y\n  and more\n---\nb\n"},
		{"a second error besides", "---\nname: a\ndescription: x: y\ntools: [a\n---\nb\n"},
		{"a comment", "---\nname: a\ndescription: x: y #note\n---\nb\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, notices, err := quoteFields([]byte(tc.in))
			if err == nil {
				t.Fatalf("quoteFields() = %q, %q, want the YAML error", got, notices)
			}
			if string(got) != tc.in || notices != nil {
				t.Errorf("quoteFields() = %q, %q, want the input unchanged", got, notices)
			}
		})
	}
}

func TestSkillsQuotesTheRealJiraHeader(t *testing.T) {
	src := filepath.Join("testdata", "skills", "format-jira-text")
	before := readTestdata(t, "skills/format-jira-text/SKILL.md")
	out := t.TempDir()
	results, err := Skills([]string{src}, Options{Team: "lime", Out: out})
	if err != nil {
		t.Fatalf("Skills() error: %v", err)
	}
	if !slicesHasPrefix(results[0].Notices, "quoted the description") {
		t.Errorf("Skills() notices = %q, want the quoting noted", results[0].Notices)
	}
	p, err := pack.Load(results[0].Dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	s, _ := p.Skill()
	if !strings.HasPrefix(s.Description, "ALWAYS use this when drafting or writing ANY text destined for a Jira field: a ticket") {
		t.Errorf("description = %q", s.Description)
	}
	copied := string(p.Files()[skill.File])
	if !strings.Contains(copied, "description: \"ALWAYS use this") || !strings.HasSuffix(copied, "Convert the draft to wiki markup, then copy it.\n") {
		t.Errorf("imported SKILL.md =\n%s", copied)
	}
	if after := readTestdata(t, "skills/format-jira-text/SKILL.md"); string(after) != string(before) {
		t.Error("Skills() changed the source SKILL.md")
	}
}

func TestAgentQuotesTheDescription(t *testing.T) {
	def, notices, err := convertAgent("p.md", []byte("---\nname: p\ndescription: Review Jira text: tickets and comments.\n---\nx\n"), false)
	if err != nil {
		t.Fatalf("convertAgent() error: %v", err)
	}
	if def.Description != "Review Jira text: tickets and comments." || !slicesHasPrefix(notices, "quoted the description") {
		t.Errorf("convertAgent() = %q, %q", def.Description, notices)
	}
}

func TestName(t *testing.T) {
	out := t.TempDir()
	agentFile := filepath.Join("testdata", "agents", "setup-doctor.md")
	skillDir := filepath.Join("testdata", "skills", "zuul-triage")

	agents, err := Agents([]string{agentFile}, AgentOptions{Options: Options{Team: "lime", Out: out, Name: "doctor"}})
	if err != nil {
		t.Fatalf("Agents(Name) error: %v", err)
	}
	if p, err := pack.Load(agents[0].Dir); err != nil || p.ID().String() != "lime/doctor" {
		t.Errorf("Agents(Name) = %v, %v, want lime/doctor", agents[0].ID, err)
	}

	skills, err := Skills([]string{skillDir}, Options{Team: "lime", Out: out, Name: "ci-triage"})
	if err != nil {
		t.Fatalf("Skills(Name) error: %v", err)
	}
	p, err := pack.Load(skills[0].Dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if s, _ := p.Skill(); s.Name != "ci-triage" || !slicesHasPrefix(s.Extra(), "allowed-tools") {
		t.Errorf("Skills(Name) skill = %q with fields %v, want ci-triage keeping allowed-tools", s.Name, s.Extra())
	}
	if !slicesHasPrefix(skills[0].Notices, "renamed the skill from zuul-triage to ci-triage") {
		t.Errorf("Skills(Name) notices = %q", skills[0].Notices)
	}

	if _, err := Agents([]string{agentFile, agentFile}, AgentOptions{Options: Options{Team: "lime", Out: out, Name: "x"}}); !errors.Is(err, ErrNameForMany) {
		t.Errorf("Agents(Name, 2 files) error = %v, want ErrNameForMany", err)
	}
	if _, err := Agents([]string{agentFile}, AgentOptions{Options: Options{Team: "lime", Out: out, Name: "Bad"}}); err == nil {
		t.Error("Agents(invalid Name) error = nil")
	}
}

func TestCollisions(t *testing.T) {
	// An agent and a skill of the same name, as teams often keep them.
	out := t.TempDir()
	agentFile := filepath.Join(t.TempDir(), "zuul-triage.md")
	if err := os.WriteFile(agentFile, []byte("---\ndescription: d\n---\nx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Agents([]string{agentFile}, AgentOptions{Options: Options{Team: "lime", Out: out}}); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join("testdata", "skills", "zuul-triage")
	_, err := Skills([]string{skillDir}, Options{Team: "lime", Out: out})
	e, ok := errors.AsType[*CollisionError](err)
	if !ok {
		t.Fatalf("Skills() error = %v, want *CollisionError", err)
	}
	if diff := cmp.Diff([]string{skillDir}, e.Sources); diff != "" || e.Dir != filepath.Join(out, "zuul-triage") {
		t.Errorf("CollisionError = %+v", e)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want already exists", err)
	}

	// Two inputs of one run name each other.
	other := filepath.Join(t.TempDir(), "b.md")
	if err := os.WriteFile(other, []byte("---\nname: planner\ndescription: d\n---\nx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(t.TempDir(), "planner.md")
	if err := os.WriteFile(first, []byte("---\ndescription: d\n---\nx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Agents([]string{first, other}, AgentOptions{Options: Options{Team: "lime", Out: out}})
	if e, ok := errors.AsType[*CollisionError](err); !ok || len(e.Sources) != 2 || !strings.Contains(err.Error(), first+" and "+other) {
		t.Errorf("Agents() error = %v, want both inputs named", err)
	}
}

func slicesHasPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
