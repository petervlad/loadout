package sqlitestore_test

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
)

func openMemory(t *testing.T, path string) *sqlitestore.MemoryStore {
	t.Helper()
	s, err := sqlitestore.OpenMemory(t.Context(), path, nil)
	if err != nil {
		t.Fatalf("OpenMemory(%s) error: %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error: %v", err)
		}
	})
	return s
}

func newMemoryStore(t *testing.T) *sqlitestore.MemoryStore {
	t.Helper()
	return openMemory(t, filepath.Join(t.TempDir(), "memory.db"))
}

func TestMemoryStoreContract(t *testing.T) {
	memorytest.RunStoreContract(t, func(t *testing.T) memory.Store {
		t.Helper()
		return newMemoryStore(t)
	})
}

func TestMemoryTokenStoreContract(t *testing.T) {
	memorytest.RunTokenStoreContract(t, func(t *testing.T) memory.TokenStore {
		t.Helper()
		return newMemoryStore(t)
	})
}

func TestMemorySwitchContract(t *testing.T) {
	memorytest.RunSwitchContract(t, func(t *testing.T) memory.SwitchStore {
		t.Helper()
		return newMemoryStore(t).Switches()
	})
}

var (
	testAt = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	dana   = memory.Caller{UserID: "u-dana", Label: "dana@example.com"}
)

func insert(t *testing.T, s *sqlitestore.MemoryStore, name, desc, body string, when time.Time) memory.Entry {
	t.Helper()
	d, err := memory.NewDraft("lime", memory.TypeReference, name, desc, body, nil)
	if err != nil {
		t.Fatalf("NewDraft(%s) error: %v", name, err)
	}
	e, err := s.Insert(t.Context(), d, dana, when)
	if err != nil {
		t.Fatalf("Insert(%s) error: %v", name, err)
	}
	return e
}

// TestSearchRanking pins the bm25 weights: a hit in the name beats one in the
// description, which beats one in the body.
func TestSearchRanking(t *testing.T) {
	s := newMemoryStore(t)
	// The newest entry is the body hit, so only the weights can put it last.
	inName := insert(t, s, "Zuul gate", "pipelines", "nothing here", testAt)
	inDesc := insert(t, s, "Pipelines", "how the zuul gate works", "nothing here", testAt.Add(time.Hour))
	inBody := insert(t, s, "Pipelines", "other", "the zuul gate runs after check", testAt.Add(2*time.Hour))

	got, err := s.Search(t.Context(), memory.Query{Text: "zuul", Namespaces: []string{"lime"}, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	var order []int64
	for _, e := range got {
		order = append(order, e.ID)
	}
	if want := []int64{inName.ID, inDesc.ID, inBody.ID}; !slices.Equal(order, want) {
		t.Errorf("Search(zuul) = %v, want %v: name, description, body", order, want)
	}

	// Porter stemming: a plural finds the singular.
	got, _ = s.Search(t.Context(), memory.Query{Text: "pipeline", Namespaces: []string{"lime"}, Limit: 10})
	if len(got) != 3 {
		t.Errorf("Search(pipeline) found %d, want 3 through stemming", len(got))
	}
}

// TestPurgeLeavesNoBytes checks the raw files after a purge: the text of the
// entry, in the table and in the full-text index, is in neither memory.db nor
// its write-ahead log.
func TestPurgeLeavesNoBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s := openMemory(t, path)
	const (
		raw  = "PURGE-MARKER-7f3a9c41"
		word = "quixoticzebrafoo"
	)
	keep := insert(t, s, "Keeper", "unrelated", "stays", testAt)
	victim := insert(t, s, word+" name", "desc "+raw, "body "+raw+" "+word, testAt)
	body := "second version " + raw + " " + word
	rev, err := memory.Change{Body: &body}.Revise(victim, testAt)
	if err != nil {
		t.Fatalf("Revise() error: %v", err)
	}
	if _, err := s.Update(t.Context(), victim.ID, victim.Version, rev, dana, testAt.Add(time.Hour)); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	held := func() (inDB, inWAL bool) {
		db, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		wal, err := os.ReadFile(path + "-wal")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		both := func(b []byte) bool { return bytes.Contains(b, []byte(raw)) || bytes.Contains(b, []byte(word)) }
		return both(db), both(wal)
	}
	if inDB, inWAL := held(); !inDB && !inWAL {
		t.Fatal("the marker is in neither file before the purge, so the test proves nothing")
	}
	if _, err := s.Purge(t.Context(), victim.ID, testAt.Add(2*time.Hour)); err != nil {
		t.Fatalf("Purge() error: %v", err)
	}
	if inDB, inWAL := held(); inDB || inWAL {
		t.Errorf("after Purge() the marker is in memory.db: %v, in memory.db-wal: %v", inDB, inWAL)
	}
	if _, err := s.Get(t.Context(), keep.ID); err != nil {
		t.Errorf("Purge() damaged another entry: %v", err)
	}
}

// TestScrubAfterBusyPurge: a reader that holds the log open makes the purge
// report that it must be run again. The entry is gone then, and Scrub, which
// the second run uses, finishes the job once the reader has left.
func TestScrubAfterBusyPurge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s := openMemory(t, path)
	const raw = "BUSY-MARKER-31c0aa17"
	victim := insert(t, s, "Busy "+raw, "d", "body "+raw, testAt)

	reader, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM entries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// A write after the reader's snapshot starts keeps the log from emptying.
	insert(t, s, "Later", "d", "b", testAt)

	_, err = s.Purge(t.Context(), victim.ID, testAt.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "again") {
		t.Fatalf("Purge() with a reader holding the log = %v, want the run-again message", err)
	}
	if _, err := s.Get(t.Context(), victim.ID); err == nil {
		t.Error("the entry survived a purge that reported the busy log")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := s.Scrub(t.Context()); err != nil {
		t.Fatalf("Scrub() after the reader left: %v", err)
	}
	if wal, err := os.ReadFile(path + "-wal"); err == nil && bytes.Contains(wal, []byte(raw)) {
		t.Error("the write-ahead log still holds the text after Scrub")
	}
}

// TestRemoveNamespaceLeavesNoBytes: deleting a namespace takes its text out of
// memory.db and its log too, and its switch row with it.
func TestRemoveNamespaceLeavesNoBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s := openMemory(t, path)
	const raw = "NAMESPACE-MARKER-5d21e8b0"
	insert(t, s, "Doomed "+raw, "desc "+raw, "body "+raw, testAt)
	if err := s.Switches().Set(t.Context(), "lime", true, "u1", testAt); err != nil {
		t.Fatal(err)
	}
	read := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return b
	}
	if !bytes.Contains(read(path), []byte(raw)) && !bytes.Contains(read(path+"-wal"), []byte(raw)) {
		t.Fatal("the marker is in neither file before the removal, so the test proves nothing")
	}
	if _, err := s.RemoveNamespace(t.Context(), "lime"); err != nil {
		t.Fatalf("RemoveNamespace() error: %v", err)
	}
	if err := s.Switches().Remove(t.Context(), "lime"); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}
	for _, p := range []string{path, path + "-wal"} {
		if bytes.Contains(read(p), []byte(raw)) {
			t.Errorf("%s still holds the text of the removed namespace", filepath.Base(p))
		}
	}
	if sp, _ := s.Switches().Get(t.Context(), "lime"); sp.Enabled || !sp.ChangedAt.IsZero() {
		t.Errorf("Get() after Remove = %+v, want a namespace never set", sp)
	}
}

// TestMemoryHasItsOwnVersion checks memory.db counts its migrations from 1,
// whatever registry.db is at.
func TestMemoryHasItsOwnVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.db")
	s := openMemory(t, path)
	open(t, filepath.Join(dir, "registry.db"), nil)
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var v int
	if err := raw.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&v); err != nil || v != 1 {
		t.Errorf("memory.db user_version = %d, %v, want 1", v, err)
	}
	if _, err := s.Get(t.Context(), 1); err == nil {
		t.Error("a new memory.db has an entry")
	}
}

func TestMemoryRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := sqlitestore.OpenMemory(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(t.Context(), "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = sqlitestore.OpenMemory(t.Context(), path, nil)
	if !errors.Is(err, sqlitestore.ErrSchemaTooNew) {
		t.Fatalf("OpenMemory(newer schema) error = %v, want ErrSchemaTooNew", err)
	}
}

func TestMemoryFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "memory.db")
	openMemory(t, path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("memory.db mode = %o, want 600", got)
	}
}
