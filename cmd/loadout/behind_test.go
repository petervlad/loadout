package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A proposal made from 0.1.0 falls behind when Bob publishes 0.1.1: the
// review commands say so, name Bob, and show what Alice changed rather than
// what she would undo. When Bob then releases the proposed version itself,
// the proposal is blocked.
func TestReviewSaysAProposalIsBehind(t *testing.T) {
	alice, bob, carol := team(t)
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan INFOTM-1") // 0.2.0 from 0.1.0

	// Not behind yet: the list says only "open".
	if out := bob.must("", "review", "list"); strings.Contains(out, "behind") {
		t.Errorf("review list before any newer release = %q", out)
	}

	// Bob, from his own folder, publishes 0.1.1 with a change of his own.
	bob.must("", "new", "agent", "lime/planner")
	editAgent(t, bob, "Answer in English.")
	bob.must("", "publish", "planner", "--bump", "patch")

	if out := carol.must("", "review", "list"); !strings.Contains(out, "open, behind 0.1.1") {
		t.Errorf("review list = %q, want the proposal open, behind 0.1.1", out)
	}
	out := carol.must("", "review", "show", id)
	for _, want := range []string{
		"Made from 0.1.0, proposes 0.2.0. The latest release is 0.1.1 by bob, released after it: approving releases 0.2.0 without 0.1.1's changes.",
		"What the author changed since 0.1.0:",
		"+Always cite the ticket.",
		"What was released since: loadout diff lime/planner 0.1.0 0.1.1",
		"The author brings it up to date with: loadout rebase --proposal " + id,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("review show is missing %q:\n%s", want, out)
		}
	}
	// The diff starts from the base, so Bob's line is not shown as removed.
	if strings.Contains(out, "Answer in English") {
		t.Errorf("review show diffs against the newer release:\n%s", out)
	}

	// review PKG says the same before it runs.
	ai := &fakeAI{}
	runOut, errOut, code := carol.reviewer(ai, "review", "lime/planner", "--no-upload", "--out", filepath.Join(carol.wd, "r.md"))
	if code != 0 {
		t.Fatalf("review lime/planner: exit %d\nstdout: %s\nstderr: %s", code, runOut, errOut)
	}
	if want := "Proposal " + id + " is based on 0.1.0; 0.1.1 was released since, so approving it would release 0.2.0 without 0.1.1's changes. The report compares it with the release 0.1.1."; !strings.Contains(runOut+errOut, want) {
		t.Errorf("review lime/planner is missing the note %q:\nstdout: %s\nstderr: %s", want, runOut, errOut)
	}

	// Bob releases 0.2.0 himself: the proposal can no longer be approved as 0.2.0.
	bob.must("", "publish", "planner", "--bump", "minor")
	if out := carol.must("", "review", "list"); !strings.Contains(out, "open, blocked by 0.2.0") {
		t.Errorf("review list = %q, want the proposal blocked by 0.2.0", out)
	}
	if out := carol.must("", "review", "show", id); !strings.Contains(out, "0.2.0 by bob was released after it, so it cannot be approved as 0.2.0") {
		t.Errorf("review show of a blocked proposal:\n%s", out)
	}
}

// Bob cannot approve Alice's proposal as it is once he released 0.1.1
// after it was made: he names 0.1.1 to release it over his change.
func TestApproveOverANewerRelease(t *testing.T) {
	alice, bob, carol := team(t)
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan INFOTM-1") // 0.2.0 from 0.1.0
	bob.must("", "new", "agent", "lime/planner")
	editAgent(t, bob, "Answer in English.")
	bob.must("", "publish", "planner", "--bump", "patch") // 0.1.1

	_, errOut, code := bob.runIn("", "review", "approve", id)
	for _, want := range []string{
		"proposal " + id + " is based on 0.1.0 and 0.1.1 was released since, so approving it releases 0.2.0 without 0.1.1's changes.",
		"The author can bring it up to date with loadout rebase --proposal " + id + ".",
		"To release it as it is, approve it over 0.1.1 with loadout review approve " + id + " --over 0.1.1 (update loadout if it does not know --over).",
	} {
		if code == 0 || !strings.Contains(errOut, want) {
			t.Errorf("plain approve of a behind proposal: exit %d, stderr %q; want it refused with %q", code, errOut, want)
		}
	}
	if strings.Contains(errOut, "registry: 409") {
		t.Errorf("the refusal carries the HTTP status: %q", errOut)
	}
	if _, errOut, code := bob.runIn("", "review", "approve", id, "--over", "0.1.0"); code == 0 || !strings.Contains(errOut, "You asked to approve it over 0.1.0, but the latest release is 0.1.1.") {
		t.Errorf("approve --over 0.1.0: exit %d, stderr %q; want refused naming 0.1.1", code, errOut)
	}

	out := bob.must("", "review", "approve", id, "--over", "0.1.1", "--note", "0.1.1 was a stopgap")
	if want := "approved " + id + " over 0.1.1: lime/planner@0.2.0 is released on office without 0.1.1's changes"; !strings.Contains(out, want) {
		t.Errorf("approve --over 0.1.1 = %q, want %q", out, want)
	}
	show := carol.must("", "review", "show", id)
	if !strings.Contains(show, "Approved over 0.1.1: released without 0.1.1's changes") || strings.Contains(show, "behind") {
		t.Errorf("review show of a proposal approved over 0.1.1:\n%s", show)
	}
	if list := carol.must("", "review", "list", "--all"); strings.Contains(list, "behind") {
		t.Errorf("review list --all says an approved proposal is behind:\n%s", list)
	}
}
