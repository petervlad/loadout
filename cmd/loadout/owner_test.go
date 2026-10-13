package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
)

// TestOwnerApprovesOwnProposal: lime requires review; olga, who owns the
// project above it, proposes and releases her own version as owner, and
// everyone can see that review was skipped.
func TestOwnerApprovesOwnProposal(t *testing.T) {
	host, base, aliceWeb, olgaWeb := ownedRegistry(t)
	host.must("", "namespace", "set", "lime", "--require-review", "on")

	olga := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, olga, olgaWeb, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}
	olga.must("", "new", "agent", "lime/planner")
	editAgent(t, olga, "Always cite the ticket.")

	// Publishing directly stays refused, and tells an owner the way.
	if _, errOut, code := olga.runIn("", "publish", "planner", "--bump", "minor"); code == 0 || !strings.Contains(errOut, "requires review") || !strings.Contains(errOut, "--as-owner") {
		t.Errorf("publish under review by an owner: exit %d, stderr %q; want refused with the hint", code, errOut)
	}
	id := propose(t, olga, "--bump", "minor", "--prompt", "Plan TRK-1")

	if _, errOut, code := olga.runIn("", "review", "approve", id); code == 0 || !strings.Contains(errOut, "--as-owner") {
		t.Errorf("approve own without --as-owner: exit %d, stderr %q; want refused with the hint", code, errOut)
	}
	if _, errOut, code := olga.runIn("", "review", "approve", id, "--as-owner"); code == 0 || !strings.Contains(errOut, "released without review") {
		t.Errorf("approve own as owner without a note: exit %d, stderr %q; want a note asked for", code, errOut)
	}

	// A machine token she made never decides.
	account, err := client.New(base, accountToken(t, base, olgaWeb))
	if err != nil {
		t.Fatal(err)
	}
	created, err := account.CreateMachineToken(t.Context(), "lime", "publish", "ci")
	if err != nil {
		t.Fatal(err)
	}
	ci, err := client.New(base, created.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ci.Approve(t.Context(), id, api.DecisionRequest{Note: "from CI", AsOwner: true}); statusOf(err) != http.StatusForbidden {
		t.Errorf("approve as owner with her machine token = %v, want 403", err)
	}

	out := olga.must("", "review", "approve", id, "--as-owner", "--note", "hotfix, nobody else is around")
	if !strings.Contains(out, "approved your own proposal "+id+" as owner, without review: lime/planner@0.2.0 is released") {
		t.Errorf("review approve --as-owner = %q", out)
	}

	// Alice, a maintainer of lime, gets it and sees how it was released.
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	alice.must("", "install", "lime/planner")
	agent, err := os.ReadFile(filepath.Join(alice.home, ".claude", "agents", "planner.md"))
	if err != nil || !strings.Contains(string(agent), "Always cite the ticket.") {
		t.Errorf("alice's agent = %s, %v; want olga's 0.2.0", agent, err)
	}
	if out := alice.must("", "review", "show", id); !strings.Contains(out, "Approved by its author as owner, without review") || !strings.Contains(out, "hotfix, nobody else is around") {
		t.Errorf("review show = %q, want the owner approval marked", out)
	}
	if out := alice.must("", "review", "list", "--all"); !strings.Contains(out, "approved without review") {
		t.Errorf("review list --all = %q, want the owner approval marked", out)
	}
	rels, err := account.Releases(t.Context(), mustPackID(t, "lime/planner"))
	if err != nil || len(rels) != 2 || rels[0].SelfApproved || !rels[1].SelfApproved || rels[1].Proposal != id {
		t.Errorf("releases = %+v, %v; want 0.2.0 marked as approved by its author from %s", rels, err, id)
	}
}

// TestWikiSwitchNeedsAnOwner: alice, a maintainer of lime, may not turn its
// wiki on; olga, who owns the project above, may, and the registry says so.
func TestWikiSwitchNeedsAnOwner(t *testing.T) {
	_, base, aliceWeb, olgaWeb := ownedRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, errOut, code := alice.runIn("", "namespace", "set", "lime", "--wiki", "on"); code == 0 || !strings.Contains(errOut, "needs an owner") {
		t.Errorf("alice turns the wiki on: exit %d, stderr %q; want refused", code, errOut)
	}

	olga := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, olga, olgaWeb, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}
	if out := olga.must("", "namespace", "set", "lime", "--wiki", "on"); !strings.Contains(out, "lime now has a wiki: open it at "+base+"/t/lime/wiki") {
		t.Errorf("olga turns the wiki on = %q", out)
	}
	if out := olga.must("", "namespace", "set", "lime", "--wiki", "off"); !strings.Contains(out, "lime's wiki is off; its history is kept") {
		t.Errorf("olga turns the wiki off = %q", out)
	}
}
