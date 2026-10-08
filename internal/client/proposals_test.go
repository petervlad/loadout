package client_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/server"
)

type proposalEnv struct {
	url string
	svc *registry.Service
}

func newProposalEnv(t *testing.T) proposalEnv {
	t.Helper()
	store := registrytest.NewMemStore()
	now := func() time.Time { return publishedAt }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	props := registry.NewProposalService(store, store, now, log)
	ts := httptest.NewServer(server.New(svc, log, "v-test", server.WithProposals(props)))
	t.Cleanup(ts.Close)
	return proposalEnv{url: ts.URL, svc: svc}
}

func (e proposalEnv) client(t *testing.T, scope registry.TokenScope, label string) *client.Client {
	t.Helper()
	secret, _, err := e.svc.CreateToken(t.Context(), "lime", scope, label)
	if err != nil {
		t.Fatal(err)
	}
	return newClient(t, e.url, secret)
}

func apiCode(err error) string {
	if apiErr, ok := errors.AsType[*client.APIError](err); ok {
		return apiErr.Code
	}
	return ""
}

func TestProposalRoundTrip(t *testing.T) {
	e := newProposalEnv(t)
	alice := e.client(t, registry.ScopeRead, "alice")
	bob := e.client(t, registry.ScopePublish, "bob")
	if _, err := bob.Publish(t.Context(), newPack(t, "lime", "planner", "0.1.0")); err != nil {
		t.Fatal(err)
	}

	candidate := newPack(t, "lime", "planner", "0.2.0")
	req := api.ProposalRequest{Note: "cite tickets", Prompts: []string{"plan A", "plan B"}, Base: "0.1.0"}
	prop, err := alice.Propose(t.Context(), candidate, req)
	if err != nil {
		t.Fatalf("Propose() error: %v", err)
	}
	if prop.Base != "0.1.0" {
		t.Errorf("Propose() base = %q, want 0.1.0", prop.Base)
	}
	if prop.Status != api.StatusOpen || prop.Package != "lime/planner" || prop.Version != "0.2.0" || prop.Author.Label != "alice" || len(prop.Prompts) != 2 {
		t.Errorf("Propose() = %+v", prop)
	}
	if _, err := alice.Propose(t.Context(), candidate, req); apiCode(err) != api.CodeVersionConflict {
		t.Errorf("second Propose() error = %v, want %s", err, api.CodeVersionConflict)
	}

	list, err := bob.Proposals(t.Context(), "", "")
	if err != nil || len(list) != 1 || list[0].ID != prop.ID {
		t.Fatalf("Proposals() = %+v, %v; want the open proposal", list, err)
	}
	got, err := bob.Proposal(t.Context(), prop.ID)
	if err != nil || got.Note != "cite tickets" {
		t.Fatalf("Proposal() = %+v, %v", got, err)
	}
	p, err := bob.FetchProposal(t.Context(), got)
	if err != nil || p.Digest() != candidate.Digest() {
		t.Fatalf("FetchProposal() = %v, %v; want the candidate", p, err)
	}

	report := "# Comparison\n\n<b>not html</b>\n"
	rev, err := bob.AddReview(t.Context(), prop.ID, []byte(report), nil)
	if err != nil || rev.Seq != 1 || rev.Author.Label != "bob" {
		t.Fatalf("AddReview() = %+v, %v", rev, err)
	}
	text, err := alice.ReviewText(t.Context(), prop.ID, 1)
	if err != nil || string(text) != report {
		t.Errorf("ReviewText() = %q, %v; want the report", text, err)
	}
	withReport, err := bob.AddReview(t.Context(), prop.ID, []byte(report), []byte(`{"prompts":[]}`))
	if err != nil || withReport.Seq != 2 || withReport.ReportSize != len(`{"prompts":[]}`) {
		t.Fatalf("AddReview(with report) = %+v, %v", withReport, err)
	}
	if _, err := bob.AddReview(t.Context(), prop.ID, []byte(report), []byte("{nope")); apiCode(err) != api.CodeInvalid {
		t.Errorf("AddReview(bad report) error = %v, want %s", err, api.CodeInvalid)
	}
	if _, err := bob.AddReview(t.Context(), prop.ID, []byte(strings.Repeat("x", registry.MaxReviewSize+1)), nil); apiCode(err) != api.CodeTooLarge {
		t.Errorf("AddReview(too large) error = %v, want %s", err, api.CodeTooLarge)
	}

	if _, err := alice.Approve(t.Context(), prop.ID, api.DecisionRequest{}); apiCode(err) != api.CodeForbidden {
		t.Errorf("Approve() by the author error = %v, want %s", err, api.CodeForbidden)
	}
	out, err := bob.Approve(t.Context(), prop.ID, api.DecisionRequest{Note: "ship it"})
	if err != nil {
		t.Fatalf("Approve() error: %v", err)
	}
	if out.Proposal.Status != api.StatusApproved || out.Proposal.DecidedBy == nil || out.Proposal.DecidedBy.Label != "bob" || out.Release.Version != "0.2.0" || len(out.Proposal.Reviews) != 2 {
		t.Errorf("Approve() = %+v", out)
	}
	latest, err := alice.Fetch(t.Context(), candidate.ID(), "latest")
	if err != nil || latest.Digest() != candidate.Digest() {
		t.Errorf("Fetch(latest) after approve = %v, %v; want the candidate", latest, err)
	}
	if _, err := bob.Reject(t.Context(), prop.ID, ""); apiCode(err) != api.CodeProposalClosed {
		t.Errorf("Reject() after approve error = %v, want %s", err, api.CodeProposalClosed)
	}
	if list, err := bob.Proposals(t.Context(), "lime/planner", api.StatusAny); err != nil || len(list) != 1 || list[0].Status != api.StatusApproved {
		t.Errorf("Proposals(any) = %+v, %v; want the approved one", list, err)
	}
}

func TestWithdrawAndRejectOverHTTP(t *testing.T) {
	e := newProposalEnv(t)
	alice := e.client(t, registry.ScopeRead, "alice")
	bob := e.client(t, registry.ScopePublish, "bob")
	prop, err := alice.Propose(t.Context(), newPack(t, "lime", "planner", "0.1.0"), api.ProposalRequest{Prompts: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Withdraw(t.Context(), prop.ID, ""); apiCode(err) != api.CodeForbidden {
		t.Errorf("Withdraw() by a non-author error = %v, want %s", err, api.CodeForbidden)
	}
	got, err := alice.Withdraw(t.Context(), prop.ID, "not ready")
	if err != nil || got.Status != api.StatusWithdrawn || got.DecisionNote != "not ready" {
		t.Errorf("Withdraw() = %+v, %v", got, err)
	}

	prop, err = alice.Propose(t.Context(), newPack(t, "lime", "planner", "0.1.0"), api.ProposalRequest{Prompts: []string{"p"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err = bob.Reject(t.Context(), prop.ID, "needs examples")
	if err != nil || got.Status != api.StatusRejected {
		t.Errorf("Reject() = %+v, %v", got, err)
	}
}

func TestProposalIDIsChecked(t *testing.T) {
	c := newClient(t, "http://127.0.0.1:1", "ldt_x")
	for _, id := range []string{"", "../packages", "A B"} {
		if _, err := c.Proposal(t.Context(), id); err == nil || !strings.Contains(err.Error(), "invalid proposal id") {
			t.Errorf("Proposal(%q) error = %v, want invalid proposal id", id, err)
		}
	}
}

func TestFetchProposalDetectsTampering(t *testing.T) {
	good := newPack(t, "lime", "planner", "0.2.0")
	other := newPack(t, "lime", "planner", "0.3.0")
	otherData, err := bundle.Encode(other)
	if err != nil {
		t.Fatal(err)
	}
	prop := api.Proposal{ID: "abc123", Package: "lime/planner", Version: "0.2.0", Digest: string(good.Digest())}

	tests := []struct {
		name      string
		announced string
		want      string
	}{
		{"announced digest does not match content", string(good.Digest()), "digest mismatch"},
		{"content is not the proposal", string(other.Digest()), "the proposal says"},
		{"no digest header", "", "no X-Loadout-Digest"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.announced != "" {
					w.Header().Set(api.HeaderDigest, tc.announced)
				}
				w.Header().Set("Content-Type", bundle.ContentType)
				_, _ = w.Write(otherData)
			}))
			t.Cleanup(ts.Close)
			_, err := newClient(t, ts.URL, "ldt_x").FetchProposal(t.Context(), prop)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("FetchProposal() error = %v, want %q", err, tc.want)
			}
		})
	}
}

// A registry older than approving as owner decodes the decision strictly:
// a normal approve still works, and approving as owner says plainly why it
// cannot.
func TestApproveAsOwnerOnAnOlderRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Note string `json:"note"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		w.Header().Set("Content-Type", "application/json")
		if err := dec.Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			msg, _ := json.Marshal("invalid request: body: " + err.Error())
			_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":` + string(msg) + `}}`))
			return
		}
		_, _ = w.Write([]byte(`{"proposal":{"id":"abc123","status":"approved"},"release":{"id":"lime/planner","version":"0.2.0"}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, "ldt_x")
	if err != nil {
		t.Fatal(err)
	}
	if res, err := c.Approve(t.Context(), "abc123", api.DecisionRequest{Note: "ok"}); err != nil || res.Release.Version != "0.2.0" {
		t.Errorf("Approve() on an older registry = %+v, %v; want it approved", res, err)
	}
	if _, err := c.Approve(t.Context(), "abc123", api.DecisionRequest{Note: "why", AsOwner: true}); !errors.Is(err, client.ErrOwnerApprovalUnsupported) {
		t.Errorf("Approve(as owner) on an older registry error = %v, want ErrOwnerApprovalUnsupported", err)
	}
	if _, err := c.Approve(t.Context(), "abc123", api.DecisionRequest{Over: "0.1.1"}); !errors.Is(err, client.ErrOverUnsupported) {
		t.Errorf("Approve(over) on an older registry error = %v, want ErrOverUnsupported", err)
	}
}

// A registry older than proposal bases decodes the metadata strictly: the
// client proposes again without the base, and only then.
func TestProposeOnARegistryOlderThanBases(t *testing.T) {
	candidate := newPack(t, "lime", "planner", "0.2.0")
	var uploads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			Note    string   `json:"note"`
			Prompts []string `json:"prompts"`
		}
		meta := r.FormValue(api.ProposalMetadataPart)
		uploads = append(uploads, meta)
		dec := json.NewDecoder(strings.NewReader(meta))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			msg, _ := json.Marshal("invalid request: metadata part: " + err.Error())
			_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":` + string(msg) + `}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"proposal":{"id":"abc123","package":"lime/planner","version":"0.2.0","status":"open","digest":"` + string(candidate.Digest()) + `"}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, "ldt_x")
	if err != nil {
		t.Fatal(err)
	}

	prop, err := c.Propose(t.Context(), candidate, api.ProposalRequest{Prompts: []string{"p"}, Base: "0.1.0"})
	if err != nil || prop.ID != "abc123" || prop.Base != "" {
		t.Fatalf("Propose() on an older registry = %+v, %v; want it proposed without a base", prop, err)
	}
	if len(uploads) != 2 || !strings.Contains(uploads[0], `"base":"0.1.0"`) || strings.Contains(uploads[1], "base") {
		t.Errorf("uploads = %q; want one with the base, then one without", uploads)
	}

	uploads = nil
	prop, err = c.Propose(t.Context(), candidate, api.ProposalRequest{Prompts: []string{"p"}, Base: "0.1.0", Replaces: "def456"})
	if err != nil || prop.Replaces != "" || len(uploads) != 2 || strings.Contains(uploads[1], "replaces") {
		t.Errorf("Propose(replaces) on an older registry = %+v, %v after uploads %q; want it proposed without replacing", prop, err, uploads)
	}

	uploads = nil
	if _, err := c.Propose(t.Context(), candidate, api.ProposalRequest{Prompts: []string{"p"}}); err != nil || len(uploads) != 1 {
		t.Errorf("Propose() without a base = %v after %d uploads; want one upload", err, len(uploads))
	}
}

// An older registry refuses the multipart upload; the client sends the
// Markdown alone instead.
func TestAddReviewFallsBackToText(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		got = append(got, ct)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(ct, "multipart/") {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":"want text/markdown"}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"review":{"seq":1,"author":{"token_id":"t","label":"bob"},"created_at":"2026-01-02T03:04:05Z","size":3}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := client.New(srv.URL, "ldt_x")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := c.AddReview(t.Context(), "abc123", []byte("# r"), []byte(`{}`))
	if err != nil || rev.Seq != 1 {
		t.Fatalf("AddReview() = %+v, %v; want review 1", rev, err)
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "multipart/form-data") || !strings.HasPrefix(got[1], "text/markdown") {
		t.Errorf("AddReview() sent %q, want multipart then text/markdown", got)
	}
}

func TestPromptsOverHTTP(t *testing.T) {
	e := newProposalEnv(t)
	alice := e.client(t, registry.ScopeRead, "alice")
	bob := e.client(t, registry.ScopePublish, "bob")
	prop, err := alice.Propose(t.Context(), newPack(t, "lime", "planner", "0.1.0"), api.ProposalRequest{Prompts: []string{"plan A"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(prop.PromptList) != 1 || prop.PromptList[0].Seq != 1 || prop.PromptList[0].Author.Label != "alice" || prop.PromptList[0].EditedAt != nil {
		t.Errorf("Propose().PromptList = %+v, want alice's prompt 1", prop.PromptList)
	}

	added, err := bob.AddPrompt(t.Context(), prop.ID, "does it cite tickets?")
	if err != nil || added.Seq != 2 || added.Author.Label != "bob" || added.Text != "does it cite tickets?" {
		t.Fatalf("AddPrompt() = %+v, %v; want bob's prompt 2", added, err)
	}
	edited, err := bob.EditPrompt(t.Context(), prop.ID, 2, "does it cite every ticket?")
	if err != nil || edited.Text != "does it cite every ticket?" || edited.EditedAt == nil {
		t.Fatalf("EditPrompt() = %+v, %v", edited, err)
	}
	if _, err := alice.EditPrompt(t.Context(), prop.ID, 2, "mine now"); apiCode(err) != api.CodeForbidden {
		t.Errorf("EditPrompt(someone else's) error = %v, want %s", err, api.CodeForbidden)
	}
	if err := alice.RemovePrompt(t.Context(), prop.ID, 1); err != nil {
		t.Errorf("RemovePrompt(own) error: %v", err)
	}
	if err := bob.RemovePrompt(t.Context(), prop.ID, 2); apiCode(err) != api.CodeInvalid {
		t.Errorf("RemovePrompt(last) error = %v, want %s", err, api.CodeInvalid)
	}

	got, err := alice.Proposal(t.Context(), prop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Prompts) != 1 || got.Prompts[0] != "does it cite every ticket?" || len(got.PromptList) != 1 || got.PromptList[0].Seq != 2 {
		t.Errorf("Proposal() prompts = %q, list %+v; want only bob's edited prompt 2", got.Prompts, got.PromptList)
	}
	if _, err := bob.AddPrompt(t.Context(), "../x", "q"); err == nil {
		t.Error("AddPrompt(bad id) succeeded")
	}
}

// TestProposalFromAnOlderRegistry: a registry without editable prompts
// sends no prompt_list, and the texts are still there.
func TestProposalFromAnOlderRegistry(t *testing.T) {
	var p api.Proposal
	if err := json.Unmarshal([]byte(`{"id":"abc","prompts":["plan A"]}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.PromptList != nil || len(p.Prompts) != 1 {
		t.Errorf("decoded %+v, want nil PromptList and one prompt", p)
	}
}
