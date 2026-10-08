package account_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/wiki"
)

// wikiFixture is the team fixture (project acme, team web below it) with a
// wiki service and both wikis on, plus projmember, a member of acme.
func wikiFixture(t *testing.T) (*fixture, *account.Service, map[string]account.Actor) {
	t.Helper()
	f := newFixture(t)
	actors := f.team(t)
	ctx := t.Context()
	ms, _, err := f.svc.AddMember(ctx, account.Operator(), "acme", "projmember@example.com", account.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	actors["projmember"] = account.UserActor(ms.UserID)
	log := slog.New(slog.DiscardHandler)
	wikis := registry.NewWikiService(f.regMem, f.regMem, f.clock.now, log)
	svc := account.NewService(f.store, f.reg, f.roles, device.New(f.clock.now), account.Options{Wiki: wikis, Now: f.clock.now, Log: log})
	for _, ns := range []string{"acme", "web"} {
		if _, err := svc.SetWiki(ctx, account.Operator(), ns, true); err != nil {
			t.Fatal(err)
		}
	}
	return f, svc, actors
}

func page(t *testing.T, text string) wiki.Doc {
	t.Helper()
	d, err := wiki.NewDoc(wiki.Markdown, text)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestWikiWhoMay(t *testing.T) {
	_, svc, actors := wikiFixture(t)
	ctx := t.Context()
	tests := []struct {
		who, ns          string
		read, edit, flip bool
	}{
		{"member", "web", true, true, false},
		{"maintainer", "web", true, true, false},
		{"owner", "web", true, true, true},
		{"owner", "acme", true, true, true},
		{"admin", "web", true, true, true},
		{"projmember", "acme", true, true, false},
		// A member of a team below reads the project's wiki, never edits it.
		{"member", "acme", true, false, false},
		{"maintainer", "acme", true, false, false},
		{"projmember", "web", false, false, false},
		{"stranger", "web", false, false, false},
	}
	for _, tt := range tests {
		a := actors[tt.who]
		_, err := svc.Wiki(ctx, a, tt.ns, 0)
		if ok := err == nil; ok != tt.read || (!ok && !errors.Is(err, registry.ErrForbidden)) {
			t.Errorf("%s reads %s: error %v, want allowed %v", tt.who, tt.ns, err, tt.read)
		}
		_, err = svc.WikiHistory(ctx, a, tt.ns)
		if ok := err == nil; ok != tt.read {
			t.Errorf("%s reads the history of %s: error %v, want allowed %v", tt.who, tt.ns, err, tt.read)
		}
		cur, err := svc.Wiki(ctx, account.Operator(), tt.ns, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, saved, err := svc.SaveWiki(ctx, a, tt.ns, cur.Seq, page(t, tt.who+" was here"), "")
		if ok := err == nil && saved; ok != tt.edit || (!ok && !errors.Is(err, registry.ErrForbidden)) {
			t.Errorf("%s edits %s: %v, %v; want allowed %v", tt.who, tt.ns, saved, err, tt.edit)
		}
		_, err = svc.SetWiki(ctx, a, tt.ns, true)
		if ok := err == nil; ok != tt.flip || (!ok && !errors.Is(err, registry.ErrForbidden)) {
			t.Errorf("%s turns on the wiki of %s: error %v, want allowed %v", tt.who, tt.ns, err, tt.flip)
		}
	}
}

func TestSaveWikiRecordsAuthor(t *testing.T) {
	f, svc, actors := wikiFixture(t)
	ctx := t.Context()
	info, saved, err := svc.SaveWiki(ctx, actors["member"], "web", 0, page(t, "# Web\n"), "first")
	if err != nil || !saved {
		t.Fatalf("SaveWiki() = %+v, %v, %v", info, saved, err)
	}
	if info.Author != "member@example.com" || info.AuthorUserID != actors["member"].UserID || info.Note != "first" || !info.CreatedAt.Equal(f.clock.now()) {
		t.Errorf("SaveWiki() = %+v, want member's email, id, note and the time", info)
	}
	rev, err := svc.Wiki(ctx, actors["member"], "web", 1)
	if err != nil || rev.Doc.Text() != "# Web\n" {
		t.Errorf("Wiki(1) = %q, %v", rev.Doc.Text(), err)
	}
	if _, _, err := svc.SaveWiki(ctx, actors["other"], "web", 0, page(t, "mine"), ""); !errors.Is(err, registry.ErrStale) {
		t.Errorf("SaveWiki(on an old base) error = %v, want ErrStale", err)
	}
	if info, _, err := svc.SaveWiki(ctx, account.Operator(), "web", 1, page(t, "host"), ""); err != nil || info.Author == "" {
		t.Errorf("SaveWiki(operator) = %+v, %v; want an author", info, err)
	}
}

func TestWikiRefusals(t *testing.T) {
	f, svc, actors := wikiFixture(t)
	ctx := t.Context()

	if _, _, err := svc.SaveWiki(ctx, actors["member"], "acme", 0, page(t, "x"), ""); err == nil ||
		err.Error() != "token does not allow this: editing the wiki of acme needs a role in acme; members of a team below only read it" {
		t.Errorf("SaveWiki(team member on the project) error = %v", err)
	}
	if _, err := svc.SetWiki(ctx, actors["maintainer"], "web", false); err == nil ||
		err.Error() != "token does not allow this: turning the wiki of web on or off needs an owner (or an admin)" {
		t.Errorf("SetWiki(maintainer) error = %v", err)
	}

	// A login token of one namespace does not reach another.
	tok := account.TokenActor(actors["member"].UserID, "lime", registry.ScopePublish)
	if _, _, err := svc.SaveWiki(ctx, tok, "web", 0, page(t, "x"), ""); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("SaveWiki(token of lime) error = %v, want ErrForbidden", err)
	}
	// An account token acts with the user's roles.
	if _, _, err := svc.SaveWiki(ctx, account.WholeAccountActor(actors["member"].UserID), "web", 0, page(t, "x"), ""); err != nil {
		t.Errorf("SaveWiki(account token) error = %v", err)
	}

	if _, err := svc.Wiki(ctx, actors["member"], "ghost", 0); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Wiki(ghost) error = %v, want ErrNotFound", err)
	}

	// A registry without a wiki answers not found to all four.
	none := f.svc
	if _, err := none.Wiki(ctx, actors["member"], "web", 0); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Wiki() without a wiki error = %v, want ErrNotFound", err)
	}
	if _, err := none.WikiHistory(ctx, actors["member"], "web"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("WikiHistory() without a wiki error = %v, want ErrNotFound", err)
	}
	if _, _, err := none.SaveWiki(ctx, actors["member"], "web", 0, page(t, "x"), ""); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("SaveWiki() without a wiki error = %v, want ErrNotFound", err)
	}
	if _, err := none.SetWiki(ctx, actors["owner"], "web", false); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("SetWiki() without a wiki error = %v, want ErrNotFound", err)
	}
}

func TestSetWikiTurnsOff(t *testing.T) {
	_, svc, actors := wikiFixture(t)
	ctx := t.Context()
	ns, err := svc.SetWiki(ctx, actors["owner"], "web", false)
	if err != nil || ns.Wiki {
		t.Fatalf("SetWiki(off) = %+v, %v", ns, err)
	}
	if _, err := svc.Wiki(ctx, actors["member"], "web", 0); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Wiki() while off error = %v, want ErrNotFound", err)
	}
}

// TestSetCourseWhoMay: the people who turn the wiki on and off choose its
// course; the registry checks the package.
func TestSetCourseWhoMay(t *testing.T) {
	f, svc, actors := wikiFixture(t)
	ctx := t.Context()
	rel := registrytest.Release(t, "acme", "onboarding", "1.0.0")
	rel.Kind = pack.KindKB
	if _, err := f.regMem.PutRelease(ctx, rel, []byte("b")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		who, ns string
		may     bool
	}{
		{"owner", "web", true},
		{"owner", "acme", true},
		{"admin", "web", true},
		{"maintainer", "web", false},
		{"member", "web", false},
		// A reader through a team below.
		{"member", "acme", false},
		{"stranger", "web", false},
	}
	for _, tt := range tests {
		ns, err := svc.SetCourse(ctx, actors[tt.who], tt.ns, rel.ID)
		if ok := err == nil; ok != tt.may || (!ok && !errors.Is(err, registry.ErrForbidden)) {
			t.Errorf("%s sets the course of %s: error %v, want allowed %v", tt.who, tt.ns, err, tt.may)
		}
		if tt.may && ns.Course != rel.ID {
			t.Errorf("%s sets the course of %s: got %+v", tt.who, tt.ns, ns)
		}
	}
	if _, err := svc.SetCourse(ctx, actors["maintainer"], "web", rel.ID); err == nil ||
		err.Error() != "token does not allow this: choosing the course of web needs an owner (or an admin)" {
		t.Errorf("SetCourse(maintainer) error = %v", err)
	}
	if ns, err := svc.SetCourse(ctx, actors["owner"], "web", pack.ID{}); err != nil || !ns.Course.IsZero() {
		t.Errorf("clearing = %+v, %v", ns, err)
	}
}
