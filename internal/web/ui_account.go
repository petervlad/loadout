package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/petervlad/loadout/internal/account"
)

// cookieTheme remembers the appearance picked on the account page.
const cookieTheme = "loadout_theme"

// themeTTL is how long the appearance choice is kept.
const themeTTL = 365 * 24 * time.Hour

// theme returns the appearance the browser picked: "light", "dark", or ""
// to follow the system.
func (h *handler) theme(r *http.Request) string {
	switch t := h.cookies.get(r, cookieTheme); t {
	case "light", "dark":
		return t
	}
	return ""
}

// accountView is the body of account.html.
type accountView struct {
	SignIn      string
	Memberships []accountMembership
	Login       string
	// Server links the admins' server page.
	Server bool
	// FirstAdmin shows the reminder to change the generated password; the
	// registry does not know whether it was changed.
	FirstAdmin bool
	// MemoryTokens is the section for memory tokens; nil when this
	// registry has no team memory.
	MemoryTokens *memoryTokensView
}

// accountMembership is one of the user's roles, with whether its page can
// be opened.
type accountMembership struct {
	Namespace string
	Role      account.Role
	Since     time.Time
}

func (h *handler) uiAccount(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {"/account"}}.Encode(), http.StatusSeeOther)
		return
	}
	notice := ""
	switch r.URL.Query().Get("done") {
	case "theme":
		notice = "Appearance saved."
	case "memory-revoked":
		notice = "The memory token was revoked. A tool using it can no longer reach team memory."
	}
	h.renderAccount(w, r, u, http.StatusOK, notice, "", nil)
}

// renderAccount renders the account page. created is a memory token just
// made, whose secret the page shows this once.
func (h *handler) renderAccount(w http.ResponseWriter, r *http.Request, u account.User, status int, notice, errText string, created *newMemoryToken) {
	ms, err := h.accounts.Memberships(r.Context(), u.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	places, err := h.ui.Places.Places(r.Context(), u)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := accountView{
		SignIn: signIn(account.MemberDetail{Google: u.GoogleSubject != "", Password: u.PasswordHash != ""}), Login: "loadout login --remote " + h.origin,
		Server:     u.IsAdmin && h.ui.Server != nil,
		FirstAdmin: u.Email == account.FirstAdminEmail && h.simple,
	}
	for _, m := range ms {
		v.Memberships = append(v.Memberships, accountMembership{Namespace: m.Namespace, Role: m.Role, Since: m.CreatedAt})
	}
	if v.MemoryTokens, err = h.memoryTokensView(r, u.ID, created); err != nil {
		h.fail(w, r, err)
		return
	}
	field := ""
	if errText != "" {
		field = "mem-label"
	}
	h.renderUI(w, r, status, "account.html", u, places, "", uiView{Title: "Account", AccountCurrent: true, Notice: notice, Error: errText, Field: field, Page: v})
}

// setTheme stores the appearance in a cookie; the pages read it to set
// data-theme, so switching needs no script.
func (h *handler) setTheme(w http.ResponseWriter, r *http.Request) {
	if !h.form(w, r) {
		return
	}
	if _, ok := h.user(r); !ok {
		http.Redirect(w, r, "/auth/login?"+url.Values{"next": {"/account"}}.Encode(), http.StatusSeeOther)
		return
	}
	switch t := r.PostForm.Get("theme"); t {
	case "light", "dark":
		h.cookies.set(w, cookieTheme, t, themeTTL)
	default:
		h.cookies.clear(w, cookieTheme)
	}
	http.Redirect(w, r, "/account?done=theme", http.StatusSeeOther)
}
