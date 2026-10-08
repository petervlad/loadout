package web

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
)

// inviteForm is the Invite someone panel of a project's settings page.
type inviteForm struct {
	// Roles are the project roles the viewer may give.
	Roles []account.Role
	// Teams are the project's teams where the viewer may give a role.
	Teams []inviteTeam
	// Governs is true when the viewer sees every team of the project, so
	// an empty Teams means the project has none.
	Governs bool
	// MailOn says whether the registry mails the invitation.
	MailOn bool
	// Email and Role refill the form after an error.
	Email string
	Role  account.Role
	// Open unfolds the panel: after an error, or when asked for with
	// ?invite=1 ("Invite someone else").
	Open bool
}

// inviteTeam is one team row of the form: a checkbox and a role picker.
type inviteTeam struct {
	Name    string
	Roles   []account.Role
	Checked bool
	Role    account.Role
}

// inviteResult is what the page shows after an invitation.
type inviteResult struct {
	Email   string
	Changed bool
	Mailed  bool
	MailOn  bool
	Link    string
	Grants  []account.Grant
}

// inviteForm builds the panel for t, a project, keeping what was typed in
// kept (after an error), if any.
func (h *handler) inviteForm(t team, roles []account.Role, kept *inviteForm) *inviteForm {
	f := &inviteForm{Roles: roles, Governs: t.place.Access.Governs, MailOn: h.ui.Teams.EmailEnabled(), Role: account.RoleMember}
	checked := map[string]account.Role{}
	if kept != nil {
		f.Open = true
		f.Email, f.Role = kept.Email, kept.Role
		for _, tm := range kept.Teams {
			checked[tm.Name] = tm.Role
		}
	}
	for _, p := range t.places {
		if p.Via != "" || p.Namespace.Kind != registry.KindTeam || p.Namespace.Parent != t.ns() {
			continue
		}
		row := inviteTeam{Name: p.Namespace.Name, Role: account.RoleMember}
		for _, r := range []account.Role{account.RoleMember, account.RoleMaintainer} {
			if p.Access.CanAssign(r) {
				row.Roles = append(row.Roles, r)
			}
		}
		if len(row.Roles) == 0 {
			continue
		}
		if r, ok := checked[row.Name]; ok {
			row.Checked, row.Role = true, r
		}
		f.Teams = append(f.Teams, row)
	}
	slices.SortFunc(f.Teams, func(a, b inviteTeam) int { return strings.Compare(a.Name, b.Name) })
	return f
}

// invite adds a person to the project and the teams ticked, then shows
// what happened and the sign-in link in place of the form. The page is
// rendered, not redirected to: the result is not worth a URL, and sending
// the form again changes nothing and mails nobody.
func (h *handler) invite(w http.ResponseWriter, r *http.Request, t team) {
	if !t.member() {
		h.notFound(w, r, t.user)
		return
	}
	email, role := r.PostForm.Get("email"), account.Role(r.PostForm.Get("role"))
	teams := map[string]account.Role{}
	kept := &inviteForm{Email: email, Role: role}
	for _, name := range r.PostForm["team"] {
		tr := account.Role(r.PostForm.Get("team_role." + name))
		teams[name] = tr
		kept.Teams = append(kept.Teams, inviteTeam{Name: name, Role: tr})
	}
	inv, err := h.ui.Teams.InviteToProject(r.Context(), t.actor(), t.ns(), email, role, teams)
	if errors.Is(err, registry.ErrInvalid) {
		h.renderSettings(w, r, t, http.StatusBadRequest, settingsView{Invite: kept}, sentence(err))
		return
	}
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns()+"/settings")
		return
	}
	h.renderSettings(w, r, t, http.StatusOK, settingsView{Invited: &inviteResult{
		Email:   inv.Email,
		Changed: inv.Changed(),
		Mailed:  inv.Mailed,
		MailOn:  h.ui.Teams.EmailEnabled(),
		Link:    inv.Link,
		Grants:  inv.Grants,
	}}, "")
}
