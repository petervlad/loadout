package web_test

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/web"
)

var requestPath = regexp.MustCompile(`^/t/lime/deletions/(del-[a-z0-9]+)\?done=`)

// ask posts a request to delete lime/planner as b and returns its id.
func (s *uiSite) ask(b *browser, reason string) string {
	s.t.Helper()
	p := b.post(askURL, url.Values{"reason": {reason}})
	m := requestPath.FindStringSubmatch(p.location)
	if p.status != http.StatusSeeOther || m == nil {
		s.t.Fatalf("POST %s = %d %s, want a redirect to the request\n%s", askURL, p.status, p.location, p.body)
	}
	return m[1]
}

// requests lists the requests of lime, newest first.
func (s *uiSite) requests() []registry.Deletion {
	s.t.Helper()
	ds, err := s.mem.Deletions(s.t.Context(), registry.DeletionFilter{Team: "lime", Limit: 20})
	if err != nil {
		s.t.Fatal(err)
	}
	return ds
}

func (s *uiSite) request(id string) registry.Deletion {
	s.t.Helper()
	d, err := s.mem.Deletion(s.t.Context(), id)
	if err != nil {
		s.t.Fatal(err)
	}
	return d
}

func deletionURL(id string) string { return "/t/lime/deletions/" + id }

func TestAskPageWho(t *testing.T) {
	s := deleteSite(t, nil)
	for _, tt := range []struct {
		who    string // empty: signed out
		status int
	}{
		{"ada@example.com", http.StatusOK},   // admin
		{"olga@example.com", http.StatusOK},  // owner
		{"alice@example.com", http.StatusOK}, // maintainer
		{"bob@example.com", http.StatusOK},   // member
		{"eve@example.com", http.StatusNotFound},
		{"", http.StatusSeeOther},
	} {
		t.Run(tt.who, func(t *testing.T) {
			b := s.browser()
			if tt.who != "" {
				b = s.user(tt.who)
			}
			p := b.get(askURL)
			if p.status != tt.status {
				t.Fatalf("GET %s = %d, want %d", askURL, p.status, tt.status)
			}
			if tt.status != http.StatusOK {
				return
			}
			wantIn(t, p, "ask page", `<h2 id="ask-h">Ask to delete lime/planner?</h2>`, "Nothing is deleted yet.",
				"A maintainer or owner of lime who is not you approves or rejects the request.",
				`for="ask-reason">Why should it be deleted?`, `name="reason"`, `required`, `>Ask to delete<span class="vh"> lime/planner</span></button>`,
				"loadout does not record who installed it", "2.3.0", `<code>`+s.proposal+`</code>`)
			wantOut(t, p, "ask page", `aria-invalid`)
		})
	}
}

func TestAskForDeletion(t *testing.T) {
	s := deleteSite(t, nil)
	bob := s.user("bob@example.com")
	id := s.ask(bob, "  unused since spring  ")
	d := s.request(id)
	if d.Status != registry.DeletionOpen || d.Reason != "unused since spring" || d.Newest.String() != "2.3.0" || d.Package.String() != "lime/planner" {
		t.Fatalf("request = %+v", d)
	}
	if !s.planner() || len(s.snaps.Labels()) != 0 {
		t.Error("asking deleted the package or took a copy")
	}

	t.Run("the request page", func(t *testing.T) {
		p := bob.get(deletionURL(id) + "?done=asked")
		wantIn(t, p, "page", "<h2>Request to delete lime/planner</h2>", "Waiting for review",
			"Asked by <strong>", `<p class="reason">`, "unused since spring", `<details class="menu">`, "This is what loadout removes.", "2.3.0", `<code>`+s.proposal+`</code>`,
			"Asked. A maintainer or owner of lime who is not you approves it or rejects it.", `role="status"`)
	})
	t.Run("the reviews list shows it beside the proposals", func(t *testing.T) {
		p := bob.get("/t/lime/reviews")
		wantIn(t, p, "reviews", `href="`+deletionURL(id)+`"`, `<span class="pill pill-bad">Delete</span>`, "unused since spring", "Waiting for review", `href="/t/lime/reviews/`+s.proposal+`"`)
		// The tab counts the open proposal and the open request.
		wantIn(t, p, "reviews", `Reviews<span class="count">2<span class="vh"> open</span></span>`)
	})
	t.Run("the package page says it was asked and offers nothing more", func(t *testing.T) {
		p := bob.get(packageURL)
		wantIn(t, p, "package page", "Deletion asked by <strong>", `href="`+deletionURL(id)+`"`)
		wantOut(t, p, "package page", `href="`+askURL+`"`)
	})
	t.Run("the ask page for the same package points to the open request", func(t *testing.T) {
		p := bob.get(askURL)
		wantIn(t, p, "ask page", "Someone already asked", `href="`+deletionURL(id)+`"`)
		wantOut(t, p, "ask page", `name="reason"`)
	})
	t.Run("a second submit goes to the open request and adds none", func(t *testing.T) {
		p := bob.post(askURL, url.Values{"reason": {"again"}})
		if p.status != http.StatusSeeOther || p.location != deletionURL(id)+"?done=exists" {
			t.Fatalf("POST = %d %s, want a redirect to the open request", p.status, p.location)
		}
		wantIn(t, bob.get(p.location), "page", "Someone already asked to delete this package. This is that request.")
		if n := len(s.requests()); n != 1 {
			t.Errorf("requests = %d, want 1", n)
		}
	})
}

func TestAskRefusals(t *testing.T) {
	s := deleteSite(t, nil)
	bob := s.user("bob@example.com")

	t.Run("no reason is a 400 with the field marked", func(t *testing.T) {
		for _, reason := range []string{"", "   \n"} {
			p := bob.post(askURL, url.Values{"reason": {reason}})
			if p.status != http.StatusBadRequest {
				t.Fatalf("POST = %d, want 400", p.status)
			}
			wantIn(t, p, "page", "Say why lime/planner should be deleted.", `aria-invalid="true"`, `aria-describedby="form-error ask-hint"`, `id="form-error"`, "autofocus")
		}
		if n := len(s.requests()); n != 0 {
			t.Errorf("requests = %d, want none", n)
		}
	})
	t.Run("the reason is kept after an error elsewhere", func(t *testing.T) {
		p := bob.post(askURL, url.Values{"reason": {strings.Repeat("x", registry.MaxNoteSize+1)}})
		if p.status != http.StatusBadRequest {
			t.Errorf("POST with a reason too long = %d, want 400", p.status)
		}
	})
	t.Run("a bad CSRF token asks nothing", func(t *testing.T) {
		p := bob.post(askURL, url.Values{"reason": {"unused"}, "csrf": {"wrong"}})
		if p.status != http.StatusForbidden {
			t.Fatalf("POST = %d, want 403", p.status)
		}
		wantIn(t, p, "page", "Form expired")
		if n := len(s.requests()); n != 0 {
			t.Errorf("requests = %d, want none", n)
		}
	})
	t.Run("a stranger and a signed-out visitor", func(t *testing.T) {
		if p := s.user("eve@example.com").post(askURL, url.Values{"reason": {"unused"}}); p.status != http.StatusNotFound {
			t.Errorf("eve POST = %d, want 404", p.status)
		}
		b := s.browser()
		b.get("/auth/login")
		if p := b.post(askURL, url.Values{"reason": {"unused"}}); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login?") {
			t.Errorf("signed-out POST = %d %s, want a redirect to sign in", p.status, p.location)
		}
		if n := len(s.requests()); n != 0 {
			t.Errorf("requests = %d, want none", n)
		}
	})
	t.Run("a package that is out of reach or not there is a 404", func(t *testing.T) {
		for _, path := range []string{
			"/t/lime/p/lime/nope/delete-request",
			"/t/lime/p/koala/planner/delete-request", // a sibling team's package
			"/t/lime/p/Bad_Team/x/delete-request",
		} {
			if p := bob.get(path); p.status != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", path, p.status)
			}
			if p := bob.post(path, url.Values{"reason": {"x"}}); p.status != http.StatusNotFound {
				t.Errorf("POST %s = %d, want 404", path, p.status)
			}
		}
	})
	t.Run("a package deleted while the page was open", func(t *testing.T) {
		id, err := pack.NewID("lime", "planner")
		if err != nil {
			t.Fatal(err)
		}
		bob.get(askURL)
		if _, err := s.mem.DeletePackage(t.Context(), id, time.Now()); err != nil {
			t.Fatal(err)
		}
		if p := bob.post(askURL, url.Values{"reason": {"unused"}}); p.status != http.StatusNotFound {
			t.Errorf("POST for a deleted package = %d, want 404", p.status)
		}
	})
}

func TestAskForTheProjectsPackage(t *testing.T) {
	s := deleteSite(t, nil)
	bob := s.user("bob@example.com")
	path := "/t/lime/p/acme/python-standards/delete-request"
	wantIn(t, bob.get(path), "ask page", "Ask to delete acme/python-standards?", "A maintainer or owner of acme who is not you")
	p := bob.post(path, url.Values{"reason": {"superseded"}})
	// bob reads acme only through lime, so he cannot open its reviews: he
	// lands on the package page, which says who decides.
	if p.status != http.StatusSeeOther || p.location != "/t/lime/p/acme/python-standards?done=asked" {
		t.Fatalf("POST = %d %s, want a redirect to the package page", p.status, p.location)
	}
	wantIn(t, bob.get(p.location), "package page", "Asked. A maintainer or owner of acme decides", "Deletion asked by <strong>")
	ds, err := s.mem.Deletions(t.Context(), registry.DeletionFilter{Team: "acme", Limit: 10})
	if err != nil || len(ds) != 1 || ds[0].Reason != "superseded" {
		t.Fatalf("acme requests = %+v, %v", ds, err)
	}
	wantOut(t, bob.get("/t/lime/p/acme/python-standards"), "package page", `/deletions/`)
	// The request is acme's: it is not in lime's reviews, and lime's pages
	// cannot open it.
	wantOut(t, bob.get("/t/lime/reviews"), "lime reviews", ds[0].ID)
	if p := bob.get("/t/lime/deletions/" + ds[0].ID); p.status != http.StatusNotFound {
		t.Errorf("GET through another namespace = %d, want 404", p.status)
	}
	// Owners of acme decide on acme's page.
	olga := s.user("olga@example.com")
	wantIn(t, olga.get("/t/acme/deletions/"+ds[0].ID), "acme request", "Request to delete acme/python-standards", "superseded", `action="/t/acme/deletions/`+ds[0].ID+`/approve"`)
}

func TestDeletionPageButtons(t *testing.T) {
	s := deleteSite(t, nil)
	id := s.ask(s.user("bob@example.com"), "unused")
	for _, tt := range []struct {
		who       string
		status    int
		approve   bool
		reject    bool
		withdraw  bool
		asOwner   bool
		wantWhy   string
		ownersOwn bool // look at olga's own request instead
	}{
		{who: "alice@example.com", status: 200, approve: true, reject: true, wantWhy: "You can approve: you maintain lime and you are not the author."},
		{who: "olga@example.com", status: 200, approve: true, reject: true, wantWhy: "You can approve"},
		{who: "ada@example.com", status: 200, approve: true, reject: true, wantWhy: "You can approve"},
		{who: "bob@example.com", status: 200, withdraw: true, wantWhy: "You asked for this, so a teammate who maintains lime approves it."},
		{who: "dan@example.com", status: 200, wantWhy: "Only maintainers of lime approve or reject."},
		{who: "eve@example.com", status: 404},
		{who: "", status: 303},
	} {
		t.Run(tt.who, func(t *testing.T) {
			b := s.browser()
			if tt.who != "" {
				b = s.user(tt.who)
			}
			p := b.get(deletionURL(id))
			if p.status != tt.status {
				t.Fatalf("GET = %d, want %d", p.status, tt.status)
			}
			if tt.status != http.StatusOK {
				return
			}
			has := func(want string) bool { return strings.Contains(p.body, want) }
			if got := has(`action="` + deletionURL(id) + `/approve"`); got != tt.approve {
				t.Errorf("approve form = %v, want %v", got, tt.approve)
			}
			if got := has(`action="` + deletionURL(id) + `/reject"`); got != tt.reject {
				t.Errorf("reject form = %v, want %v", got, tt.reject)
			}
			if got := has(`action="` + deletionURL(id) + `/withdraw"`); got != tt.withdraw {
				t.Errorf("withdraw form = %v, want %v", got, tt.withdraw)
			}
			if has(`name="as_owner"`) {
				t.Error("as owner offered to someone who did not ask")
			}
			wantIn(t, p, "page", tt.wantWhy)
			if !tt.approve && !tt.withdraw {
				wantIn(t, p, "page", `disabled aria-describedby="why"`)
			}
		})
	}

	t.Run("an owner who asked may approve as owner", func(t *testing.T) {
		olga := s.user("olga@example.com")
		// One request at a time: bob's closes first.
		if p := s.user("bob@example.com").post(deletionURL(id)+"/withdraw", url.Values{}); p.status != http.StatusSeeOther {
			t.Fatalf("withdraw = %d", p.status)
		}
		own := s.ask(olga, "mine")
		p := olga.get(deletionURL(own))
		wantIn(t, p, "page", `name="as_owner"`, "Approve as owner…", "As an owner of lime you may delete the package yourself", `action="`+deletionURL(own)+`/withdraw"`, "Delete without review")
		// Only the owner's popover posts to approve; the plain button is not offered.
		wantOut(t, p, "page", "Approve and delete")
		if n := strings.Count(p.body, `action="`+deletionURL(own)+`/approve"`); n != 1 {
			t.Errorf("approve forms = %d, want only the one in the owner popover", n)
		}
	})
}

func TestDeletionDecisions(t *testing.T) {
	post := func(b *browser, id, action string, f url.Values) page { return b.post(deletionURL(id)+"/"+action, f) }

	t.Run("a maintainer approves and the package goes", func(t *testing.T) {
		s := deleteSite(t, nil)
		bob, alice := s.user("bob@example.com"), s.user("alice@example.com")
		id := s.ask(bob, "unused")
		if p := post(bob, id, "approve", url.Values{}); p.status != http.StatusForbidden {
			t.Errorf("approve by the author = %d, want 403", p.status)
		}
		if p := post(s.user("dan@example.com"), id, "approve", url.Values{}); p.status != http.StatusForbidden {
			t.Errorf("approve by a member = %d, want 403", p.status)
		}
		if !s.planner() {
			t.Fatal("a refused approval deleted the package")
		}
		p := post(alice, id, "approve", url.Values{})
		if p.status != http.StatusSeeOther || p.location != deletionURL(id)+"?done=approved" {
			t.Fatalf("approve = %d %s, want a redirect to the request", p.status, p.location)
		}
		if s.planner() {
			t.Error("the package is still there")
		}
		if got := s.snaps.Labels(); len(got) != 1 || got[0] != "before-delete-lime-planner" {
			t.Errorf("copies = %v, want one before the delete", got)
		}
		d := s.request(id)
		if d.Status != registry.DeletionApproved || d.SelfApproved || len(d.Removed.Releases) != 1 || d.Removed.Proposals != 1 {
			t.Errorf("request = %+v", d)
		}
		page := alice.get(p.location)
		wantIn(t, page, "page", "Deleted</h2>", "Deleted 1 release, 1 proposal, 0 reviews and 2 prompts", " by <strong>", "This is what loadout removed.",
			"Approved. The package was deleted, and a copy of the database from before is in the backup folder.", `class="pill pill-ok">Deleted</span>`)
		wantOut(t, page, "page", `action="`+deletionURL(id)+`/approve"`, `<code>`+s.proposal+`</code>`)
		list := alice.get("/t/lime/reviews?status=all")
		wantIn(t, list, "closed list", `href="`+deletionURL(id)+`"`, "Deleted")
		wantOut(t, alice.get("/t/lime/reviews"), "open list", deletionURL(id))
		if g := alice.get(packageURL); g.status != http.StatusNotFound {
			t.Errorf("GET of the deleted package = %d, want 404", g.status)
		}

		// A second click ends on the same page, saying it was closed.
		again := post(alice, id, "approve", url.Values{})
		if again.status != http.StatusSeeOther || again.location != deletionURL(id)+"?done=closed" {
			t.Fatalf("second approve = %d %s", again.status, again.location)
		}
		wantIn(t, alice.get(again.location), "page", "That request was already closed. This is how it ended.")
		if got := s.snaps.Labels(); len(got) != 1 {
			t.Errorf("copies = %v, want the second click to take none", got)
		}
	})

	t.Run("reject keeps the package and shows the note", func(t *testing.T) {
		s := deleteSite(t, nil)
		bob, alice := s.user("bob@example.com"), s.user("alice@example.com")
		id := s.ask(bob, "unused")
		if p := post(bob, id, "reject", url.Values{}); p.status != http.StatusForbidden {
			t.Errorf("reject by the author = %d, want 403", p.status)
		}
		p := post(alice, id, "reject", url.Values{"note": {"still used by the demo"}})
		if p.status != http.StatusSeeOther || p.location != deletionURL(id)+"?done=rejected" {
			t.Fatalf("reject = %d %s", p.status, p.location)
		}
		wantIn(t, alice.get(p.location), "page", "Rejected</h2>", "Rejected", "so the package stays", "still used by the demo", "Rejected. The package stays.")
		if !s.planner() || len(s.snaps.Labels()) != 0 {
			t.Error("a rejection deleted the package or took a copy")
		}
		// The author may ask again.
		s.ask(bob, "second try")
	})

	t.Run("only the author withdraws", func(t *testing.T) {
		s := deleteSite(t, nil)
		bob, alice := s.user("bob@example.com"), s.user("alice@example.com")
		id := s.ask(bob, "unused")
		if p := post(alice, id, "withdraw", url.Values{}); p.status != http.StatusForbidden {
			t.Errorf("withdraw by a maintainer = %d, want 403", p.status)
		} else {
			wantIn(t, p, "page", "Only the author can withdraw request "+id+".")
		}
		p := post(bob, id, "withdraw", url.Values{"note": {"changed my mind"}})
		if p.status != http.StatusSeeOther || p.location != deletionURL(id)+"?done=withdrawn" {
			t.Fatalf("withdraw = %d %s", p.status, p.location)
		}
		wantIn(t, bob.get(p.location), "page", "Withdrawn</h2>", "changed my mind")
		if !s.planner() {
			t.Error("a withdrawal deleted the package")
		}
	})

	t.Run("an owner approves their own only as owner and with a note", func(t *testing.T) {
		s := deleteSite(t, nil)
		olga := s.user("olga@example.com")
		id := s.ask(olga, "mine to delete")
		p := post(olga, id, "approve", url.Values{})
		if p.status != http.StatusForbidden {
			t.Fatalf("approve without as owner = %d, want 403", p.status)
		}
		wantIn(t, p, "page", "as an owner of lime you may approve it yourself")
		p = post(olga, id, "approve", url.Values{"as_owner": {"on"}, "note": {"  "}})
		if p.status != http.StatusBadRequest {
			t.Fatalf("approve as owner without a note = %d, want 400", p.status)
		}
		if !s.planner() {
			t.Fatal("a refused approval deleted the package")
		}
		p = post(olga, id, "approve", url.Values{"as_owner": {"on"}, "note": {"nobody else is around"}})
		if p.status != http.StatusSeeOther || p.location != deletionURL(id)+"?done=approved-as-owner" {
			t.Fatalf("approve as owner = %d %s", p.status, p.location)
		}
		if s.planner() {
			t.Error("the package is still there")
		}
		page := olga.get(p.location)
		wantIn(t, page, "page", "Deleted without review</h2>", "Deleted 1 release", "on their own request, without a second person", "nobody else is around",
			`class="pill pill-warn">Deleted without review</span>`, "Deleted without review. The record says you approved your own request as owner.")
		if d := s.request(id); !d.SelfApproved || d.DecisionNote != "nobody else is around" {
			t.Errorf("request = %+v, want self approved with the note", d)
		}
	})

	t.Run("a maintainer who is not an owner cannot approve their own", func(t *testing.T) {
		s := deleteSite(t, nil)
		alice := s.user("alice@example.com")
		id := s.ask(alice, "unused")
		if p := post(alice, id, "approve", url.Values{"as_owner": {"on"}, "note": {"x"}}); p.status != http.StatusForbidden {
			t.Errorf("approve own = %d, want 403", p.status)
		}
		wantOut(t, alice.get(deletionURL(id)), "page", `name="as_owner"`)
	})

	t.Run("a release after the request cannot be approved over", func(t *testing.T) {
		s := deleteSite(t, nil)
		bob, alice := s.user("bob@example.com"), s.user("alice@example.com")
		id := s.ask(bob, "unused")
		s.publish("lime", "planner", "2.5.0", "Plans work", "Newer.\n")
		page := alice.get(deletionURL(id))
		wantIn(t, page, "page", "<strong>2.5.0</strong> came out after this was asked", "Approve is off: 2.5.0 came out after this was asked.", `disabled aria-describedby="why"`, `action="`+deletionURL(id)+`/reject"`)
		wantOut(t, page, "page", `action="`+deletionURL(id)+`/approve"`)
		p := post(alice, id, "approve", url.Values{})
		if p.status != http.StatusConflict {
			t.Fatalf("approve = %d, want 409", p.status)
		}
		wantIn(t, p, "page", "Something changed", "2.5.0")
		if !s.planner() || len(s.snaps.Labels()) != 0 {
			t.Error("a stale approval deleted the package or took a copy")
		}
		wantIn(t, bob.get(deletionURL(id)), "author's page", `action="`+deletionURL(id)+`/withdraw"`)
	})

	t.Run("a failed copy deletes nothing and leaves it open", func(t *testing.T) {
		s := deleteSite(t, nil)
		id := s.ask(s.user("bob@example.com"), "unused")
		s.snaps.Err = errors.New("disk full")
		alice := s.user("alice@example.com")
		p := post(alice, id, "approve", url.Values{})
		if p.status != http.StatusInternalServerError {
			t.Fatalf("approve = %d, want 500", p.status)
		}
		wantIn(t, p, "page", "Nothing was deleted", "check its backup folder")
		if !s.planner() || s.request(id).Status != registry.DeletionOpen {
			t.Error("the package was deleted, or the request closed, without a copy")
		}
	})

	t.Run("a bad CSRF token decides nothing", func(t *testing.T) {
		s := deleteSite(t, nil)
		id := s.ask(s.user("bob@example.com"), "unused")
		alice := s.user("alice@example.com")
		p := post(alice, id, "approve", url.Values{"csrf": {"wrong"}})
		if p.status != http.StatusForbidden {
			t.Fatalf("approve = %d, want 403", p.status)
		}
		wantIn(t, p, "page", "Form expired")
		if !s.planner() {
			t.Error("the package was deleted")
		}
	})

	t.Run("a signed-out POST is sent to sign in", func(t *testing.T) {
		s := deleteSite(t, nil)
		id := s.ask(s.user("bob@example.com"), "unused")
		b := s.browser()
		b.get("/auth/login")
		if p := post(b, id, "approve", url.Values{}); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login?") {
			t.Errorf("POST = %d %s, want a redirect to sign in", p.status, p.location)
		}
	})

	t.Run("an admin who deletes at once closes the request as gone", func(t *testing.T) {
		s := deleteSite(t, nil)
		id := s.ask(s.user("bob@example.com"), "unused")
		ada := s.user("ada@example.com")
		wantIn(t, ada.get(deleteURL), "delete page", "Deletion requests", `href="`+deletionURL(id)+`"`, "closed as gone when it is deleted")
		if p := ada.post(deleteURL, confirmForm(t, ada, "lime/planner")); p.status != http.StatusSeeOther {
			t.Fatalf("delete = %d", p.status)
		}
		if got := s.request(id).Status; got != registry.DeletionGone {
			t.Fatalf("request = %s, want gone", got)
		}
		alice := s.user("alice@example.com")
		page := alice.get(deletionURL(id))
		wantIn(t, page, "page", "Deleted another way</h2>", "because the package was deleted another way", `pill-plain">Deleted another way</span>`)
		wantOut(t, page, "page", `/approve"`)
		// A click on a button of an older copy of the page ends here too.
		if p := post(alice, id, "approve", url.Values{}); p.status != http.StatusSeeOther || p.location != deletionURL(id)+"?done=closed" {
			t.Errorf("approve of a gone request = %d %s", p.status, p.location)
		}
	})

	t.Run("ids that are not requests of this namespace are a 404", func(t *testing.T) {
		s := deleteSite(t, nil)
		alice := s.user("alice@example.com")
		for _, id := range []string{"del-nope", "abc", "del-", "del-UP", s.proposal} {
			if p := alice.get(deletionURL(id)); p.status != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", id, p.status)
			}
			if p := post(alice, id, "approve", url.Values{}); p.status != http.StatusNotFound {
				t.Errorf("POST approve %s = %d, want 404", id, p.status)
			}
		}
		id := s.ask(s.user("bob@example.com"), "unused")
		if p := post(alice, id, "explode", url.Values{}); p.status != http.StatusNotFound {
			t.Errorf("POST explode = %d, want 404", p.status)
		}
		// A maintainer of another team cannot open it.
		if p := s.user("eve@example.com").get(deletionURL(id)); p.status != http.StatusNotFound {
			t.Errorf("GET by a maintainer of another team = %d, want 404", p.status)
		}
	})
}

func TestRequestsOffWithoutAService(t *testing.T) {
	s := deleteSite(t, func(ui *web.UI) { ui.Requests = nil })
	bob := s.user("bob@example.com")
	for _, path := range []string{askURL, "/t/lime/deletions/del-abc"} {
		if p := bob.get(path); p.status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, p.status)
		}
	}
	wantOut(t, bob.get(packageURL), "package page", "Ask to delete", askURL)
	wantOut(t, bob.get("/t/lime/reviews"), "reviews", "pill-bad")
}
