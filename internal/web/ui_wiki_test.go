package web_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/web"
	"github.com/petervlad/loadout/internal/wiki"
)

// wikiOn turns the wiki of ns on, as an owner would on Settings.
func (s *uiSite) wikiOn(ns string) {
	s.t.Helper()
	if _, err := s.reg.SetWiki(s.t.Context(), ns, true); err != nil {
		s.t.Fatal(err)
	}
}

// saveWiki saves text as revision base+1 of lime's wiki, as email.
func (s *uiSite) saveWiki(email string, base int, text, note string) {
	s.t.Helper()
	u, err := s.store.UserByEmail(s.t.Context(), email)
	if err != nil {
		s.t.Fatal(err)
	}
	doc, err := wiki.NewDoc(wiki.Markdown, text)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, _, err := s.accounts.SaveWiki(s.t.Context(), account.UserActor(u.ID), "lime", base, doc, note); err != nil {
		s.t.Fatal(err)
	}
}

// textareaRE finds the text of the edit page's textarea.
var textareaRE = regexp.MustCompile(`(?s)<textarea id="wiki-text"[^>]*>\n(.*?)</textarea>`)

// textareaOf is the text a browser would show in the edit page's
// textarea: the template's newline after the tag is not part of it.
func textareaOf(t *testing.T, p page) string {
	t.Helper()
	m := textareaRE.FindStringSubmatch(p.body)
	if m == nil {
		t.Fatalf("no textarea in\n%s", p.body)
	}
	return html.UnescapeString(m[1])
}

// hiddenOf is the value of the hidden field name.
func hiddenOf(t *testing.T, p page, name string) string {
	t.Helper()
	m := regexp.MustCompile(`<input type="hidden" name="` + name + `" value="([^"]*)">`).FindStringSubmatch(p.body)
	if m == nil {
		t.Fatalf("no hidden %s in\n%s", name, p.body)
	}
	return m[1]
}

func TestWikiIsHiddenWhileOff(t *testing.T) {
	s := newUISite(t)
	bob := s.as("bob@example.com")
	wantOut(t, bob.get("/t/lime"), "off", `href="/t/lime/wiki"`)
	for _, path := range []string{"/t/lime/wiki", "/t/lime/wiki/edit", "/t/lime/wiki/history", "/t/lime/wiki/r/1", "/t/lime/wiki/r/1/raw", "/t/lime/wiki/r/1/draw?block=1"} {
		if p := bob.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s while off = %d, want 404", path, p.status)
		}
	}
	if p := bob.post("/t/lime/wiki", url.Values{"text": {"x"}}); p.status != http.StatusNotFound {
		t.Errorf("POST /t/lime/wiki while off = %d, want 404", p.status)
	}

	s.wikiOn("lime")
	p := bob.get("/t/lime")
	wantIn(t, p, "on", `<a href="/t/lime/wiki">Wiki</a>`)
	if csp := p.header.Get("Content-Security-Policy"); strings.Contains(csp, "frame-src") {
		t.Errorf("packages CSP = %q, want no frames", csp)
	}
}

// TestWikiWithoutService: a registry without a wiki has no routes, no
// tab and no switch, whatever the namespace says.
func TestWikiWithoutService(t *testing.T) {
	s := newUISiteWith(t, func(ui *web.UI) { ui.Wiki = nil })
	s.wikiOn("lime")
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	olga := s.as("olga@example.com")
	wantOut(t, olga.get("/t/lime"), "packages", `href="/t/lime/wiki"`)
	wantOut(t, olga.get("/t/lime/settings"), "settings", `id="wiki-h"`)
	if p := olga.get("/t/lime/wiki"); p.status != http.StatusNotFound {
		t.Errorf("GET /t/lime/wiki = %d, want 404", p.status)
	}
	if p := olga.post("/t/lime/settings/wiki", url.Values{"wiki": {"on"}}); p.status != http.StatusNotFound && p.status != http.StatusMethodNotAllowed {
		t.Errorf("POST /t/lime/settings/wiki = %d, want no route", p.status)
	}
}

func TestWikiEmpty(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("acme")
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	p := s.as("olga@example.com").get("/t/acme/wiki")
	if p.status != http.StatusOK {
		t.Fatalf("GET /t/acme/wiki = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "editor", "No wiki page yet", "Write what everyone in acme should know", `href="/t/acme/wiki/edit">Write the first version`,
		"loadout wiki push wiki.md --team acme", `aria-current="page">Wiki</a>`)
	if csp := p.header.Get("Content-Security-Policy"); !strings.HasSuffix(csp, "; frame-src 'self'") {
		t.Errorf("wiki CSP = %q, want frames of this origin", csp)
	}

	// bob reads acme through lime: a tab bar of Packages and Wiki, and
	// nothing to edit.
	bob := s.as("bob@example.com")
	p = bob.get("/t/acme/wiki")
	wantIn(t, p, "reader", "No wiki page yet", "Nobody has written it yet.", `<a href="/t/acme">Packages</a>`, `aria-current="page">Wiki</a>`)
	wantOut(t, p, "reader", "Write the first version", "/t/acme/settings", "/t/acme/reviews", "loadout wiki push")
	wantIn(t, bob.get("/t/acme"), "reader's packages", `<a href="/t/acme/wiki">Wiki</a>`)
	if p := bob.get("/t/acme/wiki/edit"); p.status != http.StatusForbidden {
		t.Errorf("reader opens the editor = %d, want 403", p.status)
	}
	if p := bob.post("/t/acme/wiki", url.Values{"text": {"mine"}, "base": {"0"}, "format": {"md"}}); p.status != http.StatusForbidden {
		t.Errorf("reader saves = %d, want 403\n%s", p.status, p.body)
	}
}

func TestWikiWriteAndSave(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("lime")
	bob := s.as("bob@example.com")
	p := bob.get("/t/lime/wiki/edit")
	if p.status != http.StatusOK {
		t.Fatalf("GET edit = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "new page", "Write the wiki", `name="base" value="0"`, `name="format" value="md"`,
		`value="write" formaction="/t/lime/wiki/edit" aria-pressed="true"`, `aria-describedby="wiki-hint"`, "Up to 256 KB.")

	// Write, Preview, Write: the text comes back as it was, CRLF as LF.
	text := "# Setup\r\n\r\n\r\nLeading and trailing spaces  \r\n```mermaid\r\ngraph TD\r\n```\r\n" + xss + "\r\n"
	form := url.Values{"text": {"\n" + text}, "base": {"0"}, "format": {"md"}, "note": {"first " + xss}}
	form.Set("view", "preview")
	p = bob.post("/t/lime/wiki/edit", form)
	if p.status != http.StatusOK {
		t.Fatalf("preview = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "preview", `value="preview" formaction="/t/lime/wiki/edit" aria-pressed="true"`, `<h1 id="setup">Setup</h1>`,
		"Diagrams are drawn once you save.", `class="wiki-write" hidden`, "first &lt;script&gt;")
	wantOut(t, p, "preview", xss, "data-frame")
	lf := "\n" + strings.ReplaceAll(text, "\r\n", "\n")
	if got := textareaOf(t, p); got != lf {
		t.Errorf("preview textarea = %q, want %q", got, lf)
	}
	form.Set("text", textareaOf(t, p))
	form.Set("view", "write")
	p = bob.post("/t/lime/wiki/edit", form)
	if got := textareaOf(t, p); got != lf {
		t.Errorf("write textarea = %q, want %q", got, lf)
	}
	wantOut(t, p, "write", `class="wiki-write" hidden`)

	p = bob.post("/t/lime/wiki", form)
	if p.status != http.StatusSeeOther || p.location != "/t/lime/wiki?done=saved&rev=1" {
		t.Fatalf("save = %d %q\n%s", p.status, p.location, p.body)
	}
	p = bob.get(p.location)
	wantIn(t, p, "saved", "Saved revision 1.", `<h1 id="setup">Setup</h1>`, `data-frame="/t/lime/wiki/r/1/draw?block=1"`,
		`href="/t/lime/wiki/r/1">Revision 1</a> · bob@example.com`, `href="/t/lime/wiki/edit">Edit</a>`,
		`href="/t/lime/wiki/r/1/raw"`, "Download wiki.md")
	wantOut(t, p, "saved", xss)
	rev, err := s.accounts.Wiki(t.Context(), account.Operator(), "lime", 1)
	if err != nil || rev.Doc.Text() != lf {
		t.Errorf("stored = %q, %v; want %q", rev.Doc.Text(), err, lf)
	}

	// The same text again saves nothing.
	p = bob.post("/t/lime/wiki", url.Values{"text": {lf}, "base": {"1"}, "format": {"md"}})
	if p.status != http.StatusSeeOther || p.location != "/t/lime/wiki?done=same" {
		t.Fatalf("save unchanged = %d %q", p.status, p.location)
	}
	wantIn(t, bob.get(p.location), "unchanged", "Nothing changed, so no revision was saved.")

	// The source of the page, and the page again.
	p = bob.get("/t/lime/wiki?source=1")
	wantIn(t, p, "source", `<pre class="markdown file-text">`, "&lt;script&gt;", `href="/t/lime/wiki">Show page</a>`)
	wantOut(t, p, "source", xss)
}

func TestWikiTooLarge(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("lime")
	bob := s.as("bob@example.com")
	big := strings.Repeat("a", 300<<10)
	for _, path := range []string{"/t/lime/wiki", "/t/lime/wiki/edit"} {
		p := bob.post(path, url.Values{"text": {big}, "base": {"0"}, "format": {"md"}, "view": {"preview"}})
		if p.status != http.StatusBadRequest {
			t.Fatalf("POST %s with 300 KB = %d, want 400", path, p.status)
		}
		wantIn(t, p, path, "The page is too large", `aria-invalid="true" autofocus`, `aria-describedby="form-error wiki-hint"`)
		if got := textareaOf(t, p); got != big {
			t.Errorf("POST %s: the textarea lost the text (%d bytes)", path, len(got))
		}
	}
	// Only the wiki forms take a large body.
	p := bob.post("/t/lime/members", url.Values{"email": {strings.Repeat("a", 20<<10)}})
	if p.status != http.StatusBadRequest || !strings.Contains(p.body, "The form could not be read.") {
		t.Errorf("POST /t/lime/members with 20 KiB = %d, want 400 from the form cap", p.status)
	}
	// A note on two lines names the note field.
	p = bob.post("/t/lime/wiki", url.Values{"text": {"hi"}, "base": {"0"}, "format": {"md"}, "note": {"a\nb"}})
	if p.status != http.StatusBadRequest {
		t.Fatalf("note on two lines = %d, want 400", p.status)
	}
	wantIn(t, p, "note", "The note is one line", `id="wiki-note" name="note" class="field" maxlength="200" value="a`, `aria-invalid="true" aria-describedby="form-error" autofocus`)
}

// TestWikiStale: a second browser saving on the same revision gets its
// text back with what changed, and its next save is on the newest.
func TestWikiStale(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("lime")
	s.saveWiki("alice@example.com", 0, "one\ntwo\n", "")
	if err := s.store.LinkGoogle(t.Context(), s.userID("alice@example.com"), "g-alice", "Alice "+xss); err != nil {
		t.Fatal(err)
	}
	bob := s.as("bob@example.com")
	p := bob.get("/t/lime/wiki/edit")
	if base := hiddenOf(t, p, "base"); base != "1" {
		t.Fatalf("base = %q, want 1", base)
	}
	s.saveWiki("alice@example.com", 1, "one\ntwo "+xss+"\n", "")

	mine := "one\ntwo\nthree, by bob\n"
	p = bob.post("/t/lime/wiki", url.Values{"text": {mine}, "base": {"1"}, "format": {"md"}, "note": {"bob's"}})
	if p.status != http.StatusConflict {
		t.Fatalf("stale save = %d, want 409\n%s", p.status, p.body)
	}
	wantIn(t, p, "stale", "Alice &lt;script&gt;alert(1)&lt;/script&gt; saved revision 2 while you were editing. Your text is still below.",
		`role="alert" id="form-error"`, "What changed since revision 1", `<span class="vh">added: </span>two &lt;script&gt;`,
		`name="base" value="2"`, `href="/t/lime/wiki/r/2">Open revision 2</a>`, `aria-invalid="true">`, `value="bob&#39;s"`)
	// No autofocus: it would scroll past the alert and the changes above.
	wantOut(t, p, "stale", xss, "autofocus")
	if got := textareaOf(t, p); got != mine {
		t.Errorf("textarea = %q, want bob's text %q", got, mine)
	}
	p = bob.post("/t/lime/wiki", url.Values{"text": {mine}, "base": {"2"}, "format": {"md"}})
	if p.status != http.StatusSeeOther || p.location != "/t/lime/wiki?done=saved&rev=3" {
		t.Errorf("save after merging = %d %q", p.status, p.location)
	}
}

func TestWikiHistoryAndRevisions(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("lime")
	s.saveWiki("alice@example.com", 0, "first\n", "start "+xss)
	s.saveWiki("bob@example.com", 1, "second\n"+fenced("mermaid", "graph TD")+fenced("drawio", "<mxfile><diagram><mxGraphModel><root/></mxGraphModel></diagram></mxfile>"), "")
	s.saveWiki("alice@example.com", 2, "third\n", "")
	bob := s.as("bob@example.com")

	p := bob.get("/t/lime/wiki/history")
	wantIn(t, p, "history", "3 revisions", `href="/t/lime/wiki/r/3">Revision 3</a>`, `href="/t/lime/wiki/r/1">Revision 1</a>`,
		"alice@example.com", "bob@example.com", "start &lt;script&gt;", "No note", `href="/t/lime/wiki/r/2/diff">Changes`)
	wantOut(t, p, "history", xss, `href="/t/lime/wiki/r/1/diff"`, "loadout keeps the newest")

	p = bob.get("/t/lime/wiki/r/2")
	wantIn(t, p, "old revision", `role="note"><p>You are looking at revision 2. The newest is <a href="/t/lime/wiki">revision 3</a>.`,
		`href="/t/lime/wiki/edit?from=2">Edit from this revision</a>`, `data-frame="/t/lime/wiki/r/2/draw?block=1"`,
		`data-frame="/t/lime/wiki/r/2/draw?drawio=1"`)
	if csp := p.header.Get("Content-Security-Policy"); !strings.HasSuffix(csp, "; frame-src 'self'") {
		t.Errorf("revision CSP = %q, want frames of this origin", csp)
	}
	p = bob.get("/t/lime/wiki/edit?from=2")
	if got := textareaOf(t, p); !strings.HasPrefix(got, "second\n") {
		t.Errorf("edit from 2 = %q, want revision 2's text", got)
	}
	wantIn(t, p, "edit from 2", `name="base" value="3"`, `name="from" value="2"`, "This text is revision 2.")

	p = bob.get("/t/lime/wiki/r/3/diff")
	wantIn(t, p, "diff", "Changes from revision 2 to 3", `<span class="vh">added: </span>third`, `<span class="vh">removed: </span>second`)

	for _, path := range []string{"/t/lime/wiki/r/4", "/t/lime/wiki/r/0", "/t/lime/wiki/r/02", "/t/lime/wiki/r/-1", "/t/lime/wiki/r/x", "/t/lime/wiki/r/1/diff", "/t/lime/wiki/edit?from=9"} {
		if p := bob.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, p.status)
		}
	}
}

func TestWikiRawAndFrames(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("lime")
	s.saveWiki("bob@example.com", 0, fenced("mermaid", "graph TD\nA-->B")+fenced("drawio", "<mxfile><diagram><mxGraphModel><root/></mxGraphModel></diagram></mxfile>"), "")
	bob := s.as("bob@example.com")

	p := bob.get("/t/lime/wiki/r/1/raw")
	if p.status != http.StatusOK || p.header.Get("Content-Type") != "text/plain; charset=utf-8" ||
		p.header.Get("Content-Disposition") != `attachment; filename=wiki.md` ||
		p.header.Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" ||
		!strings.HasPrefix(p.body, "```mermaid\n") {
		t.Errorf("raw = %d %v\n%s", p.status, p.header, p.body)
	}

	p = bob.get("/t/lime/wiki/r/1/draw?block=1")
	if p.status != http.StatusOK || p.header.Get("X-Frame-Options") != "SAMEORIGIN" ||
		!strings.Contains(p.header.Get("Content-Security-Policy"), "sandbox allow-scripts") {
		t.Fatalf("frame = %d %v", p.status, p.header)
	}
	wantIn(t, p, "frame", "graph TD\nA--&gt;B")
	wantIn(t, bob.get("/t/lime/wiki/r/1/draw?drawio=1"), "drawio frame", "&lt;mxfile&gt;")
	for _, q := range []string{"block=0", "block=2", "block=21", "drawio=2", "", "page=1"} {
		if p := bob.get("/t/lime/wiki/r/1/draw?" + q); p.status != http.StatusNotFound {
			t.Errorf("frame ?%s = %d, want 404", q, p.status)
		}
	}
	if p := bob.get("/t/lime/wiki/r/2/draw?block=1"); p.status != http.StatusNotFound {
		t.Errorf("frame of a missing revision = %d, want 404", p.status)
	}
	if _, err := s.reg.SetWiki(t.Context(), "lime", false); err != nil {
		t.Fatal(err)
	}
	if p := bob.get("/t/lime/wiki/r/1/draw?block=1"); p.status != http.StatusNotFound {
		t.Errorf("frame of a wiki that is off = %d, want 404", p.status)
	}
}

// TestWikiMDX: an MDX page keeps its format through an edit and is
// downloaded as wiki.mdx.
func TestWikiMDX(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("lime")
	bob := s.as("bob@example.com")
	doc, err := wiki.NewDoc(wiki.MDX, "import { Tabs } from './tabs'\n\n# Hi\n\n<Tabs>"+xss+"</Tabs>\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.accounts.SaveWiki(t.Context(), account.UserActor(s.userID("bob@example.com")), "lime", 0, doc, ""); err != nil {
		t.Fatal(err)
	}
	p := bob.get("/t/lime/wiki")
	wantIn(t, p, "mdx", "<pre><code>import { Tabs }", `<h1 id="hi">Hi</h1>`, " · MDX", "Download wiki.mdx")
	wantOut(t, p, "mdx", xss)
	p = bob.get("/t/lime/wiki/edit")
	wantIn(t, p, "mdx edit", `name="format" value="mdx"`, "This page is MDX")
	if p := bob.get("/t/lime/wiki/r/1/raw"); p.header.Get("Content-Disposition") != "attachment; filename=wiki.mdx" {
		t.Errorf("raw = %v", p.header)
	}
}

// TestWikiLinksTheProjectAbove: a team's wiki says its project has one.
func TestWikiLinksTheProjectAbove(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("lime")
	bob := s.as("bob@example.com")
	wantOut(t, bob.get("/t/lime/wiki"), "project off", "the project above")
	s.wikiOn("acme")
	wantIn(t, bob.get("/t/lime/wiki"), "project on", `acme, the project above, <a href="/t/acme/wiki">has a wiki too</a>.`)
}

func (s *uiSite) userID(email string) string {
	s.t.Helper()
	u, err := s.store.UserByEmail(s.t.Context(), email)
	if err != nil {
		s.t.Fatal(err)
	}
	return u.ID
}

func fenced(lang, body string) string { return "```" + lang + "\n" + body + "\n```\n\n" }

// TestWikiSectionsFold: the page, an old revision and the preview fold
// every ## section; the page and the revision start closed, the preview
// open. No heading takes an id the page around it uses.
func TestWikiSectionsFold(t *testing.T) {
	s := newUISite(t)
	s.wikiOn("lime")
	bob := s.as("bob@example.com")
	ids := regexp.MustCompile(` id="([^"]+)"`)

	// A heading for every id of the page, as an author might write one.
	var text strings.Builder
	text.WriteString("# Lime\n\nIntro.\n\n## Setup " + xss + "\n\n" + fenced("mermaid", "graph TD") + "\n### Mac\n\nbrew\n")
	for _, m := range ids.FindAllStringSubmatch(bob.get("/t/lime/wiki").body, -1) {
		text.WriteString("\n## " + strings.ReplaceAll(m[1], "-", " ") + "\n")
	}
	s.saveWiki("alice@example.com", 0, text.String(), "")
	s.saveWiki("alice@example.com", 1, text.String()+"\nMore.\n", "")

	tests := []struct {
		name, path string
		form       url.Values
		want       []string
	}{
		{"page", "/t/lime/wiki", nil, []string{
			"<h1 id=\"lime\">Lime</h1>\n<p>Intro.</p>\n<details class=\"wiki-section\">\n<summary><h2 id=\"setup-scriptalert1script\">Setup &lt;script&gt;",
			"<div class=\"wiki-section-body\">\n<figure class=\"diagram\" data-frame=\"/t/lime/wiki/r/2/draw?block=1\"", `<h3 id="mac">Mac</h3>`,
		}},
		{"old revision", "/t/lime/wiki/r/1", nil, []string{`<details class="wiki-section">`, `data-frame="/t/lime/wiki/r/1/draw?block=1"`}},
		{"preview", "/t/lime/wiki/edit", url.Values{"text": {text.String()}, "base": {"2"}, "format": {"md"}, "view": {"preview"}}, []string{
			"<details class=\"wiki-section\" open>\n<summary><h2 id=\"setup-scriptalert1script\">",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p page
			if tt.form != nil {
				p = bob.post(tt.path, tt.form)
			} else {
				p = bob.get(tt.path)
			}
			if p.status != http.StatusOK {
				t.Fatalf("status = %d", p.status)
			}
			wantIn(t, p, tt.name, tt.want...)
			wantOut(t, p, tt.name, xss)
			seen := map[string]bool{}
			for _, m := range ids.FindAllStringSubmatch(p.body, -1) {
				if seen[m[1]] {
					t.Errorf("id %q is on the page twice", m[1])
				}
				seen[m[1]] = true
			}
		})
	}
}
