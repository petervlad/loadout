package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memoryFile is a personal memory file, in the shape an agent keeps them.
func memoryFile(kind, name, description, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\ntype: " + kind + "\n---\n\n" + body + "\n"
}

// writeMemoryDir makes a folder of files, in name order.
func writeMemoryDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestMemoryImport: only the entries the person selects are saved. A file of
// type user is never offered, a file holding a key is refused on this machine
// and its text never reaches the server, a likely duplicate is skipped until
// --new, and without a terminal or --select nothing is saved.
func TestMemoryImport(t *testing.T) {
	host, alice, base, token := memoryTeam(t)
	const key = "AKIA" + "J4T6QWERTYUI3ZXC"
	dir := writeMemoryDir(t, map[string]string{
		"a-tracker.md": memoryFile("reference", "TRK Tracker host", "TRK tickets live on tracker.example", "Open them at tracker.example, never at atc."),
		"b-label.md":   memoryFile("project", "Release PR label", "Release PRs need the release label", "Without the label the publish job does nothing."),
		"c-style.md":   memoryFile("feedback", "Keep answers short", "How the author likes answers", "I prefer short answers."),
		"d-person.md":  memoryFile("user", "About me", "Who the author is", "Senior engineer, likes Go."),
		"e-key.md":     memoryFile("reference", "Build account", "Where builds run", "Use access key "+key+" for the build account."),
		"MEMORY.md":    "- [a](a-tracker.md)\n",
		"notes.txt":    "not a memory",
	})
	direct := overHTTP(t, base, "lime", token)
	recent := func() string { return direct.call(t, "recent", map[string]any{"days": 1}) }

	// No terminal and no --select: the list, and nothing saved.
	out := alice.must("", "memory", "import", dir, "--team", "lime")
	for _, want := range []string{
		"  1  [x]  reference TRK Tracker host",
		"  2  [x]  project   Release PR label",
		"  3  [ ]  feedback  Keep answers short",
		"e-key.md: refused, aws-access-key-id in the body",
		"Skipped d-person.md: type user is about a person and is never imported.",
		"Nothing was saved.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, key) {
		t.Errorf("the list shows the key:\n%s", out)
	}
	if got := recent(); strings.Contains(got, "TRK") {
		t.Fatalf("listing saved something: %s", got)
	}

	// --select saves exactly those numbers. 4 is the file with the key.
	out = alice.must("", "memory", "import", dir, "--team", "lime", "--select", "1,3,4")
	for _, want := range []string{"Saved a-tracker.md to lime's memory as", "Saved c-style.md", "Refused e-key.md: aws-access-key-id in the body at line", "Nothing was sent.", "2 of 3 saved."} {
		if !strings.Contains(out, want) {
			t.Errorf("import output lacks %q:\n%s", want, out)
		}
	}
	got := recent()
	if !strings.Contains(got, "TRK Tracker host") || !strings.Contains(got, "Keep answers short") {
		t.Errorf("recent = %s, want the two selected entries", got)
	}
	for _, not := range []string{"Release PR label", "About me", "Build account", key} {
		if strings.Contains(got, not) {
			t.Errorf("recent holds %q, which was not selected or must not be sent", not)
		}
	}
	if !strings.Contains(got, "alice@example.com") {
		t.Errorf("recent = %s, want the entry attributed to its author", got)
	}

	// The key was never on the wire: the server's files do not hold it.
	serverDir := filepath.Join(host.home, ".loadout", "server")
	files, err := filepath.Glob(filepath.Join(serverDir, "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("server files = %v, %v", files, err)
	}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), key) {
			t.Errorf("%s holds the key", f)
		}
	}

	// The same entry again is a likely duplicate: skipped, then saved with --new.
	out = alice.must("", "memory", "import", dir, "--team", "lime", "--select", "1")
	if !strings.Contains(out, "Not saved, a-tracker.md looks like [") || !strings.Contains(out, "Use --new") || !strings.Contains(out, "0 of 1 saved.") {
		t.Errorf("duplicate import = %s", out)
	}
	out = alice.must("", "memory", "import", dir, "--team", "lime", "--select", "1", "--new")
	if !strings.Contains(out, "Saved a-tracker.md") || !strings.Contains(out, "1 of 1 saved.") {
		t.Errorf("import --new = %s", out)
	}

	// Bad input is refused before anything is sent.
	if _, errOut, code := alice.runIn("", "memory", "import", dir, "--team", "lime", "--select", "9"); code == 0 || !strings.Contains(errOut, "there is no entry number 9") {
		t.Errorf("--select 9: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := alice.runIn("", "memory", "import", dir, "--team", "lime", "--select", "x"); code == 0 || !strings.Contains(errOut, "is not an entry number") {
		t.Errorf("--select x: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := alice.runIn("", "memory", "import", dir); code == 0 || !strings.Contains(errOut, "team") {
		t.Errorf("no --team: exit %d, stderr %q", code, errOut)
	}
}
