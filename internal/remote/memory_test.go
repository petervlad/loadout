package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryTokenExpiry(t *testing.T) {
	s := NewStore(t.TempDir(), func(string) string { return "" })
	if exp, err := s.StoredMemoryTokenExpiry("office"); err != nil || !exp.IsZero() {
		t.Fatalf("expiry with no file = %v, %v, want unknown", exp, err)
	}
	until := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := s.SetMemoryTokenUntil("office", "ldm_one", until); err != nil {
		t.Fatal(err)
	}
	if exp, _ := s.StoredMemoryTokenExpiry("office"); !exp.Equal(until) {
		t.Errorf("expiry = %v, want %v", exp, until)
	}
	if err := s.SetMemoryToken("office", "ldm_two"); err != nil {
		t.Fatal(err)
	}
	if exp, _ := s.StoredMemoryTokenExpiry("office"); !exp.IsZero() {
		t.Errorf("expiry after a token without one = %v, want unknown", exp)
	}
}

func TestMemoryTokenStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	env := map[string]string{}
	s := NewStore(dir, func(k string) string { return env[k] })

	if tok, err := s.MemoryToken("office"); err != nil || tok != "" {
		t.Fatalf("MemoryToken() with no file = %q, %v, want empty", tok, err)
	}
	if err := s.SetMemoryToken("office", "ldm_one"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemoryToken("home", "ldm_two"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := s.MemoryToken("office"); tok != "ldm_one" {
		t.Errorf("MemoryToken(office) = %q, want ldm_one", tok)
	}
	if tok, _ := s.MemoryToken("home"); tok != "ldm_two" {
		t.Errorf("MemoryToken(home) = %q, want ldm_two", tok)
	}

	path := filepath.Join(dir, "memory-credentials.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("memory-credentials.yaml permissions = %o, want 600", perm)
	}
	// The login credentials file is left alone and holds no memory token.
	if _, err := os.Stat(filepath.Join(dir, "credentials.yaml")); err == nil {
		t.Error("SetMemoryToken wrote credentials.yaml")
	}
	if tok, _ := s.StoredToken("office"); tok != "" {
		t.Errorf("StoredToken(office) = %q, want none", tok)
	}

	env[EnvMemoryToken] = "ldm_env"
	if tok, _ := s.MemoryToken("office"); tok != "ldm_env" {
		t.Errorf("MemoryToken() with %s = %q, want the env value", EnvMemoryToken, tok)
	}
	if tok, _ := s.StoredMemoryToken("office"); tok != "ldm_one" {
		t.Errorf("StoredMemoryToken() = %q, want the stored value", tok)
	}
	env[EnvMemoryToken] = ""

	if err := s.SetMemoryToken("office", ""); err != nil {
		t.Fatal(err)
	}
	if tok, _ := s.MemoryToken("office"); tok != "" {
		t.Errorf("MemoryToken() after delete = %q, want none", tok)
	}
	if tok, _ := s.MemoryToken("home"); tok != "ldm_two" {
		t.Errorf("deleting office removed home: %q", tok)
	}
}

func TestMemoryTokenStoreRefusesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "memory-credentials.yaml"), []byte("tokens: {}\nextra: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir, func(string) string { return "" })
	_, err := s.StoredMemoryToken("office")
	if err == nil || !strings.Contains(err.Error(), "memory-credentials.yaml") {
		t.Errorf("StoredMemoryToken() error = %v, want a parse error naming the file", err)
	}
}
