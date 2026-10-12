package web

import (
	"cmp"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// settingsView is the body of settings.html.
type settingsView struct {
	Namespace string
	// CanList is false for members: listing needs the maintainer role.
	CanList bool
	// YourRole is the viewer's role with its article, for a sentence.
	YourRole string
	// Roles are the roles the viewer may give; empty hides the controls.
	Roles   []account.Role
	Members []memberRow
	// Inherited are the owners of the projects above, whose role here is
	// computed, not stored; Projects are those projects, closest first.
	Inherited []memberRow
	Projects  []projectLink
	// Query is the member search; rows it does not match are Hidden, so
	// the script can show them again as the search changes.
	Query string
	// Tokens is nil when the viewer may not manage machine tokens.
	Tokens    []registry.Token
	CanTokens bool
	// NewSecret is a token just created, shown this once.
	NewSecret string
	NewLabel  string
	// Review is the require-review setting; CanReview lets the viewer change it.
	Review    bool
	CanReview bool
	// Wiki is the wiki switch; CanWiki lets the viewer turn it and
	// choose its course.
	Wiki    bool
	CanWiki bool
	// Course is the course form, for those who may choose it.
	Course *courseForm
	// CanTiers lets the viewer change the model settings.
	CanTiers bool
	// Email refills the add form after an error.
	Email string
	// Invite replaces the add form on a project, for those who may add
	// members there; Invited is the result of the invitation just made.
	Invite  *inviteForm
	Invited *inviteResult
}

// memberRow is one member in the list.
type memberRow struct {
	Email  string
	Name   string
	You    bool
	Role   account.Role
	SignIn string
	// Pending is true until the person signs in for the first time.
	Pending bool
	Joined  time.Time
	// Roles are the roles the viewer may set; empty means read-only.
	Roles     []account.Role
	CanRemove bool
	// From is the project above that gives an inherited role.
	From string
	// Teams are the other teams of the project the person is in.
	Teams []string
	// Find is the lowercase text the member search looks in; Hidden is
	// true when the search does not match it.
	Find   string
	Hidden bool
}

// finish fills in the search text of m and hides it unless it matches q.
func (m *memberRow) finish(q string) {
	m.Find = strings.ToLower(strings.Join(append([]string{m.Name, m.Email}, m.Teams...), " "))
	m.Hidden = q != "" && !strings.Contains(m.Find, strings.ToLower(q))
}

// sortMembers puts the highest roles first, then sorts by name.
func sortMembers(ms []memberRow) {
	rank := func(r account.Role) int {
		switch r {
		case account.RoleOwner:
			return 0
		case account.RoleMaintainer:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(ms, func(a, b memberRow) int {
		if c := rank(a.Role) - rank(b.Role); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(cmp.Or(a.Name, a.Email)), strings.ToLower(cmp.Or(b.Name, b.Email)))
	})
}

// projectLink is a project above whose owners are listed on a team's page.
type projectLink struct {
	Name string
	// CanOpen is true when the viewer may open the project's settings page;
	// Manage when they may also change members there.
	CanOpen bool
	Manage  bool
}

func (h *handler) settingsPage(w http.ResponseWriter, r *http.Request, t team) {
	h.renderSettings(w, r, t, http.StatusOK, settingsView{}, "")
}

// toSettings sends the old address of the page, from bookmarks and links
// made before it was called Settings, to the new one with its query. It
// checks nothing: the settings page does, and a 404 there reads the same.
func (h *handler) toSettings(w http.ResponseWriter, r *http.Request) {
	u := url.URL{Path: "/t/" + url.PathEscape(r.PathValue("ns")) + "/settings", RawQuery: r.URL.RawQuery}
	http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
}

// renderSettings renders the settings page over the fields v already has:
// a new token's secret, or the email of a failed add with its error.
func (h *handler) renderSettings(w http.ResponseWriter, r *http.Request, t team, status int, v settingsView, errText string) {
	field := ""
	switch {
	case errText == "":
	case v.Course != nil:
		field = "course-sel"
	case v.Invite != nil:
		field = "invite-email"
	case v.Email != "":
		field = "add-email"
	}
	if !t.member() {
		h.notFound(w, r, t.user)
		return
	}
	a := t.place.Access
	v.Namespace = t.ns()
	v.YourRole = withArticle(a.Role)
	v.Review = t.place.Namespace.RequireReview
	v.CanReview = a.CanSetReview()
	v.Wiki = t.place.Namespace.Wiki
	v.CanWiki = a.CanSetWiki() && h.ui.Wiki != nil
	v.CanTiers = a.CanSetTiers()
	v.CanList = a.CanListMembers()
	v.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	if v.CanWiki {
		form, err := h.courseForm(r, t, v.Course)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Course = form
	}
	for _, role := range []account.Role{account.RoleMember, account.RoleMaintainer, account.RoleOwner} {
		if role == account.RoleOwner && t.place.Namespace.Kind != registry.KindProject {
			continue
		}
		if a.CanAssign(role) {
			v.Roles = append(v.Roles, role)
		}
	}
	if t.place.Namespace.Kind == registry.KindProject && len(v.Roles) > 0 && v.Invited == nil {
		v.Invite = h.inviteForm(t, v.Roles, v.Invite)
		v.Invite.Open = v.Invite.Open || r.URL.Query().Get("invite") == "1"
	}
	if v.CanList {
		ms, err := h.ui.Teams.MemberDetails(r.Context(), t.actor(), t.ns())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		for _, m := range ms {
			row := memberRow{
				Email:   m.Membership.Email,
				Name:    m.Name,
				You:     m.Membership.UserID == t.user.ID,
				Role:    m.Membership.Role,
				SignIn:  signIn(m),
				Pending: !m.Verified,
				Joined:  m.Membership.CreatedAt,
				Teams:   m.Teams,
			}
			row.finish(v.Query)
			if a.CanAssign(m.Membership.Role) {
				row.Roles = v.Roles
				row.CanRemove = !row.You
			}
			v.Members = append(v.Members, row)
		}
		sortMembers(v.Members)
		if err := h.inheritedMembers(r, t, &v); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if a.CanManageTokens() {
		toks, err := h.ui.Teams.MachineTokens(r.Context(), t.actor(), t.ns())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.CanTokens, v.Tokens = true, toks
	}
	h.renderTeam(w, r, status, "settings.html", "settings", t, uiView{
		Title:  "Settings · " + t.ns(),
		Wide:   true,
		Notice: settingsNotice(t.ns(), r.URL.Query()),
		Error:  errText,
		Field:  field,
		Page:   v,
	})
}

// inheritedMembers fills in the owners of the projects above t, who have
// the owner role in it without a row of their own, and links each project
// the viewer can open.
func (h *handler) inheritedMembers(r *http.Request, t team, v *settingsView) error {
	ms, err := h.ui.Teams.InheritedMembers(r.Context(), t.actor(), t.ns())
	if err != nil {
		return err
	}
	for _, m := range ms {
		row := memberRow{
			Email:   m.Membership.Email,
			Name:    m.Name,
			You:     m.Membership.UserID == t.user.ID,
			Role:    m.Membership.Role,
			SignIn:  signIn(m),
			Pending: !m.Verified,
			From:    m.Membership.Namespace,
			Teams:   m.Teams,
		}
		row.finish(v.Query)
		v.Inherited = append(v.Inherited, row)
		if slices.ContainsFunc(v.Projects, func(p projectLink) bool { return p.Name == m.Membership.Namespace }) {
			continue
		}
		p := projectLink{Name: m.Membership.Namespace}
		// A project read only through a team below has no settings page
		// for the viewer (it answers 404), so it gets no link.
		if i := slices.IndexFunc(t.places, func(pl account.Place) bool { return pl.Namespace.Name == p.Name }); i >= 0 && t.places[i].Via == "" {
			p.CanOpen = true
			p.Manage = t.places[i].Access.CanAssign(account.RoleMember)
		}
		v.Projects = append(v.Projects, p)
	}
	return nil
}

// withArticle is a role in a sentence: "a member", "an owner".
func withArticle(r account.Role) string {
	name := strings.ToLower(roleName(r))
	if strings.ContainsAny(name[:1], "aeiou") {
		return "an " + name
	}
	return "a " + name
}

// signIn says how a member signs in.
func signIn(m account.MemberDetail) string {
	switch {
	case m.Google && m.Password:
		return "Google, email"
	case m.Google:
		return "Google"
	case m.Password:
		return "Email"
	}
	return "Not yet"
}

// settingsNotice is the line shown after a change on the settings page
// of ns.
func settingsNotice(ns string, q url.Values) string {
	email := q.Get("email")
	switch q.Get("done") {
	case "created":
		if q.Get("kind") == string(registry.KindProject) {
			return "Project created, and you are its owner. Create its teams with New team, and add other owners here."
		}
		return "Team created. Add its members here, or publish to it with loadout login."
	case "added":
		return email + " was added. They get access when they sign in with that address."
	case "role":
		return "The role of " + email + " was changed."
	case "removed":
		return email + " was removed, with the tokens loadout login gave them."
	case "revoked":
		return "The token was revoked. Machines using it can no longer read or publish."
	case "review-on":
		return "Review is now required: new versions go through a proposal."
	case "review-off":
		return "Review is no longer required: maintainers may publish directly."
	case "wiki-on":
		return "The wiki is on. Open the Wiki tab to write the first version."
	case "wiki-off":
		return "The wiki is off. Its history is kept if you turn it on again."
	case "course-set":
		if id, err := pack.ParseID(q.Get("course")); err == nil {
			return "The wiki shows " + id.String() + " as its course."
		}
		return "The course was saved."
	case "course-none":
		return ns + " has no course of its own."
	}
	return ""
}

func (h *handler) addMember(w http.ResponseWriter, r *http.Request, t team) {
	email := r.PostForm.Get("email")
	_, _, err := h.ui.Teams.AddMember(r.Context(), t.actor(), t.ns(), email, account.Role(r.PostForm.Get("role")))
	if errors.Is(err, registry.ErrInvalid) {
		h.renderSettings(w, r, t, http.StatusBadRequest, settingsView{Email: email}, sentence(err))
		return
	}
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	back(w, r, "/t/"+t.ns()+"/settings", "added", url.Values{"email": {email}})
}

// keepSearch is the query a member change comes back with: the person
// for the notice and the member search the form carried.
func keepSearch(r *http.Request, email string) url.Values {
	v := url.Values{"email": {email}}
	if q := strings.TrimSpace(r.PostForm.Get("q")); q != "" {
		v.Set("q", q)
	}
	return v
}

func (h *handler) setMemberRole(w http.ResponseWriter, r *http.Request, t team) {
	email := r.PostForm.Get("email")
	if _, err := h.ui.Teams.SetMember(r.Context(), t.actor(), t.ns(), email, account.Role(r.PostForm.Get("role"))); err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	back(w, r, "/t/"+t.ns()+"/settings", "role", keepSearch(r, email))
}

func (h *handler) removeMember(w http.ResponseWriter, r *http.Request, t team) {
	email := r.PostForm.Get("email")
	if _, err := h.ui.Teams.RemoveMember(r.Context(), t.actor(), t.ns(), email); err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	back(w, r, "/t/"+t.ns()+"/settings", "removed", keepSearch(r, email))
}

// createToken shows the settings page with the new secret instead of
// redirecting, so the secret is never in a URL or the history.
func (h *handler) createToken(w http.ResponseWriter, r *http.Request, t team) {
	label := r.PostForm.Get("label")
	secret, tok, err := h.ui.Teams.CreateMachineToken(r.Context(), t.actor(), t.ns(), registry.TokenScope(r.PostForm.Get("scope")), label)
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	h.renderSettings(w, r, t, http.StatusOK, settingsView{NewSecret: secret, NewLabel: tok.Label}, "")
}

func (h *handler) revokeToken(w http.ResponseWriter, r *http.Request, t team) {
	if err := h.ui.Teams.RevokeMachineToken(r.Context(), t.actor(), t.ns(), r.PostForm.Get("id")); err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	back(w, r, "/t/"+t.ns()+"/settings", "revoked", nil)
}

func (h *handler) saveReview(w http.ResponseWriter, r *http.Request, t team) {
	on := r.PostForm.Get("require_review") == "on"
	if _, err := h.ui.Teams.SetRequireReview(r.Context(), t.actor(), t.ns(), on); err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	done := "review-off"
	if on {
		done = "review-on"
	}
	back(w, r, "/t/"+t.ns()+"/settings", done, nil)
}

func (h *handler) saveWikiSwitch(w http.ResponseWriter, r *http.Request, t team) {
	on := r.PostForm.Get("wiki") == "on"
	if _, err := h.ui.Teams.SetWiki(r.Context(), t.actor(), t.ns(), on); err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	done := "wiki-off"
	if on {
		done = "wiki-on"
	}
	back(w, r, "/t/"+t.ns()+"/settings", done, nil)
}
