package web_test

import (
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/web"
)

const demoBanner = "Demo registry, fake data, resets with docker compose down -v"

func TestDemoSignInPage(t *testing.T) {
	demo := &web.Demo{Password: "demo-pass-1234", Accounts: []web.DemoAccount{{Email: "alice@example.com", Role: "lime maintainer"}, {Email: "ada@example.com", Role: "admin"}}}
	s := newSite(t, siteOptions{simple: true, demo: demo})
	p := s.browser().get("/auth/login")
	for _, want := range []string{demoBanner, "demo-pass-1234", "alice@example.com", "lime maintainer", "ada@example.com"} {
		if !strings.Contains(p.body, want) {
			t.Errorf("demo sign-in page is missing %q", want)
		}
	}
	for _, bad := range []string{"Google", "/auth/signup"} {
		if strings.Contains(p.body, bad) {
			t.Errorf("demo sign-in page mentions %q", bad)
		}
	}
}

func TestSignInPageWithoutDemo(t *testing.T) {
	s := newSite(t, siteOptions{simple: true})
	p := s.browser().get("/auth/login")
	for _, bad := range []string{"Demo registry", "Demo accounts"} {
		if strings.Contains(p.body, bad) {
			t.Errorf("sign-in page of a normal registry says %q", bad)
		}
	}
}
