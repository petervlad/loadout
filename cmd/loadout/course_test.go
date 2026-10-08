package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCourse scaffolds the knowledge base id in p's folder and gives it
// two lessons, the second with a quiz, and a reference document.
func writeCourse(t *testing.T, p *person, id string) {
	t.Helper()
	p.must("", "new", "kb", id)
	dir := filepath.Join(p.wd, id[strings.Index(id, "/")+1:])
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "01-setup.md"), "# Set up\n\nRead [the reference](refs/tools.md).\n")
	writeFile(t, filepath.Join(dir, "02-propose.md"), "# Propose a version\n\n"+courseQuizFence)
	writeFile(t, filepath.Join(dir, "refs", "tools.md"), "# Tools\n")
}

const courseQuizFence = "```quiz\nYour proposal is behind. What do you run first?\n\n- [ ] loadout publish --base latest\n  Not quite.\n- [x] loadout rebase\n  It merges your folder onto the latest release.\n```\n"

// TestCourse: olga chooses lime's course from the CLI, alice reads it on
// the wiki page, and the same knowledge base reaches her AI tool.
func TestCourse(t *testing.T) {
	_, base, aliceWeb, olgaWeb := ownedRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	olga := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, olga, olgaWeb, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}
	writeCourse(t, olga, "acme/onboarding")
	olga.must("", "publish", "onboarding")
	writeCourse(t, alice, "lime/lime-course")
	alice.must("", "publish", "lime-course")

	if _, errOut, code := alice.runIn("", "namespace", "set", "lime", "--course", "lime/lime-course"); code == 0 || !strings.Contains(errOut, "needs an owner") {
		t.Errorf("alice sets the course: exit %d, stderr %q", code, errOut)
	}
	out := olga.must("", "namespace", "set", "lime", "--course", "lime/lime-course")
	if !strings.Contains(out, "lime's wiki now shows the course lime/lime-course: open it at "+base+"/t/lime/wiki#course") ||
		!strings.Contains(out, "It shows once the wiki is on: loadout namespace set lime --wiki on") {
		t.Errorf("set --course = %q", out)
	}
	for _, bad := range []struct{ id, want string }{
		{"lime/planner", "lime/planner is an agent; a course is a knowledge base (kind: kb)"},
		{"web/styles", "web/styles is not in lime or a project above it"},
	} {
		if _, errOut, code := olga.runIn("", "namespace", "set", "lime", "--course", bad.id); code == 0 || !strings.Contains(errOut, bad.want) {
			t.Errorf("--course %s: exit %d, stderr %q", bad.id, code, errOut)
		}
	}

	olga.must("", "namespace", "set", "lime", "--wiki", "on")
	status, _, page := aliceWeb.send(http.MethodGet, "/t/lime/wiki", nil)
	if status != http.StatusOK || !strings.Contains(page, "Set up") || !strings.Contains(page, "Propose a version") {
		t.Errorf("wiki page = %d without both lessons", status)
	}
	status, _, page = aliceWeb.send(http.MethodGet, "/t/lime/wiki?lesson=02-propose.md", nil)
	if status != http.StatusOK || !strings.Contains(page, `<div class="quiz" role="group"`) || !strings.Contains(page, "<strong>Right.</strong> It merges") {
		t.Errorf("lesson 2 = %d without its quiz", status)
	}

	olga.must("", "namespace", "set", "acme", "--course", "acme/onboarding")
	if out := olga.must("", "namespace", "set", "lime", "--course", "none"); !strings.Contains(out, "lime has no course of its own now; its wiki shows acme/onboarding, from acme") {
		t.Errorf("--course none = %q", out)
	}
	if _, _, page := aliceWeb.send(http.MethodGet, "/t/lime/wiki", nil); !strings.Contains(page, "From acme") || !strings.Contains(page, "onboarding 0.1.0") {
		t.Error("lime's wiki does not show acme's course")
	}

	// The knowledge base reaches alice's AI tool as written, quiz and all.
	alice.must("", "install", "lime/lime-course")
	lesson := filepath.Join(alice.home, ".claude", "skills", "lime-course", "02-propose.md")
	if got := readFile(t, lesson); !strings.Contains(got, courseQuizFence) {
		t.Errorf("installed lesson = %q, want the quiz fence as written", got)
	}
}
