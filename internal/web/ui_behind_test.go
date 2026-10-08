package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/registry"
)

// release stores a release of lime/planner as published by by (the zero
// Actor for a release from before publishers were kept), with the site's
// description, so only the body and the version differ.
func (s *uiSite) release(version string, by registry.Actor, body string) {
	s.t.Helper()
	data := bundleOf(s.t, "lime", "planner", version, "Plans work "+xss, body)
	p, err := bundle.Decode(data)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.mem.PutRelease(s.t.Context(), registry.Release{
		ID: p.ID(), Version: p.Version(), Digest: p.Digest(), Description: p.Manifest().Description,
		Kind: p.Kind(), Size: len(data), PublishedAt: time.Now().Add(-2 * time.Hour), PublishedBy: by,
	}, data); err != nil {
		s.t.Fatal(err)
	}
}

func TestProposalNotBehind(t *testing.T) {
	s := newUISite(t)
	alice := s.as("alice@example.com")
	base := "/t/lime/reviews/" + s.proposal
	p := alice.get(base)
	wantIn(t, p, "proposal", "2.3.0 → 2.4.0")
	wantOut(t, p, "proposal", "behind-note", "Behind ")
	wantOut(t, alice.get("/t/lime/reviews"), "reviews", "Behind ")
	d := alice.get(base + "/diff?view=since")
	wantIn(t, d, "diff", "Changes from 2.3.0 to 2.4.0", "Name every file.")
	wantOut(t, d, "diff", "diff-tabs", "Released since")

	// Once approved, the proposal is its own latest release: still from
	// its recorded base, and never behind.
	if p := alice.post(base+"/approve", url.Values{}); p.status != http.StatusSeeOther {
		t.Fatalf("approve = %d\n%s", p.status, p.body)
	}
	p = alice.get(base)
	wantIn(t, p, "approved", "2.3.0 → 2.4.0", "Name every file.")
	wantOut(t, p, "approved", "Behind ", "behind-note")
}

// The site's proposal (2.4.0 from 2.3.0) falls behind: 2.3.1 by Alice,
// then 2.3.2 by nobody known, which only changes the version.
func TestProposalBehind(t *testing.T) {
	s := newUISite(t)
	s.release("2.3.1", registry.Actor{TokenID: "t1", Label: "alice@example.com via loadout login", UserID: s.userID("alice@example.com")}, "Keep plans short. Check twice.\n")
	s.release("2.3.2", registry.Actor{}, "Keep plans short. Check twice.\n")
	base := "/t/lime/reviews/" + s.proposal

	alice := s.as("alice@example.com")
	p := alice.get(base)
	if p.status != http.StatusOK {
		t.Fatalf("proposal = %d\n%s", p.status, p.body)
	}
	wantIn(t, p, "reviewer",
		"2.3.0 → 2.4.0", `<span class="pill pill-warn">Behind 2.3.2</span>`,
		`role="note" aria-labelledby="behind-h"`, "2 newer releases came out after this proposal",
		"<strong>2.3.2</strong>, <time", "; <strong>2.3.1</strong> by alice@example.com, <time",
		"It was made from 2.3.0, so approving releases 2.4.0 without their changes to 1 file: "+`<span class="mono">agent.md</span>.`,
		"This proposal changes it too.", "The author can bring it up to date.",
		base+"/diff?view=since\">What changed since 2.3.0</a>", base+"/diff?view=effect\">What approving does</a>",
		`action="/t/lime/reviews/`+s.proposal+`/approve"`)
	wantOut(t, p, "reviewer", xss, "loadout rebase")
	// The notice comes before the actions it explains.
	if strings.Index(p.body, "behind-note") > strings.Index(p.body, `class="actions"`) {
		t.Error("the notice comes after the actions")
	}
	wantIn(t, s.as("bob@example.com").get(base), "author", "Bring it up to date on your machine",
		`<code class="cmd" id="rebase-cmd">loadout rebase --proposal `+s.proposal+`</code>`, `data-copy="rebase-cmd"`, "it replaces this proposal and keeps its prompts")

	list := alice.get("/t/lime/reviews")
	wantIn(t, list, "reviews", "2.3.0 → 2.4.0", `<span class="pill pill-warn behind-pill">Behind 2.3.2</span>`)

	for _, tc := range []struct {
		query, current string
		want           []string
		unwanted       []string
	}{
		{"", "This proposal", []string{"Changes from 2.3.0 to 2.4.0", "Name every file.", "What the author changed"}, []string{"Check twice"}},
		{"?view=nonsense", "This proposal", []string{"Changes from 2.3.0 to 2.4.0"}, nil},
		{"?view=since", "Released since", []string{"Changes from 2.3.0 to 2.3.2", "Check twice", "does not have these changes"}, []string{"Name every file."}},
		{"?view=effect", "After approval", []string{"Changes from 2.3.2 to 2.4.0", "Check twice", "Name every file.", "next sync"}, nil},
	} {
		d := alice.get(base + "/diff" + tc.query)
		if d.status != http.StatusOK {
			t.Fatalf("diff%s = %d\n%s", tc.query, d.status, d.body)
		}
		wantIn(t, d, "diff"+tc.query, append(tc.want, `aria-current="page">`+tc.current+`</a>`,
			`<a href="?view=changes"`, `<a href="?view=since"`, `<a href="?view=effect"`)...)
		wantOut(t, d, "diff"+tc.query, append(tc.unwanted, xss)...)
	}
}

// More than five releases since: the newest five, then "and N more".
// A publisher's label is escaped.
func TestProposalBehindManyReleases(t *testing.T) {
	s := newUISite(t)
	for _, v := range []string{"2.3.1", "2.3.2", "2.3.3", "2.3.4", "2.3.5", "2.3.6", "2.3.7"} {
		s.release(v, registry.Actor{TokenID: "t9", Label: "ci " + xss}, "Keep plans short.\nRelease "+v+".\n")
	}
	p := s.as("alice@example.com").get("/t/lime/reviews/" + s.proposal)
	wantIn(t, p, "many", "7 newer releases came out after this proposal", "<strong>2.3.7</strong> by ci &lt;script&gt;",
		"<strong>2.3.3</strong>", "; and 2 more.", "Behind 2.3.7")
	wantOut(t, p, "many", xss, "<strong>2.3.2</strong>", "<strong>2.3.1</strong>")
}

// A release at the proposed version blocks it: Approve is off with the
// reason, Reject and Withdraw stay.
func TestProposalBlocked(t *testing.T) {
	s := newUISite(t)
	s.release("2.4.0", registry.Actor{}, "Keep plans short. Released first.\n")
	base := "/t/lime/reviews/" + s.proposal

	p := s.as("alice@example.com").get(base)
	wantIn(t, p, "maintainer", "2.4.0 was released after this proposal", "Behind 2.4.0",
		"cannot be approved as a version that is already released", "The author can bring it up to date and propose it again.",
		`<summary class="btn btn-lg btn-danger">Reject…</summary>`,
		`disabled aria-describedby="why">Approve and release`, `id="why">Approve is off: 2.4.0 is already released.`,
		"What 2.4.0 changed")
	wantOut(t, p, "maintainer", `action="/t/lime/reviews/`+s.proposal+`/approve"`, "What approving does")

	p = s.as("bob@example.com").get(base)
	wantIn(t, p, "author", "Withdraw…", "Bring it up to date with <code>loadout rebase --proposal "+s.proposal+"</code>, which raises its version", `disabled aria-describedby="why">Approve and release`)

	p = s.as("dan@example.com").get(base)
	wantIn(t, p, "member", `disabled aria-describedby="why">Reject…`, "Approve is off")
}

// Approving a behind proposal needs over, naming the latest release; a
// plain approve, or over an older release, gets the refusal page and
// leaves it open. The popover that sends over is the UI step's.
func TestApproveBehindNeedsOver(t *testing.T) {
	s := newUISite(t)
	s.release("2.3.1", registry.Actor{}, "Keep plans short. Check twice.\n")
	s.release("2.3.2", registry.Actor{}, "Keep plans short. Check twice.\n")
	base := "/t/lime/reviews/" + s.proposal
	alice := s.as("alice@example.com")

	for _, tc := range []struct {
		over string
		want []string
	}{
		{"", []string{"Something changed", "2.3.2 was released since", "without 2.3.2&#39;s changes"}},
		{"2.3.1", []string{"Something changed", "You asked to approve it over 2.3.1, but the latest release is 2.3.2."}},
	} {
		p := alice.post(base+"/approve", url.Values{"over": {tc.over}})
		if p.status != http.StatusConflict {
			t.Fatalf("approve over %q = %d, want 409\n%s", tc.over, p.status, p.body)
		}
		wantIn(t, p, "approve over "+tc.over, tc.want...)
		wantOut(t, p, "approve over "+tc.over, "Behind the latest release:", "proposal is behind the latest release")
	}
	wantIn(t, alice.get(base), "still open", "Behind 2.3.2")

	if p := alice.post(base+"/approve", url.Values{"over": {"2.3.2"}}); p.status != http.StatusSeeOther {
		t.Fatalf("approve over 2.3.2 = %d\n%s", p.status, p.body)
	}
	got, err := s.mem.Proposal(t.Context(), s.proposal)
	if err != nil || got.Status != registry.ProposalApproved || got.ApprovedOver.String() != "2.3.2" {
		t.Errorf("after approving over 2.3.2: %+v, %v; want approved over 2.3.2", got, err)
	}
}

// On a behind proposal Approve asks first, in a popover that sends over;
// a release landing while the page is open gets the refusal page naming
// it, and the activity says what the proposal was approved over.
func TestApproveOverPopover(t *testing.T) {
	s := newUISite(t)
	s.release("2.3.1", registry.Actor{}, "Keep plans short. Check twice.\n")
	base := "/t/lime/reviews/" + s.proposal
	alice := s.as("alice@example.com")

	p := alice.get(base)
	wantIn(t, p, "popover", `<summary class="btn btn-lg btn-primary" aria-describedby="why">Approve over 2.3.1…</summary>`,
		`<input type="hidden" name="over" value="2.3.1">`, "2.3.1 came out after this proposal was made from 2.3.0.",
		"Approving releases 2.4.0 without 2.3.1’s changes to 1 file", base+"/diff?view=effect\">What approving does</a>",
		`<button type="submit" class="btn btn-danger">Approve over 2.3.1</button>`, "so it asks first")
	wantOut(t, p, "popover", `class="btn btn-lg btn-primary">Approve and release</button>`)

	if p := alice.post(base+"/approve", url.Values{"over": {"2.3.1"}, "csrf": {"forged"}}); p.status != http.StatusForbidden {
		t.Errorf("approve over with a bad CSRF token = %d, want 403", p.status)
	}
	// 2.3.2 lands while Alice reads the page.
	s.release("2.3.2", registry.Actor{}, "Keep plans short. Check twice.\n")
	p = alice.post(base+"/approve", url.Values{"over": {"2.3.1"}})
	if p.status != http.StatusConflict {
		t.Fatalf("stale over = %d, want 409\n%s", p.status, p.body)
	}
	wantIn(t, p, "stale over", "Something changed", "the latest release is 2.3.2", base)

	p = alice.post(base+"/approve", url.Values{"over": {"2.3.2"}, "note": {"Hotfix redone " + xss}})
	if p.status != http.StatusSeeOther || p.location != base+"?done=approved" {
		t.Fatalf("approve over 2.3.2 = %d %s\n%s", p.status, p.location, p.body)
	}
	p = alice.get(p.location)
	wantIn(t, p, "approved over", "approved and released it over 2.3.2: “Hotfix redone &lt;script&gt;", "dot-warn")
	wantOut(t, p, "approved over", xss, "Behind 2.3.2", `name="over"`)
}

// An owner's own behind proposal: one form with as_owner, the note and
// over.
func TestOwnerApprovesOwnBehindProposal(t *testing.T) {
	s := newUISite(t)
	base := s.ownProposal()
	s.release("2.3.1", registry.Actor{}, "Keep plans short. Check twice.\n")
	olga := s.as("olga@example.com")

	p := olga.get(base)
	wantIn(t, p, "owner", "Approve as owner…", `name="as_owner" value="on"`, `<input type="hidden" name="over" value="2.3.1">`,
		"Approve without review over 2.3.1", "without 2.3.1’s changes")
	p = olga.post(base+"/approve", url.Values{"as_owner": {"on"}, "note": {"Urgent"}, "over": {"2.3.1"}})
	if p.status != http.StatusSeeOther || p.location != base+"?done=approved-as-owner" {
		t.Fatalf("approve as owner over 2.3.1 = %d %s\n%s", p.status, p.location, p.body)
	}
	wantIn(t, olga.get(p.location), "released", "approved own proposal as owner, without review, over 2.3.1: “Urgent”")
}

// A proposal its author replaced after a rebase reads "Replaced" in the
// list and links to its replacement, which links back; prompts others
// added come along with their authors.
func TestReplacedProposal(t *testing.T) {
	s := newUISite(t)
	ctx := t.Context()
	if _, err := s.props.AddPrompt(ctx, s.principal("alice@example.com"), s.proposal, "Plan "+xss); err != nil {
		t.Fatal(err)
	}
	s.addReport(true)
	s.release("2.3.1", registry.Actor{}, "Keep plans short. Check twice.\n")
	p, err := s.props.Propose(ctx, s.principal("bob@example.com"), bundleOf(t, "lime", "planner", "2.4.0", "Plans work", "Keep plans short. Check twice.\nName every file.\n"),
		registry.ProposalInput{Replaces: s.proposal, Base: "2.3.1"})
	if err != nil {
		t.Fatal(err)
	}
	alice := s.as("alice@example.com")
	list := alice.get("/t/lime/reviews?status=all")
	wantIn(t, list, "reviews", "Replaced", p.ID)

	old := alice.get("/t/lime/reviews/" + s.proposal)
	wantIn(t, old, "replaced", `role="note">Replaced by <a class="mono" href="/t/lime/reviews/`+p.ID+`">`+p.ID+`</a> after a rebase onto 2.3.1.`,
		`<a href="/t/lime/reviews/`+p.ID+`">replaced it with `+p.ID+`</a>`)
	wantOut(t, old, "replaced", "replaced by "+p.ID+"”", "withdrew it", "Behind ", xss)

	cur := alice.get("/t/lime/reviews/" + p.ID)
	wantIn(t, cur, "replacement", `Replaces <a class="mono" href="/t/lime/reviews/`+s.proposal+`">`+s.proposal+`</a>; its reports compare the older content`,
		"2.3.1 → 2.4.0", "replacing "+s.proposal, "added a prompt</a> on "+s.proposal, "Plan &lt;script&gt;")
	wantOut(t, cur, "replacement", "Replaced by", "Behind ", xss)
}
