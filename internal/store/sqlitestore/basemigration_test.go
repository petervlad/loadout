package sqlitestore_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
)

// Times of the schema 13 database below, in order.
const (
	t0 = "2026-09-01T12:00:00.000000000Z" // before any release
	t1 = "2026-09-02T12:00:00.000000000Z" // planner 1.0.0, published directly
	t2 = "2026-09-03T12:00:00.000000000Z" // between the releases
	t3 = "2026-09-04T12:00:00.000000000Z" // planner 2.0.0, approved from p-between
	t4 = "2026-09-05T12:00:00.000000000Z" // after both
)

// stmt is one statement with its arguments.
type stmt struct {
	q    string
	args []any
}

// openAt13 makes a database at schema version 13 (before proposal bases
// and release publishers), runs stmts on it and opens it with the current
// migrations.
func openAt13(t *testing.T, stmts ...stmt) *sqlitestore.Store {
	t.Helper()
	return openAt(t, 13, stmts...)
}

// openAt makes a database at schema version n, runs stmts on it and
// opens it with the current migrations.
func openAt(t *testing.T, n int, stmts ...stmt) *sqlitestore.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join("migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files[:n] {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.ExecContext(t.Context(), string(b)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	for _, st := range append([]stmt{{q: fmt.Sprintf("PRAGMA user_version = %d", n)}}, stmts...) {
		if _, err := raw.ExecContext(t.Context(), st.q, st.args...); err != nil {
			t.Fatalf("exec %q: %v", st.q, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	return open(t, path, nil)
}

// release13 inserts a release of lime/name the way schema 13 stores it;
// proposal is the id it was approved from, "" for a direct publish.
func release13(name, version, at, proposal string) stmt {
	major, minor, patch := parts(version)
	return stmt{
		`INSERT INTO releases (team, name, version, major, minor, patch, digest, description, size, published_at, bundle, proposal_id)
		VALUES ('lime', ?, ?, ?, ?, ?, 'd', '', 1, ?, x'00', ?)`,
		[]any{name, version, major, minor, patch, at, proposal},
	}
}

// proposal13 inserts a proposal of lime/name by alice; a decided one was
// decided by bob at decided.
func proposal13(id, name, version, status, created, decided string) stmt {
	major, minor, patch := parts(version)
	by := [3]string{}
	if decided != "" {
		by = [3]string{"tok-bob", "bob@example.com", "u-bob"}
	}
	return stmt{
		`INSERT INTO proposals (id, team, name, version, major, minor, patch, digest, description, size,
			author_token_id, author_label, author_user_id, note, prompts, status, created_at, decided_at,
			decided_by_token_id, decided_by_label, decided_by_user_id, bundle)
		VALUES (?, 'lime', ?, ?, ?, ?, ?, 'd', '', 1, 'tok-alice', 'alice@example.com', 'u-alice', '', '[]', ?, ?, ?, ?, ?, ?, x'00')`,
		[]any{id, name, version, major, minor, patch, status, created, decided, by[0], by[1], by[2]},
	}
}

// parts splits "1.2.3" into the integer columns.
func parts(version string) (major, minor, patch int) {
	if _, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil {
		panic(err)
	}
	return major, minor, patch
}

// TestBaseMigrationBackfillsBases opens a schema 13 database with releases
// 1.0.0 and 2.0.0 and proposals made before, between and after them, and
// checks each proposal's base is the newest release published before it.
func TestBaseMigrationBackfillsBases(t *testing.T) {
	s := openAt13(t,
		release13("planner", "1.0.0", t1, ""),
		release13("planner", "2.0.0", t3, "p-between"),
		proposal13("p-before", "planner", "0.9.0", "rejected", t0, t1),
		// Approved into 2.0.0, which is later than the proposal: never its own base.
		proposal13("p-between", "planner", "2.0.0", "approved", t2, t3),
		proposal13("p-after", "planner", "3.0.0", "open", t4, ""),
		proposal13("p-first", "reviewer", "0.1.0", "open", t4, ""),
	)
	for id, want := range map[string]string{"p-before": "", "p-between": "1.0.0", "p-after": "2.0.0", "p-first": ""} {
		p, err := s.Proposal(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Base.String() != want || !p.ApprovedOver.IsZero() || p.Replaces != "" || p.ReplacedBy != "" {
			t.Errorf("Proposal(%s) after migration = base %q, over %q, replaces %q, replaced by %q; want base %q and nothing else",
				id, p.Base, p.ApprovedOver, p.Replaces, p.ReplacedBy, want)
		}
	}
}

// TestBaseMigrationBackfillsPublishers checks that after the migration a
// direct release has no known publisher, a release approved from a proposal
// was published by whoever approved it, and a release naming a proposal
// that is gone keeps none rather than failing the migration.
func TestBaseMigrationBackfillsPublishers(t *testing.T) {
	s := openAt13(t,
		release13("planner", "1.0.0", t1, ""),
		release13("planner", "2.0.0", t3, "p-between"),
		release13("writer", "1.0.0", t3, "p-gone"),
		proposal13("p-between", "planner", "2.0.0", "approved", t2, t3),
	)
	bob := registry.Actor{TokenID: "tok-bob", Label: "bob@example.com", UserID: "u-bob"}
	for _, tc := range []struct {
		name, version string
		want          registry.Actor
	}{
		{"planner", "1.0.0", registry.Actor{}},
		{"planner", "2.0.0", bob},
		{"writer", "1.0.0", registry.Actor{}},
	} {
		id, err := pack.NewID("lime", tc.name)
		if err != nil {
			t.Fatal(err)
		}
		v, err := semver.Parse(tc.version)
		if err != nil {
			t.Fatal(err)
		}
		rel, _, err := s.Release(t.Context(), id, v)
		if err != nil {
			t.Fatal(err)
		}
		if rel.PublishedBy != tc.want {
			t.Errorf("Release(%s@%s).PublishedBy after migration = %+v, want %+v", id, v, rel.PublishedBy, tc.want)
		}
	}
}
