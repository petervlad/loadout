package remote

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigAddRemoveResolve(t *testing.T) {
	var c Config
	if _, err := c.Resolve(""); !errors.Is(err, ErrNoRemote) {
		t.Errorf("Resolve on empty config error = %v, want ErrNoRemote", err)
	}
	if err := c.Add(Remote{Name: "office", URL: "http://10.0.4.12:7420"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(Remote{Name: "cloud", URL: "https://reg.example.com"}); err != nil {
		t.Fatal(err)
	}
	if c.Default != "office" {
		t.Errorf("Default = %q, want the first remote", c.Default)
	}
	if err := c.Add(Remote{Name: "office", URL: "http://x"}); err == nil {
		t.Error("adding a duplicate name succeeded")
	}
	if r, err := c.Resolve("cloud"); err != nil || r.URL != "https://reg.example.com" {
		t.Errorf("Resolve(cloud) = %v, %v", r, err)
	}
	if _, err := c.Remove("office"); err != nil {
		t.Fatal(err)
	}
	if c.Default != "cloud" {
		t.Errorf("Default after removing it = %q, want cloud", c.Default)
	}
	if _, err := c.Remove("office"); err == nil {
		t.Error("removing a missing remote succeeded")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		r     Remote
		valid bool
	}{
		{Remote{Name: "office", URL: "http://10.0.4.12:7420"}, true},
		{Remote{Name: "cloud", URL: "https://reg.example.com/"}, true},
		{Remote{Name: "Office", URL: "http://x"}, false},
		{Remote{Name: "office", URL: "ftp://x"}, false},
		{Remote{Name: "office", URL: "10.0.4.12:7420"}, false},
	}
	for _, tc := range tests {
		if got := tc.r.Validate() == nil; got != tc.valid {
			t.Errorf("Validate(%+v) valid = %v, want %v", tc.r, got, tc.valid)
		}
	}
}

func TestInsecure(t *testing.T) {
	tests := map[string]bool{
		"http://10.0.4.12:7420":   true,
		"http://localhost:7420":   false,
		"http://127.0.0.1:7420":   false,
		"https://reg.example.com": false,
	}
	for u, want := range tests {
		if got := (Remote{Name: "r", URL: u}).Insecure(); got != want {
			t.Errorf("Insecure(%s) = %v, want %v", u, got, want)
		}
	}
}

func TestStoreRoundTripAndTokenPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	env := map[string]string{}
	s := NewStore(dir, func(k string) string { return env[k] })

	c, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Add(Remote{Name: "office", URL: "http://10.0.4.12:7420"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	if err := s.SetToken("office", "ldt_secret"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if r, err := got.Resolve(""); err != nil || r.Name != "office" {
		t.Errorf("reloaded default = %v, %v", r, err)
	}
	if tok, err := s.Token("office"); err != nil || tok != "ldt_secret" {
		t.Errorf("Token() = %q, %v", tok, err)
	}
	info, err := os.Stat(filepath.Join(dir, "credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials.yaml permissions = %o, want 600", perm)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "ldt_secret") {
		t.Error("config.yaml contains the token")
	}

	env[EnvToken] = "ldt_from_env"
	if tok, _ := s.Token("office"); tok != "ldt_from_env" {
		t.Errorf("Token() with %s = %q, want the env value", EnvToken, tok)
	}

	if err := s.SetToken("office", ""); err != nil {
		t.Fatal(err)
	}
	delete(env, EnvToken)
	if tok, _ := s.Token("office"); tok != "" {
		t.Errorf("Token() after delete = %q, want empty", tok)
	}
}
