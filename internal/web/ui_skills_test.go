package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/builtin"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/skill"
	"github.com/petervlad/loadout/internal/web"
)

// skillsSite is a registry whose pages show loadout's own skills at 1.2.3.
func skillsSite(t *testing.T) *uiSite {
	t.Helper()
	packs, err := builtin.Packs("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	return newUISiteWith(t, func(ui *web.UI) { ui.Skills = &web.BuiltinSkills{Packs: packs, Version: "1.2.3"} })
}

// TestSkillsPagesForEveryone: anyone reads the built-in skills, signed out,
// in no team, as a member or as an admin, and every signed-in page lists
// them above the projects.
func TestSkillsPagesForEveryone(t *testing.T) {
	s := skillsSite(t)
	if _, err := s.accounts.SetAdmin(t.Context(), account.Operator(), "root@example.com", true); err != nil {
		t.Fatal(err)
	}
	visitors := map[string]func() *browser{
		"signed out":        s.browser,
		"member of nothing": func() *browser { return s.as("nobody@example.com") },
		"member":            func() *browser { return s.as("bob@example.com") },
		"admin":             func() *browser { return s.as("root@example.com") },
	}
	for who, open := range visitors {
		t.Run(who, func(t *testing.T) {
			b := open()
			p := b.get("/skills")
			if p.status != http.StatusOK {
				t.Fatalf("/skills = %d", p.status)
			}
			wantIn(t, p, who+" /skills", "<h1>Start here</h1>", "loadout never runs AI and never sees your questions.",
				`<h2 id="how-h">How it works</h2>`, `<h2 id="words-h">Words you will meet</h2>`, "<dt>Knowledge base</dt>",
				`<h2 id="start-h">Get started</h2>`, `<code class="cmd" id="start-login">loadout login --remote http://localhost</code>`,
				"loadout install --all", "loadout sync", `href="https://loadout.lat/docs/?from=http%3A%2F%2Flocalhost%2Fskills#1-install-the-cli"`,
				`href="/skills/loadout-setup"`, `href="/skills/loadout-review"`, "Set up loadout</a>", "<li>Install my team&#39;s packages</li>",
				"<code>loadout-setup</code>", "loadout setup --skills", "loadout 1.2.3", `aria-current="page">`, "Start here</a>")
			wantOut(t, p, who+" /skills", "Your AI tool loads it when")
			p = b.get("/skills/loadout-review")
			if p.status != http.StatusOK {
				t.Fatalf("/skills/loadout-review = %d", p.status)
			}
			wantIn(t, p, who+" skill page", "<h1>Review a teammate&#39;s change</h1>", "<code>loadout-review</code>", "Built in",
				"Your AI tool loads it when", `href="/skills#skills-h"`,
				"<code>SKILL.md</code>", "<h1>Reviewing a loadout proposal</h1>", "<code>--safe</code>", "loadout setup --skills")
			if b.get("/skills/nope").status != http.StatusNotFound {
				t.Error("an unknown skill is not a 404")
			}
			if who == "signed out" {
				wantIn(t, p, who, `href="/auth/login?next=%2Fskills%2Floadout-review"`, "Sign in")
				wantOut(t, p, who, `href="/account"`, "Projects")
				wantHelpLinks(t, b.get("/skills"), "?from=http%3A%2F%2Flocalhost%2Fskills", `class="side-item side-user" href="/auth/login?next=%2Fskills"`)
				// The front door offers to sign in, next to the steps.
				start := b.get("/skills")
				wantIn(t, start, who+" /skills", `class="btn btn-lg btn-primary" href="/auth/login?next=%2Fskills"`, "Sign in, check that the code matches")
				return
			}
			// Every signed-in page has the entry, above the projects.
			// Signed in already: the steps do not ask for it again.
			start := b.get("/skills")
			wantIn(t, start, who+" /skills", "You are already signed in")
			wantOut(t, start, who+" /skills", "btn-primary\" href=\"/auth/login", "Already in a team?")
			home := b.get("/account")
			wantIn(t, home, who+" sidebar", `<a class="side-item" href="/skills">`)
			wantOut(t, home, who+" sidebar", `href="/auth/login?next=`)
		})
	}
}

// TestSkillPageLinksItsFiles: a skill's relative links lead to its files'
// sections on the same page, which render as Markdown.
func TestSkillPageLinksItsFiles(t *testing.T) {
	s := skillsSite(t)
	p := s.browser().get("/skills/loadout-setup")
	wantIn(t, p, "skill page", `href="#file-references-troubleshooting-md"`, `id="file-references-troubleshooting-md"`,
		"<code>references/troubleshooting.md</code>", "<table>", "<h1>Troubleshooting loadout</h1>")
}

// TestSkillPageIsEscaped: the text goes through the safe Markdown
// renderer, so raw HTML is text, dangerous links lose their target and
// images are their alt text.
func TestSkillPageIsEscaped(t *testing.T) {
	body := "Hi " + xss + "\n\n[x](javascript:alert(1)) ![logo](http://evil.example/x.png)\n\n<div onclick=\"bad()\">block</div>\n"
	sk, err := skill.New("tricky", "Does tricks "+xss+". Use when asked.", "", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	main, err := sk.Marshal("")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pack.FromFiles(map[string][]byte{pack.ManifestFile: manifest("loadout", "tricky", "skill", "1.0.0", "Does tricks."), skill.File: main})
	if err != nil {
		t.Fatal(err)
	}
	s := newUISiteWith(t, func(ui *web.UI) { ui.Skills = &web.BuiltinSkills{Packs: []*pack.Pack{p}, Version: "dev"} })
	page := s.browser().get("/skills/tricky")
	wantIn(t, page, "escaped", "&lt;script&gt;", "&lt;div onclick=", "logo")
	wantOut(t, page, "escaped", xss, "javascript:", "<img", "evil.example", `<div onclick`)
	// A skill without a plain-words intro shows its name and description,
	// escaped, and nothing to ask.
	start := s.browser().get("/skills")
	wantIn(t, start, "card", "tricky</a>", "Does tricks &lt;script&gt;")
	wantOut(t, start, "card", xss, `class="asks"`)
}

// TestNoSkillsPagesWithoutSkills: a UI without built-in skills has neither
// the pages nor the sidebar entry.
func TestNoSkillsPagesWithoutSkills(t *testing.T) {
	s := newUISite(t)
	if p := s.browser().get("/skills"); p.status != http.StatusNotFound {
		t.Errorf("/skills = %d, want 404", p.status)
	}
	wantOut(t, s.as("bob@example.com").get("/t/lime"), "sidebar", `href="/skills"`)
}

// The home page shows loadout's own skills when the registry has them,
// each linking to its page.
func TestHomeListsTheSkills(t *testing.T) {
	p := skillsSite(t).as("bob@example.com").get("/")
	wantIn(t, p, "home", `id="skills-h">Skills that come with loadout`, `<a href="/skills#skills-h">About them</a>`, `<a href="/skills/loadout-setup">`,
		"Set up loadout</a>")
	wantOut(t, newUISite(t).as("bob@example.com").get("/"), "home without skills", `id="skills-h"`)
}

// docsHref is a link into the docs, with its address.
var docsHref = regexp.MustCompile(`href="(https://loadout\.lat/docs/[^"]*)"`)

// TestDocsLinksCarryFrom: every link from a registry page into the docs
// names that page in ?from, so the docs' "loadout docs" link leads back to
// it, and keeps a #fragment after the query. A page address with markup in
// it reaches the docs encoded.
func TestDocsLinksCarryFrom(t *testing.T) {
	s := skillsSite(t)
	checked := 0
	paths := []string{"/skills", "/skills/loadout-review", "/account", "/t/lime", "/t/lime?kind=skill", "/t/lime/wiki", "/skills?q=" + url.QueryEscape(xss)}
	for _, who := range []string{"signed out", "bob@example.com"} {
		b := s.browser()
		if who != "signed out" {
			b = s.as(who)
		}
		for _, path := range paths {
			p := b.get(path)
			if p.status != http.StatusOK {
				continue
			}
			checked++
			links := docsHref.FindAllStringSubmatch(p.body, -1)
			if len(links) < 2 {
				t.Errorf("%s %s: %d docs links, want the sidebar's two at least", who, path, len(links))
			}
			from := "from=" + url.QueryEscape("http://localhost"+path)
			for _, l := range links {
				u, err := url.Parse(strings.ReplaceAll(l[1], "&amp;", "&"))
				if err != nil {
					t.Fatalf("%s %s: %v", who, path, err)
				}
				if u.RawQuery != from {
					t.Errorf("%s %s: %s has query %q, want %q", who, path, l[1], u.RawQuery, from)
				}
			}
			wantOut(t, p, who+" "+path, xss)
		}
	}
	if checked < 8 {
		t.Errorf("checked %d pages, want 8 at least", checked)
	}
	wantIn(t, s.browser().get("/skills"), "install link", `href="https://loadout.lat/docs/?from=http%3A%2F%2Flocalhost%2Fskills#1-install-the-cli"`)
}
