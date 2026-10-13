package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
)

// TestMemoryPurge: on the host, purge shows the memory and stops without
// --yes, removes it with --yes, names the backups made since it was saved
// and no others, and the log records the purge without the text.
func TestMemoryPurge(t *testing.T) {
	admin := &person{session: newSession(t)}
	cfg, err := config.Load(func(k string) string { return map[string]string{"HOME": admin.home}[k] })
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(cfg.StateDir, "server")
	const marker = "SECRET-MARKER-7f3a"

	created := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	seed(t, data, created, marker)

	backups := t.TempDir()
	stamp := func(at time.Time) string { return "loadout-" + at.Format("20060102T150405Z") + ".tar.gz" }
	before := stamp(created.Add(-time.Hour))
	after := stamp(created.Add(time.Hour))
	later := stamp(created.Add(24 * time.Hour))
	future := stamp(time.Now().UTC().Add(48 * time.Hour))
	for _, n := range []string{before, after, later, future, "unrelated.txt"} {
		if err := os.WriteFile(filepath.Join(backups, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out := admin.must("", "memory", "purge", "--id", "1", "--data", data)
	for _, want := range []string{"Memory 1 in lime", "dana@example.com", "2 versions", "Nothing was purged"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, marker) {
		t.Errorf("preview shows the text:\n%s", out)
	}
	if _, _, code := admin.runIn("", "memory", "purge", "--id", "1", "--data", data); code != 0 {
		t.Fatalf("second preview: exit %d, the memory must still be there", code)
	}

	out = admin.must("", "memory", "purge", "--id", "1", "--data", data, "--yes", "--backups", backups)
	for _, want := range []string{"Purged memory 1 and its 2 versions", after, later} {
		if !strings.Contains(out, want) {
			t.Errorf("purge output lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{before, future, "unrelated.txt"} {
		if strings.Contains(out, not) {
			t.Errorf("purge output lists %q:\n%s", not, out)
		}
	}
	if !strings.HasSuffix(out, "Copies made off this machine are not listed. Rotate the secret.\n") {
		t.Errorf("purge output does not end with the warning:\n%s", out)
	}

	// A purged memory can be purged again: it cleans the files once more.
	if again := admin.must("", "memory", "purge", "--id", "1", "--data", data, "--yes", "--backups", backups); !strings.Contains(again, "already purged") {
		t.Errorf("purging a purged memory again:\n%s", again)
	}
	if _, errOut, code := admin.runIn("", "memory", "purge", "--id", "99", "--data", data, "--yes"); code == 0 {
		t.Error("purging a memory that never existed: want an error")
	} else if strings.Contains(errOut, marker) {
		t.Errorf("error shows the text: %s", errOut)
	}

	logText, err := os.ReadFile(cfg.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"msg":"memory entry purged"`, `"namespace":"lime"`, `"versions":2`} {
		if !strings.Contains(string(logText), want) {
			t.Errorf("log lacks %q", want)
		}
	}
	if strings.Contains(string(logText), marker) {
		t.Error("the log holds the text of the memory")
	}
}

// seed saves one memory with two versions in the data folder's memory.db.
func seed(t *testing.T, data string, at time.Time, marker string) {
	t.Helper()
	store, err := sqlitestore.OpenMemory(t.Context(), filepath.Join(data, memoryDBFile), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close memory store: %v", err)
		}
	}()
	dana := memory.Caller{UserID: "u1", Label: "dana@example.com"}
	d, err := memory.NewDraft("lime", memory.TypeReference, "Host "+marker, "where "+marker, "body "+marker, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.Insert(t.Context(), d, dana, at)
	if err != nil {
		t.Fatal(err)
	}
	body := "body two " + marker
	rev, err := memory.Change{Body: &body}.Revise(e, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(t.Context(), e.ID, e.Version, rev, dana, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}
