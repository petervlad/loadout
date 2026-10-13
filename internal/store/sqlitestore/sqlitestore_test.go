package sqlitestore_test

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/account/accounttest"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
)

func open(t *testing.T, path string, log *slog.Logger) *sqlitestore.Store {
	t.Helper()
	s, err := sqlitestore.Open(t.Context(), path, log)
	if err != nil {
		t.Fatalf("Open(%s) error: %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error: %v", err)
		}
	})
	return s
}

func newStore(t *testing.T) *sqlitestore.Store {
	t.Helper()
	return open(t, filepath.Join(t.TempDir(), "registry.db"), nil)
}

func TestStoreContract(t *testing.T) {
	registrytest.RunStoreContract(t, func(t *testing.T) registry.Store {
		t.Helper()
		return newStore(t)
	})
}

func TestTokenStoreContract(t *testing.T) {
	registrytest.RunTokenStoreContract(t, func(t *testing.T) registry.TokenStore {
		t.Helper()
		return newStore(t)
	})
}

func TestNamespaceStoreContract(t *testing.T) {
	registrytest.RunNamespaceStoreContract(t, func(t *testing.T) registry.NamespaceStore {
		t.Helper()
		return newStore(t)
	})
}

func TestAccountStoreContract(t *testing.T) {
	accounttest.RunStoreContract(t, func(t *testing.T) accounttest.Backend {
		t.Helper()
		s := newStore(t)
		return accounttest.Backend{Store: s, Namespaces: s}
	})
}

func TestPackageDeleteContract(t *testing.T) {
	registrytest.RunPackageDeleteContract(t, func(t *testing.T) registrytest.DeleteBackend {
		t.Helper()
		return newStore(t)
	})
}

func TestDeletionContract(t *testing.T) {
	registrytest.RunDeletionContract(t, func(t *testing.T) registrytest.DeletionBackend {
		t.Helper()
		return newStore(t)
	})
}

func TestCountStoreContract(t *testing.T) {
	registrytest.RunCountStoreContract(t, func(t *testing.T) registrytest.CountBackend {
		t.Helper()
		return newStore(t)
	})
	accounttest.RunCountStoreContract(t, func(t *testing.T) accounttest.Backend {
		t.Helper()
		s := newStore(t)
		return accounttest.Backend{Store: s, Namespaces: s}
	})
}

func TestWikiStoreContract(t *testing.T) {
	registrytest.RunWikiStoreContract(t, func(t *testing.T) registrytest.WikiBackend {
		t.Helper()
		return newStore(t)
	})
}

func TestProposalStoreContract(t *testing.T) {
	registrytest.RunProposalStoreContract(t, func(t *testing.T) registrytest.ProposalBackend {
		t.Helper()
		return newStore(t)
	})
}

// TestNamespacesMigrationKeepsExistingTeams opens a database made by the
// first schema version and checks that its teams become root namespaces.
func TestNamespacesMigrationKeepsExistingTeams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	init, err := os.ReadFile(filepath.Join("migrations", "0001_init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		string(init),
		"PRAGMA user_version = 1",
		"INSERT INTO tokens (id, hash, team, scope, label, created_at) VALUES ('t1', x'01', 'lime', 'read', '', '2026-09-28T12:00:00.000000000Z')",
		"INSERT INTO releases (team, name, version, major, minor, patch, digest, description, size, published_at, bundle) VALUES ('koala', 'p', '1.0.0', 1, 0, 0, 'd', '', 1, '2026-09-28T12:00:00.000000000Z', x'00')",
	} {
		if _, err := raw.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s := open(t, path, nil)
	got, err := s.Namespaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "koala" || got[1].Name != "lime" {
		t.Fatalf("Namespaces() after migration = %+v, want koala and lime", got)
	}
	for _, ns := range got {
		if ns.Parent != "" || ns.Kind != registry.KindTeam {
			t.Errorf("migrated namespace %+v, want a root team", ns)
		}
	}
}

// TestWikiMigrationKeepsNamespaces opens a database at schema version 12
// with namespaces and checks that their wikis start off and empty.
func TestWikiMigrationKeepsNamespaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join("migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files[:12] {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.ExecContext(t.Context(), string(b)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	for _, stmt := range []string{
		"PRAGMA user_version = 12",
		"INSERT INTO namespaces (name, parent, kind, created_at, require_review) VALUES ('acme', NULL, 'project', '2026-09-28T12:00:00.000000000Z', 1)",
		"INSERT INTO namespaces (name, parent, kind, created_at, require_review) VALUES ('lime', 'acme', 'team', '2026-09-28T12:00:00.000000000Z', 0)",
	} {
		if _, err := raw.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s := open(t, path, nil)
	got, err := s.Namespaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].RequireReview || got[1].Parent != "acme" {
		t.Fatalf("Namespaces() after migration = %+v, want acme and lime as they were", got)
	}
	for _, ns := range got {
		if ns.Wiki {
			t.Errorf("migrated namespace %s has its wiki on, want off", ns.Name)
		}
		if revs, err := s.WikiRevisions(t.Context(), ns.Name); err != nil || len(revs) != 0 {
			t.Errorf("WikiRevisions(%s) after migration = %+v, %v; want none", ns.Name, revs, err)
		}
	}
}

func TestParentMustExist(t *testing.T) {
	s := newStore(t)
	err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Parent: "ghost", Kind: registry.KindTeam})
	if err == nil {
		t.Error("CreateNamespace() with a missing parent succeeded, want a foreign key error")
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s, err := sqlitestore.Open(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := registrytest.Release(t, "lime", "planner", "1.2.3")
	put, err := s.PutRelease(t.Context(), in, []byte("bundle"))
	if err != nil {
		t.Fatal(err)
	}
	tok := registry.Token{ID: "t1", Team: "lime", Scope: registry.ScopePublish, Label: "ci", CreatedAt: in.PublishedAt}
	if err := s.CreateToken(t.Context(), tok, []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = open(t, path, nil)
	rel, data, err := s.Release(t.Context(), in.ID, in.Version)
	if err != nil {
		t.Fatalf("Release() after reopen error: %v", err)
	}
	if rel.Seq != put.Seq || !bytes.Equal(data, []byte("bundle")) || !rel.PublishedAt.Equal(in.PublishedAt) {
		t.Errorf("Release() after reopen = %+v, %q; want Seq %d, %q", rel, data, put.Seq, "bundle")
	}
	got, err := s.TokenByHash(t.Context(), []byte("hash"))
	if err != nil || got.ID != "t1" || got.Scope != registry.ScopePublish {
		t.Errorf("TokenByHash() after reopen = %+v, %v; want t1 with publish scope", got, err)
	}
	// Seq keeps growing after a reopen.
	next, err := s.PutRelease(t.Context(), registrytest.Release(t, "lime", "planner", "1.2.4"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Seq <= put.Seq {
		t.Errorf("PutRelease() after reopen Seq = %d, want > %d", next.Seq, put.Seq)
	}
}

func TestMigrationsRunOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	s, err := sqlitestore.Open(t.Context(), path, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "applied migration"); n == 0 {
		t.Fatalf("first Open() logged %d applied migrations, want > 0; log:\n%s", n, buf.String())
	}

	buf.Reset()
	open(t, path, log)
	if n := strings.Count(buf.String(), "applied migration"); n != 0 {
		t.Errorf("second Open() logged %d applied migrations, want 0; log:\n%s", n, buf.String())
	}
}

func TestRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s, err := sqlitestore.Open(t.Context(), path, nil)
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

	s, err = sqlitestore.Open(t.Context(), path, nil)
	if err == nil {
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("Close() error: %v", err)
			}
		})
	}
	if !errors.Is(err, sqlitestore.ErrSchemaTooNew) {
		t.Fatalf("Open(newer schema) error = %v, want ErrSchemaTooNew", err)
	}
}

func TestFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "state")
	path := filepath.Join(dir, "registry.db")
	s := open(t, path, nil)
	if _, err := s.PutRelease(t.Context(), registrytest.Release(t, "lime", "planner", "1.0.0"), []byte("x")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		want os.FileMode
	}{
		{dir, 0o700},
		{path, 0o600},
	}
	for _, tt := range tests {
		t.Run(filepath.Base(tt.path), func(t *testing.T) {
			fi, err := os.Stat(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode().Perm(); got != tt.want {
				t.Errorf("Stat(%s).Mode().Perm() = %o, want %o", tt.path, got, tt.want)
			}
		})
	}
}

func TestDemoMarker(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "registry.db")
	if marked, err := sqlitestore.DemoMarked(ctx, path); err != nil || marked {
		t.Fatalf("sqlitestore.DemoMarked(no file) = %v, %v; want false", marked, err)
	}
	s, err := sqlitestore.Open(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if marked, err := sqlitestore.DemoMarked(ctx, path); err != nil || marked {
		t.Fatalf("sqlitestore.DemoMarked(fresh) = %v, %v; want false", marked, err)
	}
	if err := s.MarkDemo(ctx); err != nil {
		t.Fatal(err)
	}
	if marked, err := sqlitestore.DemoMarked(ctx, path); err != nil || !marked {
		t.Fatalf("sqlitestore.DemoMarked(marked) = %v, %v; want true", marked, err)
	}
}
