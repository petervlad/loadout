package memimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func file(typ, name, body string) string {
	return "---\nname: " + name + "\ndescription: " + name + " description\ntype: " + typ + "\nowner: ignored\n---\n" + body + "\n"
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPreselection(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		body string
		want bool
	}{
		{"reference", "reference", "The dashboard is at the wiki.", true},
		{"project", "project", "The migration ships in March.", true},
		{"feedback is not", "feedback", "Keep answers short.", false},
		{"first person", "reference", "I found the dashboard here.", false},
		{"contraction", "project", "I've moved the job.", false},
		{"my", "reference", "See My notes for details.", false},
		{"the user prefers", "project", "The user prefers tabs.", false},
		{"email", "reference", "Ask jo@example.com about it.", false},
		{"word containing i is fine", "reference", "Kubernetes is in the cluster.", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "a.md", file(tt.typ, "a", tt.body))
			l, err := Read(dir)
			if err != nil || len(l.Entries) != 1 {
				t.Fatalf("Read = %+v, %v", l, err)
			}
			e := l.Entries[0]
			if e.Selected != tt.want {
				t.Errorf("Selected = %v, want %v", e.Selected, tt.want)
			}
			if len(e.Why) == 0 {
				t.Error("no reason given")
			}
		})
	}
}

func TestParseType(t *testing.T) {
	head := "name: n\ndescription: d\n"
	tests := []struct {
		name    string
		header  string
		want    Kind
		wantErr string
	}{
		{"top level", head + "type: reference\n", KindReference, ""},
		{"nested", head + "metadata:\n  node_type: memory\n  type: feedback\n  originSessionId: abc\n", KindFeedback, ""},
		{"both, top level wins", head + "type: project\nmetadata:\n  type: feedback\n", KindProject, ""},
		{"nested user", head + "metadata:\n  type: user\n", "", "never imported"},
		{"nested missing", head + "metadata:\n  node_type: memory\n", "", "must be feedback"},
		{"metadata not a mapping", head + "metadata: text\n", "", "must be feedback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := parse("a.md", []byte("---\n"+tt.header+"---\nBody.\n"))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || e.Kind != tt.want {
				t.Fatalf("Kind = %q, err = %v, want %q", e.Kind, err, tt.want)
			}
		})
	}
}

func TestParseQuotedDescription(t *testing.T) {
	data := "---\nname: a\ndescription: \"Link every \\\"PR\\\" to its ticket\"\nmetadata:\n  type: feedback\n---\nBody.\n"
	e, err := parse("a.md", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if want := `Link every "PR" to its ticket`; e.Description != want {
		t.Errorf("Description = %q, want %q", e.Description, want)
	}
}

func TestReadSkips(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "MEMORY.md", "- [a](a.md)\n")
	write(t, dir, "b-user.md", file("user", "u", "Role."))
	write(t, dir, "c-nofm.md", "just text\n")
	write(t, dir, "d-badtype.md", file("odd", "d", "Text."))
	write(t, dir, "e-badyaml.md", "---\nname: [unclosed\n---\nText\n")
	write(t, dir, "f-ok.md", file("reference", "f", "Text."))
	write(t, dir, "notes.txt", "x")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "sub/g.md", file("reference", "g", "Text."))

	l, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].File != "f-ok.md" || l.Entries[0].Number != 1 {
		t.Fatalf("entries = %+v", l.Entries)
	}
	got := map[string]string{}
	for _, s := range l.Skipped {
		got[s.File] = s.Reason
	}
	for _, f := range []string{"b-user.md", "c-nofm.md", "d-badtype.md", "e-badyaml.md"} {
		if got[f] == "" {
			t.Errorf("%s not skipped with a reason: %v", f, got)
		}
	}
	if !strings.Contains(got["b-user.md"], "user") {
		t.Errorf("user reason = %q", got["b-user.md"])
	}
	if len(l.Skipped) != 4 {
		t.Errorf("skipped = %v", l.Skipped)
	}
}

var awsKey = "AKIA" + "IOSFODNN7ABCDEFG"

func TestSecretRefusedAndNeverSaved(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.md", file("reference", "a", "Line one.\nkey "+awsKey+" here"))
	write(t, dir, "b.md", file("reference", "b", "Clean."))
	l, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := l.Entries[0]
	if a.Refusal == nil || a.Refusal.Rule != "aws-access-key-id" || a.Refusal.Line != 2 || a.Selected {
		t.Fatalf("entry a = %+v", a)
	}
	if !slices.Equal(l.Numbers(), []int{2}) {
		t.Errorf("Numbers = %v", l.Numbers())
	}

	s := &fakeSaver{}
	reps, err := Run(context.Background(), l, []int{1, 2}, s, false)
	if err != nil {
		t.Fatal(err)
	}
	if reps[0].Outcome != Refused || reps[0].Finding == nil || reps[1].Outcome != Saved {
		t.Fatalf("reports = %+v", reps)
	}
	for _, e := range s.got {
		if strings.Contains(e.Body, awsKey) {
			t.Fatal("secret reached the Saver")
		}
	}
	if len(s.got) != 1 {
		t.Errorf("saver got %d entries", len(s.got))
	}
}

// A listing built by hand without the scan must still not leak.
func TestRunRescans(t *testing.T) {
	l := Listing{Entries: []Entry{{Number: 1, File: "a.md", Name: "a", Description: "d", Kind: KindReference, Body: awsKey}}}
	s := &fakeSaver{}
	reps, err := Run(context.Background(), l, []int{1}, s, true)
	if err != nil || reps[0].Outcome != Refused || len(s.got) != 0 {
		t.Fatalf("reps = %+v, err = %v, saved = %d", reps, err, len(s.got))
	}
}

type fakeSaver struct {
	got  []Entry
	dup  map[string]bool
	fail map[string]bool
	news []bool
}

func (f *fakeSaver) Save(_ context.Context, e Entry, isNew bool) (Result, error) {
	f.got = append(f.got, e)
	f.news = append(f.news, isNew)
	if f.fail[e.Name] {
		return Result{}, errors.New("boom")
	}
	return Result{ID: int64(len(f.got)), Duplicate: f.dup[e.Name]}, nil
}

func listing() Listing {
	var l Listing
	for i, n := range []string{"a", "b", "c"} {
		l.Entries = append(l.Entries, Entry{Number: i + 1, File: n + ".md", Name: n, Description: "d", Kind: KindReference, Body: "text"})
	}
	return l
}

func TestRunDuplicatesAndFailures(t *testing.T) {
	s := &fakeSaver{dup: map[string]bool{"b": true}, fail: map[string]bool{"c": true}}
	reps, err := Run(context.Background(), listing(), []int{3, 1, 2, 2}, s, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []Outcome{Saved, Duplicate, Failed}
	for i, r := range reps {
		if r.Number != i+1 || r.Outcome != want[i] {
			t.Errorf("report %d = %+v, want %s", i, r, want[i])
		}
	}
	if reps[2].Err == nil {
		t.Error("failure lost its error")
	}

	// With new, the saver is told so and the entry counts as saved.
	s = &fakeSaver{dup: map[string]bool{"b": true}}
	reps, _ = Run(context.Background(), listing(), []int{2}, s, true)
	if reps[0].Outcome != Saved || !s.news[0] {
		t.Errorf("new run = %+v, news = %v", reps, s.news)
	}
}

func TestRunUnknownNumberSavesNothing(t *testing.T) {
	s := &fakeSaver{}
	if _, err := Run(context.Background(), listing(), []int{1, 9}, s, false); err == nil {
		t.Fatal("want an error")
	}
	if len(s.got) != 0 {
		t.Error("saved before validating numbers")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &fakeSaver{}
	reps, err := Run(ctx, listing(), []int{1, 2}, s, false)
	if !errors.Is(err, context.Canceled) || len(reps) != 0 || len(s.got) != 0 {
		t.Fatalf("reps = %v, err = %v", reps, err)
	}
}
