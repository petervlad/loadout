package web_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/review"
)

// activePrompts returns the texts of the proposal's active prompts.
func (s *uiSite) activePrompts() []string {
	s.t.Helper()
	p, err := s.props.Proposal(s.t.Context(), s.principal("alice@example.com"), s.proposal)
	if err != nil {
		s.t.Fatal(err)
	}
	return p.PromptTexts()
}

func TestPromptsByRole(t *testing.T) {
	s := newUISite(t)
	base := "/t/lime/reviews/" + s.proposal
	added := base + "?done=prompt-added#prompts"

	// Dan, a member who did not propose, sees the author's prompts without
	// buttons, and may add his own.
	dan := s.as("dan@example.com")
	p := dan.get(base)
	wantIn(t, p, "member", `id="prompts"`, "2 of 20", "Plan INFOTM-1", "Plan a migration", "Added by <strong>bob@example.com</strong>",
		`action="`+base+`/prompts"`, `name="text" class="field" maxlength="4096"`)
	wantOut(t, p, "member", base+`/prompts/1"`, base+`/prompts/1/remove"`)

	p = dan.post(base+"/prompts", url.Values{"text": {"  Check " + xss + "\nsecond line  "}})
	if p.status != http.StatusSeeOther || p.location != added {
		t.Fatalf("add a prompt = %d %s\n%s", p.status, p.location, p.body)
	}
	p = dan.get(base + "?done=prompt-added")
	// Escaped, line break kept as text, never Markdown.
	wantIn(t, p, "after adding", "Prompt added. The next comparison asks it.", "3 of 20",
		`<p class="prompt-body">Check &lt;script&gt;alert(1)&lt;/script&gt;`+"\nsecond line</p>",
		base+`/prompts/3"`, base+`/prompts/3/remove"`, "Remove prompt",
		`added a prompt</a> <span class="quote">“Check &lt;script&gt;`, `href="#prompt-3"`)
	wantOut(t, p, "after adding", xss, base+`/prompts/1"`)

	// Others' prompts are not theirs to change: a maintainer, the
	// proposal's author and an owner of the project above all get no
	// buttons, and the service refuses a forged form.
	if _, _, err := s.accounts.AddMember(t.Context(), account.Operator(), "acme", "olga@example.com", account.RoleOwner); err != nil {
		t.Fatal(err)
	}
	people := map[string]*browser{}
	for _, email := range []string{"alice@example.com", "bob@example.com", "olga@example.com"} {
		b := s.as(email)
		people[email] = b
		wantOut(t, b.get(base), email, base+`/prompts/3"`, base+`/prompts/3/remove"`)
		if p := b.post(base+"/prompts/3", url.Values{"text": {"mine now"}}); p.status != http.StatusForbidden {
			t.Errorf("%s edits Dan's prompt = %d, want 403", email, p.status)
		}
		if p := b.post(base+"/prompts/3/remove", url.Values{}); p.status != http.StatusForbidden {
			t.Errorf("%s removes Dan's prompt = %d, want 403", email, p.status)
		} else {
			wantIn(t, p, email, "Only the person who added prompt 3 can remove it")
		}
	}
	// Bob proposed with two prompts; they are his to edit.
	bob := people["bob@example.com"]
	wantIn(t, bob.get(base), "author", base+`/prompts/1"`, base+`/prompts/2/remove"`)

	// Edit: the service's own messages come back as sentences.
	for _, tc := range []struct {
		text, want string
	}{
		{"   ", "The prompt is empty."},
		{strings.Repeat("a", registry.MaxPromptSize+1), "The prompt is over 4096 bytes."},
	} {
		p := dan.post(base+"/prompts/3", url.Values{"text": {tc.text}})
		if p.status != http.StatusBadRequest {
			t.Errorf("edit to %.10q = %d, want 400", tc.text, p.status)
		}
		wantIn(t, p, "refused edit", tc.want, `href="`+base+`"`)
	}
	if p := dan.post(base+"/prompts/3", url.Values{"text": {"Check the upload job"}}); p.status != http.StatusSeeOther || p.location != base+"?done=prompt-edited#prompts" {
		t.Fatalf("edit = %d %s\n%s", p.status, p.location, p.body)
	}
	p = dan.get(base + "?done=prompt-edited")
	wantIn(t, p, "edited", "Prompt saved.", "Check the upload job", " · edited", "edited a prompt")

	// Remove, then again from a stale page.
	if p := dan.post(base+"/prompts/3/remove", url.Values{}); p.status != http.StatusSeeOther || p.location != base+"?done=prompt-removed#prompts" {
		t.Fatalf("remove = %d %s\n%s", p.status, p.location, p.body)
	}
	p = dan.get(base)
	wantIn(t, p, "removed", "2 of 20", "removed a prompt", "Check the upload job")
	wantOut(t, p, "removed", `id="prompt-3"`, `href="#prompt-3"`)
	if p := dan.post(base+"/prompts/3/remove", url.Values{}); p.status != http.StatusConflict {
		t.Errorf("remove a removed prompt = %d, want 409", p.status)
	} else {
		wantIn(t, p, "stale remove", "That prompt was removed")
	}
	for _, seq := range []string{"0", "abc", "99999"} {
		if p := dan.post(base+"/prompts/"+seq+"/remove", url.Values{}); p.status != http.StatusNotFound {
			t.Errorf("remove prompt %q = %d, want 404", seq, p.status)
		}
	}

	// The last prompt stays: the page says so, and the service refuses.
	if p := bob.post(base+"/prompts/1/remove", url.Values{}); p.status != http.StatusSeeOther {
		t.Fatalf("remove prompt 1 = %d\n%s", p.status, p.body)
	}
	p = bob.get(base)
	wantIn(t, p, "last prompt", `disabled aria-describedby="last-2"`, "A proposal keeps at least one prompt, so you can edit this one but not remove it.")
	wantOut(t, p, "last prompt", base+`/prompts/2/remove"`)
	p = bob.post(base+"/prompts/2/remove", url.Values{})
	if p.status != http.StatusBadRequest {
		t.Errorf("remove the last prompt = %d, want 400", p.status)
	}
	wantIn(t, p, "last prompt refused", "A proposal keeps at least one prompt.")
	if got := s.activePrompts(); len(got) != 1 || got[0] != "Plan a migration" {
		t.Errorf("prompts left = %q, want the last one", got)
	}
}

func TestPromptsOutsiders(t *testing.T) {
	s := newUISite(t)
	base := "/t/lime/reviews/" + s.proposal
	// Eve maintains koala and cannot open lime's proposal at all.
	eve := s.as("eve@example.com")
	for _, path := range []string{base + "/prompts", base + "/prompts/1", base + "/prompts/1/remove"} {
		if p := eve.post(path, url.Values{"text": {"x"}}); p.status != http.StatusNotFound {
			t.Errorf("POST %s by an outsider = %d, want 404", path, p.status)
		}
	}
	// Signed out: sent to sign in, nothing changes.
	out := s.browser()
	out.get("/auth/login")
	if p := out.post(base+"/prompts", url.Values{"text": {"x"}}); p.status != http.StatusSeeOther || !strings.HasPrefix(p.location, "/auth/login") {
		t.Errorf("POST by a visitor = %d %s, want a redirect to sign in", p.status, p.location)
	}
	if got := s.activePrompts(); len(got) != 2 {
		t.Errorf("prompts after outsiders posted = %q, want the two from propose", got)
	}
}

func TestPromptsLimitAndClosed(t *testing.T) {
	s := newUISite(t)
	base := "/t/lime/reviews/" + s.proposal
	dan := s.as("dan@example.com")
	for i := len(s.activePrompts()); i < registry.MaxPrompts; i++ {
		if _, err := s.props.AddPrompt(t.Context(), s.principal("dan@example.com"), s.proposal, "prompt"); err != nil {
			t.Fatal(err)
		}
	}
	p := dan.get(base)
	wantIn(t, p, "full", "20 of 20", "This proposal has 20 prompts, the most it can have.")
	wantOut(t, p, "full", `action="`+base+`/prompts"`)
	p = dan.post(base+"/prompts", url.Values{"text": {"one more"}})
	if p.status != http.StatusBadRequest {
		t.Errorf("add over the limit = %d, want 400", p.status)
	}
	wantIn(t, p, "over the limit", "already has 20 prompts; remove one first.")

	if p := s.as("alice@example.com").post(base+"/approve", url.Values{}); p.status != http.StatusSeeOther {
		t.Fatalf("approve = %d\n%s", p.status, p.body)
	}
	p = dan.get(base)
	wantOut(t, p, "closed", `action="`+base+`/prompts`, "Remove prompt")
	wantIn(t, p, "closed", "Plan INFOTM-1", "20 prompts", "They no longer change.")
	p = dan.post(base+"/prompts", url.Values{"text": {"late"}})
	if p.status != http.StatusConflict {
		t.Errorf("add to a closed proposal = %d, want 409", p.status)
	}
	wantIn(t, p, "closed add", "This proposal is closed, so its prompts cannot change any more.")
}

// addReportOf attaches rep with data, as alice.
func (s *uiSite) addReportOf(rep review.Report) {
	s.t.Helper()
	data, err := json.Marshal(rep)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.props.AddReview(s.t.Context(), s.principal("alice@example.com"), s.proposal, []byte("# Report\n"), data); err != nil {
		s.t.Fatal(err)
	}
}

func TestReportSourcesModeAndContext(t *testing.T) {
	s := newUISite(t)
	answer := review.Outcome{Answer: review.Answer{Text: "ok", Duration: time.Second}}
	s.addReportOf(review.Report{
		Tool: "copilot", Package: "lime/planner", Runs: 1, Mode: review.ModeSafe,
		Current:   &review.Version{Version: "2.3.0", Source: review.SourceInstalled},
		Candidate: review.Version{Version: "2.4.0", Source: review.SourceProposal, ProposalID: s.proposal},
		Context:   []review.ContextPackage{{ID: "lime/runbooks", Version: "1.0.0", Kind: "kb"}, {ID: "acme/python-standards", Version: "4.1.0", Kind: "agent"}},
		Notes:     []string{"Copilot CLI refuses every tool call " + xss},
		Prompts:   []review.PromptResult{{Prompt: "Plan INFOTM-1", Runs: []review.RunPair{{Current: &answer, Candidate: answer}}}},
	})
	dan := s.as("dan@example.com")
	p := dan.get("/t/lime/reviews/" + s.proposal)
	wantIn(t, p, "new report", "Installed · 2.3.0", "Proposal · 2.4.0", "Copilot CLI · default model · 1 run each · Safe mode",
		`<div class="report-notes" role="note"><p>Copilot CLI refuses every tool call &lt;script&gt;`,
		"Beside each side: 2 team packages", `<span class="mono">lime/runbooks</span> 1.0.0 <span class="pill pill-kind">KB</span>`)
	wantOut(t, p, "new report", "Current · ", "Candidate · ", xss)
	// The report asked only the first prompt; the second is marked.
	if n := strings.Count(p.body, "Not in this report"); n != 1 {
		t.Errorf("%d prompts marked as not in the report, want 1", n)
	}

	// A new report without team packages says so.
	s.addReportOf(review.Report{
		Tool: "claude", Package: "lime/planner", Runs: 1, Mode: review.ModeFull,
		Current:   &review.Version{Version: "2.3.0", Source: review.SourceRelease},
		Candidate: review.Version{Version: "2.4.0", Source: review.SourceProposal},
		Prompts:   []review.PromptResult{{Prompt: "Plan INFOTM-1", Runs: []review.RunPair{{Current: &answer, Candidate: answer}}}},
	})
	p = dan.get("/t/lime/reviews/" + s.proposal + "?report=2")
	wantIn(t, p, "no context", "Release · 2.3.0", "· Full access", "No team packages beside it.")
	wantOut(t, p, "no context", "report-notes", "Beside each side")
}

func TestActivityReportLinksKeepTheView(t *testing.T) {
	s := newUISite(t)
	s.addReport(true)
	s.addReport(true)
	base := "/t/lime/reviews/" + s.proposal
	alice := s.as("alice@example.com")
	p := alice.get(base + "?report=2&prompt=2&full=1")
	wantIn(t, p, "full view", `<a href="?report=1&amp;prompt=2&amp;run=1&amp;full=1">attached a comparison</a>`,
		`<a href="?report=2&amp;prompt=2&amp;run=1&amp;full=1">attached a comparison</a>`)
	p = alice.get(base + "?report=1&prompt=2&run=2")
	wantIn(t, p, "run 2", `<a href="?report=2&amp;prompt=2&amp;run=2">attached a comparison</a>`)
}

func TestApprovedProposalShowsTheVersionItReplaced(t *testing.T) {
	s := newUISite(t)
	base := "/t/lime/reviews/" + s.proposal
	alice := s.as("alice@example.com")
	if p := alice.post(base+"/approve", url.Values{}); p.status != http.StatusSeeOther {
		t.Fatalf("approve = %d\n%s", p.status, p.body)
	}
	p := alice.get(base)
	wantIn(t, p, "released", `<span class="arrow">2.3.0 → 2.4.0</span>`, "Name every file.")
	wantOut(t, p, "released", "2.4.0 → 2.4.0", "The agent itself is unchanged", "review-cmd")
	wantIn(t, alice.get(base+"/diff"), "diff", "Keep plans short.", "Name every file.")
	l := alice.get("/t/lime/reviews?status=all")
	wantIn(t, l, "list", "2.3.0 → 2.4.0")
	wantOut(t, l, "list", "2.4.0 → 2.4.0")
}

func TestReviewsListCountsReports(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	wantIn(t, alice.get("/t/lime/reviews"), "no report", "None yet")
	s.addReport(true)
	p := alice.get("/t/lime/reviews")
	wantIn(t, p, "one report", "1 report")
	wantOut(t, p, "one report", "None yet")
}

func TestTryItNamesTheProposal(t *testing.T) {
	s := newUISite(t)
	first := "/t/lime/reviews/" + s.proposal
	newer := s.ownProposal() // planner 2.5.0, proposed later
	b := s.as("dan@example.com")
	wantIn(t, b.get(newer), "newest", `id="review-cmd">loadout review lime/planner</code>`)
	wantIn(t, b.get(first), "older", `id="review-cmd">loadout review lime/planner --to `+s.proposal+`</code>`,
		"Compares the version you have installed with this proposal")
}
