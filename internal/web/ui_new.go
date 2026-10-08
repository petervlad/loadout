package web

import (
	"errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
)

// newView is the body of new.html.
type newView struct {
	// CanProject is true for admins, who may also create projects and
	// teams outside any project.
	CanProject bool
	// Parents are the projects the user may create teams in.
	Parents []string
	// Kind, Name and Parent refill the form after an error, or preselect
	// the project from ?parent=.
	Kind   registry.NamespaceKind
	Name   string
	Parent string
}

// creatable reports what u may create: projects (admins only) and teams in
// the projects listed.
func creatable(u account.User, places []account.Place) (canProject bool, parents []string) {
	for _, p := range places {
		if p.Via == "" && p.Namespace.Kind == registry.KindProject && p.Access.CanCreateChild(registry.KindProject, registry.KindTeam) {
			parents = append(parents, p.Namespace.Name)
		}
	}
	return u.IsAdmin, parents
}

// createLabel is the sidebar link to the new page, empty when the user may
// create nothing.
func createLabel(u account.User, places []account.Place) string {
	canProject, parents := creatable(u, places)
	switch {
	case canProject:
		return "New project or team"
	case len(parents) > 0:
		return "New team"
	}
	return ""
}

func (h *handler) newPage(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(r)
	if !ok {
		h.toLogin(w, r)
		return
	}
	v := newView{Kind: registry.KindTeam, Parent: r.URL.Query().Get("parent")}
	if v.Parent == "" && u.IsAdmin && r.URL.Query().Get("kind") != "team" {
		v.Kind = registry.KindProject
	}
	h.renderNew(w, r, u, http.StatusOK, v, "")
}

func (h *handler) renderNew(w http.ResponseWriter, r *http.Request, u account.User, status int, v newView, errText string) {
	field := ""
	if errText != "" {
		field = "new-name"
	}
	places, err := h.ui.Places.Places(r.Context(), u)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.CanProject, v.Parents = creatable(u, places)
	if !v.CanProject && len(v.Parents) == 0 {
		h.notFound(w, r, u)
		return
	}
	if !v.CanProject {
		v.Kind = registry.KindTeam
	}
	if v.Parent != "" && !slices.Contains(v.Parents, v.Parent) {
		v.Parent = ""
	}
	title := "New team"
	if v.CanProject {
		title = "New project or team"
	}
	h.renderUI(w, r, status, "new.html", u, places, "", uiView{Title: title, NewCurrent: true, Error: errText, Field: field, Page: v})
}

func (h *handler) createNamespace(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	u, ok := h.user(r)
	if !ok {
		h.toLogin(w, r)
		return
	}
	v := newView{
		Kind:   registry.NamespaceKind(r.PostForm.Get("kind")),
		Name:   r.PostForm.Get("name"),
		Parent: r.PostForm.Get("parent"),
	}
	parent := v.Parent
	if v.Kind == registry.KindProject {
		parent = ""
	}
	_, err := h.ui.Teams.CreateNamespace(r.Context(), account.UserActor(u.ID), v.Name, parent, v.Kind)
	switch {
	case errors.Is(err, registry.ErrExists):
		h.renderNew(w, r, u, http.StatusConflict, v, "The name "+v.Name+" is taken. Pick another one.")
		return
	case errors.Is(err, registry.ErrInvalid), errors.Is(err, registry.ErrForbidden):
		h.renderNew(w, r, u, http.StatusBadRequest, v, sentence(err))
		return
	case err != nil:
		h.fail(w, r, err)
		return
	}
	back(w, r, "/t/"+v.Name+"/settings", "created", url.Values{"kind": {string(v.Kind)}})
}
