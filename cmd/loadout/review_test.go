package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/review"
)

// fakeAI stands in for the reviewer's AI tool. It answers with the agent's
// rendered prompt, so the report shows which version answered, and never
// starts a process: tests must not call real AI tools.
type fakeAI struct {
	mu   sync.Mutex
	runs []review.Request
	// probe, if set, is a path inside the run folder; found records for
	// each run whether it was there, since the folder is gone afterwards.
	probe string
	found []bool
}

func (f *fakeAI) Version(context.Context) (string, error) { return "0.0.0-fake", nil }

func (f *fakeAI) Run(_ context.Context, req review.Request) (review.Answer, error) {
	f.mu.Lock()
	f.runs = append(f.runs, req)
	if f.probe != "" {
		_, err := os.Stat(filepath.Join(req.Dir, f.probe))
		f.found = append(f.found, err == nil)
	}
	f.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(req.Dir, ".claude", "agents", req.Agent+".md"))
	if err != nil {
		return review.Answer{}, err
	}
	return review.Answer{Text: "answer to " + req.Prompt + " from:\n" + string(data), Duration: time.Second, InputTokens: 7, OutputTokens: 3}, nil
}

// fakeCopilot is fakeAI with the real Copilot runner's caveats, so the
// notice before a run can be tested without starting Copilot.
type fakeCopilot struct{ *fakeAI }

func (fakeCopilot) Caveat(m review.Mode) string {
	return review.NewCopilotCLI("copilot", nil).Caveat(m)
}

// reviewer runs loadout with ai in place of the real AI tools.
func (p *person) reviewer(ai *fakeAI, args ...string) (string, string, int) {
	p.t.Helper()
	var out, errOut bytes.Buffer
	env := map[string]string{"HOME": p.home}
	code := runWith(p.t.Context(), args, strings.NewReader(""), &out, &errOut, func(k string) string { return env[k] }, p.wd,
		externals{runners: map[string]review.Runner{"claude": ai, "copilot": fakeCopilot{ai}}})
	return out.String(), errOut.String(), code
}

func createLabeledToken(t *testing.T, admin *person, scope, label string) string {
	t.Helper()
	ensureTeam(admin, "lime")
	out := admin.must("", "token", "create", "--team", "lime", "--"+scope, "--label", label)
	tok := tokenRE.FindString(out)
	if tok == "" {
		t.Fatalf("no token in output: %s", out)
	}
	return tok
}

var proposalIDRE = regexp.MustCompile(` as ([a-z0-9]+) `)

func propose(t *testing.T, p *person, args ...string) string {
	t.Helper()
	out := p.must("", append([]string{"propose", "planner"}, args...)...)
	m := proposalIDRE.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no proposal id in: %s", out)
	}
	return m[1]
}

// team sets up a registry with an author (Alice, publish), a reviewer (Bob,
// publish) and a reader (Carol), and publishes lime/planner 0.1.0.
func team(t *testing.T) (alice, bob, carol *person) {
	t.Helper()
	alice = &person{session: newSession(t)}
	bob = &person{session: newSession(t)}
	carol = &person{session: newSession(t)}
	url := startServer(t, alice)
	alice.must(createLabeledToken(t, alice, "publish", "alice@example.com via loadout login")+"\n", "remote", "add", "office", url, "--token-stdin")
	bob.must(createLabeledToken(t, alice, "publish", "bob")+"\n", "remote", "add", "office", url, "--token-stdin")
	carol.must(createLabeledToken(t, alice, "read", "carol")+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")
	return alice, bob, carol
}

func editAgent(t *testing.T, p *person, line string) {
	t.Helper()
	agentFile := filepath.Join(p.wd, "planner", "agent.md")
	data, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentFile, append(data, []byte(line+"\n")...), 0o644); err != nil { //nolint:gosec // inside the test temp dir
		t.Fatal(err)
	}
}

func TestProposeReviewApprove(t *testing.T) {
	alice, bob, carol := team(t)
	carol.must("", "install", "lime/planner", "--policy", "auto")
	carolAgent := filepath.Join(carol.home, ".claude", "agents", "planner.md")

	// Alice proposes 0.2.0 with the two prompts she tried.
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--note", "plans now cite tickets", "--prompt", "Plan INFOTM-1", "--prompt", "Plan a DB migration")

	// She cannot approve her own proposal.
	if _, errOut, code := alice.runIn("", "review", "approve", id); code == 0 || !strings.Contains(errOut, "cannot approve their own") {
		t.Errorf("author approve: exit %d, stderr %q; want refused", code, errOut)
	}
	// A proposal is not a release: Carol's sync does not see it.
	carol.must("", "sync")
	if data, _ := os.ReadFile(carolAgent); !strings.Contains(string(data), "@0.1.0") {
		t.Errorf("sync installed a proposal:\n%s", data)
	}

	// Bob lists and inspects it.
	if out := bob.must("", "review", "list"); !strings.Contains(out, id) || !strings.Contains(out, "0.2.0") || !strings.Contains(out, "alice") {
		t.Errorf("review list = %q", out)
	}
	out := bob.must("", "review", "show", id)
	for _, want := range []string{"plans now cite tickets", "1. Plan INFOTM-1", "2. Plan a DB migration", "+Always cite the ticket.", "-version: 0.1.0", "+version: 0.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("review show is missing %q:\n%s", want, out)
		}
	}

	// Bob compares both versions in his (fake) AI tool and attaches the report.
	ai := &fakeAI{}
	out, errOut, code := bob.reviewer(ai, "review", "compare", id, "--upload")
	if code != 0 {
		t.Fatalf("review compare: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "2 prompt(s) x 1 run(s) x 2 version(s) = 4 claude run(s)") || !strings.Contains(out, "attached as review 1") {
		t.Errorf("review compare output = %q", out)
	}
	if len(ai.runs) != 4 || ai.runs[0].Agent == ai.runs[1].Agent || ai.runs[0].Mode != review.ModeFull {
		t.Errorf("fake AI runs = %+v, want 4 in full mode with distinct agent names per version", ai.runs)
	}
	if !strings.Contains(errOut, "warning: running proposal "+id+" by alice@example.com with full access") || strings.Contains(errOut, "via loadout login") {
		t.Errorf("review compare stderr = %q, want the full-access warning naming alice@example.com", errOut)
	}
	report, err := os.ReadFile(filepath.Join(bob.wd, "review-"+id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "Always cite the ticket.") || !strings.Contains(string(report), "### Release 0.1.0, run 1") {
		t.Errorf("report does not show both versions:\n%s", report)
	}
	if out := carol.must("", "review", "report", id); !strings.Contains(out, "# Review of lime/planner 0.2.0") {
		t.Errorf("review report = %q", out)
	}

	// Bob approves; Carol's next sync installs 0.2.0.
	if out := bob.must("", "review", "approve", id, "--note", "nice"); !strings.Contains(out, "lime/planner@0.2.0 is released") {
		t.Errorf("review approve = %q", out)
	}
	if out := carol.must("", "sync"); !strings.Contains(out, "updated") {
		t.Errorf("sync after approve = %q, want updated", out)
	}
	if data, _ := os.ReadFile(carolAgent); !strings.Contains(string(data), "@0.2.0") || !strings.Contains(string(data), "Always cite the ticket.") {
		t.Errorf("Carol's agent after sync:\n%s", data)
	}
	if out := carol.must("", "review", "list"); !strings.Contains(out, "no open proposals") {
		t.Errorf("review list after approve = %q", out)
	}
	if out := carol.must("", "review", "show", id); !strings.Contains(out, "Approved by bob") {
		t.Errorf("review show after approve = %q", out)
	}
}

func TestRejectWithdrawAndStaleApproval(t *testing.T) {
	alice, bob, carol := team(t)

	// Bob rejects a proposal; Alice's own reject is refused.
	editAgent(t, alice, "Be brief.")
	id := propose(t, alice, "--bump", "patch", "--prompt", "Plan X")
	if _, errOut, code := alice.runIn("", "review", "reject", id); code == 0 || !strings.Contains(errOut, "forbidden") {
		t.Errorf("author reject: exit %d, stderr %q", code, errOut)
	}
	if out := bob.must("", "review", "reject", id, "--note", "too terse"); !strings.Contains(out, "rejected "+id) {
		t.Errorf("review reject = %q", out)
	}
	if out := bob.must("", "review", "list", "--all"); !strings.Contains(out, "rejected") {
		t.Errorf("review list --all = %q", out)
	}

	// Only the author withdraws; a reader token may propose.
	carol.must("", "new", "agent", "lime/helper")
	pf := filepath.Join(carol.wd, "prompts.md")
	if err := os.WriteFile(pf, []byte("First prompt\nwith two lines\n---\nSecond prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := carol.must("", "propose", "helper", "--prompts-file", pf)
	if !strings.Contains(out, "proposed lime/helper@0.1.0") || !strings.Contains(out, "with 2 prompt(s)") {
		t.Fatalf("propose from a read token = %q", out)
	}
	helperID := proposalIDRE.FindStringSubmatch(out)[1]
	if _, errOut, code := bob.runIn("", "review", "withdraw", helperID); code == 0 || !strings.Contains(errOut, "only the author") {
		t.Errorf("withdraw by a non-author: exit %d, stderr %q", code, errOut)
	}
	if out := carol.must("", "review", "withdraw", helperID); !strings.Contains(out, "withdrawn "+helperID) {
		t.Errorf("review withdraw = %q", out)
	}

	// A release published after proposing makes the approval fail cleanly.
	id = propose(t, alice, "--prompt", "Plan Y") // 0.1.1 again: the rejected one is closed
	alice.must("", "publish", "planner")         // Alice publishes 0.1.1 directly
	if _, errOut, code := bob.runIn("", "review", "approve", id); code == 0 || !strings.Contains(errOut, "version_conflict") || !strings.Contains(errOut, "proposes again") {
		t.Errorf("stale approve: exit %d, stderr %q; want a version conflict with a hint", code, errOut)
	}
}

// A prompt a reviewer adds on the registry is what the next review asks,
// and one they remove is not: compare always takes the current list.
func TestReviewRunsTheCurrentPrompts(t *testing.T) {
	alice := &person{session: newSession(t)}
	carol := &person{session: newSession(t)}
	url := startServer(t, alice)
	alice.must(createLabeledToken(t, alice, "publish", "alice@example.com via loadout login")+"\n", "remote", "add", "office", url, "--token-stdin")
	bob, err := client.New(url, createLabeledToken(t, alice, "read", "bob"))
	if err != nil {
		t.Fatal(err)
	}
	carol.must(createLabeledToken(t, alice, "read", "carol")+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan INFOTM-1")

	added, err := bob.AddPrompt(t.Context(), id, "Plan a rollback of "+id)
	if err != nil {
		t.Fatal(err)
	}
	asked := func() []string {
		t.Helper()
		ai := &fakeAI{}
		if out, errOut, code := carol.reviewer(ai, "review", "lime/planner", "--no-upload"); code != 0 {
			t.Fatalf("review lime/planner: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
		}
		var got []string
		for _, r := range ai.runs {
			if !slices.Contains(got, r.Prompt) {
				got = append(got, r.Prompt)
			}
		}
		return got
	}
	if got, want := asked(), []string{"Plan INFOTM-1", "Plan a rollback of " + id}; !slices.Equal(got, want) {
		t.Errorf("prompts run after Bob added one = %q, want %q", got, want)
	}
	if _, err := bob.EditPrompt(t.Context(), id, added.Seq, "Plan a rollback, step by step"); err != nil {
		t.Fatal(err)
	}
	if got, want := asked(), []string{"Plan INFOTM-1", "Plan a rollback, step by step"}; !slices.Equal(got, want) {
		t.Errorf("prompts run after Bob edited his = %q, want %q", got, want)
	}
	if err := bob.RemovePrompt(t.Context(), id, added.Seq); err != nil {
		t.Fatal(err)
	}
	if got, want := asked(), []string{"Plan INFOTM-1"}; !slices.Equal(got, want) {
		t.Errorf("prompts run after Bob removed his = %q, want %q", got, want)
	}
}

func TestReviewPackage(t *testing.T) {
	alice, bob, carol := team(t)
	alice.must("", "new", "kb", "lime/runbooks")
	writeFile(t, filepath.Join(alice.wd, "runbooks", "deploy.md"), "# Deploying\n\nRun make deploy.\n")
	alice.must("", "publish", "runbooks")
	carol.must("", "install", "lime/planner")               // 0.1.0
	alice.must("", "publish", "planner", "--bump", "patch") // 0.1.1, the latest release
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan INFOTM-1")

	// Carol reviews with one command: her installed 0.1.0 against the open
	// proposal, the team's KB beside each, in full mode, uploaded.
	ai := &fakeAI{probe: filepath.Join(".claude", "skills", "runbooks", "SKILL.md")}
	out, errOut, code := carol.reviewer(ai, "review", "lime/planner")
	if code != 0 {
		t.Fatalf("review lime/planner: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, want := range []string{
		"to:     proposal " + id + " (0.2.0) by alice@example.com",
		"from:   installed 0.1.0",
		"beside: 1 team package(s): lime/runbooks 0.1.0",
		"the report will be attached to proposal " + id + ", where everyone who reads lime on office can see it; add --no-upload",
		"1 prompt(s) x 1 run(s) x 2 version(s) = 2 claude run(s) on your own subscription, full access",
		"attached as review 1 of proposal " + id,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("review output is missing %q:\n%s", want, out)
		}
	}
	if want := "warning: running proposal " + id + " by alice@example.com with full access to this machine (files, shell, web); add --safe"; !strings.Contains(errOut, want) {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
	if len(ai.runs) != 2 || ai.runs[0].Mode != review.ModeFull || ai.runs[0].Prompt != "Plan INFOTM-1" {
		t.Errorf("fake AI runs = %+v, want 2 full-mode runs of the proposal's prompt", ai.runs)
	}
	if len(ai.found) != 2 || !ai.found[0] || !ai.found[1] {
		t.Errorf("the team's KB beside each side: %v, want it in both folders", ai.found)
	}
	report := readFile(t, filepath.Join(carol.wd, "review-"+id+".md"))
	for _, want := range []string{"Installed 0.1.0", "Proposal " + id + " (0.2.0)", "lime/runbooks", "Mode: full access"} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
	if out := bob.must("", "review", "report", id); !strings.Contains(out, "Installed 0.1.0") {
		t.Errorf("uploaded report = %q", out)
	}

	// --safe, explicit sides and --no-upload: no warning, nothing attached.
	ai = &fakeAI{}
	out, errOut, code = carol.reviewer(ai, "review", "lime/planner", "--safe", "--from", "latest", "--to", "0.2.0", "--no-upload", "--out", "safe.md")
	if code != 0 || strings.Contains(errOut, "warning:") || strings.Contains(out, "attached") || !strings.Contains(out, "from:   release 0.1.1") {
		t.Errorf("--safe --no-upload: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if len(ai.runs) == 0 || ai.runs[0].Mode != review.ModeSafe {
		t.Errorf("--safe runs = %+v, want safe mode", ai.runs)
	}
	if !exists(filepath.Join(carol.wd, "safe.md")) {
		t.Error("--out safe.md was not written")
	}

	// --no-context leaves the KB out.
	ai = &fakeAI{probe: filepath.Join(".claude", "skills", "runbooks", "SKILL.md")}
	out, _, code = carol.reviewer(ai, "review", "lime/planner", "--no-context", "--no-upload")
	if code != 0 || !strings.Contains(out, "beside: no team packages (--no-context)") || len(ai.found) == 0 || ai.found[0] {
		t.Errorf("--no-context: exit %d, found %v\n%s", code, ai.found, out)
	}

	// Copilot in safe mode gets the caveat before the run.
	_, errOut, _ = carol.reviewer(&fakeAI{}, "review", "lime/planner", "--safe", "--tool", "copilot", "--no-upload")
	if !strings.Contains(errOut, "notice: Copilot CLI refuses every tool call in safe mode") {
		t.Errorf("copilot --safe stderr = %q, want the caveat", errOut)
	}

	// Two releases: the prompts come from --prompt, the warning names the
	// package, and the report stays local with the reason.
	if _, errOut, code := carol.reviewer(&fakeAI{}, "review", "lime/planner", "--from", "0.1.0", "--to", "latest"); code == 0 || !strings.Contains(errOut, "0.1.1 is a release and has no prompts; give them with --prompt") {
		t.Errorf("release without prompts: exit %d, stderr %q", code, errOut)
	}
	out, errOut, code = carol.reviewer(&fakeAI{}, "review", "lime/planner", "--from", "0.1.0", "--to", "latest", "--prompt", "Plan X")
	if code != 0 || !strings.Contains(errOut, "warning: running lime/planner with full access") ||
		!strings.Contains(out, "note: the report stays on this machine: lime/planner 0.1.1 is a release") ||
		!exists(filepath.Join(carol.wd, "review-planner-0.1.1.md")) {
		t.Errorf("two releases: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}

	// Subcommands keep their meaning; a word without a slash is no package.
	if out := carol.must("", "review", "list"); !strings.Contains(out, id) {
		t.Errorf("review list = %q", out)
	}
	if _, errOut, code := carol.runIn("", "review", "lst"); code == 0 || !strings.Contains(errOut, `unknown command or package "lst"`) {
		t.Errorf("review lst: exit %d, stderr %q", code, errOut)
	}
	if out := carol.must("", "review"); !strings.Contains(out, "loadout review payments/planner") {
		t.Errorf("review without arguments = %q, want help", out)
	}

	// compare --safe runs safe and keeps the report local by default.
	ai = &fakeAI{}
	out, errOut, code = carol.reviewer(ai, "review", "compare", id, "--safe")
	if code != 0 || strings.Contains(errOut, "warning:") || strings.Contains(out, "attached") || len(ai.runs) == 0 || ai.runs[0].Mode != review.ModeSafe {
		t.Errorf("compare --safe: exit %d, runs %+v\nstdout: %s\nstderr: %s", code, ai.runs, out, errOut)
	}
}
