package main

import (
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/mail/mailtest"
)

// TestMemberInvite: a project owner invites a person to the project and two
// teams from the CLI, gets the summary and the sign-in link, and the
// registry mails the invitation; a team maintainer cannot, and a refused
// invitation changes nothing.
func TestMemberInvite(t *testing.T) {
	host := &person{session: newSession(t)}
	mails := &mailtest.Recorder{}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL}
	base, _ := serveInBackground(t, host, env, externals{mailer: mails})
	host.must("", "namespace", "create", "acme", "--kind", "project")
	host.must("", "namespace", "create", "lime", "--parent", "acme")
	host.must("", "namespace", "create", "web", "--parent", "acme")
	host.must("", "member", "add", "--namespace", "lime", "--email", "alice@example.com", "--role", "maintainer")
	host.must("", "member", "add", "--namespace", "web", "--email", "dana@example.com")
	host.must("", "member", "add", "--namespace", "acme", "--email", "olga@example.com", "--role", "owner")
	aliceWeb := signUp(t, base, mails, "alice@example.com")
	olgaWeb := signUp(t, base, mails, "olga@example.com")

	olga := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, olga, olgaWeb, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}
	out := olga.must("", "member", "invite", "--namespace", "acme", "--email", "Dana@Example.com", "--team", "lime=maintainer", "--team", "web")
	link := publicURL + "/auth/login?email=dana%40example.com&next=%2Ft%2Facme"
	for _, want := range []string{
		"acme       member      added",
		"lime       maintainer  added",
		"web        member      unchanged, already member",
		"Mailed dana@example.com an invitation with this sign-in link:",
		link,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("invite output lacks %q:\n%s", want, out)
		}
	}
	if m := mails.WaitFor(t, "dana@example.com", 0); !strings.Contains(m.Text, "olga@example.com added you to acme") || !strings.Contains(m.Text, link) {
		t.Errorf("invitation mail = %s", m.Text)
	}
	list := host.must("", "member", "list", "--namespace", "lime")
	if !strings.Contains(list, "dana@example.com   maintainer") {
		t.Errorf("lime members after the invitation:\n%s", list)
	}

	// Again: nothing changes and nothing is mailed.
	out = olga.must("", "member", "invite", "--namespace", "acme", "--email", "dana@example.com", "--team", "lime=member")
	if !strings.Contains(out, "unchanged, already maintainer") || !strings.Contains(out, "Nothing changed, so nothing was mailed") {
		t.Errorf("repeat invite output:\n%s", out)
	}
	if n := mails.Count("dana@example.com"); n != 1 {
		t.Errorf("mails to dana = %d, want 1", n)
	}

	// A team maintainer cannot add people to the project, so nothing
	// happens in lime either.
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	_, errOut, code := alice.runIn("", "member", "invite", "--namespace", "acme", "--email", "erin@example.com", "--team", "lime")
	if code == 0 || !strings.Contains(errOut, "nothing was changed") {
		t.Errorf("team maintainer invites: exit %d, %s; want refused", code, errOut)
	}
	if list := host.must("", "member", "list", "--namespace", "lime"); strings.Contains(list, "erin@example.com") {
		t.Errorf("a refused invitation added erin to lime:\n%s", list)
	}

	// Flags are checked before anything is sent.
	if _, errOut, code := olga.runIn("", "member", "invite", "--namespace", "acme", "--email", "x@example.com", "--team", "lime=owner"); code == 0 || !strings.Contains(errOut, "TEAM=maintainer") {
		t.Errorf("--team lime=owner: exit %d, %s; want refused", code, errOut)
	}

	// On the registry host the operator may give owner; the host command
	// has no mail and, without a public URL, no link.
	out = host.must("", "member", "invite", "--namespace", "acme", "--email", "fred@example.com", "--role", "owner")
	if !strings.Contains(out, "acme       owner  added") || !strings.Contains(out, "sends no mail") || !strings.Contains(out, "no public URL") {
		t.Errorf("host invite output:\n%s", out)
	}
}
