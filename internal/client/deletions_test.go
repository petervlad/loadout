package client_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

type fakeSnapshots struct{}

func (fakeSnapshots) Take(_ context.Context, label string) (string, error) { return label + ".db", nil }

func TestDeletionRoundTrip(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return publishedAt }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	dels := registry.NewDeletionService(store, store, now, log, registry.WithDeletionSnapshots(fakeSnapshots{}))
	ts := httptest.NewServer(server.New(svc, log, "v-test", server.WithDeletions(dels)))
	t.Cleanup(ts.Close)
	token := func(scope registry.TokenScope, label string) string {
		secret, _, err := svc.CreateToken(t.Context(), "lime", scope, label)
		if err != nil {
			t.Fatal(err)
		}
		return secret
	}
	alice := newClient(t, ts.URL, token(registry.ScopeRead, "alice"))
	bob := newClient(t, ts.URL, token(registry.ScopePublish, "bob"))
	if _, err := bob.Publish(t.Context(), newPack(t, "lime", "planner", "0.1.0")); err != nil {
		t.Fatal(err)
	}

	d, err := alice.AskDelete(t.Context(), api.DeletionRequest{Package: "lime/planner", Reason: "unused"})
	if err != nil || d.Status != api.StatusOpen || !d.Yours || d.Newest != "0.1.0" {
		t.Fatalf("AskDelete() = %+v, %v", d, err)
	}
	if _, err := alice.AskDelete(t.Context(), api.DeletionRequest{Package: "lime/planner", Reason: "again"}); apiCode(err) != api.CodeExists {
		t.Errorf("second AskDelete() error = %v, want code %s", err, api.CodeExists)
	}
	if _, err := alice.AskDelete(t.Context(), api.DeletionRequest{Package: "lime/planner"}); apiCode(err) != api.CodeInvalid && apiCode(err) != api.CodeExists {
		t.Errorf("AskDelete() without a reason error = %v, want an invalid request", err)
	}

	list, err := bob.Deletions(t.Context(), "", api.StatusOpen)
	if err != nil || len(list) != 1 || list[0].ID != d.ID || list[0].Yours {
		t.Fatalf("Deletions() = %+v, %v", list, err)
	}
	got, err := bob.Deletion(t.Context(), d.ID)
	if err != nil || got.Latest != "0.1.0" || got.Stale {
		t.Fatalf("Deletion() = %+v, %v", got, err)
	}
	if _, err := bob.Deletion(t.Context(), "../packages"); err == nil || !strings.Contains(err.Error(), "invalid deletion request id") {
		t.Errorf("Deletion(bad id) error = %v, want a refusal before any request", err)
	}

	if _, err := alice.ApproveDelete(t.Context(), d.ID, api.DecisionRequest{}); apiCode(err) != api.CodeForbidden {
		t.Errorf("ApproveDelete() by the author error = %v, want forbidden", err)
	}
	done, err := bob.ApproveDelete(t.Context(), d.ID, api.DecisionRequest{Note: "agreed"})
	if err != nil || done.Status != api.StatusApproved || done.Removed == nil || len(done.Removed.Releases) != 1 {
		t.Fatalf("ApproveDelete() = %+v, %v", done, err)
	}
	if _, err := bob.Releases(t.Context(), newPack(t, "lime", "planner", "0.1.0").ID()); err == nil {
		t.Error("the package is still there after the approval")
	}

	// A new request, rejected, then another, withdrawn.
	if _, err := bob.Publish(t.Context(), newPack(t, "lime", "planner", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	d2, err := alice.AskDelete(t.Context(), api.DeletionRequest{Package: "lime/planner", Reason: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := bob.RejectDelete(t.Context(), d2.ID, "still used"); err != nil || r.Status != api.StatusRejected {
		t.Errorf("RejectDelete() = %+v, %v", r, err)
	}
	d3, err := alice.AskDelete(t.Context(), api.DeletionRequest{Package: "lime/planner", Reason: "third time"})
	if err != nil {
		t.Fatal(err)
	}
	if w, err := alice.WithdrawDelete(t.Context(), d3.ID, ""); err != nil || w.Status != api.StatusWithdrawn {
		t.Errorf("WithdrawDelete() = %+v, %v", w, err)
	}
	all, err := alice.Deletions(t.Context(), "", api.StatusAny)
	if err != nil || len(all) != 3 {
		t.Errorf("Deletions(any) = %d, %v; want 3 (approved, rejected, withdrawn)", len(all), err)
	}
}
