package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An unexpected error shows the site's own page, with a way back, and
// never the error itself.
func TestFailShowsAPage(t *testing.T) {
	pages, err := parsePages()
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{log: slog.New(slog.DiscardHandler), pages: pages, cookies: newCookies(false)}
	rec := httptest.NewRecorder()
	h.fail(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/t/lime", nil), errors.New("disk on fire"))
	body := rec.Body.String()
	if rec.Code != http.StatusInternalServerError || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("fail = %d %s, want a 500 page", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{"Something went wrong on our side", `href="/"`, "style.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	if strings.Contains(body, "disk on fire") {
		t.Error("page shows the error")
	}
}
