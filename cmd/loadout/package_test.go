package main

import (
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/config"
)

// The operator deletes a package on the registry host while people have it
// installed: the dry run lists, --yes deletes, and the next sync of an
// installed machine says so and keeps the files.
// adminPassword is the first admin's password in the registries these tests start.
const adminPassword = "a long enough password"

func TestPackageDeleteThenSync(t *testing.T) {
	alice, bob, _ := team(t) // lime/planner 0.1.0 is published
	cfg, err := config.Load(func(k string) string { return map[string]string{"HOME": alice.home}[k] })
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(cfg.StateDir, "server") // the throwaway registry the test server serves

	editAgent(t, alice, "Always cite the ticket.")
	alice.must("", "publish", "planner", "--bump", "minor") // 0.2.0
	editAgent(t, alice, "Answer in English.")
	id := propose(t, alice, "--bump", "minor", "--prompt", "Plan INFOTM-1") // 0.3.0, left open
	bob.must("", "install", "lime/planner")
	bobAgent := filepath.Join(bob.home, ".claude", "agents", "planner.md")

	// 1. The dry run lists everything and deletes nothing.
	out := alice.must("", "package", "delete", "lime/planner", "--data", data)
	for _, want := range []string{
		"lime/planner (agent) would be deleted:",
		"releases   2: 0.1.0, 0.2.0",
		"proposals  1, 1 open: " + id,
		"course of  none",
		"Nothing was deleted. Run it again with --yes to delete it. Back up the database first.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run is missing %q:\n%s", want, out)
		}
	}
	if list := bob.must("", "list"); !strings.Contains(list, "lime/planner") {
		t.Errorf("list after the dry run = %q, want lime/planner still there", list)
	}

	// 2. --yes deletes it and says what it removed.
	out = alice.must("", "package", "delete", "lime/planner", "--data", data, "--yes")
	if want := "deleted lime/planner: 2 releases, 1 proposal,"; !strings.Contains(out, want) {
		t.Errorf("--yes output = %q, want %q", out, want)
	}
	if list := bob.must("", "list"); strings.Contains(list, "lime/planner") {
		t.Errorf("list after delete = %q, want lime/planner gone", list)
	}
	if list := bob.must("", "review", "list"); strings.Contains(list, id) {
		t.Errorf("review list after delete = %q, want proposal %s gone", list, id)
	}

	// 3. Bob's sync is not a failure: it says so, keeps his file and names
	// the way to remove it.
	out, errOut, code := bob.run("sync")
	if code != 0 {
		t.Fatalf("sync after delete: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, want := range []string{
		"removed from the registry",
		"lime/planner is no longer on its registry. loadout keeps your files; remove them with: loadout uninstall lime/planner --scope user",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sync is missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(bobAgent); err != nil {
		t.Errorf("sync removed Bob's agent file: %v", err)
	}
	bob.must("", "uninstall", "lime/planner")
	if _, err := os.Stat(bobAgent); !os.IsNotExist(err) {
		t.Errorf("uninstall left Bob's agent file: %v", err)
	}

	// 4. Nothing is left to delete.
	_, errOut, code = alice.run("package", "delete", "lime/planner", "--data", data, "--yes")
	if code != 1 || !strings.Contains(errOut, "lime/planner is not on this registry.") {
		t.Errorf("second delete: exit %d, stderr %q; want exit 1 and \"not on this registry\"", code, errOut)
	}

	// 5. The name is free again.
	if out := alice.must("", "publish", "planner"); !strings.Contains(out, "published lime/planner@") {
		t.Errorf("publish after delete = %q, want it published again", out)
	}
}

// An admin deletes a package on the web while a teammate has it installed:
// the registry saves a copy of its database first, the page says what went,
// and the next sync of the installed machine says so and keeps the files.
func TestPackageDeleteOnTheWeb(t *testing.T) {
	host := &person{session: newSession(t)}
	alice := &person{session: newSession(t)}
	bob := &person{session: newSession(t)}
	backups := t.TempDir()
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_LOGIN": "simple", "LOADOUT_ADMIN_PASSWORD": adminPassword, "LOADOUT_BACKUP_DIR": backups}
	base, serverLog := serveInBackground(t, host, env, externals{})
	if !strings.Contains(serverLog.String(), "deleting packages on the web is on") {
		t.Fatalf("log = %s, want web deletes on with a backup folder", serverLog.String())
	}

	alice.must(createInvite(t, host, "lime", "publish", base)+"\n", "team", "join")
	bob.must(createInvite(t, host, "lime", "read", base)+"\n", "team", "join")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")
	bob.must("", "install", "lime/planner")
	bobAgent := filepath.Join(bob.home, ".claude", "agents", "planner.md")

	// The first admin of simple login signs in and opens the delete page.
	web := newWebBrowser(t, base)
	web.send(http.MethodGet, "/auth/login", nil)
	if status, _, page := web.send(http.MethodPost, "/auth/login", url.Values{"email": {"admin@loadout.local"}, "password": {adminPassword}}); status != http.StatusSeeOther {
		t.Fatalf("sign in = %d\n%s", status, page)
	}
	const deletePath = "/t/lime/p/lime/planner/delete"
	status, _, page := web.send(http.MethodGet, deletePath, nil)
	seen := regexp.MustCompile(`name="seen" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	if status != http.StatusOK || seen == nil || !strings.Contains(page, "Delete lime/planner?") {
		t.Fatalf("GET %s = %d, want the delete page\n%s", deletePath, status, page)
	}

	// A wrong name deletes nothing; the right one does.
	if status, _, _ := web.send(http.MethodPost, deletePath, url.Values{"reason": {"replaced"}, "confirm": {"planner"}, "seen": {seen[1]}}); status != http.StatusBadRequest {
		t.Errorf("wrong name = %d, want 400", status)
	}
	if list := bob.must("", "list"); !strings.Contains(list, "lime/planner") {
		t.Errorf("list after a refused delete = %q, want lime/planner still there", list)
	}
	status, loc, page := web.send(http.MethodPost, deletePath, url.Values{"reason": {"replaced by lime/planner-2"}, "confirm": {"lime/planner"}, "seen": {seen[1]}})
	if status != http.StatusSeeOther || !strings.HasPrefix(loc, "/t/lime?done=deleted&package=lime%2Fplanner") {
		t.Fatalf("delete = %d %s, want a redirect with the counts\n%s", status, loc, page)
	}
	_, _, list := web.send(http.MethodGet, loc, nil)
	if !strings.Contains(list, "lime/planner was deleted: 1 release, 0 proposals") {
		t.Errorf("packages page after the delete lacks the notice:\n%s", list)
	}
	if out := bob.must("", "list"); strings.Contains(out, "lime/planner") {
		t.Errorf("list after the delete = %q, want lime/planner gone", out)
	}

	// The copy exists, is a database, and still has the package.
	copies, err := filepath.Glob(filepath.Join(backups, "before-delete-lime-planner-*.db"))
	if err != nil || len(copies) != 1 {
		t.Fatalf("copies = %v (%v), want one before-delete-lime-planner file", copies, err)
	}
	db, err := sql.Open("sqlite", copies[0])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM releases WHERE team = 'lime' AND name = 'planner'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("copy has %d releases of lime/planner (%v), want the one from before the delete", n, err)
	}

	// Bob's sync says so and keeps his file.
	out, errOut, code := bob.run("sync")
	if code != 0 || !strings.Contains(out, "removed from the registry") {
		t.Errorf("sync after the web delete: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if _, err := os.Stat(bobAgent); err != nil {
		t.Errorf("sync removed Bob's agent file: %v", err)
	}
}

// Without LOADOUT_BACKUP_DIR the web does not delete: the page is not there.
func TestPackageDeleteOnTheWebNeedsABackupFolder(t *testing.T) {
	host := &person{session: newSession(t)}
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_LOGIN": "simple", "LOADOUT_ADMIN_PASSWORD": adminPassword}
	base, serverLog := serveInBackground(t, host, env, externals{})
	if !strings.Contains(serverLog.String(), "deleting packages on the web is off") {
		t.Errorf("log = %s, want it to say web deletes are off", serverLog.String())
	}
	web := newWebBrowser(t, base)
	web.send(http.MethodGet, "/auth/login", nil)
	web.send(http.MethodPost, "/auth/login", url.Values{"email": {"admin@loadout.local"}, "password": {adminPassword}})
	if status, _, _ := web.send(http.MethodGet, "/t/lime/p/lime/planner/delete", nil); status != http.StatusNotFound {
		t.Errorf("GET the delete page = %d, want 404", status)
	}
}

// A member asks to delete a package with the CLI, a maintainer rejects the
// first request, and the second is approved on the web by an admin: the
// package goes after a copy of the database, the requests stay as the
// record, and the member's next sync says it was removed.
func TestPackageDeleteRequest(t *testing.T) {
	host := &person{session: newSession(t)}
	alice := &person{session: newSession(t)}
	bob := &person{session: newSession(t)}
	backups := t.TempDir()
	env := map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_LOGIN": "simple", "LOADOUT_ADMIN_PASSWORD": adminPassword, "LOADOUT_BACKUP_DIR": backups}
	base, _ := serveInBackground(t, host, env, externals{})
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}

	alice.must(createInvite(t, host, "lime", "publish", base)+"\n", "team", "join")
	bob.must(createInvite(t, host, "lime", "read", base)+"\n", "team", "join")
	alice.must("", "new", "agent", "lime/planner")
	alice.must("", "publish", "planner")
	bob.must("", "install", "lime/planner")
	bobAgent := filepath.Join(bob.home, ".claude", "agents", "planner.md")

	// A reason is required, and asking deletes nothing.
	if _, errOut, code := bob.run("propose", "delete", "lime/planner"); code == 0 || !strings.Contains(errOut, "--reason") {
		t.Errorf("propose delete without a reason: exit %d, stderr %q; want a refusal naming --reason", code, errOut)
	}
	out := bob.must("", "propose", "delete", "lime/planner", "--reason", "nobody uses it")
	first := regexp.MustCompile(`del-[a-z0-9]+`).FindString(out)
	if first == "" || !strings.Contains(out, "asked to delete lime/planner") || !strings.Contains(out, "loadout review approve "+first) {
		t.Fatalf("propose delete output = %q, want the request id and who approves", out)
	}
	if list := bob.must("", "list"); !strings.Contains(list, "lime/planner") {
		t.Errorf("list after asking = %q, want lime/planner still there", list)
	}
	if _, errOut, code := bob.run("propose", "delete", "lime/planner", "--reason", "again"); code == 0 || !strings.Contains(errOut, "already exists") {
		t.Errorf("a second request: exit %d, stderr %q; want it refused as already there", code, errOut)
	}

	// The CLI lists, shows, and refuses the wrong people.
	list := alice.must("", "review", "list")
	if !strings.Contains(list, first) || !strings.Contains(list, "delete") || !strings.Contains(list, "lime/planner") {
		t.Errorf("review list = %q, want the request as a delete row", list)
	}
	if show := alice.must("", "review", "show", first); !strings.Contains(show, "delete lime/planner (open)") || !strings.Contains(show, "nobody uses it") {
		t.Errorf("review show = %q, want the request and its reason", show)
	}
	if _, errOut, code := bob.run("review", "approve", first); code == 0 || !strings.Contains(errOut, "publish token") {
		t.Errorf("approve by a member: exit %d, stderr %q; want it refused", code, errOut)
	}
	if _, errOut, code := alice.run("review", "compare", first); code == 0 || !strings.Contains(errOut, "not a proposal") {
		t.Errorf("compare of a request: exit %d, stderr %q; want it refused as not a proposal", code, errOut)
	}
	if out := alice.must("", "review", "reject", first, "--note", "still used by the demo"); !strings.Contains(out, "rejected "+first) {
		t.Errorf("reject = %q", out)
	}

	// Only the author withdraws; the next request is approved on the web by
	// a seeded admin.
	out = bob.must("", "propose", "delete", "lime/planner", "--reason", "maybe it is not used")
	withdrawn := regexp.MustCompile(`del-[a-z0-9]+`).FindString(out)
	if _, errOut, code := alice.run("review", "withdraw", withdrawn); code == 0 || !strings.Contains(errOut, "only the author") {
		t.Errorf("withdraw by someone else: exit %d, stderr %q; want it refused", code, errOut)
	}
	if _, errOut, code := alice.run("review", "report", withdrawn); code == 0 || !strings.Contains(errOut, "not a proposal") {
		t.Errorf("report of a request: exit %d, stderr %q; want it refused as not a proposal", code, errOut)
	}
	if out := bob.must("", "review", "withdraw", withdrawn); !strings.Contains(out, "withdrawn "+withdrawn) {
		t.Errorf("withdraw = %q", out)
	}
	out = bob.must("", "propose", "delete", "lime/planner", "--reason", "it is not used, really")
	second := regexp.MustCompile(`del-[a-z0-9]+`).FindString(out)
	web := newWebBrowser(t, base)
	web.send(http.MethodGet, "/auth/login", nil)
	if status, _, page := web.send(http.MethodPost, "/auth/login", url.Values{"email": {"admin@loadout.local"}, "password": {adminPassword}}); status != http.StatusSeeOther {
		t.Fatalf("sign in = %d\n%s", status, page)
	}
	page := "/t/lime/deletions/" + second
	status, _, body := web.send(http.MethodGet, page, nil)
	if status != http.StatusOK || !strings.Contains(body, "Request to delete lime/planner") || !strings.Contains(body, "it is not used, really") || !strings.Contains(body, "Approve and delete") {
		t.Fatalf("GET %s = %d, want the request with an Approve button\n%s", page, status, body)
	}
	status, loc, body := web.send(http.MethodPost, page+"/approve", url.Values{})
	if status != http.StatusSeeOther || loc != page+"?done=approved" {
		t.Fatalf("approve = %d %s, want a redirect back to the request\n%s", status, loc, body)
	}
	_, _, body = web.send(http.MethodGet, loc, nil)
	if !strings.Contains(body, "Deleted 1 release, 0 proposals, 0 reviews and 0 prompts") {
		t.Errorf("the request page after approval lacks the outcome:\n%s", body)
	}
	if out := bob.must("", "list"); strings.Contains(out, "lime/planner") {
		t.Errorf("list after the delete = %q, want lime/planner gone", out)
	}
	out, errOut, code := bob.run("sync")
	if code != 0 || !strings.Contains(out, "removed from the registry") {
		t.Errorf("sync after the delete: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if _, err := os.Stat(bobAgent); err != nil {
		t.Errorf("sync removed Bob's agent file: %v", err)
	}

	// The copy was taken, and both requests stay as the record.
	if copies, err := filepath.Glob(filepath.Join(backups, "before-delete-lime-planner-*.db")); err != nil || len(copies) != 1 {
		t.Errorf("copies = %v (%v), want one before-delete-lime-planner file", copies, err)
	}
	all := alice.must("", "review", "list", "--all")
	for _, want := range []string{first, withdrawn, second, "rejected", "withdrawn", "approved"} {
		if !strings.Contains(all, want) {
			t.Errorf("review list --all lacks %q:\n%s", want, all)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(cfg.StateDir, "server", dbFile))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, want := range map[string][2]string{first: {"rejected", ""}, withdrawn: {"withdrawn", ""}, second: {"approved", `"releases":["0.1.0"]`}} {
		var status, removed, reason string
		if err := db.QueryRowContext(t.Context(), `SELECT status, removed, reason FROM deletions WHERE id = ?`, id).Scan(&status, &removed, &reason); err != nil {
			t.Fatalf("record %s: %v", id, err)
		}
		if status != want[0] || !strings.Contains(removed, want[1]) || reason == "" {
			t.Errorf("record %s = %s %q %q, want %s with %q", id, status, removed, reason, want[0], want[1])
		}
	}
}
