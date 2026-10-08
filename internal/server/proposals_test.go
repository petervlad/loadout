package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

type part struct{ name, body string }

func multipartBody(t *testing.T, parts ...part) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q", p.name))
		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(p.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func candidateBundle(t *testing.T) string {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: lime/planner\nkind: agent\nversion: 0.2.0\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: d\n---\nprompt\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := bundle.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestProposeRequestShape(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	h := server.New(svc, log, "test", server.WithProposals(registry.NewProposalService(store, store, now, log)))
	secret, _, err := svc.CreateToken(t.Context(), "lime", registry.ScopeRead, "alice")
	if err != nil {
		t.Fatal(err)
	}

	meta := `{"note":"n","prompts":["p1"]}`
	b := candidateBundle(t)
	tests := []struct {
		name        string
		parts       []part
		contentType string
		wantStatus  int
		wantCode    string
	}{
		{"ok", []part{{"metadata", meta}, {"bundle", b}}, "", http.StatusCreated, ""},
		{"not multipart", nil, bundle.ContentType, http.StatusUnsupportedMediaType, api.CodeInvalid},
		{"missing bundle", []part{{"metadata", meta}}, "", http.StatusBadRequest, api.CodeInvalid},
		{"duplicate part", []part{{"metadata", meta}, {"metadata", meta}, {"bundle", b}}, "", http.StatusBadRequest, api.CodeInvalid},
		{"unknown part", []part{{"metadata", meta}, {"bundle", b}, {"extra", "x"}}, "", http.StatusBadRequest, api.CodeInvalid},
		{"unknown field", []part{{"metadata", `{"prompts":["p"],"admin":true}`}, {"bundle", b}}, "", http.StatusBadRequest, api.CodeInvalid},
		{"no prompts", []part{{"metadata", `{"prompts":[]}`}, {"bundle", b}}, "", http.StatusBadRequest, api.CodeInvalid},
		{"base not a release", []part{{"metadata", `{"prompts":["p"],"base":"0.1.0"}`}, {"bundle", b}}, "", http.StatusBadRequest, api.CodeInvalid},
		{"base not a version", []part{{"metadata", `{"prompts":["p"],"base":7}`}, {"bundle", b}}, "", http.StatusBadRequest, api.CodeInvalid},
		{"bundle too large", []part{{"metadata", meta}, {"bundle", strings.Repeat("x", bundle.MaxBundleSize+1)}}, "", http.StatusRequestEntityTooLarge, api.CodeTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, ct := multipartBody(t, tc.parts...)
			if tc.contentType != "" {
				ct = tc.contentType
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/proposals", body)
			req.Header.Set("Authorization", "Bearer "+secret)
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("POST /v1/proposals status = %d, want %d; body %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantCode != "" && !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Errorf("body = %s, want code %s", rec.Body, tc.wantCode)
			}
			if tc.wantStatus == http.StatusCreated {
				// Withdraw so the next case can propose the same version.
				var id string
				if _, err := fmt.Sscanf(rec.Header().Get("Location"), "/v1/proposals/%s", &id); err != nil {
					t.Fatal(err)
				}
				wreq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/proposals/"+id+"/withdraw", http.NoBody)
				wreq.Header.Set("Authorization", "Bearer "+secret)
				wrec := httptest.NewRecorder()
				h.ServeHTTP(wrec, wreq)
				if wrec.Code != http.StatusOK {
					t.Errorf("withdraw status = %d; body %s", wrec.Code, wrec.Body)
				}
			}
		})
	}
}

func TestProposalRoutesNeedTheOption(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/proposals", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+f.token(t, "lime", registry.ScopePublish))
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /v1/proposals without WithProposals status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestProposalPathValues(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	h := server.New(svc, log, "test", server.WithProposals(registry.NewProposalService(store, store, now, log)))
	secret, _, err := svc.CreateToken(t.Context(), "lime", registry.ScopePublish, "bob")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, method, path string
		wantStatus         int
	}{
		{"unknown id", http.MethodGet, "/v1/proposals/abc123", http.StatusNotFound},
		{"bad id", http.MethodGet, "/v1/proposals/ABC", http.StatusBadRequest},
		{"bad review number", http.MethodGet, "/v1/proposals/abc123/reviews/0", http.StatusBadRequest},
		{"bad status", http.MethodGet, "/v1/proposals?status=maybe", http.StatusBadRequest},
		{"bad package", http.MethodGet, "/v1/proposals?package=nope", http.StatusBadRequest},
		{"no token", http.MethodGet, "/v1/proposals", http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, http.NoBody)
			if tc.name != "no token" {
				req.Header.Set("Authorization", "Bearer "+secret)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("%s %s status = %d, want %d; body %s", tc.method, tc.path, rec.Code, tc.wantStatus, rec.Body)
			}
		})
	}
}

func TestPromptRoutes(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	props := registry.NewProposalService(store, store, now, log)
	h := server.New(svc, log, "test", server.WithProposals(props))
	token := func(label string) (string, registry.Principal) {
		t.Helper()
		secret, _, err := svc.CreateToken(t.Context(), "lime", registry.ScopeRead, label)
		if err != nil {
			t.Fatal(err)
		}
		pr, err := svc.Authenticate(t.Context(), secret)
		if err != nil {
			t.Fatal(err)
		}
		return secret, pr
	}
	alice, author := token("alice")
	bob, _ := token("bob")
	p, err := props.Propose(t.Context(), author, []byte(candidateBundle(t)), registry.ProposalInput{Prompts: []string{"one", "two"}})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/proposals/" + p.ID + "/prompts"

	tests := []struct {
		name, secret, method, path, body string
		wantStatus                       int
		wantCode                         string
	}{
		{"add", bob, http.MethodPost, base, `{"text":"three"}`, http.StatusCreated, ""},
		{"add, unknown field", bob, http.MethodPost, base, `{"text":"x","seq":1}`, http.StatusBadRequest, api.CodeInvalid},
		{"add, two values", bob, http.MethodPost, base, `{"text":"x"}{"text":"y"}`, http.StatusBadRequest, api.CodeInvalid},
		{"add, no body", bob, http.MethodPost, base, ``, http.StatusBadRequest, api.CodeInvalid},
		{"add, blank", bob, http.MethodPost, base, `{"text":"  "}`, http.StatusBadRequest, api.CodeInvalid},
		{"add, too long", bob, http.MethodPost, base, `{"text":"` + strings.Repeat("x", registry.MaxPromptSize+1) + `"}`, http.StatusBadRequest, api.CodeInvalid},
		{"add, body over the limit", bob, http.MethodPost, base, `{"text":"` + strings.Repeat("x", 64<<10) + `"}`, http.StatusRequestEntityTooLarge, api.CodeTooLarge},
		{"add, unknown proposal", bob, http.MethodPost, "/v1/proposals/abc123/prompts", `{"text":"x"}`, http.StatusNotFound, api.CodeNotFound},
		{"add, bad id", bob, http.MethodPost, "/v1/proposals/ABC/prompts", `{"text":"x"}`, http.StatusBadRequest, api.CodeInvalid},
		{"edit own", bob, http.MethodPut, base + "/3", `{"text":"three, reworded"}`, http.StatusOK, ""},
		{"edit someone else's", alice, http.MethodPut, base + "/3", `{"text":"mine now"}`, http.StatusForbidden, api.CodeForbidden},
		{"edit, bad number", bob, http.MethodPut, base + "/0", `{"text":"x"}`, http.StatusBadRequest, api.CodeInvalid},
		{"edit, no such prompt", bob, http.MethodPut, base + "/9", `{"text":"x"}`, http.StatusNotFound, api.CodeNotFound},
		{"remove someone else's", bob, http.MethodDelete, base + "/1", ``, http.StatusForbidden, api.CodeForbidden},
		{"remove own", bob, http.MethodDelete, base + "/3", ``, http.StatusNoContent, ""},
		{"remove again", bob, http.MethodDelete, base + "/3", ``, http.StatusNotFound, api.CodeNotFound},
		{"remove, bad number", bob, http.MethodDelete, base + "/+1", ``, http.StatusBadRequest, api.CodeInvalid},
		{"no token", "", http.MethodPost, base, `{"text":"x"}`, http.StatusUnauthorized, api.CodeUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body))
			if tc.secret != "" {
				req.Header.Set("Authorization", "Bearer "+tc.secret)
			}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s %s status = %d, want %d; body %s", tc.method, tc.path, rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantCode != "" && !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Errorf("body = %s, want code %s", rec.Body, tc.wantCode)
			}
		})
	}

	got, err := props.Proposal(t.Context(), author, p.ID)
	if err != nil || len(got.Prompts) != 2 || len(got.RemovedPrompts) != 1 || got.RemovedPrompts[0].Text != "three, reworded" {
		t.Errorf("Proposal() after the routes = %+v, %v; want two active and the reworded one removed", got, err)
	}
}

// TestProposalBaseAndLatest checks that a proposal carries the base sent
// with it, that reading it says what the package's latest release is now,
// in a list and alone, and that a release says who published it unless
// nobody is known.
func TestProposalBaseAndLatest(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	h := server.New(svc, log, "test", server.WithProposals(registry.NewProposalService(store, store, now, log)))
	token := func(scope registry.TokenScope, label string) string {
		t.Helper()
		secret, _, err := svc.CreateToken(t.Context(), "lime", scope, label)
		if err != nil {
			t.Fatal(err)
		}
		return secret
	}
	dana, alice := token(registry.ScopePublish, "dana"), token(registry.ScopeRead, "alice")
	// A release from before publishers were kept.
	old := registrytest.Release(t, "lime", "planner", "0.0.1")
	if _, err := store.PutRelease(t.Context(), old, []byte("x")); err != nil {
		t.Fatal(err)
	}
	publish := func(version string) {
		t.Helper()
		rec := serve(t, h, request{method: http.MethodPost, path: "/v1/packages", token: dana, contentType: bundle.ContentType, body: bundleFor(t, "lime", version)})
		if rec.Code != http.StatusCreated {
			t.Fatalf("publish %s status = %d; body %s", version, rec.Code, rec.Body)
		}
	}
	publish("0.1.0")

	body, ct := multipartBody(t, part{"metadata", `{"prompts":["p"],"base":"0.1.0"}`}, part{"bundle", candidateBundle(t)})
	rec := serve(t, h, request{method: http.MethodPost, path: "/v1/proposals", token: alice, contentType: ct, body: body.Bytes()})
	var created api.ProposalResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || created.Proposal.Base != "0.1.0" {
		t.Fatalf("propose with base 0.1.0 = %d %s; want 201 with the base", rec.Code, rec.Body)
	}
	publish("0.1.1")

	var list api.ProposalsResponse
	rec = serve(t, h, request{method: http.MethodGet, path: "/v1/proposals", token: alice})
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Proposals) != 1 {
		t.Fatalf("GET /v1/proposals = %d %s", rec.Code, rec.Body)
	}
	var one api.ProposalResponse
	rec = serve(t, h, request{method: http.MethodGet, path: "/v1/proposals/" + created.Proposal.ID, token: alice})
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatalf("GET /v1/proposals/ID = %d %s", rec.Code, rec.Body)
	}
	for what, p := range map[string]api.Proposal{"list": list.Proposals[0], "get": one.Proposal} {
		if p.Base != "0.1.0" || p.Latest != "0.1.1" || !p.Behind() {
			t.Errorf("%s: base %q, latest %q, behind %v; want made from 0.1.0, latest 0.1.1, behind", what, p.Base, p.Latest, p.Behind())
		}
		if !p.ByCaller() {
			t.Errorf("%s: yours = %v to its author, want true", what, p.Yours)
		}
	}
	// Someone else reads it as not theirs, said explicitly.
	var theirs api.ProposalResponse
	rec = serve(t, h, request{method: http.MethodGet, path: "/v1/proposals/" + created.Proposal.ID, token: dana})
	if err := json.Unmarshal(rec.Body.Bytes(), &theirs); err != nil || theirs.Proposal.Yours == nil || *theirs.Proposal.Yours {
		t.Errorf("GET by another token: yours = %v, %v; want false", theirs.Proposal.Yours, err)
	}

	// Its author replaces it, keeping the version and (for the approval
	// below) the base; it then names its
	// replacement, and the replacement what it replaced.
	body, ct = multipartBody(t, part{"metadata", `{"prompts":[],"base":"0.1.0","replaces":"` + created.Proposal.ID + `"}`}, part{"bundle", candidateBundle(t)})
	rec = serve(t, h, request{method: http.MethodPost, path: "/v1/proposals", token: alice, contentType: ct, body: body.Bytes()})
	var replaced api.ProposalResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &replaced) != nil || replaced.Proposal.Replaces != created.Proposal.ID || len(replaced.Proposal.Prompts) != 1 {
		t.Fatalf("propose replacing %s = %d %s; want 201 replacing it with its prompt", created.Proposal.ID, rec.Code, rec.Body)
	}
	rec = serve(t, h, request{method: http.MethodGet, path: "/v1/proposals/" + created.Proposal.ID, token: alice})
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || one.Proposal.ReplacedBy != replaced.Proposal.ID || one.Proposal.Status != api.StatusWithdrawn {
		t.Fatalf("GET the replaced proposal = %d %s; want withdrawn, replaced by %s", rec.Code, rec.Body, replaced.Proposal.ID)
	}
	created = replaced

	// Approving it needs over, naming the latest release.
	approve := "/v1/proposals/" + created.Proposal.ID + "/approve"
	for body, want := range map[string]string{`{}`: api.CodeBehind, `{"over":"0.1.0"}`: api.CodeBehind, `{"over":7}`: api.CodeInvalid} {
		rec = serve(t, h, request{method: http.MethodPost, path: approve, token: dana, contentType: "application/json", body: []byte(body)})
		if got := errorCode(t, rec); got.Code != want {
			t.Errorf("approve %s = %d %s, want code %s", body, rec.Code, rec.Body, want)
		}
	}
	rec = serve(t, h, request{method: http.MethodPost, path: approve, token: dana, contentType: "application/json", body: []byte(`{"over":"0.1.1"}`)})
	var approved api.ApproveResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &approved) != nil || approved.Proposal.ApprovedOver != "0.1.1" || approved.Release.Version != "0.2.0" {
		t.Fatalf("approve over 0.1.1 = %d %s; want 0.2.0 released, approved over 0.1.1", rec.Code, rec.Body)
	}

	rec = serve(t, h, request{method: http.MethodGet, path: "/v1/packages/lime/planner", token: alice})
	var rels struct {
		Releases []map[string]json.RawMessage `json:"releases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rels); err != nil || len(rels.Releases) != 4 {
		t.Fatalf("GET /v1/packages/lime/planner = %d %s", rec.Code, rec.Body)
	}
	if by, ok := rels.Releases[0]["published_by"]; ok {
		t.Errorf("release from before publishers has published_by %s, want it left out", by)
	}
	for _, r := range rels.Releases[1:] {
		var by api.Actor
		if err := json.Unmarshal(r["published_by"], &by); err != nil || by.Label != "dana" || by.TokenID == "" {
			t.Errorf("release %s published_by = %s, want dana's token", r["version"], r["published_by"])
		}
	}
}
