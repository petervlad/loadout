package sqlitestore_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/registry"
)

// TestPromptsMigrationKeepsOldPrompts opens a database from before editable
// prompts (schema 10) and checks that each proposal's JSON prompts read
// back in order, credited to the proposal's author at its creation time.
func TestPromptsMigrationKeepsOldPrompts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"0001_init.sql", "0002_namespaces.sql", "0003_proposals.sql", "0004_accounts.sql", "0005_token_creator.sql",
		"0006_token_expiry.sql", "0007_code_failures.sql", "0008_review_data.sql", "0009_package_kind.sql", "0010_self_approval.sql",
	} {
		b, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.ExecContext(t.Context(), string(b)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	const insert = `INSERT INTO proposals (id, team, name, version, major, minor, patch, digest, description, size,
		author_token_id, author_label, author_user_id, note, prompts, status, created_at, bundle)
		VALUES (?, 'lime', 'planner', ?, 0, ?, 0, 'd', '', 1, 'tok-alice', 'alice@example.com via loadout login', 'u-alice', '', ?, ?, ?, x'00')`
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{"PRAGMA user_version = 10", nil},
		{insert, []any{"p1", "0.2.0", 2, `["plan TRK-1","say \"why\"\nin two lines"]`, "open", "2026-09-28T12:00:00.000000000Z"}},
		{insert, []any{"p2", "0.3.0", 3, `["only one"]`, "rejected", "2026-09-28T13:00:00.000000000Z"}},
	} {
		if _, err := raw.ExecContext(t.Context(), stmt.q, stmt.args...); err != nil {
			t.Fatalf("exec %q: %v", stmt.q, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s := open(t, path, nil)
	alice := registry.Actor{TokenID: "tok-alice", Label: "alice@example.com via loadout login", UserID: "u-alice"}
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for id, want := range map[string][]registry.Prompt{
		"p1": {
			{Seq: 1, Text: "plan TRK-1", Author: alice, CreatedAt: at},
			{Seq: 2, Text: "say \"why\"\nin two lines", Author: alice, CreatedAt: at},
		},
		"p2": {{Seq: 1, Text: "only one", Author: alice, CreatedAt: at.Add(time.Hour)}},
	} {
		p, err := s.Proposal(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, p.Prompts); diff != "" || p.RemovedPrompts != nil {
			t.Errorf("Proposal(%s).Prompts after migration mismatch (-want +got):\n%s\nremoved %+v", id, diff, p.RemovedPrompts)
		}
	}

	// The old column keeps following the active texts, for a rollback.
	if _, err := s.AddPrompt(t.Context(), "p1", registry.Prompt{Text: "a third", Author: alice, CreatedAt: at}, 20, 60); err != nil {
		t.Fatal(err)
	}
	if err := s.RemovePrompt(t.Context(), "p1", 1, alice, at); err != nil {
		t.Fatal(err)
	}
	var old string
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := raw.QueryRowContext(t.Context(), `SELECT prompts FROM proposals WHERE id = 'p1'`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if want := `["say \"why\"\nin two lines","a third"]`; old != want {
		t.Errorf("old prompts column = %s, want %s", old, want)
	}
}
