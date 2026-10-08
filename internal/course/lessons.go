package course

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/kb"
)

// MaxLessons is how many lessons a course box lists; a knowledge base
// with more top-level documents shows the first MaxLessons and says so.
const MaxLessons = 50

// IsLesson reports whether a knowledge base's path is a lesson: a
// Markdown document (.md, .markdown) at the top level. Documents in
// folders are reference material the lessons link to.
func IsLesson(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return !strings.Contains(p, "/") && (ext == ".md" || ext == ".markdown")
}

// Lesson is one lesson: its file and its title (kb.Title).
type Lesson struct {
	Path  string
	Title string
}

// Lessons returns the lessons among files (path to content, as
// bundle.Scan returns them for IsLesson) in path order, the order the
// knowledge base's index lists them in, at most MaxLessons, and how many
// there were in all. Authors order lessons by numbering their files:
// 01-setup.md, 02-propose.md.
func Lessons(files map[string][]byte) (lessons []Lesson, total int) {
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if !IsLesson(p) {
			continue
		}
		total++
		if len(lessons) < MaxLessons {
			lessons = append(lessons, Lesson{Path: p, Title: kb.Title(p, files[p])})
		}
	}
	return lessons, total
}
