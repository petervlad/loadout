package web

// The team pages, served with Config.UI (HTML, server-rendered; the script
// only adds Copy buttons and small conveniences, every page works without
// it):
//
//	GET  /                                     the first team, or sign in
//	GET  /t/{ns}                               packages the team reads (?q= filters)
//	GET  /t/{ns}/p/{team}/{name}               one package: releases, main file, files (?version=)
//	GET  /t/{ns}/p/{team}/{name}/{version}/... a release's zip, files and previews (ui_files.go)
//	GET  /t/{ns}/p/{team}/{name}/delete        what deleting it removes, and the form (admins)
//	POST /t/{ns}/p/{team}/{name}/delete        delete it, then back to /t/{ns} with the counts
//	GET|POST /t/{ns}/p/{team}/{name}/delete-request   ask for a deletion (everyone who reads it; ui_deletions.go)
//	GET  /t/{ns}/deletions/{id}                one request to delete a package
//	POST /t/{ns}/deletions/{id}/approve|reject|withdraw
//	GET  /t/{ns}/reviews                       proposals (?status=all for closed ones)
//	GET  /t/{ns}/reviews/{id}                  one proposal (?prompt=, ?run=, ?report=)
//	GET  /t/{ns}/reviews/{id}/diff             its full diff from its base (?view=since|effect when behind)
//	POST /t/{ns}/reviews/{id}/approve|reject|withdraw
//	GET  /t/{ns}/settings                      members, review setting, models, machine tokens
//	GET  /t/{ns}/members                       301 to settings, query kept (the old address)
//	POST /t/{ns}/members                       add a member
//	POST /t/{ns}/members/role|remove
//	POST /t/{ns}/invite                        add a person to a project and its teams
//	POST /t/{ns}/tokens, /t/{ns}/tokens/revoke
//	POST /t/{ns}/settings/review               require review on or off (also POST /t/{ns}/settings)
//	GET  /t/{ns}/tiers, POST /t/{ns}/tiers     which model each AI tool runs per hint
//	GET  /t/{ns}/wiki/...                      the namespace's wiki page, while it is on (ui_wiki.go)
//	POST /t/{ns}/settings/course               the knowledge base the wiki shows as its course
//	GET  /new, POST /new                       create a project or team
//	GET  /skills, /skills/{name}               loadout's own skills, for anyone (ui_skills.go)
//	GET  /account                              profile, teams, appearance
//	GET  /account/server                       server, costs and counts (admins only)
//	POST /account/theme                        light, dark or system
//	POST /t/{ns}/settings/memory               team memory on or off (ui_memory.go)
//	POST /account/memory-tokens, /account/memory-tokens/revoke   memory tokens, shown once
//
// Every page acts as the signed-in user with their role in the namespace
// (account.Roles.Principal); the registry and account services decide what
// is allowed, the pages only hide what would be refused. A namespace the
// user cannot open is a 404, like one that does not exist.

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/tier"
)

// Registry is what the team pages need from the registry service.
type Registry interface {
	Packages(ctx context.Context, pr registry.Principal) ([]registry.Release, error)
	Releases(ctx context.Context, pr registry.Principal, id pack.ID) ([]registry.Release, error)
	Fetch(ctx context.Context, pr registry.Principal, id pack.ID, version string) (registry.Release, []byte, error)
}

// Proposals is what the review pages need from the proposal service.
type Proposals interface {
	Proposals(ctx context.Context, pr registry.Principal, pkg pack.ID, status registry.ProposalStatus) ([]registry.Proposal, error)
	Proposal(ctx context.Context, pr registry.Principal, id string) (registry.Proposal, error)
	ProposalBundle(ctx context.Context, pr registry.Principal, id string) (registry.Proposal, []byte, error)
	Standings(ctx context.Context, pr registry.Principal, props []registry.Proposal) ([]registry.Standing, error)
	Approve(ctx context.Context, pr registry.Principal, id string, in registry.ApproveInput) (registry.Proposal, registry.Release, error)
	Reject(ctx context.Context, pr registry.Principal, id, note string) (registry.Proposal, error)
	Withdraw(ctx context.Context, pr registry.Principal, id, note string) (registry.Proposal, error)
	AddPrompt(ctx context.Context, pr registry.Principal, id, text string) (registry.Prompt, error)
	EditPrompt(ctx context.Context, pr registry.Principal, id string, seq int, text string) (registry.Prompt, error)
	RemovePrompt(ctx context.Context, pr registry.Principal, id string, seq int) error
	ReviewText(ctx context.Context, pr registry.Principal, id string, seq int) (registry.Review, []byte, error)
	ReviewData(ctx context.Context, pr registry.Principal, id string, seq int) (registry.Review, []byte, error)
}

// Places tells which namespaces a user can open and who they act as there.
type Places interface {
	Places(ctx context.Context, u account.User) ([]account.Place, error)
	Principal(ctx context.Context, u account.User, namespace string) (registry.Principal, account.Place, error)
}

// Teams is what the settings page needs from the account service.
type Teams interface {
	MemberDetails(ctx context.Context, actor account.Actor, namespace string) ([]account.MemberDetail, error)
	InheritedMembers(ctx context.Context, actor account.Actor, namespace string) ([]account.MemberDetail, error)
	AddMember(ctx context.Context, actor account.Actor, namespace, email string, role account.Role) (account.Membership, bool, error)
	SetMember(ctx context.Context, actor account.Actor, namespace, email string, role account.Role) (account.Membership, error)
	RemoveMember(ctx context.Context, actor account.Actor, namespace, email string) (int, error)
	InviteToProject(ctx context.Context, actor account.Actor, project, email string, projectRole account.Role, teams map[string]account.Role) (account.Invitation, error)
	EmailEnabled() bool
	MachineTokens(ctx context.Context, actor account.Actor, namespace string) ([]registry.Token, error)
	CreateMachineToken(ctx context.Context, actor account.Actor, namespace string, scope registry.TokenScope, label string) (string, registry.Token, error)
	RevokeMachineToken(ctx context.Context, actor account.Actor, namespace, id string) error
	SetRequireReview(ctx context.Context, actor account.Actor, name string, on bool) (registry.Namespace, error)
	SetWiki(ctx context.Context, actor account.Actor, name string, on bool) (registry.Namespace, error)
	SetCourse(ctx context.Context, actor account.Actor, namespace string, course pack.ID) (registry.Namespace, error)
	ModelTiers(ctx context.Context, actor account.Actor, namespace string) (tier.Stack, error)
	SetModelTiers(ctx context.Context, actor account.Actor, namespace string, t tier.Table) (tier.Stack, error)
	CreateNamespace(ctx context.Context, actor account.Actor, name, parent string, kind registry.NamespaceKind) (registry.Namespace, error)
	DisplayNames(ctx context.Context, ids []string) (map[string]string, error)
}

var (
	_ Registry  = (*registry.Service)(nil)
	_ Proposals = (*registry.ProposalService)(nil)
	_ Places    = (*account.Roles)(nil)
	_ Teams     = (*account.Service)(nil)
	_ Wiki      = (*account.Service)(nil)
)

// UI holds the services behind the team pages.
type UI struct {
	Registry  Registry
	Proposals Proposals
	Places    Places
	Teams     Teams
	// Tools are the AI tools the model settings page has a section for,
	// in order: the installer's adapters.
	Tools []ModelTool
	// Server reports on the server for the admins' page; nil leaves the
	// page out.
	Server ServerStats
	// Now is time.Now in production; it dates "2 days ago".
	Now func() time.Time
	// Skills are loadout's own skills for the /skills pages and the
	// sidebar entry; nil leaves both out.
	Skills *BuiltinSkills
	// Wiki serves the namespaces' wiki pages; nil leaves out their
	// routes, the Wiki tab and the switch on Settings.
	Wiki Wiki
	// Memory is the team memory service; nil leaves out the Team memory
	// switch on Settings and its route.
	Memory Memory
	// MemoryTokens makes and revokes the memory tokens of the Account
	// page; nil leaves the section out.
	MemoryTokens MemoryTokens
	// MemoryChanged, if set, is called after the Memory tab retracts or
	// moves an entry, so the MCP server drops the index it cached
	// (mcpmemory.Server.InvalidateIndex). Nil leaves the index to expire.
	MemoryChanged func()
	// Deletes deletes packages at once for admins; nil leaves out the
	// delete routes and the package page's Delete section.
	Deletes Deletes
	// Requests reviews requests to delete a package; nil leaves out their
	// routes, the Ask to delete button and the deletions in the reviews list.
	Requests DeleteRequests
}

// uiPages is the UI with its parsed templates.
type uiPages struct {
	UI
	pages map[string]*template.Template
	// frame is the diagram frame, a document of its own (no layout).
	frame *template.Template
}

func newUIPages(ui *UI) (*uiPages, error) {
	if ui.Registry == nil || ui.Proposals == nil || ui.Places == nil || ui.Teams == nil || ui.Now == nil {
		return nil, errors.New("web UI: every service and Now must be set")
	}
	names, err := fs.Glob(assets, "templates/ui/*.html")
	if err != nil {
		return nil, fmt.Errorf("web UI templates: %w", err)
	}
	pages := map[string]*template.Template{}
	for _, n := range names {
		base := strings.TrimPrefix(n, "templates/ui/")
		if base == "layout.html" {
			continue
		}
		t, err := template.New(base).Funcs(uiFuncs(ui.Now)).ParseFS(assets, "templates/ui/layout.html", n)
		if err != nil {
			return nil, fmt.Errorf("web UI template %s: %w", base, err)
		}
		pages[base] = t
	}
	frame, err := template.New("frame.html").Funcs(assetFuncs()).ParseFS(assets, "templates/frame/frame.html")
	if err != nil {
		return nil, fmt.Errorf("web UI template frame.html: %w", err)
	}
	return &uiPages{UI: *ui, pages: pages, frame: frame}, nil
}

func (h *handler) routeUI(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /account", h.uiAccount)
	mux.HandleFunc("GET /account/server", h.serverPage)
	mux.HandleFunc("GET /new", h.newPage)
	mux.HandleFunc("POST /new", h.createNamespace)
	mux.HandleFunc("POST /account/theme", h.setTheme)
	mux.HandleFunc("GET /t/{ns}", h.inTeam(h.packagesPage))
	mux.HandleFunc("GET /t/{ns}/p/{team}/{name}", h.inTeam(h.packagePage))
	mux.HandleFunc("GET /t/{ns}/p/{team}/{name}/{version}/download", h.inTeam(h.download))
	mux.HandleFunc("GET /t/{ns}/p/{team}/{name}/{version}/raw/{path...}", h.inTeam(h.rawFile))
	mux.HandleFunc("GET /t/{ns}/p/{team}/{name}/{version}/files/{path...}", h.inTeam(h.filePage))
	mux.HandleFunc("GET /t/{ns}/p/{team}/{name}/{version}/draw/{path...}", h.inTeam(h.drawFrame))
	mux.HandleFunc("GET /t/{ns}/reviews", h.inTeam(h.reviewsPage))
	mux.HandleFunc("GET /t/{ns}/reviews/{id}", h.inTeam(h.proposalPage))
	mux.HandleFunc("GET /t/{ns}/reviews/{id}/diff", h.inTeam(h.diffPage))
	mux.HandleFunc("POST /t/{ns}/reviews/{id}/{action}", h.inTeam(h.decideProposal))
	mux.HandleFunc("POST /t/{ns}/reviews/{id}/prompts", h.inTeam(h.addPrompt))
	mux.HandleFunc("POST /t/{ns}/reviews/{id}/prompts/{seq}", h.inTeam(h.editPrompt))
	mux.HandleFunc("POST /t/{ns}/reviews/{id}/prompts/{seq}/remove", h.inTeam(h.removePrompt))
	mux.HandleFunc("GET /t/{ns}/settings", h.inTeam(h.settingsPage))
	mux.HandleFunc("GET /t/{ns}/members", h.toSettings)
	mux.HandleFunc("POST /t/{ns}/members", h.inTeam(h.addMember))
	mux.HandleFunc("POST /t/{ns}/members/role", h.inTeam(h.setMemberRole))
	mux.HandleFunc("POST /t/{ns}/members/remove", h.inTeam(h.removeMember))
	mux.HandleFunc("POST /t/{ns}/invite", h.inTeam(h.invite))
	mux.HandleFunc("POST /t/{ns}/tokens", h.inTeam(h.createToken))
	mux.HandleFunc("POST /t/{ns}/tokens/revoke", h.inTeam(h.revokeToken))
	mux.HandleFunc("POST /t/{ns}/settings/review", h.inTeam(h.saveReview))
	// The address of the review form before Settings, so a page open from
	// before the release still saves.
	mux.HandleFunc("POST /t/{ns}/settings", h.inTeam(h.saveReview))
	mux.HandleFunc("GET /t/{ns}/tiers", h.inTeam(h.tiersPage))
	mux.HandleFunc("POST /t/{ns}/tiers", h.inTeam(h.saveTiers))
	if h.ui.Deletes != nil {
		// One segment fewer than .../{version}/download, so the mux sees no conflict.
		mux.HandleFunc("GET /t/{ns}/p/{team}/{name}/delete", h.inTeam(h.deletePage))
		mux.HandleFunc("POST /t/{ns}/p/{team}/{name}/delete", h.inTeam(h.deletePackage))
	}
	if h.ui.Requests != nil {
		mux.HandleFunc("GET /t/{ns}/p/{team}/{name}/delete-request", h.inTeam(h.askPage))
		mux.HandleFunc("POST /t/{ns}/p/{team}/{name}/delete-request", h.inTeam(h.askDeletion))
		mux.HandleFunc("GET /t/{ns}/deletions/{id}", h.inTeam(h.deletionPage))
		mux.HandleFunc("POST /t/{ns}/deletions/{id}/{action}", h.inTeam(h.decideRequest))
	}
	if h.ui.Skills != nil {
		h.routeSkills(mux)
	}
	if h.ui.Wiki != nil {
		h.routeWiki(mux)
	}
	h.routeMemory(mux)
}

// team is the request context of a team page: the signed-in user, where
// they are and who they act as there.
type team struct {
	user   account.User
	places []account.Place
	place  account.Place
	pr     registry.Principal
}

// ns is the namespace of the page.
func (t team) ns() string { return t.place.Namespace.Name }

// actor is the user, for the account service.
func (t team) actor() account.Actor { return account.UserActor(t.user.ID) }

// member reports whether the user has a role in the namespace itself, not
// only reads it through a team below.
func (t team) member() bool { return t.place.Via == "" }

type teamHandler func(w http.ResponseWriter, r *http.Request, t team)

// inTeam wraps a team page: it sends a visitor without a session to sign
// in, checks the CSRF token of a POST, and answers 404 for a namespace the
// user cannot open.
func (h *handler) inTeam(next teamHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !h.form(w, r) {
			return
		}
		u, ok := h.user(r)
		if !ok {
			h.toLogin(w, r)
			return
		}
		ns := r.PathValue("ns")
		if ident.Check("namespace", ns) != nil {
			h.notFound(w, r, u)
			return
		}
		pr, place, err := h.ui.Places.Principal(r.Context(), u, ns)
		if errors.Is(err, registry.ErrNotFound) {
			h.notFound(w, r, u)
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		places, err := h.ui.Places.Places(r.Context(), u)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		next(w, r, team{user: u, places: places, place: place, pr: pr})
	}
}

// toLogin sends the browser to sign in and back here. Only a GET comes back
// to the same place; a POST comes back to its page.
func (h *handler) toLogin(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Path
	if r.Method == http.MethodGet && r.URL.RawQuery != "" {
		next += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, "/auth/login?"+url.Values{"next": {safeNext(next)}}.Encode(), http.StatusSeeOther)
}

// home is the signed-in user's start page: what loadout is for, the kinds
// of packages and the layers, and the command that sets up a machine. A
// visitor goes to sign in.
func (h *handler) home(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {"/"}}.Encode(), http.StatusSeeOther)
		return
	}
	places, err := h.ui.Places.Places(r.Context(), u)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := homeView{Install: h.installCommand(team{}), Project: "project", Team: "team"}
	// The diagram uses the user's own names when they are in a team of a
	// project.
	projects, _ := navTree(places, "")
	for _, p := range projects {
		if len(p.Children) > 0 {
			v.Project, v.Team = p.Name, p.Children[0].Name
			break
		}
	}
	if h.ui.Skills != nil {
		for _, p := range h.ui.Skills.Packs {
			s, _ := p.Skill()
			v.Skills = append(v.Skills, cardOf(s))
		}
	}
	h.renderUI(w, r, http.StatusOK, "home.html", u, places, "", uiView{Title: "Home", HomeCurrent: true, Page: v})
}

// homeView is the body of home.html.
type homeView struct {
	Install string
	// Project and Team name the layers in the diagram.
	Project, Team string
	// Skills are loadout's own skills.
	Skills []skillCard
}

// sortedPlaces orders places as the sidebar lists them: teams first, then
// projects, each by name.
func sortedPlaces(places []account.Place) []account.Place {
	out := slices.Clone(places)
	slices.SortStableFunc(out, func(a, b account.Place) int {
		ak, bk := a.Namespace.Kind == registry.KindProject, b.Namespace.Kind == registry.KindProject
		switch {
		case ak == bk:
			return strings.Compare(a.Namespace.Name, b.Namespace.Name)
		case ak:
			return 1
		}
		return -1
	})
	return out
}

// DefaultDocsURL is the user guide on the project's site. A registry
// without internet access points its pages elsewhere with LOADOUT_DOCS_URL.
// The guide's "loadout docs" link goes back to the page named in ?from, if
// that page is on a loadout site. Link into it through docsLink.
const DefaultDocsURL = "https://loadout.lat/docs/"

// Paths under the docs address, for docsLink. whatsNewPath lists what each
// update changed and, like the guide, links back to the page named in ?from.
const (
	whatsNewPath = "whats-new/"
	installPath  = "#1-install-the-cli"
)

// docsLink is target, a page of the docs relative to the docs address,
// with an optional #fragment, and ?from naming the registry page r asks
// for, so the docs' "loadout docs" link leads back to it. Every link from
// a registry page into the docs is made here; the fragment stays after the
// query, where it belongs.
func (h *handler) docsLink(r *http.Request, target string) string {
	page, frag, _ := strings.Cut(target, "#")
	link := h.docsURL + page + "?" + url.Values{"from": {h.origin + r.URL.RequestURI()}}.Encode()
	if frag != "" {
		link += "#" + frag
	}
	return link
}

// uiView is the data of every team page; Page holds what one page adds.
type uiView struct {
	Title    string
	CSRF     string
	Theme    string
	User     account.User
	Projects []navItem
	Teams    []navItem
	// AccountCurrent marks the account link in the sidebar.
	AccountCurrent bool
	// DocsURL opens the user guide, telling it which page to link back to.
	DocsURL string
	// WhatsNewURL opens the list of what each update changed, the same way.
	WhatsNewURL string
	// CreateLabel is the sidebar link to the new page, empty when the user
	// may create nothing; NewCurrent marks it.
	CreateLabel string
	NewCurrent  bool
	// SkillsNav shows the Start here entry above the projects;
	// SkillsCurrent marks it.
	SkillsNav     bool
	SkillsCurrent bool
	// Field is the id of the form field the Error is about; the page marks
	// it invalid, ties it to the message and focuses it.
	Field string
	// HomeCurrent marks the brand link, which opens the home page.
	HomeCurrent bool
	// SignIn is set for a visitor on a page anyone may read: the sidebar
	// then offers to sign in instead of naming the user.
	SignIn string
	// Bare leaves out the sidebar and the team header, for the wide
	// proposal pages; they carry their own way back.
	Bare bool
	// Wide lets a list page grow with the window, so its first column
	// (names and descriptions) gets the room; reading pages keep the
	// narrower measure.
	Wide bool
	// Header is the breadcrumb, title and tabs of a team page; nil on
	// other pages.
	Header *teamHeader
	// NS is the namespace of a team page.
	NS     string
	Notice string
	Error  string
	Page   any
}

// navItem is one namespace in the sidebar. Level is the layer by kind: L0
// for a project, L1 for a team, with or without a project above it. A
// project holds its teams in Children, shown when Open.
type navItem struct {
	Name     string
	Level    int
	Current  bool
	Children []navItem
	// Open shows the teams of a project: the project of the page, or the
	// only project the user sees.
	Open bool
}

// navTree arranges places for the sidebar: projects with the teams in them
// nested below, and the teams outside any project the user sees on their
// own. Sorted by name.
func navTree(places []account.Place, current string) (projects, teams []navItem) {
	sorted := sortedPlaces(places)
	index := map[string]int{}
	for _, p := range sorted {
		if p.Namespace.Kind == registry.KindProject {
			index[p.Namespace.Name] = len(projects)
			projects = append(projects, navItem{Name: p.Namespace.Name, Level: 0, Current: p.Namespace.Name == current})
		}
	}
	for _, p := range sorted {
		if p.Namespace.Kind == registry.KindProject {
			continue
		}
		item := navItem{Name: p.Namespace.Name, Level: 1, Current: p.Namespace.Name == current}
		i, ok := index[p.Namespace.Parent]
		if !ok {
			teams = append(teams, item)
			continue
		}
		projects[i].Children = append(projects[i].Children, item)
		if item.Current {
			projects[i].Open = true
		}
	}
	for i := range projects {
		projects[i].Open = projects[i].Open || projects[i].Current || len(projects) == 1
	}
	return projects, teams
}

// teamHeader is the top of a team page.
type teamHeader struct {
	Crumbs []crumb
	Name   string
	// Via is set when the user reads this namespace through a team below.
	Via     string
	Tab     string
	Reviews int
	// Tabs is false for a namespace read through a team below: it has only
	// packages to show, and its wiki.
	Tabs bool
	// Wiki shows the Wiki tab: the namespace's wiki is on.
	Wiki bool
	// Memory shows the Memory tab: the namespace's team memory is on.
	Memory bool
	// NewTeam shows a New team button on a project the user governs.
	NewTeam bool
}

// crumb is one step of the breadcrumb; Link is false for a namespace the
// user cannot open.
type crumb struct {
	Name string
	Link bool
}

// renderUI renders a team page with the sidebar.
func (h *handler) renderUI(w http.ResponseWriter, r *http.Request, status int, page string, u account.User, places []account.Place, current string, v uiView) {
	v.CSRF = h.csrfToken(w, r)
	v.Theme = h.theme(r)
	v.User = u
	v.CreateLabel = createLabel(u, places)
	v.SkillsNav = h.ui.Skills != nil
	v.DocsURL = h.docsLink(r, "")
	v.WhatsNewURL = h.docsLink(r, whatsNewPath)
	v.Projects, v.Teams = navTree(places, current)
	t, ok := h.ui.pages[page]
	if !ok {
		h.fail(w, r, fmt.Errorf("no page %s", page))
		return
	}
	var buf strings.Builder
	if err := t.ExecuteTemplate(&buf, "layout", v); err != nil {
		h.fail(w, r, fmt.Errorf("render %s: %w", page, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := fmt.Fprint(w, buf.String()); err != nil {
		h.log.Debug("write page", "err", err)
	}
}

// renderTeam renders a page of the team t with its header.
func (h *handler) renderTeam(w http.ResponseWriter, r *http.Request, status int, page, tab string, t team, v uiView) {
	hd := &teamHeader{Name: t.ns(), Via: t.place.Via, Tab: tab, Tabs: t.member(), Wiki: h.wikiOn(t)}
	on, err := h.memoryOn(r.Context(), t)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	hd.Memory = on
	hd.NewTeam = t.member() && t.place.Namespace.Kind == registry.KindProject &&
		t.place.Access.CanCreateChild(registry.KindProject, registry.KindTeam)
	open := map[string]bool{}
	for _, p := range t.places {
		open[p.Namespace.Name] = true
	}
	for _, l := range slices.Backward(t.pr.Layers) {
		hd.Crumbs = append(hd.Crumbs, crumb{Name: l.Namespace, Link: open[l.Namespace]})
		if l.Namespace == t.ns() {
			break
		}
	}
	if hd.Tabs && !v.Bare {
		props, err := h.ui.Proposals.Proposals(r.Context(), t.pr, pack.ID{}, registry.ProposalOpen)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		hd.Reviews = len(props)
		if h.ui.Requests != nil {
			n, err := h.openRequests(r, t)
			if err != nil {
				h.fail(w, r, err)
				return
			}
			hd.Reviews += n
		}
	}
	if !v.Bare {
		v.Header = hd
	}
	v.NS = t.ns()
	if v.Title == "" {
		v.Title = t.ns()
	}
	h.renderUI(w, r, status, page, t.user, t.places, t.ns(), v)
}

// notFound answers 404 for a namespace, proposal or package the user
// cannot see, the same way whether or not it exists.
func (h *handler) notFound(w http.ResponseWriter, r *http.Request, u account.User) {
	places, err := h.ui.Places.Places(r.Context(), u)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderUI(w, r, http.StatusNotFound, "error.html", u, places, "", uiView{
		Title: "Not found",
		Page:  errorPage{Heading: "Nothing here", Text: "This page does not exist, or you do not have access to it. Ask a maintainer of the team to invite you."},
	})
}

// errorPage is the body of error.html.
type errorPage struct {
	Heading string
	Text    string
	Back    string
}

// refused shows why the services refused an action: 403 for a rule, 400
// for bad input, 409 for a conflict, with a link to the page at backPath.
// Anything else is a 500.
func (h *handler) refused(w http.ResponseWriter, r *http.Request, t team, err error, backPath string) {
	var (
		status  int
		heading string
	)
	switch {
	case errors.Is(err, registry.ErrNotFound):
		h.notFound(w, r, t.user)
		return
	case errors.Is(err, registry.ErrForbidden):
		status, heading = http.StatusForbidden, "Not allowed"
	case errors.Is(err, registry.ErrVersionConflict), errors.Is(err, registry.ErrProposalClosed), errors.Is(err, registry.ErrBehind), errors.Is(err, registry.ErrExists):
		status, heading = http.StatusConflict, "Something changed"
	case errors.Is(err, registry.ErrInvalid):
		status, heading = http.StatusBadRequest, "That did not work"
	default:
		h.fail(w, r, err)
		return
	}
	h.explain(w, r, t, status, heading, sentence(err), backPath)
}

// explain answers with an error page: a heading, a sentence and a link to
// the page at backPath.
func (h *handler) explain(w http.ResponseWriter, r *http.Request, t team, status int, heading, text, backPath string) {
	h.renderTeam(w, r, status, "error.html", "", t, uiView{
		Title: heading,
		Page:  errorPage{Heading: heading, Text: text, Back: backPath},
	})
}

// sentence turns a service error into a sentence for a person: the text
// after the sentinel's prefix, capitalized, with a full stop.
func sentence(err error) string {
	msg := err.Error()
	for _, prefix := range []string{registry.ErrForbidden.Error() + ": ", registry.ErrInvalid.Error() + ": ", registry.ErrBehind.Error() + ": ", registry.ErrExists.Error() + ": "} {
		if i := strings.LastIndex(msg, prefix); i >= 0 {
			msg = msg[i+len(prefix):]
		}
	}
	if msg == "" {
		return "The request was refused."
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// back redirects to a page of the team after a POST, with a notice code
// the page turns into a sentence.
func back(w http.ResponseWriter, r *http.Request, path, notice string, extra url.Values) {
	q := url.Values{}
	maps.Copy(q, extra)
	if notice != "" {
		q.Set("done", notice)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	// Every caller builds path from a namespace and package ids that passed
	// ident and pack.NewID, so it is a path of this site, never a URL.
	http.Redirect(w, r, path, http.StatusSeeOther) //nolint:gosec // G710: path is built from validated ids
}
