package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/client"
)

// rebaseTeam is a registry with Alice (publish, a login token's label),
// Bob (publish) and Carol (read, through the API only), where Alice
// published lime/planner 0.1.0.
func rebaseTeam(t *testing.T) (alice, bob *person, carol *client.Client) {
	t.Helper()
	alice = &person{session: newSession(t)}
	bob = &person{session: newSession(t)}
	url := startServer(t, alice)
	alice.must(createLabeledToken(t, alice, "publish", "alice@example.com via loadout login")+"\n", "remote", "add", "office", url, "--token-stdin")
	bob.must(createLabeledToken(t, alice, "publish", "bob")+"\n", "remote", "add", "office", url, "--token-stdin")
	carol, err := client.New(url, createLabeledToken(t, alice, "read", "carol"))
	if err != nil {
		t.Fatal(err)
	}
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")
	return alice, bob, carol
}

// bobReleases has Bob publish 0.1.1 from his own copy of 0.1.0, changed by
// edit.
func bobReleases(t *testing.T, bob *person, edit func(agent string) string) {
	t.Helper()
	bob.must("", "new", "agent", "lime/planner")
	path := filepath.Join(bob.wd, "planner", "agent.md")
	writeFile(t, path, edit(readFile(t, path)))
	bob.must("", "publish", "planner", "--bump", "patch")
}

func TestRebaseAProposalAndProposeAgain(t *testing.T) {
	alice, bob, carol := rebaseTeam(t)
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan TRK-1") // 0.2.0 from 0.1.0
	if _, err := carol.AddPrompt(t.Context(), id, "Plan a rollback"); err != nil {
		t.Fatal(err)
	}
	// Bob changes another line and releases 0.1.1.
	bobReleases(t, bob, func(agent string) string {
		return strings.Replace(agent, "description: What this agent does.", "description: Plans sprints.", 1)
	})
	if out := bob.must("", "review", "list"); !strings.Contains(out, "open, behind 0.1.1") {
		t.Fatalf("review list = %q", out)
	}

	out := alice.must("", "rebase", "planner")
	for _, want := range []string{
		"planner: proposal " + id + ", made from 0.1.0, onto 0.1.1",
		"merged  agent.md",
		"No conflicts; propose it with: loadout propose planner",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rebase output is missing %q:\n%s", want, out)
		}
	}
	agent := readFile(t, filepath.Join(alice.wd, "planner", "agent.md"))
	if !strings.Contains(agent, "Plans sprints.") || !strings.Contains(agent, "Always cite the ticket.") {
		t.Fatalf("rebased agent.md lacks a side:\n%s", agent)
	}

	// Proposing again replaces the old proposal; Carol's prompt comes along.
	out = alice.must("", "propose", "planner")
	newID := proposalIDRE.FindStringSubmatch(out)[1]
	for _, want := range []string{"proposed lime/planner@0.2.0", "with 2 prompt(s)", "replaces " + id + " (closed; its prompts came along)", "based on 0.1.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("propose after rebase is missing %q:\n%s", want, out)
		}
	}
	if out := bob.must("", "review", "list", "--all"); strings.Contains(out, "behind") || !strings.Contains(out, "withdrawn, replaced by "+newID) {
		t.Errorf("review list --all after the replace = %q", out)
	}
	show := bob.must("", "review", "show", newID)
	if !strings.Contains(show, "Plan a rollback") || !strings.Contains(show, "Replaces proposal "+id) {
		t.Errorf("review show of the replacement:\n%s", show)
	}

	out = bob.must("", "review", "approve", newID)
	if !strings.Contains(out, "approved "+newID+": lime/planner@0.2.0 is released") {
		t.Fatalf("approve the replacement = %q", out)
	}
	released, err := carol.Fetch(t.Context(), mustPackID(t, "lime/planner"), "latest")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(released.Files()["agent.md"]); !strings.Contains(got, "Plans sprints.") || !strings.Contains(got, "Always cite the ticket.") {
		t.Errorf("0.2.0 as released lacks a change:\n%s", got)
	}
}

func TestRebaseWithAConflict(t *testing.T) {
	alice, bob, _ := rebaseTeam(t)
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan TRK-1")
	bobReleases(t, bob, func(agent string) string { return agent + "Answer in English.\n" })

	out := alice.must("", "rebase", "planner")
	if want := "1 file has conflicts, marked with <<<<<<<: agent.md. Resolve them in your editor or AI tool, then: loadout propose planner"; !strings.Contains(out, want) {
		t.Fatalf("rebase output = %q, want %q", out, want)
	}
	path := filepath.Join(alice.wd, "planner", "agent.md")
	agent := readFile(t, path)
	for _, want := range []string{"<<<<<<< yours (proposal " + id + ")\nAlways cite the ticket.\n", "||||||| base 0.1.0\n", "=======\nAnswer in English.\n>>>>>>> release 0.1.1\n"} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent.md is missing %q:\n%s", want, agent)
		}
	}
	if _, errOut, code := alice.runIn("", "propose", "planner"); code == 0 || !strings.Contains(errOut, "1 file still has conflict markers from loadout rebase (<<<<<<<): agent.md") {
		t.Errorf("propose with markers left: exit %d, stderr %q", code, errOut)
	}

	// Alice keeps both lines.
	before, _, _ := strings.Cut(agent, "<<<<<<<")
	if err := os.WriteFile(path, []byte(before+"Always cite the ticket.\nAnswer in English.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = alice.must("", "propose", "planner")
	if !strings.Contains(out, "replaces "+id) || !strings.Contains(out, "based on 0.1.1") {
		t.Errorf("propose after resolving = %q", out)
	}
}

func TestProposeRefusesAFolderThatIsBehind(t *testing.T) {
	alice, bob, _ := rebaseTeam(t)
	bobReleases(t, bob, func(agent string) string { return agent + "Answer in English.\n" })
	editAgent(t, alice, "Always cite the ticket.")

	_, errOut, code := alice.runIn("", "propose", "planner", "--bump", "minor", "--prompt", "Plan TRK-1")
	want := "planner is based on 0.1.0, and 0.1.1 by bob was released since; its changes are not in your folder. Bring them in with: loadout rebase planner. If your folder already has them, propose with --base 0.1.1"
	if code == 0 || !strings.Contains(errOut, want) {
		t.Fatalf("propose from a folder behind: exit %d, stderr %q; want %q", code, errOut, want)
	}
	if out := bob.must("", "review", "list"); !strings.Contains(out, "no open proposals") {
		t.Errorf("a refused propose uploaded something: %q", out)
	}
	out := alice.must("", "propose", "planner", "--bump", "minor", "--prompt", "Plan TRK-1", "--base", "0.1.1")
	if !strings.Contains(out, "proposed lime/planner@0.2.0") || !strings.Contains(out, "based on 0.1.1") {
		t.Errorf("propose --base 0.1.1 = %q", out)
	}
}

// Bob's folder is still at 0.1.0 when Alice publishes 0.1.1: publish
// refuses it, naming Alice; a rebase brings her change in and publish then
// goes through; --base latest skips the check; a folder that is not behind
// publishes as before.
func TestPublishRefusesAFolderThatIsBehind(t *testing.T) {
	alice, bob, _ := rebaseTeam(t)
	bob.must("", "new", "agent", "lime/planner") // a copy of 0.1.0
	editAgent(t, alice, "Always cite the ticket.")
	if out := alice.must("", "publish", "planner", "--bump", "patch"); !strings.Contains(out, "published lime/planner@0.1.1") {
		t.Fatalf("a folder that is not behind publishes as before: %q", out)
	}

	editAgent(t, bob, "Answer in English.")
	_, errOut, code := bob.runIn("", "publish", "planner", "--bump", "patch")
	want := "planner is based on 0.1.0, and 0.1.1 by alice@example.com was released since; its changes are not in your folder. Bring them in with: loadout rebase planner. If your folder already has them, publish with --base 0.1.1"
	if code == 0 || !strings.Contains(errOut, want) {
		t.Fatalf("publish from a folder behind: exit %d, stderr %q; want %q", code, errOut, want)
	}

	out := bob.must("", "rebase", "planner")
	if !strings.Contains(out, "planner: made from 0.1.0, onto 0.1.1") || !strings.Contains(out, "The version is now 0.1.2, the next after 0.1.1.") {
		t.Errorf("rebase of a folder from a release = %q", out)
	}
	if want := "1 file has conflicts"; !strings.Contains(out, want) {
		t.Fatalf("both appended a line, so the rebase conflicts: %q", out)
	}
	path := filepath.Join(bob.wd, "planner", "agent.md")
	before, _, _ := strings.Cut(readFile(t, path), "<<<<<<<")
	writeFile(t, path, before+"Always cite the ticket.\nAnswer in English.\n")
	if out := bob.must("", "publish", "planner"); !strings.Contains(out, "published lime/planner@0.1.2") {
		t.Fatalf("publish after the rebase = %q", out)
	}
	// The record is gone: the next check reads the folder's version again.
	editAgent(t, bob, "Be brief.")
	if out := bob.must("", "publish", "planner", "--bump", "patch"); !strings.Contains(out, "published lime/planner@0.1.3") {
		t.Errorf("publish after the record was cleared = %q", out)
	}

	// Alice's folder is now behind 0.1.3; --base latest says it has those changes.
	editAgent(t, alice, "Say thanks.")
	if _, _, code := alice.runIn("", "publish", "planner", "--bump", "patch"); code == 0 {
		t.Error("Alice's folder behind 0.1.3 was published")
	}
	if out := alice.must("", "publish", "planner", "--bump", "patch", "--base", "latest"); !strings.Contains(out, "published lime/planner@0.1.4") {
		t.Errorf("publish --base latest = %q", out)
	}
}

// Publishing a folder rebased for a proposal leaves the proposal open, and
// says how to withdraw it.
func TestPublishARebasedProposalFolder(t *testing.T) {
	alice, bob, _ := rebaseTeam(t)
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan TRK-1")
	bobReleases(t, bob, func(agent string) string {
		return strings.Replace(agent, "description: What this agent does.", "description: Plans sprints.", 1)
	})
	alice.must("", "rebase", "planner")
	out := alice.must("", "publish", "planner")
	if !strings.Contains(out, "published lime/planner@0.2.0") || !strings.Contains(out, "your proposal "+id+" stays open and is now blocked by 0.2.0; withdraw it with: loadout review withdraw "+id) {
		t.Errorf("publish of a rebased proposal folder = %q", out)
	}
}

// The release deletes a document Alice changed in her proposal: the rebase
// keeps her file but leaves it undecided, and propose refuses until she
// deletes it or keeps it with --resolved.
func TestRebaseLeavesADeletedFileUndecided(t *testing.T) {
	alice, bob, _ := rebaseTeam(t)
	alice.must("", "new", "kb", "lime/runbooks")
	deploy := filepath.Join(alice.wd, "runbooks", "deploy.md")
	writeFile(t, deploy, "# Deploying\n\nRun make deploy.\n")
	alice.must("", "publish", "runbooks") // 0.1.0
	writeFile(t, deploy, "# Deploying\n\nRun make deploy, then check the dashboard.\n")
	out := alice.must("", "propose", "runbooks", "--bump", "minor", "--prompt", "How do I deploy?")
	id := proposalIDRE.FindStringSubmatch(out)[1]

	// Bob's copy of 0.1.0 never had deploy.md: he releases 0.1.1 without it.
	bob.must("", "new", "kb", "lime/runbooks")
	bob.must("", "publish", "runbooks", "--bump", "patch")

	out = alice.must("", "rebase", "runbooks")
	for _, want := range []string{
		"conflict  deploy.md: the release deleted it and you changed it; it is kept as yours",
		"Decide each file deleted on one side and changed on the other: delete it, or keep it with: loadout rebase runbooks --resolved deploy.md",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rebase output is missing %q:\n%s", want, out)
		}
	}
	_, errOut, code := alice.runIn("", "propose", "runbooks")
	want := "1 file was deleted on one side and changed on the other when loadout rebase merged: deploy.md. Delete it, or keep it with: loadout rebase runbooks --resolved deploy.md; then propose again"
	if code == 0 || !strings.Contains(errOut, want) {
		t.Fatalf("propose with an undecided file: exit %d, stderr %q; want %q", code, errOut, want)
	}
	if _, errOut, code := alice.runIn("", "publish", "runbooks"); code == 0 || !strings.Contains(errOut, "then publish again") {
		t.Errorf("publish with an undecided file: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := alice.runIn("", "rebase", "runbooks", "--resolved", "README.md"); code == 0 || !strings.Contains(errOut, "README.md has no undecided conflict") {
		t.Errorf("--resolved on a file without a conflict: exit %d, stderr %q", code, errOut)
	}
	if out := alice.must("", "rebase", "runbooks", "--resolved", "deploy.md"); !strings.Contains(out, "kept deploy.md") {
		t.Errorf("rebase --resolved = %q", out)
	}
	if out := alice.must("", "propose", "runbooks"); !strings.Contains(out, "replaces "+id) {
		t.Errorf("propose after keeping deploy.md = %q", out)
	}
}

// A teammate whose folder is at the version of someone else's open
// proposal proposes their own with --bump: the CLI never replaces, takes
// the base from, or speaks of "your proposal" for, a proposal the caller
// did not write.
func TestTeammateAtAnotherAuthorsProposalVersionCanStillPropose(t *testing.T) {
	alice, bob, _ := rebaseTeam(t)
	editAgent(t, alice, "Always cite the ticket.")
	propose(t, alice, "--bump", "minor", "--prompt", "Plan TRK-1") // 0.2.0 from 0.1.0

	bob.must("", "new", "agent", "lime/planner")
	manifest := filepath.Join(bob.wd, "planner", "loadout.yaml")
	writeFile(t, manifest, strings.Replace(readFile(t, manifest), "version: 0.1.0", "version: 0.2.0", 1))
	editAgent(t, bob, "Answer in English.")
	out, errOut, code := bob.runIn("", "propose", "planner", "--bump", "patch", "--prompt", "Plan TRK-2")
	if code != 0 || !strings.Contains(out, "proposed lime/planner@0.2.1") {
		t.Errorf("Bob's propose --bump patch: exit %d, stdout %q, stderr %q; want 0.2.1 proposed beside Alice's 0.2.0", code, out, errOut)
	}
}

// Publishing from a folder at the version of a teammate's open proposal
// says nothing about "your proposal".
func TestPublishAtAnotherAuthorsProposalVersion(t *testing.T) {
	alice, bob, _ := rebaseTeam(t)
	editAgent(t, alice, "Always cite the ticket.")
	propose(t, alice, "--bump", "minor", "--prompt", "Plan TRK-1") // 0.2.0
	bob.must("", "new", "agent", "lime/planner")
	manifest := filepath.Join(bob.wd, "planner", "loadout.yaml")
	writeFile(t, manifest, strings.Replace(readFile(t, manifest), "version: 0.1.0", "version: 0.2.0", 1))
	out := bob.must("", "publish", "planner")
	if !strings.Contains(out, "published lime/planner@0.2.0") || strings.Contains(out, "your proposal") {
		t.Errorf("Bob's publish at Alice's proposal version = %q; want no word about a proposal of his", out)
	}
}

// Rebasing a teammate's proposal gives you its files brought up to date,
// and proposing them makes a proposal of your own; the teammate's stays.
func TestRebaseSomeoneElsesProposal(t *testing.T) {
	alice, bob, _ := rebaseTeam(t)
	editAgent(t, alice, "Always cite the ticket.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan TRK-1") // 0.2.0 from 0.1.0
	bobReleases(t, bob, func(agent string) string {
		return strings.Replace(agent, "description: What this agent does.", "description: Plans sprints.", 1)
	})

	out := bob.must("", "rebase", "alices", "--proposal", id)
	if want := "planner: proposal " + id + " by someone else (proposing makes one of your own), made from 0.1.0, onto 0.1.1"; !strings.Contains(out, want) {
		t.Errorf("rebase of Alice's proposal = %q, want %q", out, want)
	}
	if _, errOut, code := bob.runIn("", "propose", "alices", "--prompt", "Plan TRK-2"); code == 0 || !strings.Contains(errOut, "already has an open proposal") || !strings.Contains(errOut, "--bump patch") {
		t.Errorf("Bob proposing at Alice's version: exit %d, stderr %q; want the version conflict and the --bump hint", code, errOut)
	}
	out = bob.must("", "propose", "alices", "--bump", "patch", "--prompt", "Plan TRK-2")
	if !strings.Contains(out, "proposed lime/planner@0.2.1") || strings.Contains(out, "replaces") || !strings.Contains(out, "based on 0.1.1") {
		t.Errorf("Bob's propose of the rebased files = %q; want his own 0.2.1 based on 0.1.1", out)
	}
	if list := bob.must("", "review", "list"); !strings.Contains(list, id) {
		t.Errorf("Alice's proposal is gone: %q", list)
	}
}
