package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/pack"
)

// courseQuiz is a quiz fence with a right and a wrong answer.
const courseQuiz = "```quiz\nWhat do you run first?\n\n- [ ] loadout publish\n  Not quite.\n- [x] loadout rebase\n  It merges onto the latest release.\n```\n"

// publishCourse adds the knowledge base team/name at version: three
// lessons and a reference document in a folder.
func (s *uiSite) publishCourse(team, name, version, desc string) pack.ID {
	s.t.Helper()
	s.publishBundle(team, encode(s.t, map[string][]byte{
		pack.ManifestFile: manifest(team, name, "kb", version, desc),
		"01-start.md": []byte("# Start\n\nRead [the next one](02-next.md), the [reference](refs/ref.md#top) and [a site](https://example.com).\n" +
			"Not [this](javascript:alert(1)).\n"),
		"02-next.md":  []byte("# Next\n\nIntro.\n\n## Sub part\n\nText.\n\n" + courseQuiz + "\n```mermaid\ngraph TD; A-->B\n```\n"),
		"03-end.md":   []byte("# The end " + xss + "\n\nDone " + xss + ".\n"),
		"refs/ref.md": []byte("# Reference\n"),
	}))
	id, err := pack.NewID(team, name)
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}

// courseSite is the UI site with lime's wiki on and a page saved, lime's
// course lime/lime-course, and olga, an owner of acme.
func courseSite(t *testing.T) *uiSite {
	t.Helper()
	s := newUISite(t)
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	s.wikiOn("lime")
	s.saveWiki("alice@example.com", 0, "# Lime\n\n## Setup\n\nRun make.\n", "")
	id := s.publishCourse("lime", "lime-course", "1.0.0", "Seven steps "+xss)
	if _, err := s.reg.SetCourse(t.Context(), "lime", id); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCourseBox(t *testing.T) {
	s := courseSite(t)
	b := s.as("bob@example.com")
	p := b.get("/t/lime/wiki")
	if p.status != http.StatusOK {
		t.Fatalf("GET /t/lime/wiki = %d", p.status)
	}
	wantIn(t, p, "box",
		`<section class="panel course" id="course" aria-labelledby="course-h">`,
		`<a href="/t/lime/p/lime/lime-course">lime-course 1.0.0</a> · 3 lessons`,
		`<p class="course-desc">Seven steps &lt;script&gt;alert(1)&lt;/script&gt;</p>`,
		`<a href="/t/lime/wiki?lesson=01-start.md#lesson-h"><span class="lesson-n" aria-hidden="true">1</span><span class="lesson-title">Start</span></a>`,
		`<span class="lesson-title">Next</span>`,
		`<span class="lesson-title">The end &lt;script&gt;alert(1)&lt;/script&gt;</span>`,
	)
	wantOut(t, p, "box", "From lime", `<details class="course-all">`, xss)
	if i, j := strings.Index(p.body, "Start</span>"), strings.Index(p.body, "Next</span>"); i < 0 || j < i {
		t.Error("lessons are not in file order")
	}
	if strings.Index(p.body, `id="wiki-h"`) > strings.Index(p.body, `id="course"`) {
		t.Error("the box is not after the wiki")
	}
}

func TestCourseLesson(t *testing.T) {
	s := courseSite(t)
	b := s.as("bob@example.com")
	p := b.get("/t/lime/wiki?lesson=02-next.md")
	if p.status != http.StatusOK {
		t.Fatalf("GET lesson = %d", p.status)
	}
	wantIn(t, p, "lesson",
		"<title>Next · Wiki · lime",
		`<details class="course-all">`, "<summary>All 3 lessons</summary>",
		`<a href="/t/lime/wiki?lesson=02-next.md#lesson-h" aria-current="page">`,
		`<article class="course-lesson" aria-labelledby="lesson-h">`, "Lesson 2 of 3", `<h3 id="lesson-h">Next</h3>`,
		"<h4>Sub part</h4>",
		`<div class="quiz" role="group" aria-labelledby="lesson_quiz_1">`, "<strong>Right.</strong> It merges",
		`data-frame="/t/lime/p/lime/lime-course/1.0.0/draw/02-next.md?block=1"`,
		`<a class="file-step" href="/t/lime/wiki?lesson=01-start.md#lesson-h" rel="prev">`,
		`<a class="file-step file-next" href="/t/lime/wiki?lesson=03-end.md#lesson-h" rel="next">`,
		`<a href="/t/lime/wiki#course">Close lesson</a>`,
	)
	wantOut(t, p, "lesson", "<h1>Next</h1>", "<h3>Next</h3>", "<h2>Sub part</h2>")
	if csp := p.header.Get("Content-Security-Policy"); !strings.HasSuffix(csp, "; frame-src 'self'") {
		t.Errorf("CSP = %q, want frames of this origin", csp)
	}
	// The frame answers for a reader of the team.
	if f := b.get("/t/lime/p/lime/lime-course/1.0.0/draw/02-next.md?block=1"); f.status != http.StatusOK {
		t.Errorf("lesson frame = %d", f.status)
	}

	first := b.get("/t/lime/wiki?lesson=01-start.md")
	wantIn(t, first, "first", "Lesson 1 of 3", `rel="next"`,
		`<a href="/t/lime/wiki?lesson=02-next.md#lesson-h">the next one</a>`,
		`<a href="/t/lime/p/lime/lime-course/1.0.0/files/refs/ref.md">reference</a>`,
		`<a href="https://example.com">a site</a>`, `<a href="">this</a>`)
	wantOut(t, first, "first", `rel="prev"`, "javascript:", `<h3>Start</h3>`)

	last := b.get("/t/lime/wiki?lesson=03-end.md")
	wantIn(t, last, "last", "Lesson 3 of 3", `rel="prev"`, "Done &lt;script&gt;")
	wantOut(t, last, "last", `rel="next"`, xss)

	for _, bad := range []string{"refs/ref.md", "../x.md", "gone.md", "SKILL.md"} {
		p := b.get("/t/lime/wiki?" + url.Values{"lesson": {bad}}.Encode())
		if p.status != http.StatusOK {
			t.Errorf("?lesson=%s = %d, want 200", bad, p.status)
		}
		wantIn(t, p, "?lesson="+bad, "That lesson is not in the course any more.", `<ol class="lessons"`)
		wantOut(t, p, "?lesson="+bad, `<article class="course-lesson"`)
	}
}

// TestCourseInherited: a team without a course of its own shows its
// project's, and says where it comes from; with none anywhere there is
// no box.
func TestCourseInherited(t *testing.T) {
	s := courseSite(t)
	ctx := t.Context()
	if _, err := s.reg.SetCourse(ctx, "lime", pack.ID{}); err != nil {
		t.Fatal(err)
	}
	b := s.as("bob@example.com")
	wantOut(t, b.get("/t/lime/wiki"), "no course", `id="course"`)

	acme := s.publishCourse("acme", "onboarding", "2.1.0", "Onboarding for acme")
	if _, err := s.reg.SetCourse(ctx, "acme", acme); err != nil {
		t.Fatal(err)
	}
	p := b.get("/t/lime/wiki")
	wantIn(t, p, "inherited", `<a href="/t/lime/p/acme/onboarding">onboarding 2.1.0</a> · 3 lessons · From acme`)
	wantIn(t, b.get("/t/lime/wiki?lesson=02-next.md"), "inherited lesson", `data-frame="/t/lime/p/acme/onboarding/2.1.0/draw/02-next.md?block=1"`)
	if f := b.get("/t/lime/p/acme/onboarding/2.1.0/draw/02-next.md?block=1"); f.status != http.StatusOK {
		t.Errorf("inherited lesson frame = %d", f.status)
	}
}

// TestCourseMissing: a course that cannot be shown leaves the box out
// for readers and says why to those who may choose another.
func TestCourseMissing(t *testing.T) {
	tests := []struct {
		name  string
		setup func(s *uiSite)
		want  string
	}{
		{"cannot be found", func(s *uiSite) {
			ghost, _ := pack.NewID("koala", "ghost")
			if err := s.mem.SetCourse(s.t.Context(), "lime", ghost); err != nil {
				s.t.Fatal(err)
			}
		}, "The course koala/ghost cannot be found. Choose another in Settings."},
		{"a skill now", func(s *uiSite) {
			s.publishBundle("lime", encode(s.t, map[string][]byte{
				pack.ManifestFile: manifest("lime", "lime-course", "skill", "2.0.0", "Now a skill"),
				"SKILL.md":        []byte("---\nname: lime-course\ndescription: Now a skill. Use it.\n---\nBody.\n"),
			}))
		}, "lime/lime-course is a skill now, not a knowledge base. Choose another in Settings."},
		{"no lessons", func(s *uiSite) {
			s.publishBundle("lime", encode(s.t, map[string][]byte{
				pack.ManifestFile: manifest("lime", "lime-course", "kb", "2.0.0", "Only references"),
				"refs/a.md":       []byte("# A\n"),
			}))
		}, "lime/lime-course has no lessons: Markdown files at its top level are its lessons."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := courseSite(t)
			tt.setup(s)
			p := s.as("olga@example.com").get("/t/lime/wiki")
			wantIn(t, p, "owner", `id="course"`, tt.want, `<a href="/t/lime/settings#course-h">Open Settings</a>`)
			wantOut(t, p, "owner", `<ol class="lessons"`)
			for _, who := range []string{"bob@example.com", "alice@example.com"} {
				if p := s.as(who).get("/t/lime/wiki"); p.status != http.StatusOK || strings.Contains(p.body, `id="course"`) {
					t.Errorf("%s: status %d, box shown: %v", who, p.status, strings.Contains(p.body, `id="course"`))
				}
			}
		})
	}
}

// TestCourseOnlyOnThePage: old revisions, the source and the edit
// preview have no box; the empty wiki has it.
func TestCourseOnlyOnThePage(t *testing.T) {
	s := courseSite(t)
	s.saveWiki("alice@example.com", 1, "# Lime\n\nNewer.\n", "")
	a := s.as("alice@example.com")
	for _, path := range []string{"/t/lime/wiki/r/1", "/t/lime/wiki/r/2", "/t/lime/wiki?source=1", "/t/lime/wiki/edit"} {
		p := a.get(path)
		if p.status != http.StatusOK {
			t.Fatalf("GET %s = %d", path, p.status)
		}
		wantOut(t, p, path, `id="course"`)
	}
	wantOut(t, a.post("/t/lime/wiki/edit", url.Values{"text": {"# x\n"}, "view": {"preview"}, "base": {"2"}}), "preview", `id="course"`)

	// koala has no page yet, but a course of its own.
	s.wikiOn("koala")
	id := s.publishCourse("koala", "steps", "1.0.0", "Steps")
	if _, err := s.reg.SetCourse(t.Context(), "koala", id); err != nil {
		t.Fatal(err)
	}
	wantIn(t, s.as("eve@example.com").get("/t/koala/wiki"), "empty wiki", "No wiki page yet", `id="course"`, "steps 1.0.0")
}

// TestCourseSettings: owners choose the course among the knowledge bases
// the namespace reads; a team's first option is its project's course.
func TestCourseSettings(t *testing.T) {
	s := courseSite(t)
	ctx := t.Context()
	acme := s.publishCourse("acme", "onboarding", "1.0.0", "Onboarding")
	if _, err := s.reg.SetCourse(ctx, "acme", acme); err != nil {
		t.Fatal(err)
	}
	s.publishCourse("koala", "elsewhere", "1.0.0", "Not lime's")
	olga := s.as("olga@example.com")
	p := olga.get("/t/lime/settings")
	wantIn(t, p, "panel", `<h2 id="course-h">Course</h2>`, `action="/t/lime/settings/course"`,
		`<option value="">acme&#39;s course (acme/onboarding)</option>`,
		`<optgroup label="lime"><option value="lime/lime-course" selected>lime/lime-course</option></optgroup>`,
		`<optgroup label="acme"><option value="acme/onboarding">acme/onboarding</option></optgroup>`)
	wantOut(t, p, "panel", "koala/elsewhere", "lime/planner", "It shows once the wiki is on.")
	wantIn(t, olga.get("/t/acme/settings"), "project", `<option value="">No course</option>`, "It shows once the wiki is on.")

	alice := s.as("alice@example.com")
	wantOut(t, alice.get("/t/lime/settings"), "maintainer", `id="course-h"`)
	if p := alice.post("/t/lime/settings/course", url.Values{"course": {""}}); p.status != http.StatusForbidden {
		t.Errorf("alice POST = %d, want 403", p.status)
	}

	for _, bad := range []struct{ value, want string }{
		{"lime/planner", "Lime/planner is an agent; a course is a knowledge base (kind: kb)."},
		{"koala/elsewhere", "Koala/elsewhere is not in lime or a project above it"},
		{"not an id", "Choose a knowledge base from the list."},
	} {
		p := olga.post("/t/lime/settings/course", url.Values{"course": {bad.value}})
		if p.status != http.StatusBadRequest {
			t.Errorf("POST %q = %d, want 400", bad.value, p.status)
		}
		wantIn(t, p, "refused "+bad.value, bad.want, `id="course-sel" name="course" class="sel" aria-invalid="true"`)
	}
	if ns, _ := s.reg.Namespace(ctx, "lime"); ns.Course.String() != "lime/lime-course" {
		t.Errorf("a refusal changed the course to %v", ns.Course)
	}

	p = olga.post("/t/lime/settings/course", url.Values{"course": {"acme/onboarding"}})
	if p.status != http.StatusSeeOther || p.location != "/t/lime/settings?course=acme%2Fonboarding&done=course-set" {
		t.Errorf("POST acme/onboarding = %d %q", p.status, p.location)
	}
	wantIn(t, olga.get(p.location), "set", "The wiki shows acme/onboarding as its course.")
	p = olga.post("/t/lime/settings/course", url.Values{"course": {""}})
	if p.status != http.StatusSeeOther || p.location != "/t/lime/settings?done=course-none" {
		t.Errorf("POST none = %d %q", p.status, p.location)
	}
	wantIn(t, olga.get(p.location), "none", "lime has no course of its own.", `<option value="" selected>acme&#39;s course`)
	if ns, _ := s.reg.Namespace(ctx, "lime"); !ns.Course.IsZero() {
		t.Errorf("course after none = %v", ns.Course)
	}
}
