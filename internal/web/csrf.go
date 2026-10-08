package web

import (
	"net/http"
	"time"
)

// csrfTTL is how long a browser keeps its CSRF cookie.
const csrfTTL = 30 * 24 * time.Hour

// csrfToken returns the browser's CSRF token, setting a new cookie if it
// has none. Forms send it back in a hidden field.
func (h *handler) csrfToken(w http.ResponseWriter, r *http.Request) string {
	if tok := h.cookies.get(r, cookieCSRF); len(tok) == 43 {
		return tok
	}
	tok, err := randomToken()
	if err != nil {
		// Without a token every form post fails the check; nothing unsafe.
		h.log.ErrorContext(r.Context(), "csrf token", "err", err)
		return ""
	}
	h.cookies.set(w, cookieCSRF, tok, csrfTTL)
	return tok
}

// checkCSRF accepts a form post only if its token matches the cookie
// (double submit: another site can make the browser send the cookie, but
// cannot read it to fill the field), and the browser does not report it as
// coming from another site.
func (h *handler) checkCSRF(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != h.origin {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	cookie := h.cookies.get(r, cookieCSRF)
	return cookie != "" && equal(cookie, r.PostForm.Get("csrf"))
}
