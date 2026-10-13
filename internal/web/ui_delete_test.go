package web_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/web"
)

const (
	deleteURL  = "/t/lime/p/lime/planner/delete"
	askURL     = "/t/lime/p/lime/planner/delete-request"
	packageURL = "/t/lime/p/lime/planner"
)

// deleteSite is the UI site with an admin (ada) and an owner of acme (olga).
// Admins delete at once; olga and everyone else ask for a deletion.
func deleteSite(t *testing.T, change func(*web.UI)) *uiSite {
	t.Helper()
	s := newUISiteWith(t, change)
	ctx, op := t.Context(), account.Operator()
	if _, _, err := s.accounts.AddMember(ctx, op, "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.accounts.SetAdmin(ctx, op, "ada@example.com", true); err != nil {
		t.Fatal(err)
	}
	return s
}

// planner reports whether lime/planner, the package these tests delete, is
// still in the registry.
func (s *uiSite) planner() bool {
	s.t.Helper()
	id, err := pack.NewID("lime", "planner")
	if err != nil {
		s.t.Fatal(err)
	}
	_, err = s.mem.Releases(s.t.Context(), id)
	return err == nil
}

// confirmForm is what the delete page posts: the reason, the typed id and
// the list it showed, read from the page.
func confirmForm(t *testing.T, b *browser, typed string) url.Values {
	t.Helper()
	p := b.get(deleteURL)
	if p.status != http.StatusOK {
		t.Fatalf("GET %s = %d, want the delete page", deleteURL, p.status)
	}
	i := strings.Index(p.body, `name="seen" value="`)
	if i < 0 {
		t.Fatalf("no seen field on the page:\n%s", p.body)
	}
	seen, _, _ := strings.Cut(p.body[i+len(`name="seen" value="`):], `"`)
	return url.Values{"reason": {"replaced by lime/planner-2"}, "confirm": {typed}, "seen": {seen}}
}

func TestDeletePageWho(t *testing.T) {
	s := deleteSite(t, nil)
	for _, tt := range []struct {
		who        string // empty: signed out
		status     int
		showButton bool
	}{
		{"ada@example.com", http.StatusOK, true},
		{"olga@example.com", http.StatusForbidden, false},  // owner: asks instead
		{"alice@example.com", http.StatusForbidden, false}, // maintainer
		{"bob@example.com", http.StatusForbidden, false},   // member
		{"", http.StatusSeeOther, false},
	} {
		t.Run(tt.who, func(t *testing.T) {
			b := s.browser()
			if tt.who != "" {
				b = s.as(tt.who)
			}
			p := b.get(deleteURL)
			if p.status != tt.status {
				t.Fatalf("GET %s = %d, want %d", deleteURL, p.status, tt.status)
			}
			switch tt.status {
			case http.StatusOK:
				wantIn(t, p, "delete page", "<h2 id=\"delete-h\">Delete lime/planner?</h2>", "This cannot be undone.",
					`name="confirm"`, `for="delete-confirm">Type lime/planner to confirm`, `>Delete package<span class="vh"> lime/planner</span></button>`,
					`for="delete-reason">Why is it deleted?`, `name="reason"`,
					"loadout does not record who installed it", "2.3.0", `<code>`+s.proposal+`</code>`)
			case http.StatusForbidden:
				wantIn(t, p, "refusal", "Not allowed", "Only an admin deletes lime/planner at once; ask for the deletion instead")
				wantOut(t, p, "refusal", `name="confirm"`)
			case http.StatusSeeOther:
				if !strings.HasPrefix(p.location, "/auth/login?") {
					t.Errorf("location = %q, want sign in", p.location)
				}
				return
			}
			pkg := b.get(packageURL)
			has := strings.Contains(pkg.body, `href="`+deleteURL+`"`)
			if has != tt.showButton {
				t.Errorf("package page has the Delete link = %v, want %v", has, tt.showButton)
			}
			if tt.showButton {
				wantIn(t, pkg, "package page", "<h2 id=\"delete-h\">Delete package</h2>", "this removes every release and proposal of lime/planner for good.")
			}
			// Everyone signed in who reads the package may ask instead.
			wantIn(t, pkg, "package page", `href="`+askURL+`"`)
		})
	}
	if s.snaps != nil && len(s.snaps.Labels()) != 0 {
		t.Errorf("copies = %v, want none: nothing was deleted", s.snaps.Labels())
	}
}

func TestDeleteSucceeds(t *testing.T) {
	s := deleteSite(t, nil)
	olga := s.as("ada@example.com") // an admin deletes at once
	p := olga.post(deleteURL, confirmForm(t, olga, "lime/planner"))
	want := "/t/lime?done=deleted&package=lime%2Fplanner&prompts=2&proposals=1&releases=1&reviews=0"
	if p.status != http.StatusSeeOther || p.location != want {
		t.Fatalf("POST = %d %s, want 303 to %s\n%s", p.status, p.location, want, p.body)
	}
	if s.planner() {
		t.Error("lime/planner is still there")
	}
	if got := s.snaps.Labels(); len(got) != 1 || got[0] != "before-delete-lime-planner" {
		t.Errorf("copies = %v, want one before the delete", got)
	}
	// Every delete on the web leaves a record.
	recs, err := s.mem.Deletions(t.Context(), registry.DeletionFilter{Team: "lime", Limit: 10})
	if err != nil || len(recs) != 1 || recs[0].Reason != "replaced by lime/planner-2" || recs[0].Author.Label != "ada@example.com on the web" || recs[0].Status != registry.DeletionApproved {
		t.Errorf("records = %+v, %v; want one approved record with the reason and the admin", recs, err)
	}
	list := olga.get(p.location)
	wantIn(t, list, "notice", "lime/planner was deleted: 1 release, 1 proposal, 0 reviews, 2 prompts. A copy of the database from before is in the backup folder.", `role="status"`)
	wantOut(t, list, "list", `href="/t/lime/p/lime/planner"`)

	// A second submit, the page still open in another tab, lands on the
	// "already deleted" notice and does nothing.
	again := olga.post(deleteURL, url.Values{"reason": {"x"}, "confirm": {"lime/planner"}, "seen": {"x"}})
	if again.status != http.StatusSeeOther || again.location != "/t/lime?done=deleted-gone&package=lime%2Fplanner" {
		t.Fatalf("second POST = %d %s, want 303 to the already deleted notice", again.status, again.location)
	}
	wantIn(t, olga.get(again.location), "notice", "lime/planner was already deleted.")
	if got := s.snaps.Labels(); len(got) != 1 {
		t.Errorf("copies = %v, want the second submit to take none", got)
	}
	if g := olga.get(deleteURL); g.status != http.StatusNotFound {
		t.Errorf("GET of a deleted package = %d, want 404", g.status)
	}
}

func TestDeleteRefusals(t *testing.T) {
	s := deleteSite(t, nil)
	ada, olga, alice, bob := s.as("ada@example.com"), s.as("olga@example.com"), s.as("alice@example.com"), s.as("bob@example.com")

	t.Run("a missing reason is a 400 with the field marked", func(t *testing.T) {
		form := confirmForm(t, ada, "lime/planner")
		form.Set("reason", "  ")
		p := ada.post(deleteURL, form)
		if p.status != http.StatusBadRequest {
			t.Fatalf("POST = %d, want 400", p.status)
		}
		wantIn(t, p, "page", "Say why lime/planner is deleted.", `aria-invalid="true"`, `aria-describedby="form-error reason-hint"`, "autofocus")
		if !s.planner() {
			t.Error("the package was deleted")
		}
	})

	t.Run("a wrong typed name is a 400 with the field marked", func(t *testing.T) {
		form := confirmForm(t, ada, "lime/other")
		p := ada.post(deleteURL, form)
		if p.status != http.StatusBadRequest {
			t.Fatalf("POST = %d, want 400", p.status)
		}
		wantIn(t, p, "page", "Type lime/planner exactly to delete it.", `aria-invalid="true"`, `aria-describedby="form-error delete-hint"`, `id="form-error"`, `value="lime/other"`, "autofocus")
		if !s.planner() {
			t.Error("the package was deleted")
		}
	})
	t.Run("the typed name is trimmed", func(t *testing.T) {
		if p := ada.post("/t/lime/p/lime/planner/delete", url.Values{"reason": {"why"}, "confirm": {"  lime/planner "}, "seen": {"x"}}); p.status != http.StatusConflict {
			t.Errorf("POST with a stale list = %d, want 409 (the name matched)", p.status)
		}
	})
	t.Run("a changed list is a 409 and shows the new one", func(t *testing.T) {
		form := confirmForm(t, ada, "lime/planner")
		s.publish("lime", "planner", "2.5.0", "Plans work", "Newer.\n")
		p := ada.post(deleteURL, form)
		if p.status != http.StatusConflict {
			t.Fatalf("POST = %d, want 409", p.status)
		}
		wantIn(t, p, "page", "Something changed since you opened this page. Check the list again.", "2.5.0")
		if !s.planner() {
			t.Error("the package was deleted")
		}
	})
	t.Run("a bad CSRF token deletes nothing", func(t *testing.T) {
		form := confirmForm(t, ada, "lime/planner")
		form.Set("csrf", "wrong")
		p := ada.post(deleteURL, form)
		if p.status != http.StatusForbidden {
			t.Fatalf("POST = %d, want 403", p.status)
		}
		wantIn(t, p, "page", "Form expired")
		if !s.planner() {
			t.Error("the package was deleted")
		}
	})
	t.Run("an owner, a maintainer and a member are refused on the POST too", func(t *testing.T) {
		for who, b := range map[string]*browser{"olga": olga, "alice": alice, "bob": bob} {
			if p := b.post(deleteURL, url.Values{"reason": {"why"}, "confirm": {"lime/planner"}, "seen": {"x"}}); p.status != http.StatusForbidden {
				t.Errorf("%s POST = %d, want 403", who, p.status)
			}
		}
		if !s.planner() {
			t.Error("the package was deleted")
		}
	})
	t.Run("a signed-out POST is sent to sign in", func(t *testing.T) {
		b := s.browser()
		b.get("/auth/login")
		if p := b.post(deleteURL, url.Values{"confirm": {"lime/planner"}}); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login?") {
			t.Errorf("POST = %d %s, want a redirect to sign in", p.status, p.location)
		}
	})
	t.Run("a package that does not exist or is out of reach is a 404", func(t *testing.T) {
		for _, path := range []string{
			"/t/lime/p/lime/nope/delete",
			"/t/lime/p/koala/planner/delete", // a sibling team's package
			"/t/lime/p/Bad_Team/x/delete",
		} {
			if p := ada.get(path); p.status != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", path, p.status)
			}
		}
	})
	t.Run("a package of the project above, from a team's page", func(t *testing.T) {
		if p := ada.get("/t/lime/p/acme/python-standards/delete"); p.status != http.StatusOK {
			t.Errorf("admin GET = %d, want 200", p.status)
		}
		if p := olga.get("/t/lime/p/acme/python-standards/delete"); p.status != http.StatusForbidden {
			t.Errorf("owner GET = %d, want 403", p.status)
		}
		// alice is a maintainer of lime and reads acme only through it.
		if p := alice.get("/t/lime/p/acme/python-standards/delete"); p.status != http.StatusForbidden {
			t.Errorf("maintainer GET = %d, want 403", p.status)
		}
		if strings.Contains(alice.get("/t/lime/p/acme/python-standards").body, `href="/t/lime/p/acme/python-standards/delete"`) {
			t.Error("a maintainer sees a delete link for the project's package")
		}
	})
}

func TestDeleteBackupFailureDeletesNothing(t *testing.T) {
	s := deleteSite(t, nil)
	ada := s.as("ada@example.com")
	form := confirmForm(t, ada, "lime/planner")
	s.snaps.Err = errors.New("disk full")
	p := ada.post(deleteURL, form)
	if p.status != http.StatusInternalServerError {
		t.Fatalf("POST = %d, want 500", p.status)
	}
	wantIn(t, p, "page", "Nothing was deleted", "check its backup folder")
	if !s.planner() {
		t.Error("the package was deleted without a copy")
	}
}

func TestDeleteOffWithoutAService(t *testing.T) {
	s := deleteSite(t, func(ui *web.UI) { ui.Deletes = nil })
	ada := s.as("ada@example.com")
	if p := ada.get(deleteURL); p.status != http.StatusNotFound {
		t.Errorf("GET = %d, want 404", p.status)
	}
	if p := ada.post(deleteURL, url.Values{"confirm": {"lime/planner"}}); p.status != http.StatusNotFound && p.status != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 404", p.status)
	}
	// The request flow is its own switch: it still offers to ask.
	pkg := ada.get(packageURL)
	wantOut(t, pkg, "package page", "Delete package…", `href="`+deleteURL+`"`)
	wantIn(t, pkg, "package page", `href="`+askURL+`"`)
}

func TestPackagesNoticeIgnoresJunk(t *testing.T) {
	s := deleteSite(t, nil)
	ada := s.as("ada@example.com")
	for _, q := range []string{"?done=deleted&package=not+an+id", "?done=deleted", "?done=other&package=lime%2Fx"} {
		wantOut(t, ada.get("/t/lime"+q), q, `role="status"`)
	}
	wantIn(t, ada.get("/t/lime?done=deleted&package=lime%2F"+"x&releases=-3&proposals=zz"), "clamped", "lime/x was deleted: 0 releases, 0 proposals")
}
