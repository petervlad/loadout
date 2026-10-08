package web

import (
	"net/http"
	"time"
)

// Cookie names, without the prefix.
const (
	cookieSession   = "loadout_session"
	cookieCSRF      = "loadout_csrf"
	cookieChallenge = "loadout_challenge"
	cookieOAuth     = "loadout_oauth"
)

// cookies writes the pages' cookies: HttpOnly, SameSite=Lax, path /. Over
// https they are Secure with the __Host- prefix, which a browser only
// accepts from this exact host over TLS, so no subdomain can plant or
// overwrite them.
type cookies struct {
	secure bool
	prefix string
}

func newCookies(secure bool) cookies {
	if secure {
		return cookies{secure: true, prefix: "__Host-"}
	}
	return cookies{}
}

// Secure is off only for an http://localhost public URL (see ParsePublicURL).
func (c cookies) set(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: see above
		Name:     c.prefix + name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		Secure:   c.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (c cookies) clear(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: as in set
		Name:     c.prefix + name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   c.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// get returns a cookie's value, or "".
func (c cookies) get(r *http.Request, name string) string {
	ck, err := r.Cookie(c.prefix + name)
	if err != nil {
		return ""
	}
	return ck.Value
}
