package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/server"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
)

// person is one loadout user with their own home and working directory.
type person struct {
	*session
}

func (p *person) runIn(stdin string, args ...string) (string, string, int) {
	p.t.Helper()
	return p.exec(stdin, args...)
}

func (p *person) must(stdin string, args ...string) string {
	p.t.Helper()
	out, errOut, code := p.runIn(stdin, args...)
	if code != 0 {
		p.t.Fatalf("loadout %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

// startServer serves the admin's registry database on a test server and
// returns its URL. It is the handler `loadout serve` runs, on a listener
// that is already bound, so there is no port to race for.
func startServer(t *testing.T, admin *person) string {
	t.Helper()
	return startRegistry(t, admin).URL
}

// startRegistry is startServer for a test that stops the registry itself.
// Its build has no release, so it advertises no CLI release.
func startRegistry(t *testing.T, admin *person) *httptest.Server {
	t.Helper()
	return startRegistryAt(t, admin, "dev")
}

// startRegistryAt is startRegistry for a registry built as build (v0.5.0
// advertises 0.5.0 to CLIs, as cmd/loadout wires it), with more options.
func startRegistryAt(t *testing.T, admin *person, build string, opts ...server.Option) *httptest.Server {
	t.Helper()
	cfg, err := config.Load(func(k string) string { return map[string]string{"HOME": admin.home}[k] })
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	store, err := sqlitestore.Open(t.Context(), filepath.Join(cfg.StateDir, "server", "registry.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close registry store: %v", err)
		}
	})
	svc := registry.NewService(store, store, store, time.Now, log)
	props := registry.NewProposalService(store, store, time.Now, log)
	b := buildinfo.Current(build, nil, false)
	opts = append([]server.Option{server.WithProposals(props), server.WithCLI(advertised(b), server.MinimumCLI())}, opts...)
	srv := httptest.NewServer(server.New(svc, log, b.Raw(), opts...))
	t.Cleanup(srv.Close)
	return srv
}

var tokenRE = regexp.MustCompile(`ldt_[a-z0-9]+`)

// ensureTeam creates team inside a project on the admin's registry, unless
// it already exists: token create no longer makes namespaces.
func ensureTeam(admin *person, team string) {
	admin.runIn("", "namespace", "create", "base", "--kind", "project")
	admin.runIn("", "namespace", "create", team, "--parent", "base")
}

func createToken(t *testing.T, admin *person, scope string) string {
	t.Helper()
	ensureTeam(admin, "lime")
	out := admin.must("", "token", "create", "--team", "lime", "--"+scope, "--label", "test")
	tok := tokenRE.FindString(out)
	if tok == "" {
		t.Fatalf("no token in output: %s", out)
	}
	return tok
}

func TestTeamSharesAnAgentThroughALANRegistry(t *testing.T) {
	alice := &person{session: newSession(t)}
	bob := &person{session: newSession(t)}

	url := startServer(t, alice)
	publishToken := createToken(t, alice, "publish")
	readToken := createToken(t, alice, "read")

	// Alice sets up the registry and publishes an agent.
	alice.must(publishToken+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	if out := alice.must("", "publish", "planner"); !strings.Contains(out, "published lime/planner@0.1.0 to office") {
		t.Errorf("publish output = %q", out)
	}

	// Bob installs it with a read-only token.
	bob.must(readToken+"\n", "remote", "add", "office", url, "--token-stdin")
	if out := bob.must("", "list"); !strings.Contains(out, "lime/planner") || !strings.Contains(out, "0.1.0") {
		t.Errorf("list output = %q", out)
	}
	bob.must("", "install", "lime/planner")
	bobAgent := filepath.Join(bob.home, ".claude", "agents", "planner.md")
	if _, err := os.Stat(bobAgent); err != nil {
		t.Fatalf("Bob's agent not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bob.home, ".copilot")); err == nil {
		t.Error("Copilot folder created although Bob does not use Copilot")
	}

	// Bob cannot publish with a read token.
	bob.must("", "new", "agent", "lime/reviewer")
	if _, errOut, code := bob.runIn("", "publish", "reviewer"); code == 0 || !strings.Contains(errOut, "forbidden") {
		t.Errorf("publish with read token: exit %d, stderr %q; want forbidden", code, errOut)
	}

	// Alice ships 0.1.1; Bob's default policy (notify) only reports it.
	alice.must("", "publish", "planner", "--bump", "patch")
	out := bob.must("", "sync")
	if !strings.Contains(out, "update available") || !strings.Contains(out, "0.1.1") {
		t.Errorf("sync with notify = %q, want update available 0.1.1", out)
	}
	if data, _ := os.ReadFile(bobAgent); !strings.Contains(string(data), "@0.1.0") {
		t.Error("notify policy changed the installed file")
	}

	// Bob opts in to auto; the next sync installs 0.1.1.
	bob.must("", "policy", "lime/planner", "auto")
	if out := bob.must("", "sync"); !strings.Contains(out, "updated") {
		t.Errorf("sync with auto = %q, want updated", out)
	}
	if data, _ := os.ReadFile(bobAgent); !strings.Contains(string(data), "@0.1.1") {
		t.Errorf("auto sync did not install 0.1.1:\n%s", data)
	}

	// Bob edits his copy; the next update is held back, not overwritten,
	// and sync exits 2 so his cron notices.
	if err := os.WriteFile(bobAgent, []byte("Bob's tweak"), 0o644); err != nil {
		t.Fatal(err)
	}
	alice.must("", "publish", "planner", "--bump", "minor")
	if out, errOut, code := bob.runIn("", "sync"); code != 2 || !strings.Contains(out, "held back by your edits") || !strings.Contains(errOut, "1 update is held back") {
		t.Errorf("sync over an edit: exit %d\n%s\n%s\nwant exit 2, held back", code, out, errOut)
	}
	if data, _ := os.ReadFile(bobAgent); string(data) != "Bob's tweak" {
		t.Error("sync overwrote Bob's edit")
	}

	// Republishing the same version is refused with a hint.
	if _, errOut, code := alice.runIn("", "publish", "planner"); code == 0 || !strings.Contains(errOut, "--bump") {
		t.Errorf("republish: exit %d, stderr %q; want a conflict with a --bump hint", code, errOut)
	}
}

func TestRemoteAddRejectsBadToken(t *testing.T) {
	alice := &person{session: newSession(t)}
	url := startServer(t, alice)
	_, errOut, code := alice.runIn("ldt_notarealtoken\n", "remote", "add", "office", url, "--token-stdin")
	if code == 0 || !strings.Contains(errOut, "rejected") {
		t.Errorf("remote add with a bad token: exit %d, stderr %q", code, errOut)
	}
	if out := alice.must("", "remote", "list"); !strings.Contains(out, "no remotes") {
		t.Errorf("a rejected remote was saved: %s", out)
	}
}

func TestTokenNeverWrittenToLogs(t *testing.T) {
	alice := &person{session: newSession(t)}
	url := startServer(t, alice)
	tok := createToken(t, alice, "publish")
	alice.must(tok+"\n", "remote", "add", "office", url, "--token-stdin", "-v")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner", "-v")

	for _, name := range []string{"loadout.log", "config.yaml", "lock.yaml"} {
		data, err := os.ReadFile(filepath.Join(alice.home, ".loadout", name))
		if err != nil {
			continue
		}
		if bytes.Contains(data, []byte(tok)) {
			t.Errorf("%s contains the token", name)
		}
	}
	creds, err := os.Stat(filepath.Join(alice.home, ".loadout", "credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%o", creds.Mode().Perm()); got != "600" {
		t.Errorf("credentials.yaml permissions = %s, want 600", got)
	}
}

func TestDiffAndRollback(t *testing.T) {
	alice := &person{session: newSession(t)}
	url := startServer(t, alice)
	tok := createToken(t, alice, "publish")
	alice.must(tok+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")

	agentFile := filepath.Join(alice.wd, "planner", "agent.md")
	data, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentFile, append(data, []byte("Always cite the ticket.\n")...), 0o644); err != nil { //nolint:gosec // path is inside the test temp dir
		t.Fatal(err)
	}
	alice.must("", "publish", "planner", "--bump", "minor")

	out := alice.must("", "diff", "lime/planner", "0.1.0", "0.2.0")
	if !strings.Contains(out, "+Always cite the ticket.") || !strings.Contains(out, "-version: 0.1.0") {
		t.Errorf("diff output missing the change:\n%s", out)
	}

	alice.must("", "install", "lime/planner", "--policy", "auto")
	out = alice.must("", "rollback", "lime/planner")
	if !strings.Contains(out, "rolled back lime/planner to 0.1.0") {
		t.Errorf("rollback output = %q", out)
	}
	installed, err := os.ReadFile(filepath.Join(alice.home, ".claude", "agents", "planner.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), "@0.1.0") {
		t.Errorf("after rollback the file is not 0.1.0:\n%s", installed)
	}
	if out := alice.must("", "sync"); !strings.Contains(out, "pinned") {
		t.Errorf("sync after rollback = %q, want pinned (no auto re-upgrade)", out)
	}
}

func TestFailedBumpLeavesTheManifestAlone(t *testing.T) {
	alice := &person{session: newSession(t)}
	url := startServer(t, alice)
	readOnly := createToken(t, alice, "read")
	alice.must(readOnly+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	if _, _, code := alice.runIn("", "publish", "planner", "--bump", "patch"); code == 0 {
		t.Fatal("publish with a read token succeeded")
	}
	data, err := os.ReadFile(filepath.Join(alice.wd, "planner", "loadout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "version: 0.1.0") {
		t.Errorf("a failed publish changed the version:\n%s", data)
	}
}

func TestRollbackRefusesToGoForward(t *testing.T) {
	alice := &person{session: newSession(t)}
	url := startServer(t, alice)
	tok := createToken(t, alice, "publish")
	alice.must(tok+"\n", "remote", "add", "office", url, "--token-stdin")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")
	alice.must("", "install", "lime/planner")
	if _, errOut, code := alice.runIn("", "rollback", "lime/planner", "0.1.0"); code == 0 || !strings.Contains(errOut, "not older") {
		t.Errorf("rollback to the installed version: exit %d, stderr %q", code, errOut)
	}
}

// A token is only for a team that exists: it no longer creates a team
// outside any project.
func TestTokenNeedsAnExistingTeam(t *testing.T) {
	admin := &person{session: newSession(t)}
	if _, errOut, code := admin.runIn("", "token", "create", "--team", "ghost", "--read"); code == 0 || !strings.Contains(errOut, "namespace create ghost --parent") {
		t.Errorf("token for an unknown team = %d %q, want refused with the command to create it", code, errOut)
	}
	if _, errOut, code := admin.runIn("", "namespace", "create", "loose"); code == 0 || !strings.Contains(errOut, "belongs to a project") {
		t.Errorf("team without a project = %d %q, want refused", code, errOut)
	}
}
