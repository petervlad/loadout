package memimport_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petervlad/loadout/internal/memimport"
)

// TestMCPSaverRefusesPlainHTTP: the memory token is not sent over http to a
// registry on another machine.
func TestMCPSaverRefusesPlainHTTP(t *testing.T) {
	if _, err := memimport.NewMCPSaver(t.Context(), "http://registry.example.com", "lime", "ldm_x", ""); err == nil {
		t.Error("NewMCPSaver(http, remote host) = nil error, want a refusal")
	}
}

// TestMCPSaverRefusesRedirects: a redirect does not carry the token anywhere.
func TestMCPSaverRefusesRedirects(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer other.Close()
	from := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer from.Close()
	if _, err := memimport.NewMCPSaver(t.Context(), from.URL, "lime", "ldm_x", ""); err == nil {
		t.Error("NewMCPSaver followed a redirect")
	}
	if hit {
		t.Error("the redirect target was contacted")
	}
}
