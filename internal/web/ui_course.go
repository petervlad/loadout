package web

import (
	"context"
	"errors"
	"html/template"
	"net/url"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/course"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// The Course box at the end of a wiki page: a knowledge base the
// namespace (or the closest one above it) chose, shown as lessons. It
// reads the package's newest release through the page's principal, so
// the normal read check runs on every view, and it never fails the wiki
// page: a course that cannot be shown is left out, or replaced by one
// line for those who may choose another.

// courseView is the Course box.
type courseView struct {
	// Name and Version are the package's, for the meta line; Package is
	// its id and PackageURL its page.
	Name, Version, Package, PackageURL string
	Description                        string
	// From is set when the course comes from a namespace above.
	From    string
	Lessons []lessonLink
	// Total counts every lesson, More those past course.MaxLessons that
	// are not listed.
	Total, More int
	// Open is the lesson the reader opened; Gone says the one they asked
	// for is not in the course.
	Open *lessonView
	Gone string
	// Missing replaces the box with one line when the course cannot be
	// shown; Settings links where to choose another.
	Missing  string
	Settings string
}

// lessonLink is one row of the lesson list.
type lessonLink struct {
	N          int
	Title, URL string
	Current    bool
}

// lessonView is the open lesson.
type lessonView struct {
	N, Of int
	Title string
	HTML  template.HTML
	// Note says what the lesson leaves undrawn or cut.
	Note       string
	Prev, Next *lessonLink
	// Close is the wiki page without the lesson.
	Close string
}

// lessonQuizzes starts the ids of a lesson's quizzes, so they never meet
// those of the wiki page above it.
const lessonQuizzes = "lesson_quiz_"

// courseBox builds the Course box of t's wiki page, with the lesson q
// names open; nil when there is none to show the reader.
func (h *handler) courseBox(ctx context.Context, t team, q url.Values) *courseView {
	c, ok := registry.CourseOf(chainOf(t))
	if !ok {
		return nil
	}
	canSet := t.member() && t.place.Access.CanSetWiki()
	missing := func(line string) *courseView {
		if !canSet {
			return nil
		}
		return &courseView{Missing: line, Settings: "/t/" + t.ns() + "/settings#course-h"}
	}
	failed := func(err error) *courseView {
		h.log.WarnContext(ctx, "read the course", "namespace", t.ns(), "package", c.Package.String(), "err", err)
		return &courseView{Missing: "The course could not be loaded just now."}
	}
	id := c.Package
	rels, err := h.ui.Registry.Releases(ctx, t.pr, id)
	if gone(err) || (err == nil && len(rels) == 0) {
		return missing("The course " + id.String() + " cannot be found. Choose another in Settings.")
	}
	if err != nil {
		return failed(err)
	}
	newest := rels[len(rels)-1]
	if newest.Kind != pack.KindKB {
		return missing(id.String() + " is " + newest.Kind.WithArticle() + " now, not a knowledge base. Choose another in Settings.")
	}
	_, data, err := h.ui.Registry.Fetch(ctx, t.pr, id, newest.Version.String())
	if gone(err) {
		return missing("The course " + id.String() + " cannot be found. Choose another in Settings.")
	}
	if err != nil {
		return failed(err)
	}
	entries, files, err := bundle.Scan(data, course.IsLesson)
	if err != nil {
		return failed(err)
	}
	lessons, total := course.Lessons(files)
	if total == 0 {
		return missing(id.String() + " has no lessons: Markdown files at its top level are its lessons.")
	}
	pkg := "/t/" + t.ns() + "/p/" + id.Team() + "/" + id.Name()
	v := &courseView{
		Name: id.Name(), Version: newest.Version.String(), Package: id.String(), PackageURL: pkg,
		Description: newest.Description, Total: total, More: total - len(lessons),
	}
	if c.From != t.ns() {
		v.From = c.From
	}
	open := q.Get("lesson")
	for i, l := range lessons {
		v.Lessons = append(v.Lessons, lessonLink{N: i + 1, Title: l.Title, URL: lessonURL(t, l.Path), Current: l.Path == open})
	}
	if open == "" {
		return v
	}
	i := slices.IndexFunc(lessons, func(l course.Lesson) bool { return l.Path == open })
	if pack.CheckPath(open) != nil || i < 0 {
		v.Gone = "That lesson is not in the course any more."
		return v
	}
	release := pkg + "/" + newest.Version.String()
	v.Open = openLesson(t, lessons, i, files[open], entries, release)
	return v
}

// gone reports whether err means the reader cannot see the package: it
// does not exist, or they may not read it.
func gone(err error) bool {
	return errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrForbidden)
}

// chainOf is t's namespace and those above it that the user reads,
// closest first.
func chainOf(t team) []registry.Namespace {
	chain := []registry.Namespace{t.place.Namespace}
	for cur := t.place.Namespace.Parent; cur != "" && len(chain) < registry.MaxLayers; {
		i := slices.IndexFunc(t.places, func(p account.Place) bool { return p.Namespace.Name == cur })
		if i < 0 {
			break
		}
		chain = append(chain, t.places[i].Namespace)
		cur = t.places[i].Namespace.Parent
	}
	return chain
}

// lessonURL opens the lesson at path on t's wiki page and scrolls to it.
// The path names the lesson, so a link keeps naming it when a new release
// adds one before it.
func lessonURL(t team, path string) string {
	return wikiBase(t) + "?" + url.Values{"lesson": {path}}.Encode() + "#lesson-h"
}

// openLesson renders lessons[i], whose text is data, from the release at
// the base path release. A link to another lesson opens it in the box; a
// link to any other file of the release leads to its preview page, and
// diagrams draw on the release's own frame route.
func openLesson(t team, lessons []course.Lesson, i int, data []byte, entries []bundle.Entry, release string) *lessonView {
	l := lessons[i]
	isLesson := map[string]bool{}
	for _, x := range lessons {
		isLesson[x.Path] = true
	}
	files := map[string]bool{}
	for _, e := range entries {
		files[e.Path] = true
	}
	links := docLinks{dir: ".", files: files, to: func(p string) string {
		if isLesson[p] {
			return lessonURL(t, p)
		}
		return release + "/files/" + p
	}}
	src, cut := string(data), ""
	if len(data) > maxPreview {
		n := maxPreview
		for n > 0 && !utf8.RuneStart(data[n]) {
			n--
		}
		src = string(data[:n])
		cut = "This lesson shows its first " + strconv.Itoa(maxPreview>>10) + " KB. Open it in the package for all of it."
	}
	d := docParse{
		links:   links,
		frames:  &diagramFrames{route: release + "/draw/" + l.Path, name: l.Title},
		quizzes: &quizzes{prefix: lessonQuizzes},
		lesson:  l.Title,
	}
	html, note := d.render(src)
	v := &lessonView{N: i + 1, Of: len(lessons), Title: l.Title, HTML: html, Note: joinNotes(note, cut), Close: wikiBase(t) + "#course"}
	if i > 0 {
		p := lessons[i-1]
		v.Prev = &lessonLink{N: i, Title: p.Title, URL: lessonURL(t, p.Path)}
	}
	if i+1 < len(lessons) {
		n := lessons[i+1]
		v.Next = &lessonLink{N: i + 2, Title: n.Title, URL: lessonURL(t, n.Path)}
	}
	return v
}

// lessonKey, set in a parse, holds the title of the lesson being
// rendered, for lessonHeadings.
var lessonKey = parser.NewContextKey()

// lessonHeadings fits a lesson under the box's own headings: the box
// writes the title as an h3, so a first level-1 heading equal to it is
// dropped, and every other heading moves down two levels (h1 to h3, at
// most h6). The page's outline then reads Wiki, Course, lesson.
type lessonHeadings struct{}

func (lessonHeadings) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	title, ok := pc.Get(lessonKey).(string)
	if !ok {
		return
	}
	src := reader.Source()
	var headings []*ast.Heading
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering {
			headings = append(headings, h)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	dropped := false
	for _, h := range headings {
		if !dropped && h.Level == 1 {
			dropped = true
			if plainText(h, src) == title {
				h.Parent().RemoveChild(h.Parent(), h)
				continue
			}
		}
		h.Level = min(h.Level+2, 6)
	}
}
