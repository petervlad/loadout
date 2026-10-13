package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/demo"
)

// seedDemo seeds the host's server folder and returns its path.
func seedDemo(t *testing.T, host *person) string {
	t.Helper()
	dir := filepath.Join(host.home, ".loadout", "server")
	if out := host.must("", "demo", "seed", "--data", dir); !strings.Contains(out, "seeded the demo registry") {
		t.Fatalf("demo seed said %q", out)
	}
	return dir
}

func count(t *testing.T, db, query string) int {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+db+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func fingerprint(t *testing.T, dir string) [32]byte {
	t.Helper()
	h := sha256.New()
	for _, name := range []string{"registry.db", "memory.db"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		h.Write(data)
	}
	return [32]byte(h.Sum(nil))
}

func TestDemoSeedWritesTheWorld(t *testing.T) {
	host := &person{session: newSession(t)}
	dir := seedDemo(t, host)
	reg, mem := filepath.Join(dir, "registry.db"), filepath.Join(dir, "memory.db")
	for _, c := range []struct {
		what, db, query string
		want            int
	}{
		{"namespaces", reg, "SELECT COUNT(*) FROM namespaces", 6},
		{"people", reg, "SELECT COUNT(*) FROM users", 7},
		{"admins", reg, "SELECT COUNT(*) FROM users WHERE is_admin = 1", 1},
		{"packages", reg, "SELECT COUNT(DISTINCT team || '/' || name) FROM releases", 44},
		{"releases", reg, "SELECT COUNT(*) FROM releases", 59},
		{"pipelines", reg, "SELECT COUNT(*) FROM releases WHERE kind = 'pipeline'", 10},
		{"releases this week", reg, "SELECT COUNT(DISTINCT team) FROM releases WHERE published_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-7 days')", 6},
		{"open proposals", reg, "SELECT COUNT(*) FROM proposals WHERE status = 'open'", 5},
		{"approved proposals", reg, "SELECT COUNT(*) FROM proposals WHERE status = 'approved'", 3},
		{"rejected proposals", reg, "SELECT COUNT(*) FROM proposals WHERE status = 'rejected'", 2},
		{"reports", reg, "SELECT COUNT(*) FROM proposal_reviews", 1},
		{"deletion requests", reg, "SELECT COUNT(*) FROM deletions WHERE status = 'open'", 2},
		{"wiki pages", reg, "SELECT COUNT(*) FROM wiki_revisions", 5},
		{"courses", reg, "SELECT COUNT(*) FROM namespaces WHERE course != ''", 2},
		{"memories", mem, "SELECT COUNT(*) FROM entries", 58},
		{"retracted memories", mem, "SELECT COUNT(*) FROM entries WHERE status = 'retracted'", 2},
		{"teams with memory from this week", mem, "SELECT COUNT(DISTINCT namespace) FROM entries WHERE created_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-7 days')", 6},
		{"memories with history (4 edited, 2 retracted, 2 moved)", mem, "SELECT COUNT(DISTINCT entry_id) FROM entry_versions WHERE version > 1", 8},
	} {
		if got := count(t, c.db, c.query); got != c.want {
			t.Errorf("%s = %d, want %d", c.what, got, c.want)
		}
	}
	// The history spreads over weeks: the first release is a month old.
	if n := count(t, reg, "SELECT COUNT(*) FROM releases WHERE published_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-28 days')"); n == 0 {
		t.Error("no release is older than four weeks, want the history backdated")
	}
	if n := count(t, reg, "SELECT COUNT(*) FROM tokens"); n != 0 {
		t.Errorf("%d tokens left behind, want none", n)
	}
	if n := count(t, reg, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Errorf("%d sessions left behind, want none", n)
	}
}

func TestDemoSeedTwiceChangesNothing(t *testing.T) {
	host := &person{session: newSession(t)}
	dir := seedDemo(t, host)
	before := fingerprint(t, dir)
	if out := host.must("", "demo", "seed", "--data", dir); !strings.Contains(out, "already holds the demo registry") {
		t.Errorf("second seed said %q", out)
	}
	if fingerprint(t, dir) != before {
		t.Error("the second seed changed the databases")
	}
}

func TestDemoSeedRefusesAFolderItDidNotMake(t *testing.T) {
	host := &person{session: newSession(t)}
	dir := t.TempDir()
	mine := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(mine, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := host.runIn("", "demo", "seed", "--data", dir)
	if code == 0 || !strings.Contains(errOut, "is not empty") {
		t.Errorf("seed into a folder with a file: exit %d, stderr %q; want it refused", code, errOut)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the folder holds %d entries, want only the file that was there", len(entries))
	}

	// A registry of someone else is not the demo, even without the file.
	other := filepath.Join(host.home, "other")
	host.must("", "namespace", "create", "acme", "--kind", "project", "--data", other)
	if _, errOut, code := host.runIn("", "demo", "seed", "--data", other); code == 0 || !strings.Contains(errOut, "is not empty") {
		t.Errorf("seed into another registry: exit %d, stderr %q; want it refused", code, errOut)
	}
}

func demoEnv(host *person) map[string]string {
	return map[string]string{"HOME": host.home, "LOADOUT_PUBLIC_URL": publicURL, "LOADOUT_DEMO": "1"}
}

func TestDemoModeGuards(t *testing.T) {
	host := &person{session: newSession(t)}
	run := func(env map[string]string, args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := runWith(t.Context(), args, strings.NewReader(""), &out, &errOut, func(k string) string { return env[k] }, host.wd, realExternals())
		return code, errOut.String()
	}
	env := demoEnv(host)
	env["LOADOUT_PUBLIC_URL"] = "https://registry.example.com"
	if code, msg := run(env, "serve", "--addr", "127.0.0.1:0"); code == 0 || !strings.Contains(msg, "localhost") {
		t.Errorf("demo on a public address: exit %d, stderr %q; want it refused", code, msg)
	}

	// A registry that is not the demo's, and an empty folder, are refused too.
	other := filepath.Join(host.home, "other")
	host.must("", "namespace", "create", "acme", "--kind", "project", "--data", other)
	for name, dir := range map[string]string{"another registry": other, "an empty folder": filepath.Join(host.home, "empty")} {
		if code, msg := run(demoEnv(host), "serve", "--addr", "127.0.0.1:0", "--data", dir); code == 0 || !strings.Contains(msg, "demo seed made") {
			t.Errorf("demo serving %s: exit %d, stderr %q; want it refused", name, code, msg)
		}
	}
	// demo up seeds the empty folder, so only a missing switch stops it.
	delete(env, "LOADOUT_DEMO")
	env["LOADOUT_PUBLIC_URL"] = publicURL
	if code, msg := run(env, "demo", "up", "--data", filepath.Join(host.home, "up")); code == 0 || !strings.Contains(msg, "LOADOUT_DEMO=1") {
		t.Errorf("demo up without the switch: exit %d, stderr %q; want it refused", code, msg)
	}
}

// TestDemoPagesAndCLI: every demo account signs in, the main pages answer
// with the banner, Google is never offered, and alice signs the CLI in and
// installs a package.
func TestDemoPagesAndCLI(t *testing.T) {
	host := &person{session: newSession(t)}
	seedDemo(t, host)
	base, serverLog := serveInBackground(t, host, demoEnv(host), externals{})
	if !strings.Contains(serverLog.String(), "simple login is on") {
		t.Errorf("serve log = %s, want simple login on", serverLog.String())
	}

	anon := newWebBrowser(t, base)
	_, _, page := anon.send(http.MethodGet, "/auth/login", nil)
	for _, want := range []string{"Demo registry, fake data", demo.Password} {
		if !strings.Contains(page, want) {
			t.Errorf("sign-in page is missing %q", want)
		}
	}
	for _, a := range demo.Accounts() {
		if !strings.Contains(page, a.Email) {
			t.Errorf("sign-in page does not list %s", a.Email)
		}
	}
	for _, bad := range []string{"Google", "/auth/signup"} {
		if strings.Contains(page, bad) {
			t.Errorf("sign-in page offers %q", bad)
		}
	}
	if status, _, _ := anon.send(http.MethodGet, "/auth/google/start", nil); status != http.StatusNotFound {
		t.Errorf("GET /auth/google/start = %d, want 404", status)
	}

	pages := []string{"/", "/account", "/skills"}
	for _, ns := range []string{"acme", "lime", "koala", "globex", "pear", "otter"} {
		pages = append(pages, "/t/"+ns, "/t/"+ns+"/memory")
	}
	pages = append(pages, "/t/lime/reviews", "/t/koala/reviews", "/t/pear/reviews", "/t/otter/reviews")
	pages = append(pages, "/t/lime/wiki", "/t/pear/wiki", "/t/lime/settings", "/t/lime/p/lime/planner", "/t/lime/p/lime/dev-loop",
		"/t/lime/p/acme/dev-loop", "/t/koala/p/koala/incident", "/t/koala/p/acme/incident", "/t/pear/p/pear/dev-loop", "/t/otter/p/otter/release",
		"/t/otter/p/globex/release", "/t/lime/p/lime/handbook", "/t/lime/p/lime/dev-course")
	// Where each person belongs: a team they are not in does not exist for them.
	reach := map[string][]string{
		"alice@example.com":  {"acme", "lime", "koala", "globex", "pear"},
		"bogdan@example.com": {"acme", "lime"},
		"olga@example.com":   {"acme", "lime", "koala"},
		"chen@example.com":   {"globex", "pear", "otter"},
		"dana@example.com":   {"globex", "pear"},
		"emil@example.com":   {"globex", "otter"},
	}
	var alice *webBrowser
	for _, a := range demo.Accounts() {
		web := newWebBrowser(t, base)
		web.send(http.MethodGet, "/auth/login", nil)
		if status, loc, body := web.send(http.MethodPost, "/auth/login", url.Values{"email": {a.Email}, "password": {demo.Password}, "next": {"/"}}); status != http.StatusSeeOther {
			t.Fatalf("%s sign in = %d %s\n%s", a.Email, status, loc, body)
		}
		for _, p := range pages {
			status, _, body := web.send(http.MethodGet, p, nil)
			if ns, ok := strings.CutPrefix(p, "/t/"); ok && !slices.Contains(reach[a.Email], strings.SplitN(ns, "/", 2)[0]) {
				continue
			}
			if status != http.StatusOK || !strings.Contains(body, "Demo registry, fake data, resets with docker compose down -v") {
				t.Errorf("%s GET %s = %d, want 200 with the banner", a.Email, p, status)
			}
		}
		if a.Email == "alice@example.com" {
			alice = web
		}
	}

	// The CLI: alice approves loadout login, then installs a package.
	cli := &person{session: newSession(t)}
	var out, errOut bytes.Buffer
	codeRE := regexp.MustCompile(`enter the code ([A-Z]{4}-[A-Z]{4})`)
	approved := false
	approve := func(context.Context, time.Duration) error {
		m := codeRE.FindStringSubmatch(out.String())
		if m == nil || approved {
			return nil
		}
		approved = true
		status, _, page := alice.send(http.MethodPost, "/auth/device", url.Values{"code": {m[1]}, "namespace": {"*"}, "action": {"approve"}})
		if status != http.StatusOK || !strings.Contains(page, "Terminal signed in") {
			t.Errorf("approve = %d\n%s", status, page)
		}
		return nil
	}
	cliEnv := map[string]string{"HOME": cli.home}
	if exit := runWith(t.Context(), []string{"login", "--remote", base}, strings.NewReader(""), &out, &errOut,
		func(k string) string { return cliEnv[k] }, cli.wd, externals{sleep: approve}); exit != 0 {
		t.Fatalf("login: exit %d\nstdout: %s\nstderr: %s", exit, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "as alice@example.com") {
		t.Errorf("login output:\n%s", out.String())
	}
	cli.must("", "install", "lime/planner")
	assertManagedBy(t, filepath.Join(cli.home, ".claude", "agents", "planner.md"), "lime/planner@2.3.0")
}
