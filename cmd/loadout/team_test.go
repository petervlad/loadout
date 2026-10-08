package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var inviteRE = regexp.MustCompile(`ldi_[A-Za-z0-9_-]+`)

// createInvite runs `token create --invite` on the admin's registry.
func createInvite(t *testing.T, admin *person, team, scope, url string) string {
	t.Helper()
	ensureTeam(admin, team)
	out := admin.must("", "token", "create", "--team", team, "--"+scope, "--invite", "--url", url)
	inv := inviteRE.FindString(out)
	if inv == "" {
		t.Fatalf("no invite in output: %s", out)
	}
	if tokenRE.MatchString(out) {
		t.Errorf("token create --invite printed a bare token:\n%s", out)
	}
	return inv
}

func (p *person) file(name string) string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.home, ".loadout", name))
	if err != nil {
		p.t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func installedAgent(t *testing.T, p *person, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.home, ".claude", "agents", name+".md"))
	if err != nil {
		t.Fatalf("%s not installed: %v", name, err)
	}
	return string(data)
}

func TestTeamsOnTwoRegistries(t *testing.T) {
	kate := &person{session: newSession(t)} // lime lead, runs registry A
	nora := &person{session: newSession(t)} // north lead, runs registry B
	vera := &person{session: newSession(t)} // in both teams

	urlA := startServer(t, kate)
	urlB := startServer(t, nora)

	// Each lead joins their own team with a publish invite and publishes.
	kate.must(createInvite(t, kate, "lime", "publish", urlA)+"\n", "team", "join")
	kate.must("", "new", "agent", "lime/x")
	if out := kate.must("", "publish", "x"); !strings.Contains(out, "published lime/x@0.1.0") {
		t.Errorf("lime publish = %q", out)
	}
	nora.must(createInvite(t, nora, "north", "publish", urlB)+"\n", "team", "join")
	nora.must("", "new", "agent", "north/y")
	if out := nora.must("", "publish", "y"); !strings.Contains(out, "published north/y@0.1.0") {
		t.Errorf("north publish = %q", out)
	}

	// Vera joins both teams with read invites.
	invA := createInvite(t, kate, "lime", "read", urlA)
	invB := createInvite(t, nora, "north", "read", urlB)
	if out := vera.must(invA+"\n", "team", "join", "-v"); !strings.Contains(out, "joined lime on "+urlA) {
		t.Errorf("join lime = %q", out)
	}
	if out := vera.must(invB+"\n", "team", "join", "-v"); !strings.Contains(out, "joined north on "+urlB) {
		t.Errorf("join north = %q", out)
	}
	teams := vera.must("", "team", "list")
	for _, want := range []string{"lime", "north", urlA, urlB, "read"} {
		if !strings.Contains(teams, want) {
			t.Errorf("team list missing %q:\n%s", want, teams)
		}
	}
	remotes := vera.must("", "remote", "list")
	if !strings.Contains(remotes, "127-0-0-1 ") || !strings.Contains(remotes, "127-0-0-1-2 ") {
		t.Errorf("remote list = %q, want two de-duplicated host names", remotes)
	}

	// Joining again reuses the remote instead of adding a third.
	vera.must(invA+"\n", "team", "join")
	if out := vera.must("", "remote", "list"); strings.Contains(out, "127-0-0-1-3") {
		t.Errorf("rejoining added a remote:\n%s", out)
	}

	// Each install goes to its team's registry without --remote.
	vera.must("", "install", "lime/x", "--policy", "auto")
	vera.must("", "install", "north/y", "--policy", "auto")
	lock := vera.file("lock.yaml")
	if !strings.Contains(lock, "source: "+urlA) || !strings.Contains(lock, "source: "+urlB) {
		t.Errorf("lock does not record both registries:\n%s", lock)
	}
	if _, errOut, code := vera.runIn("", "install", "mango/z"); code == 0 || !strings.Contains(errOut, "no team registry for namespace mango") {
		t.Errorf("install from an unjoined team: exit %d, stderr %q", code, errOut)
	}

	// list shows both team registries.
	list := vera.must("", "list")
	if !strings.Contains(list, "lime/x") || !strings.Contains(list, "north/y") {
		t.Errorf("list = %q, want packages of both teams", list)
	}

	// Both leads ship an update; one sync brings both.
	kate.must("", "publish", "x", "--bump", "patch")
	nora.must("", "publish", "y", "--bump", "minor")
	out := vera.must("", "sync")
	if strings.Count(out, "updated") != 2 {
		t.Errorf("sync = %q, want both updated", out)
	}
	if got := installedAgent(t, vera, "x"); !strings.Contains(got, "@0.1.1") {
		t.Errorf("lime/x after sync:\n%s", got)
	}
	if got := installedAgent(t, vera, "y"); !strings.Contains(got, "@0.2.0") {
		t.Errorf("north/y after sync:\n%s", got)
	}

	// Leaving north removes its binding, remote and token, and warns about
	// the agent installed from it; lime keeps working.
	out, errOut, code := vera.runIn("", "team", "leave", "north")
	if code != 0 {
		t.Fatalf("team leave: exit %d, stderr %s", code, errOut)
	}
	if !strings.Contains(out, "removed remote 127-0-0-1-2") {
		t.Errorf("team leave = %q, want the remote removed", out)
	}
	if !strings.Contains(errOut, "north/y") {
		t.Errorf("team leave stderr = %q, want a warning about north/y", errOut)
	}
	cfg := vera.file("config.yaml")
	if strings.Contains(cfg, "north") || strings.Contains(cfg, urlB) {
		t.Errorf("config.yaml still mentions north:\n%s", cfg)
	}
	if !strings.Contains(cfg, "namespace: lime") {
		t.Errorf("config.yaml lost lime:\n%s", cfg)
	}
	creds := vera.file("credentials.yaml")
	if strings.Count(creds, "ldt_") != 1 {
		t.Errorf("credentials.yaml should hold only lime's token:\n%s", creds)
	}
	kate.must("", "publish", "x", "--bump", "patch")
	out, _, _ = vera.runIn("", "sync")
	if !strings.Contains(out, "lime/x") || !strings.Contains(out, "updated") {
		t.Errorf("sync after leaving north = %q, want lime/x updated", out)
	}
	if _, errOut, code := vera.runIn("", "team", "leave", "north"); code == 0 || !strings.Contains(errOut, "not bound") {
		t.Errorf("leaving twice: exit %d, stderr %q", code, errOut)
	}

	// The invites (and so their tokens) never reach config or logs.
	for _, p := range []*person{kate, nora, vera} {
		for _, name := range []string{"config.yaml", "loadout.log", "lock.yaml"} {
			data, err := os.ReadFile(filepath.Join(p.home, ".loadout", name))
			if err != nil {
				continue
			}
			if inviteRE.Match(data) {
				t.Errorf("%s of %s contains an invite", name, p.home)
			}
			if tokenRE.Match(data) {
				t.Errorf("%s of %s contains a token", name, p.home)
			}
		}
	}
	if log := vera.file("loadout.log"); !bytes.Contains([]byte(log), []byte("joining team")) {
		t.Error("loadout.log has no record of the join")
	}
}

func TestTeamJoinRejectsBadInvites(t *testing.T) {
	kate := &person{session: newSession(t)}
	url := startServer(t, kate)
	tok := createToken(t, kate, "read")
	tests := map[string]string{
		"empty":      "",
		"bare token": tok,
		"garbage":    "ldi_not-an-invite",
	}
	for name, stdin := range tests {
		t.Run(name, func(t *testing.T) {
			vera := &person{session: newSession(t)}
			_, errOut, code := vera.runIn(stdin+"\n", "team", "join")
			if code == 0 {
				t.Fatalf("team join with %s succeeded", name)
			}
			if tok != "" && strings.Contains(errOut, tok) {
				t.Errorf("error output contains the token: %s", errOut)
			}
			if _, err := os.Stat(filepath.Join(vera.home, ".loadout", "config.yaml")); err == nil {
				t.Error("a failed join wrote config.yaml")
			}
		})
	}

	// A well-formed invite whose token the registry rejects.
	inv := createInvite(t, kate, "lime", "read", url)
	revoked := strings.Fields(kate.must("", "token", "list", "--team", "lime"))
	id := revoked[len(revoked)-4] // ID column of the last row
	kate.must("", "token", "revoke", id)
	vera := &person{session: newSession(t)}
	if _, errOut, code := vera.runIn(inv+"\n", "team", "join"); code == 0 || !strings.Contains(errOut, "rejected") {
		t.Errorf("join with a revoked token: exit %d, stderr %q", code, errOut)
	}
	if out := vera.must("", "team", "list"); !strings.Contains(out, "no teams") {
		t.Errorf("a rejected join was saved: %s", out)
	}
}

func TestInviteNeedsURL(t *testing.T) {
	kate := &person{session: newSession(t)}
	if _, errOut, code := kate.runIn("", "token", "create", "--team", "lime", "--read", "--invite"); code == 0 || !strings.Contains(errOut, "--url") {
		t.Errorf("--invite without --url: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := kate.runIn("", "token", "create", "--team", "lime", "--read", "--invite", "--url", "ftp://x"); code == 0 || !strings.Contains(errOut, "url") {
		t.Errorf("--invite with a bad --url: exit %d, stderr %q", code, errOut)
	}
	if out, _, _ := kate.runIn("", "token", "list", "--team", "lime"); strings.Contains(out, "read") {
		t.Errorf("a refused invite still created a token:\n%s", out)
	}
}
