package client_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/client"
)

// TestHTTPClientRefusesRedirects: a memory token must never follow a redirect
// to another place, so the client stops at the first one.
func TestHTTPClientRefusesRedirects(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer other.Close()
	from := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer from.Close()

	hc, err := client.HTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, from.URL, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer ldm_secret")
	resp, err := hc.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the client followed a redirect")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %v, want it to say a redirect was refused", err)
	}
	if hit {
		t.Error("the redirect target was contacted with the token")
	}
}

func TestCheckMemoryURL(t *testing.T) {
	tests := []struct {
		url  string
		fail bool
	}{
		{"https://registry.example.com", false},
		{"https://10.0.4.12", false},
		{"http://127.0.0.1:7420", false},
		{"http://localhost:7420", false},
		{"http://[::1]:7420", false},
		{"http://registry.example.com", true},
		{"http://10.0.4.12", true},
		{"http://127.0.0.1.example.com", true},
		{"ftp://registry.example.com", true},
		{"registry.example.com", true},
		{"https://", true},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := client.CheckMemoryURL(tt.url)
			if (err != nil) != tt.fail {
				t.Errorf("CheckMemoryURL(%q) = %v, want failure %v", tt.url, err, tt.fail)
			}
		})
	}
}
