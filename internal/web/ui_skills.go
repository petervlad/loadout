package web

import (
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/builtin"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/skill"
)

// The Start here page and the pages of loadout's own skills, readable by
// anyone, signed in or not: for a visitor /skills is the front door, so it
// says what loadout is and how to start before it lists the skills. The
// skills' text ships in the public binary. They are not a namespace:
// nothing is published or proposed here.
//
//	GET /skills          Start here: what loadout is, the words, the first
//	                     commands, and the skills as cards
//	GET /skills/{name}   one skill: SKILL.md and its files, as Markdown

// BuiltinSkills are the skills this registry build installs with
// `loadout login`, for the /skills pages.
type BuiltinSkills struct {
	// Packs are the skills, built from the same embedded files the CLI
	// installs.
	Packs []*pack.Pack
	// Version is the loadout version the text belongs to.
	Version string
}

// setupCommand installs the skills by hand; login does it on its own.
const setupCommand = "loadout setup --skills"

// startTitle names the /skills page, in its heading, the sidebar and the
// skill pages' back link.
const startTitle = "Start here"

// skillCard is one skill on the /skills page.
type skillCard struct {
	Name string
	// Title, Summary and Asks present the skill in plain words (see
	// builtin.Intro); a skill without an intro falls back to its name and
	// What.
	Title, Summary string
	Asks           []string
	// What the skill does and When the agent loads it, from its
	// description ("... Use when ...").
	What, When string
}

type skillsView struct {
	Cards   []skillCard
	Install string
	Version string
	// Login connects the CLI to this registry; InstallDocs explains how to
	// get the CLI.
	Login       string
	InstallDocs string
}

// skillFile is one supporting file of a skill, on its page.
type skillFile struct {
	Path string
	// ID is the anchor links to the file point at.
	ID string
	// HTML is set for Markdown; other text is shown as it is, in Text.
	HTML template.HTML
	Text string
}

type skillView struct {
	skillCard
	Body    template.HTML
	Files   []skillFile
	Install string
	Version string
}

func (h *handler) routeSkills(mux *http.ServeMux) {
	mux.HandleFunc("GET /skills", h.skillsPage)
	mux.HandleFunc("GET /skills/{name}", h.skillPage)
}

func (h *handler) skillsPage(w http.ResponseWriter, r *http.Request) {
	v := skillsView{Install: setupCommand, Version: h.ui.Skills.Version, Login: "loadout login --remote " + h.origin, InstallDocs: h.docsLink(r, installPath)}
	for _, p := range h.ui.Skills.Packs {
		s, _ := p.Skill()
		v.Cards = append(v.Cards, cardOf(s))
	}
	h.renderOpen(w, r, http.StatusOK, "skills.html", uiView{Title: startTitle, Page: v})
}

func (h *handler) skillPage(w http.ResponseWriter, r *http.Request) {
	i := slices.IndexFunc(h.ui.Skills.Packs, func(p *pack.Pack) bool { return p.ID().Name() == r.PathValue("name") })
	if i < 0 {
		h.renderOpen(w, r, http.StatusNotFound, "error.html", uiView{
			Title: "Not found",
			Page:  errorPage{Heading: "Nothing here", Text: "loadout has no skill by that name.", Back: "/skills"},
		})
		return
	}
	s, _ := h.ui.Skills.Packs[i].Skill()
	files := s.Files()
	v := skillView{skillCard: cardOf(s), Install: setupCommand, Version: h.ui.Skills.Version}
	// One count of quizzes for the whole page keeps their ids unique.
	qs := &quizzes{}
	v.Body, _ = docParse{links: skillLinks(".", files), quizzes: qs}.render(s.Body)
	for _, p := range slices.Sorted(maps.Keys(files)) {
		f := skillFile{Path: p, ID: fileAnchor(p)}
		if strings.HasSuffix(p, ".md") {
			f.HTML, _ = docParse{links: skillLinks(path.Dir(p), files), quizzes: qs}.render(string(files[p]))
		} else {
			f.Text = string(files[p])
		}
		v.Files = append(v.Files, f)
	}
	h.renderOpen(w, r, http.StatusOK, "skill.html", uiView{Title: v.Title, Page: v})
}

// renderOpen renders a page anyone may read: with the signed-in person's
// sidebar, or with the sidebar of a visitor, which offers to sign in.
func (h *handler) renderOpen(w http.ResponseWriter, r *http.Request, status int, page string, v uiView) {
	v.SkillsCurrent = true
	u, ok := h.user(r)
	if !ok {
		v.SignIn = "/auth/login?" + url.Values{"next": {r.URL.Path}}.Encode()
		h.renderUI(w, r, status, page, u, nil, "", v)
		return
	}
	places, err := h.ui.Places.Places(r.Context(), u)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderUI(w, r, status, page, u, places, "", v)
}

// cardOf presents a skill: its plain-words intro, and its description
// split into what it does and when the agent loads it (the description
// says both, in that order).
func cardOf(s *skill.Skill) skillCard {
	what, when, ok := strings.Cut(s.Description, " Use when ")
	c := skillCard{Name: s.Name, Title: s.Name, Summary: what, What: what}
	if ok {
		c.When = "Your AI tool loads it when " + when
	}
	if i, ok := builtin.IntroOf(s.Name); ok {
		c.Title, c.Summary, c.Asks = i.Title, i.Summary, i.Asks
	}
	return c
}

// fileAnchor is the id of a file's section: references/models.md is
// file-references-models-md.
func fileAnchor(p string) string {
	return "file-" + strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(p)
}

// skillLinks leads the relative links of a skill file in dir to the
// sections of the skill's page: SKILL.md and each supporting file.
func skillLinks(dir string, files map[string][]byte) docLinks {
	all := map[string]bool{skill.File: true}
	for p := range files {
		all[p] = true
	}
	return docLinks{dir: dir, files: all, to: func(p string) string {
		if p == skill.File {
			return "#skill-md"
		}
		return "#" + fileAnchor(p)
	}}
}
