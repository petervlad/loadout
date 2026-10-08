package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (p *person) writeFile(name, text string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.wd, name), []byte(text), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func (p *person) readFile(name string) string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.wd, name))
	if err != nil {
		p.t.Fatal(err)
	}
	return string(data)
}

// TestWikiPullAndPush: two people edit lime's wiki from the CLI. The
// second save on the same revision is refused with how to merge, a pull
// never replaces an edit, and the history names both.
func TestWikiPullAndPush(t *testing.T) {
	_, base, aliceWeb, olgaWeb := ownedRegistry(t)
	alice := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, alice, aliceWeb, "--remote", base); code != 0 {
		t.Fatalf("alice login: exit %d\n%s\n%s", code, out, errOut)
	}
	olga := &person{session: newSession(t)}
	if out, errOut, code := loginAs(t, olga, olgaWeb, "--remote", base); code != 0 {
		t.Fatalf("olga login: exit %d\n%s\n%s", code, out, errOut)
	}

	alice.writeFile("wiki.md", "# Lime\n")
	if _, errOut, code := alice.runIn("", "wiki", "push", "wiki.md", "--team", "lime"); code == 0 || !strings.Contains(errOut, "an owner turns it on") {
		t.Errorf("push before the switch: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := alice.runIn("", "namespace", "set", "lime", "--wiki", "on"); code == 0 || !strings.Contains(errOut, "needs an owner") {
		t.Errorf("alice turns the wiki on: exit %d, stderr %q", code, errOut)
	}
	olga.must("", "namespace", "set", "lime", "--wiki", "on")

	// alice starts the page: the empty pull creates the file.
	if err := os.Remove(filepath.Join(alice.wd, "wiki.md")); err != nil {
		t.Fatal(err)
	}
	if out := alice.must("", "wiki", "pull", "--team", "lime"); !strings.Contains(out, "no wiki page yet") || alice.readFile("wiki.md") != "" {
		t.Errorf("pull of the empty page = %q", out)
	}
	alice.writeFile("wiki.md", "# Lime\n\nAsk alice.\n")
	if out := alice.must("", "wiki", "push", "wiki.md", "--team", "lime", "--note", "First"); !strings.Contains(out, "revision 1") {
		t.Errorf("first push = %q", out)
	}

	// olga edits on top of it.
	olga.must("", "wiki", "pull", "--team", "lime")
	olga.writeFile("wiki.md", "# Lime\n\nAsk alice or olga.\n")
	if out := olga.must("", "wiki", "push", "wiki.md", "--team", "lime"); !strings.Contains(out, "revision 2") {
		t.Errorf("olga's push = %q", out)
	}

	// alice's copy is from revision 1: her push is refused and her text kept.
	mine := "# Lime\n\nAsk alice.\n\n## Releases\n"
	alice.writeFile("wiki.md", mine)
	_, errOut, code := alice.runIn("", "wiki", "push", "wiki.md", "--team", "lime")
	hint := "Run loadout wiki pull theirs.md --team lime, merge it into wiki.md, then loadout wiki push wiki.md --team lime --base 2"
	if code == 0 || !strings.Contains(errOut, hint) || !strings.Contains(errOut, "olga@example.com") {
		t.Errorf("stale push: exit %d, stderr %q", code, errOut)
	}
	if got := alice.readFile("wiki.md"); got != mine {
		t.Errorf("stale push changed wiki.md to %q", got)
	}
	if _, errOut, code := alice.runIn("", "wiki", "pull", "--team", "lime"); code == 0 || !strings.Contains(errOut, "wiki.md has changes that are not on the registry. Push them first") {
		t.Errorf("pull over an edit: exit %d, stderr %q", code, errOut)
	}
	if got := alice.readFile("wiki.md"); got != mine {
		t.Errorf("refused pull changed wiki.md to %q", got)
	}

	// She merges theirs into hers and pushes on their revision.
	alice.must("", "wiki", "pull", "theirs.md", "--team", "lime")
	alice.writeFile("wiki.md", "# Lime\n\nAsk alice or olga.\n\n## Releases\n")
	if out := alice.must("", "wiki", "push", "wiki.md", "--team", "lime", "--base", "2"); !strings.Contains(out, "revision 3") {
		t.Errorf("merged push = %q", out)
	}
	if out := alice.must("", "wiki", "push", "wiki.md", "--team", "lime"); !strings.Contains(out, "Nothing changed: revision 3 is the same text.") {
		t.Errorf("unchanged push = %q", out)
	}

	log := alice.must("", "wiki", "log", "--team", "lime")
	if lines := strings.Split(strings.TrimSpace(log), "\n"); len(lines) != 4 || !strings.HasPrefix(lines[1], "3 ") || !strings.HasPrefix(lines[3], "1 ") ||
		!strings.Contains(log, "alice@example.com") || !strings.Contains(log, "olga@example.com") || !strings.Contains(log, "First") {
		t.Errorf("wiki log =\n%s", log)
	}
	if out := olga.must("", "wiki", "show", "--team", "lime", "--revision", "1"); out != "# Lime\n\nAsk alice.\n" {
		t.Errorf("show --revision 1 = %q", out)
	}

	// A page over the limit is refused on this machine.
	alice.writeFile("big.md", strings.Repeat("x", 300<<10))
	if _, errOut, code := alice.runIn("", "wiki", "push", "big.md", "--team", "lime"); code == 0 || !strings.Contains(errOut, "big.md: the page is too large") || strings.Contains(errOut, "the wiki of lime") {
		t.Errorf("300 KB push: exit %d, stderr %q", code, errOut)
	}

	// Off hides the page; on again shows it as it was.
	olga.must("", "namespace", "set", "lime", "--wiki", "off")
	if _, errOut, code := olga.runIn("", "wiki", "show", "--team", "lime"); code == 0 || !strings.Contains(errOut, "an owner turns it on") {
		t.Errorf("show while off: exit %d, stderr %q", code, errOut)
	}
	olga.must("", "namespace", "set", "lime", "--wiki", "on")
	if out := olga.must("", "wiki", "show", "--team", "lime"); !strings.Contains(out, "## Releases") {
		t.Errorf("show after on = %q", out)
	}

	// An .mdx file stores MDX, and a pull without FILE then writes wiki.mdx.
	alice.writeFile("page.mdx", "import Tabs from './tabs'\n\n# Lime\n")
	if out := alice.must("", "wiki", "push", "page.mdx", "--team", "lime", "--base", "3"); !strings.Contains(out, "revision 4") {
		t.Errorf("mdx push = %q", out)
	}
	elsewhere := *olga.session
	elsewhere.wd = t.TempDir()
	fresh := &person{session: &elsewhere}
	if out := fresh.must("", "wiki", "pull", "--team", "lime"); !strings.Contains(out, "wiki.mdx") {
		t.Errorf("pull of an MDX page = %q", out)
	}
	if got := fresh.readFile("wiki.mdx"); !strings.HasPrefix(got, "import Tabs") {
		t.Errorf("wiki.mdx = %q", got)
	}
}
