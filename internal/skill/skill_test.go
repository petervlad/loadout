package skill

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const testMain = "---\nname: release-notes\ndescription: Write release notes. Use when asked for release notes.\nlicense: MIT\n---\nCollect the merged PRs.\n\nSee references/format.md.\n"

// testExtra carries fields of the Agent Skills format and of Claude Code
// that loadout does not read.
const testExtra = `---
name: build-triage
description: >-
  Find why a CI build failed. Use when the user pastes a build URL.
license: Apache-2.0
allowed-tools: Bash(curl:*) Read Grep
metadata:
  author: lime
  version: "1.2"
compatibility: Needs curl and network access to the CI server.
disable-model-invocation: false
argument-hint: "[build-url]"
---
Fetch the build, then read job-output.txt.
`

func testFiles() map[string][]byte {
	return map[string][]byte{
		"references/format.md": []byte("# Format\n"),
		"scripts/collect.sh":   []byte("#!/bin/sh\necho hi\n"),
	}
}

func TestParse(t *testing.T) {
	s, err := Parse([]byte(testMain), testFiles())
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if s.Name != "release-notes" || s.License != "MIT" || !strings.HasPrefix(s.Description, "Write release notes.") {
		t.Errorf("Parse() = %+v", s)
	}
	if s.Body != "Collect the merged PRs.\n\nSee references/format.md." {
		t.Errorf("Body = %q, want it trimmed", s.Body)
	}
	if diff := cmp.Diff(testFiles(), s.Files()); diff != "" {
		t.Errorf("Files() mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		main string
	}{
		{"with license", testMain},
		{"without license", "---\nname: a\ndescription: d\n---\nb\n"},
		{"crlf and bom", "\ufeff---\r\nname: a\r\ndescription: d\r\n---\r\nb\r\n"},
		{"description needing quotes", "---\nname: a\ndescription: \"Use when: the user says '---' or #tag\"\n---\nb\n"},
		{"extra fields", testExtra},
		{"extra fields first", "---\nallowed-tools: Read Grep\nname: a\ndescription: d\n---\nb\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse([]byte(tc.main), testFiles())
			if err != nil {
				t.Fatalf("Parse() error: %v", err)
			}
			data, err := s.Marshal("")
			if err != nil {
				t.Fatalf("Marshal() error: %v", err)
			}
			again, err := Parse(data, s.Files())
			if err != nil {
				t.Fatalf("Parse(Marshal()) error: %v\n%s", err, data)
			}
			if diff := cmp.Diff(s, again, cmp.AllowUnexported(Skill{})); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
			data2, err := again.Marshal("")
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != string(data2) {
				t.Errorf("Marshal() is not canonical:\n%s\nthen\n%s", data, data2)
			}
		})
	}
}

func TestParseKeepsExtraFields(t *testing.T) {
	s, err := Parse([]byte(testExtra), nil)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	wantKeys := []string{"allowed-tools", "metadata", "compatibility", "disable-model-invocation", "argument-hint"}
	if diff := cmp.Diff(wantKeys, s.Extra()); diff != "" {
		t.Errorf("Extra() mismatch (-want +got):\n%s", diff)
	}
	data, err := s.Marshal("")
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	want := `---
name: build-triage
description: Find why a CI build failed. Use when the user pastes a build URL.
license: Apache-2.0
allowed-tools: Bash(curl:*) Read Grep
metadata:
  author: lime
  version: "1.2"
compatibility: Needs curl and network access to the CI server.
disable-model-invocation: false
argument-hint: "[build-url]"
---
Fetch the build, then read job-output.txt.
`
	if diff := cmp.Diff(want, string(data)); diff != "" {
		t.Errorf("Marshal() mismatch (-want +got):\n%s", diff)
	}
}

func TestExtraFieldsSurviveRenameAndNote(t *testing.T) {
	s, err := Parse([]byte(testExtra), nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Renamed("candidate-build-triage")
	if err != nil {
		t.Fatal(err)
	}
	data, err := r.Marshal("<!-- managed by loadout -->")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: candidate-build-triage\n", "allowed-tools: Bash(curl:*) Read Grep\n", "  author: lime\n", "argument-hint: \"[build-url]\"\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("Renamed().Marshal() =\n%s\nwant %q", data, want)
		}
	}
}

func TestMarshalWithNote(t *testing.T) {
	s, err := Parse([]byte(testMain), nil)
	if err != nil {
		t.Fatal(err)
	}
	const note = "<!-- managed by loadout -->"
	data, err := s.Marshal(note)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	want := "---\nname: release-notes\ndescription: Write release notes. Use when asked for release notes.\nlicense: MIT\n---\n\n" + note + "\n\nCollect the merged PRs.\n\nSee references/format.md.\n"
	if string(data) != want {
		t.Errorf("Marshal(note) =\n%s\nwant\n%s", data, want)
	}
	// The note is not part of the header, so the file still parses.
	again, err := Parse(data, nil)
	if err != nil {
		t.Fatalf("Parse(Marshal(note)) error: %v", err)
	}
	if again.Name != s.Name || again.Description != s.Description {
		t.Errorf("Parse(Marshal(note)) = %+v, want the same header", again)
	}
}

func TestParseErrors(t *testing.T) {
	head := func(name, desc string) string {
		return "---\nname: " + name + "\ndescription: " + desc + "\n---\n"
	}
	tests := []struct {
		name  string
		main  string
		files map[string][]byte
		want  string
	}{
		{"no frontmatter", "Just text.", nil, "frontmatter"},
		{"unclosed frontmatter", "---\nname: a\n", nil, "not closed"},
		{"not a mapping", "---\n- a\n---\nb", nil, "want fields"},
		{"duplicate field", "---\nname: a\ndescription: d\nname: b\n---\nb", nil, `"name" appears twice`},
		{"duplicate extra field", "---\nname: a\ndescription: d\nx: 1\nx: 2\n---\nb", nil, `"x" appears twice`},
		{"name not text", "---\nname: [a]\ndescription: d\n---\nb", nil, "frontmatter"},
		{"alias in extra field", "---\nname: a\ndescription: &d d\nsummary: *d\n---\nb", nil, "alias"},
		{"empty frontmatter", "---\n---\nb", nil, "name is empty"},
		{"missing description", "---\nname: a\n---\nb", nil, "description is empty"},
		{"blank description", head("a", `"   "`) + "b", nil, "description is empty"},
		{"description too long", head("a", strings.Repeat("x", MaxDescription+1)) + "b", nil, "the limit is 1024"},
		{"empty body", head("a", "d") + "\n  \n", nil, "instructions"},
		{"uppercase name", head("Notes", "d") + "b", nil, "lowercase"},
		{"underscore name", head("release_notes", "d") + "b", nil, "lowercase"},
		{"double hyphen", head("release--notes", "d") + "b", nil, "single hyphens"},
		{"long name", head(strings.Repeat("a", 65), "d") + "b", nil, "longer than 64"},
		{"skill file as supporting file", head("a", "d") + "b", map[string][]byte{File: []byte("x")}, "main file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.main), tc.files)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDescriptionLimit(t *testing.T) {
	tests := []struct {
		name    string
		desc    string
		wantErr bool
	}{
		{"at the limit", strings.Repeat("x", MaxDescription), false},
		{"over the limit", strings.Repeat("x", MaxDescription+1), true},
		// The limit counts characters, not bytes.
		{"multibyte at the limit", strings.Repeat("\u00e9", MaxDescription), false},
		{"multibyte over the limit", strings.Repeat("\u00e9", MaxDescription+1), true},
		{"spaces around do not count", "  " + strings.Repeat("x", MaxDescription) + "\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New("a", tc.desc, "", "b", nil)
			if (err != nil) != tc.wantErr {
				t.Errorf("New() error = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}

func TestParseReportsEveryProblem(t *testing.T) {
	_, err := Parse([]byte("---\nname: Bad\n---\n"), map[string][]byte{File: nil})
	if err == nil {
		t.Fatal("Parse() error = nil")
	}
	for _, want := range []string{"lowercase", "description is empty", "instructions", "main file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Parse() error = %v, want it to mention %q", err, want)
		}
	}
}

func TestRenamed(t *testing.T) {
	s, err := Parse([]byte(testMain), testFiles())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Renamed("candidate-release-notes")
	if err != nil {
		t.Fatalf("Renamed() error: %v", err)
	}
	if r.Name != "candidate-release-notes" || s.Name != "release-notes" {
		t.Errorf("Renamed() names = %q (copy), %q (original)", r.Name, s.Name)
	}
	if r.Description != s.Description || r.License != s.License || r.Body != s.Body {
		t.Errorf("Renamed() = %+v, want everything but the name kept", r)
	}
	if diff := cmp.Diff(s.Files(), r.Files()); diff != "" {
		t.Errorf("Renamed() files mismatch (-want +got):\n%s", diff)
	}
	if _, err := s.Renamed("Not Valid"); err == nil {
		t.Error("Renamed() to an invalid name: error = nil")
	}
}

func TestFilesAreCopies(t *testing.T) {
	in := testFiles()
	s, err := New("a", "d", "", "b", in)
	if err != nil {
		t.Fatal(err)
	}
	in["references/format.md"][0] = 'X'
	in["new.md"] = []byte("added")
	got := s.Files()
	got["references/format.md"][0] = 'Y'
	delete(got, "scripts/collect.sh")
	if diff := cmp.Diff(testFiles(), s.Files()); diff != "" {
		t.Errorf("mutating the input or Files() changed the skill (-want +got):\n%s", diff)
	}
}
