package course

import (
	"fmt"
	"reflect"
	"testing"
)

func TestIsLesson(t *testing.T) {
	tests := map[string]bool{
		"01-a.md":     true,
		"A.MARKDOWN":  true,
		"README.md":   true,
		"refs/b.md":   false,
		"notes.txt":   false,
		"diagram.mmd": false,
		"md":          false,
	}
	for p, want := range tests {
		if got := IsLesson(p); got != want {
			t.Errorf("IsLesson(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestLessons(t *testing.T) {
	files := map[string][]byte{
		"10-last.md":    []byte("# Ten\n"),
		"02-propose.md": []byte("Intro\n\n# Propose a version\n"),
		"01-setup.md":   []byte("no heading here"),
		"refs/x.md":     []byte("# Reference"),
		"notes.txt":     []byte("# Not a lesson"),
	}
	got, total := Lessons(files)
	want := []Lesson{{"01-setup.md", "01-setup"}, {"02-propose.md", "Propose a version"}, {"10-last.md", "Ten"}}
	if !reflect.DeepEqual(got, want) || total != 3 {
		t.Errorf("Lessons() = %v, %d; want %v, 3", got, total, want)
	}
}

func TestLessonsLimit(t *testing.T) {
	files := map[string][]byte{}
	for i := range 60 {
		files[fmt.Sprintf("%02d.md", i)] = []byte("x")
	}
	got, total := Lessons(files)
	if len(got) != MaxLessons || total != 60 || got[0].Path != "00.md" || got[MaxLessons-1].Path != "49.md" {
		t.Errorf("Lessons() gave %d lessons from %s to %s, total %d", len(got), got[0].Path, got[len(got)-1].Path, total)
	}
}
