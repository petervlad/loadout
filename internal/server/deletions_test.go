package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

type noSnapshots struct{}

func (noSnapshots) Take(_ context.Context, label string) (string, error) { return label + ".db", nil }

type deletionServer struct {
	h       http.Handler
	store   *registrytest.MemStore
	alice   string // a read token, the author
	bob     string // a publish token, the reviewer
	outside string // a token of another team
}

func newDeletionServer(t *testing.T, withRoutes bool) *deletionServer {
	t.Helper()
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	var opts []server.Option
	if withRoutes {
		opts = append(opts, server.WithDeletions(registry.NewDeletionService(store, store, now, log, registry.WithDeletionSnapshots(noSnapshots{}))))
	}
	f := &deletionServer{h: server.New(svc, log, "test", opts...), store: store}
	for name, team := range map[string]string{"alice": "lime", "bob": "lime", "zed": "zeta"} {
		scope := registry.ScopePublish
		if name == "alice" {
			scope = registry.ScopeRead
		}
		secret, _, err := svc.CreateToken(t.Context(), team, scope, name)
		if err != nil {
			t.Fatal(err)
		}
		switch name {
		case "alice":
			f.alice = secret
		case "bob":
			f.bob = secret
		default:
			f.outside = secret
		}
	}
	if _, err := store.PutRelease(t.Context(), registrytest.Release(t, "lime", "planner", "1.0.0"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *deletionServer) do(t *testing.T, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func (f *deletionServer) ask(t *testing.T, token string) string {
	t.Helper()
	rec := f.do(t, token, http.MethodPost, "/v1/deletions", `{"package":"lime/planner","reason":"unused"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/deletions status = %d; body %s", rec.Code, rec.Body)
	}
	return strings.TrimPrefix(rec.Header().Get("Location"), "/v1/deletions/")
}

func TestDeletionRoutesNeedTheOption(t *testing.T) {
	f := newDeletionServer(t, false)
	if rec := f.do(t, f.alice, http.MethodGet, "/v1/deletions", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /v1/deletions without the option status = %d, want 404", rec.Code)
	}
}

func TestAskForDeletion(t *testing.T) {
	tests := []struct {
		name       string
		token      func(*deletionServer) string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"ok", func(f *deletionServer) string { return f.alice }, `{"package":"lime/planner","reason":"unused"}`, http.StatusCreated, ""},
		{"no token", func(*deletionServer) string { return "" }, `{"package":"lime/planner","reason":"unused"}`, http.StatusUnauthorized, api.CodeUnauthorized},
		{"another team", func(f *deletionServer) string { return f.outside }, `{"package":"lime/planner","reason":"unused"}`, http.StatusForbidden, api.CodeForbidden},
		{"no reason", func(f *deletionServer) string { return f.alice }, `{"package":"lime/planner","reason":""}`, http.StatusBadRequest, api.CodeInvalid},
		{"unknown field", func(f *deletionServer) string { return f.alice }, `{"package":"lime/planner","reason":"x","force":true}`, http.StatusBadRequest, api.CodeInvalid},
		{"not json", func(f *deletionServer) string { return f.alice }, `lime/planner`, http.StatusBadRequest, api.CodeInvalid},
		{"not a package id", func(f *deletionServer) string { return f.alice }, `{"package":"planner","reason":"x"}`, http.StatusBadRequest, api.CodeInvalid},
		{"no such package", func(f *deletionServer) string { return f.alice }, `{"package":"lime/nope","reason":"x"}`, http.StatusNotFound, api.CodeNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeletionServer(t, true)
			rec := f.do(t, tc.token(f), http.MethodPost, "/v1/deletions", tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantCode != "" && !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Errorf("body = %s, want code %s", rec.Body, tc.wantCode)
			}
			if tc.wantStatus != http.StatusCreated {
				return
			}
			var got api.DeletionResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			d := got.Deletion
			if !strings.HasPrefix(d.ID, api.DeletionIDPrefix) || rec.Header().Get("Location") != "/v1/deletions/"+d.ID || d.Status != api.StatusOpen ||
				d.Package != "lime/planner" || d.Newest != "1.0.0" || !d.Yours || d.Author.Label != "alice" {
				t.Errorf("response = %+v", d)
			}
		})
	}

	t.Run("a second request for the package is a conflict", func(t *testing.T) {
		f := newDeletionServer(t, true)
		f.ask(t, f.alice)
		rec := f.do(t, f.bob, http.MethodPost, "/v1/deletions", `{"package":"lime/planner","reason":"again"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"`+api.CodeExists+`"`) {
			t.Errorf("status = %d, body %s; want 409 exists", rec.Code, rec.Body)
		}
	})
}

func TestReviewDeletionRoutes(t *testing.T) {
	t.Run("list and get", func(t *testing.T) {
		f := newDeletionServer(t, true)
		id := f.ask(t, f.alice)
		rec := f.do(t, f.bob, http.MethodGet, "/v1/deletions", "")
		var list api.DeletionsResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Deletions) != 1 || list.Deletions[0].ID != id || list.Deletions[0].Yours {
			t.Fatalf("GET /v1/deletions = %d %s; want [%s] not yours", rec.Code, rec.Body, id)
		}
		rec = f.do(t, f.alice, http.MethodGet, "/v1/deletions/"+id, "")
		var one api.DeletionResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &one) != nil || !one.Deletion.Yours || one.Deletion.Latest != "1.0.0" || one.Deletion.Stale {
			t.Fatalf("GET /v1/deletions/%s = %d %s; want yours, latest 1.0.0, not stale", id, rec.Code, rec.Body)
		}
		if rec := f.do(t, f.outside, http.MethodGet, "/v1/deletions/"+id, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET by another team status = %d, want 404", rec.Code)
		}
		if rec := f.do(t, f.bob, http.MethodGet, "/v1/deletions?status=bogus", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("GET ?status=bogus status = %d, want 400", rec.Code)
		}
		if rec := f.do(t, f.bob, http.MethodGet, "/v1/deletions?status=any", ""); rec.Code != http.StatusOK {
			t.Errorf("GET ?status=any status = %d, want 200", rec.Code)
		}
	})

	t.Run("a release after the request shows as stale and refuses approval", func(t *testing.T) {
		f := newDeletionServer(t, true)
		id := f.ask(t, f.alice)
		if _, err := f.store.PutRelease(t.Context(), registrytest.Release(t, "lime", "planner", "1.1.0"), []byte("b")); err != nil {
			t.Fatal(err)
		}
		var one api.DeletionResponse
		rec := f.do(t, f.bob, http.MethodGet, "/v1/deletions/"+id, "")
		if json.Unmarshal(rec.Body.Bytes(), &one) != nil || !one.Deletion.Stale || one.Deletion.Latest != "1.1.0" {
			t.Errorf("GET = %s, want stale at 1.1.0", rec.Body)
		}
		rec = f.do(t, f.bob, http.MethodPost, "/v1/deletions/"+id+"/approve", "")
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"`+api.CodeBehind+`"`) {
			t.Errorf("approve = %d %s; want 409 behind", rec.Code, rec.Body)
		}
	})

	t.Run("approve deletes the package", func(t *testing.T) {
		f := newDeletionServer(t, true)
		id := f.ask(t, f.alice)
		if rec := f.do(t, f.alice, http.MethodPost, "/v1/deletions/"+id+"/approve", ""); rec.Code != http.StatusForbidden {
			t.Errorf("approve by the author status = %d, want 403", rec.Code)
		}
		rec := f.do(t, f.bob, http.MethodPost, "/v1/deletions/"+id+"/approve", `{"note":"agreed"}`)
		var got api.DeletionResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("approve = %d %s", rec.Code, rec.Body)
		}
		d := got.Deletion
		if d.Status != api.StatusApproved || d.DecidedBy == nil || d.DecidedBy.Label != "bob" || d.Removed == nil || len(d.Removed.Releases) != 1 || d.DecisionNote != "agreed" {
			t.Errorf("approved = %+v", d)
		}
		pid, err := pack.NewID("lime", "planner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PackageFootprint(t.Context(), pid); err == nil {
			t.Error("the package is still there")
		}
		if rec := f.do(t, f.bob, http.MethodPost, "/v1/deletions/"+id+"/approve", ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeProposalClosed) {
			t.Errorf("second approve = %d %s; want 409 proposal_closed", rec.Code, rec.Body)
		}
	})

	t.Run("reject and withdraw", func(t *testing.T) {
		f := newDeletionServer(t, true)
		id := f.ask(t, f.alice)
		if rec := f.do(t, f.alice, http.MethodPost, "/v1/deletions/"+id+"/reject", ""); rec.Code != http.StatusForbidden {
			t.Errorf("reject by the author status = %d, want 403", rec.Code)
		}
		if rec := f.do(t, f.bob, http.MethodPost, "/v1/deletions/"+id+"/withdraw", ""); rec.Code != http.StatusForbidden {
			t.Errorf("withdraw by someone else status = %d, want 403", rec.Code)
		}
		if rec := f.do(t, f.bob, http.MethodPost, "/v1/deletions/"+id+"/reject", `{"note":"still used"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"rejected"`) {
			t.Errorf("reject = %d %s", rec.Code, rec.Body)
		}
		id = f.ask(t, f.alice)
		if rec := f.do(t, f.alice, http.MethodPost, "/v1/deletions/"+id+"/withdraw", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"withdrawn"`) {
			t.Errorf("withdraw = %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("ids are checked before a lookup", func(t *testing.T) {
		f := newDeletionServer(t, true)
		for _, id := range []string{"abc123", "del-", "del-UPPER", "del-a%2Fb", "del-" + strings.Repeat("a", 61)} {
			if rec := f.do(t, f.bob, http.MethodGet, "/v1/deletions/"+id, ""); rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
				t.Errorf("GET /v1/deletions/%s status = %d, want 400 or 404", id, rec.Code)
			}
		}
		if rec := f.do(t, f.bob, http.MethodGet, "/v1/deletions/del-nope", ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET of an unknown id status = %d, want 404", rec.Code)
		}
	})
}
