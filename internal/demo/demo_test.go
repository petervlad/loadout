package demo

import (
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

// Every package of the demo is a valid package, so a bad edit fails here and
// not half way through a seed.
func TestEveryPackageBuilds(t *testing.T) {
	for _, r := range releases {
		if _, err := bundleOf(r.f); err != nil {
			t.Errorf("%s: %v", r.f["loadout.yaml"], err)
		}
	}
	for _, p := range proposals {
		if _, err := bundleOf(p.f); err != nil {
			t.Errorf("%s: %v", p.f["loadout.yaml"], err)
		}
	}
	if len(releases) < 40 {
		t.Errorf("%d releases, want the whole demo", len(releases))
	}
}

// Every note links only to an earlier one, and the keys are unique.
func TestNotesLinkBackwards(t *testing.T) {
	seen := map[string]time.Duration{}
	for _, n := range notes {
		if _, dup := seen[n.key]; dup {
			t.Errorf("key %s is used twice", n.key)
		}
		for _, l := range n.links {
			if ago, ok := seen[l]; !ok || ago <= n.ago {
				t.Errorf("%s links to %s, which is not an earlier entry", n.key, l)
			}
		}
		seen[n.key] = n.ago
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
