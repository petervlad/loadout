package demo

import (
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/password"
)

func TestPasswordIsAcceptable(t *testing.T) {
	for _, a := range Accounts() {
		if err := password.Check(Password, a.Email); err != nil {
			t.Errorf("password.Check(demo password, %s) = %v", a.Email, err)
		}
		if !strings.HasSuffix(a.Email, "@example.com") {
			t.Errorf("%s is not an example.com address", a.Email)
		}
	}
}

func TestAccountsReturnsACopy(t *testing.T) {
	a := Accounts()
	a[0].Email = "changed"
	if Accounts()[0].Email == "changed" {
		t.Error("Accounts shares its slice")
	}
}

// Every package folder is a valid package, so a bad edit fails here and not
// half way through a seed.
func TestEveryPackageFolderBuilds(t *testing.T) {
	n := 0
	for _, team := range []string{"acme", "lime", "koala"} {
		entries, err := fs.ReadDir(content, "content/"+team)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			ref := team + "/" + e.Name()
			if _, err := bundleOf(ref); err != nil {
				t.Errorf("%s: %v", ref, err)
			}
			n++
		}
	}
	if n < 15 {
		t.Errorf("found %d package folders, want the whole demo", n)
	}
}

func TestClockMoves(t *testing.T) {
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	c := NewClock(start)
	c.Set(start.Add(-time.Hour))
	if got := c.Now(); !got.Equal(start.Add(-time.Hour)) {
		t.Errorf("Now() = %v after Set", got)
	}
}
