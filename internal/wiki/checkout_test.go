package wiki_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/wiki"
)

func TestCheckoutsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := wiki.NewCheckouts(dir)
	if _, ok, err := c.Get("/work/wiki.md"); ok || err != nil {
		t.Fatalf("Get on no file = %v, %v; want nothing", ok, err)
	}
	want := wiki.Checkout{Ref: ref, Revision: 7, Digest: wiki.Digest([]byte("text"))}
	if err := c.Put("/work/./wiki.md", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := wiki.NewCheckouts(dir).Get("/work/wiki.md")
	if err != nil || !ok || got != want {
		t.Errorf("Get = %+v, %v, %v; want %+v", got, ok, err, want)
	}
	if got.Edited([]byte("text")) || !got.Edited([]byte("text!")) {
		t.Error("Edited does not compare the digest")
	}
	fi, err := os.Stat(filepath.Join(dir, "wiki.yaml"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("wiki.yaml = %v, %v; want mode 0600", fi, err)
	}
}

func TestCheckoutsRefuseUnknownFields(t *testing.T) {
	dir := t.TempDir()
	data := "files:\n  /work/wiki.md:\n    remote: x\n    namespace: lime\n    revision: 1\n    digest: d\n    branch: main\n"
	if err := os.WriteFile(filepath.Join(dir, "wiki.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := wiki.NewCheckouts(dir).Get("/work/wiki.md"); err == nil || !strings.Contains(err.Error(), "branch") {
		t.Errorf("Get = %v, want the unknown field refused", err)
	}
}
