package account_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

// deleteFixture is a registry with lime/x, web/x (web is a team of project
// acme) and acme/x published, a person of every role from f.team, and a
// recording snapshot double.
func deleteFixture(t *testing.T, snaps account.Snapshots, log *slog.Logger) (*fixture, map[string]account.Actor) {
	t.Helper()
	f := newFixtureWith(t, func(o *account.Options) {
		o.Snapshots = snaps
		if log != nil {
			o.Log = log
		}
	})
	actors := f.team(t)
	for _, team := range []string{"lime", "web", "acme"} {
		if _, err := f.regMem.PutRelease(t.Context(), registrytest.Release(t, team, "x", "0.1.0"), []byte("b")); err != nil {
			t.Fatal(err)
		}
	}
	return f, actors
}

func mustID(t *testing.T, team, name string) pack.ID {
	t.Helper()
	id, err := pack.NewID(team, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPackageDeleteWho(t *testing.T) {
	for _, tt := range []struct {
		name  string
		actor string // key of the team() actors; empty means use mk
		mk    func(actors map[string]account.Actor) account.Actor
		team  string
		want  error // nil: allowed
	}{
		{name: "the operator", mk: func(map[string]account.Actor) account.Actor { return account.Operator() }, team: "lime"},
		{name: "an admin", actor: "admin", team: "lime"},
		{name: "an owner of the project, its team's package", actor: "owner", team: "web", want: registry.ErrForbidden},
		{name: "an owner of the project, the project's package", actor: "owner", team: "acme", want: registry.ErrForbidden},
		{name: "an owner of the project, an unrelated team", actor: "owner", team: "lime", want: registry.ErrForbidden},
		{name: "a maintainer", actor: "maintainer", team: "web", want: registry.ErrForbidden},
		{name: "a member", actor: "member", team: "web", want: registry.ErrForbidden},
		{name: "a stranger", actor: "stranger", team: "web", want: registry.ErrForbidden},
		{name: "nobody", mk: func(map[string]account.Actor) account.Actor { return account.Actor{} }, team: "lime", want: registry.ErrForbidden},
		{name: "a publish token of an admin", mk: func(a map[string]account.Actor) account.Actor {
			return account.TokenActor(a["admin"].UserID, "lime", registry.ScopePublish)
		}, team: "lime", want: registry.ErrForbidden},
		{name: "a whole-account token of an owner", mk: func(a map[string]account.Actor) account.Actor {
			return account.WholeAccountActor(a["owner"].UserID)
		}, team: "web", want: registry.ErrForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snaps := &accounttest.Snapshots{}
			f, actors := deleteFixture(t, snaps, nil)
			actor := actors[tt.actor]
			if tt.mk != nil {
				actor = tt.mk(actors)
			}
			id := mustID(t, tt.team, "x")

			_, ferr := f.svc.PackageFootprint(t.Context(), actor, id)
			if !errors.Is(ferr, tt.want) {
				t.Errorf("PackageFootprint() error = %v, want %v", ferr, tt.want)
			}
			fp, derr := f.svc.DeletePackage(t.Context(), actor, id, "retired")
			if !errors.Is(derr, tt.want) {
				t.Fatalf("DeletePackage() error = %v, want %v", derr, tt.want)
			}
			_, gone := f.regMem.Releases(t.Context(), id)
			switch {
			case tt.want == nil && (len(fp.Releases) != 1 || gone == nil):
				t.Errorf("DeletePackage() = %+v, gone = %v, want the one release removed", fp, gone)
			case tt.want != nil && gone != nil:
				t.Errorf("a refused delete removed the package: %v", gone)
			}
			if tt.want != nil && !strings.Contains(derr.Error(), "delete") {
				t.Errorf("error %q does not say what to do", derr)
			}
			recs, err := f.regMem.Deletions(t.Context(), registry.DeletionFilter{Team: tt.team, Limit: 10})
			if err != nil || (tt.want == nil) != (len(recs) == 1) {
				t.Errorf("records = %+v, %v; want one exactly when the delete was allowed", recs, err)
			}
		})
	}
}

// TestOwnersAskInstead: an owner may not delete directly, and says so in a
// sentence that points to the request flow.
func TestOwnersAskInstead(t *testing.T) {
	f, actors := deleteFixture(t, &accounttest.Snapshots{}, nil)
	_, err := f.svc.DeletePackage(t.Context(), actors["owner"], mustID(t, "web", "x"), "retired")
	if !errors.Is(err, registry.ErrForbidden) || !strings.Contains(err.Error(), "ask for the deletion") {
		t.Errorf("DeletePackage() by an owner error = %v, want ErrForbidden pointing to the request flow", err)
	}
}

func TestPackageDeleteTakesACopyFirst(t *testing.T) {
	snaps := &accounttest.Snapshots{}
	var logs bytes.Buffer
	f, actors := deleteFixture(t, snaps, slog.New(slog.NewTextHandler(&logs, nil)))
	if _, err := f.svc.DeletePackage(t.Context(), actors["admin"], mustID(t, "web", "x"), "retired"); err != nil {
		t.Fatal(err)
	}
	if got := snaps.Labels(); len(got) != 1 || got[0] != "before-delete-web-x" {
		t.Errorf("copies = %v, want one labelled before-delete-web-x", got)
	}
	recs, err := f.regMem.Deletions(t.Context(), registry.DeletionFilter{Team: "web", Limit: 10})
	if err != nil || len(recs) != 1 {
		t.Fatalf("records = %+v, %v; want one", recs, err)
	}
	if r := recs[0]; r.Reason != "retired" || r.Author.UserID != actors["admin"].UserID || !strings.HasSuffix(r.Author.Label, " on the web") || r.Status != registry.DeletionApproved || !r.SelfApproved {
		t.Errorf("record = %+v, want the admin's reason and name, approved without review", r)
	}
	for _, want := range []string{"package deleted on the web", "user_id=" + actors["admin"].UserID, "backup=before-delete-web-x.db", "releases=1"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q lacks %q", logs.String(), want)
		}
	}

	// The operator deletes without a copy: the deploy skill backs up by hand.
	if _, err := f.svc.DeletePackage(t.Context(), account.Operator(), mustID(t, "lime", "x"), ""); err != nil {
		t.Fatal(err)
	}
	if got := snaps.Labels(); len(got) != 1 {
		t.Errorf("copies = %v, want the operator's delete to take none", got)
	}
	recs, err = f.regMem.Deletions(t.Context(), registry.DeletionFilter{Team: "lime", Limit: 10})
	if err != nil || len(recs) != 1 || recs[0].Author.Label != "operator" || recs[0].Reason != "Deleted on the registry host." {
		t.Errorf("operator's record = %+v, %v; want author operator and the default reason", recs, err)
	}
}

func TestPackageDeleteFailures(t *testing.T) {
	t.Run("a failing copy deletes nothing", func(t *testing.T) {
		snaps := &accounttest.Snapshots{Err: errors.New("disk full")}
		f, actors := deleteFixture(t, snaps, nil)
		id := mustID(t, "web", "x")
		_, err := f.svc.DeletePackage(t.Context(), actors["admin"], id, "retired")
		if err == nil || !strings.Contains(err.Error(), "nothing was deleted") || errors.Is(err, registry.ErrForbidden) || !errors.Is(err, account.ErrBackupFailed) {
			t.Fatalf("DeletePackage() error = %v, want ErrBackupFailed saying nothing was deleted", err)
		}
		if _, err := f.regMem.Releases(t.Context(), id); err != nil {
			t.Errorf("the package is gone after a failed copy: %v", err)
		}
	})
	t.Run("a web delete needs a reason, before any copy", func(t *testing.T) {
		snaps := &accounttest.Snapshots{}
		f, actors := deleteFixture(t, snaps, nil)
		id := mustID(t, "web", "x")
		_, err := f.svc.DeletePackage(t.Context(), actors["admin"], id, "  ")
		if !errors.Is(err, registry.ErrInvalid) {
			t.Fatalf("DeletePackage() without a reason error = %v, want ErrInvalid", err)
		}
		if _, err := f.regMem.Releases(t.Context(), id); err != nil || len(snaps.Labels()) != 0 {
			t.Errorf("a refused delete removed the package (%v) or took a copy (%v)", err, snaps.Labels())
		}
	})
	t.Run("a missing package takes no copy", func(t *testing.T) {
		snaps := &accounttest.Snapshots{}
		f, actors := deleteFixture(t, snaps, nil)
		_, err := f.svc.DeletePackage(t.Context(), actors["admin"], mustID(t, "lime", "nope"), "retired")
		if !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("DeletePackage() error = %v, want ErrNotFound", err)
		}
		if got := snaps.Labels(); len(got) != 0 {
			t.Errorf("copies = %v, want none", got)
		}
	})
	t.Run("a refused person learns nothing about a missing package", func(t *testing.T) {
		f, actors := deleteFixture(t, &accounttest.Snapshots{}, nil)
		_, err := f.svc.DeletePackage(t.Context(), actors["member"], mustID(t, "web", "nope"), "retired")
		if !errors.Is(err, registry.ErrForbidden) {
			t.Errorf("DeletePackage() error = %v, want ErrForbidden", err)
		}
	})
	t.Run("without a backup folder the web is off, the operator is not", func(t *testing.T) {
		f, actors := deleteFixture(t, nil, nil)
		_, err := f.svc.DeletePackage(t.Context(), actors["admin"], mustID(t, "lime", "x"), "retired")
		if !errors.Is(err, registry.ErrForbidden) || !strings.Contains(err.Error(), "LOADOUT_BACKUP_DIR") {
			t.Errorf("DeletePackage() error = %v, want ErrForbidden naming LOADOUT_BACKUP_DIR", err)
		}
		if _, err := f.svc.DeletePackage(t.Context(), account.Operator(), mustID(t, "lime", "x"), "retired"); err != nil {
			t.Errorf("operator DeletePackage() error = %v, want nil", err)
		}
	})
}
