package registrytest

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/tier"
)

// Release returns a release for tests.
func Release(t *testing.T, team, name, version string) registry.Release {
	t.Helper()
	id, err := pack.NewID(team, name)
	if err != nil {
		t.Fatal(err)
	}
	v, err := semver.Parse(version)
	if err != nil {
		t.Fatal(err)
	}
	return registry.Release{
		ID:          id,
		Version:     v,
		Digest:      pack.Digest("sha256:" + name + version),
		Description: "about " + name,
		Size:        3,
		PublishedAt: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
	}
}

// RunStoreContract runs the behaviour every registry.Store must have.
// newStore must return a fresh, empty store for each call.
func RunStoreContract(t *testing.T, newStore func(t *testing.T) registry.Store) {
	t.Helper()

	t.Run("put and get", func(t *testing.T) {
		s := newStore(t)
		in := Release(t, "lime", "planner", "1.0.0")
		got, err := s.PutRelease(t.Context(), in, []byte("abc"))
		if err != nil {
			t.Fatalf("PutRelease() error: %v", err)
		}
		if got.Seq <= 0 {
			t.Errorf("PutRelease() Seq = %d, want > 0", got.Seq)
		}
		rel, data, err := s.Release(t.Context(), in.ID, in.Version)
		if err != nil {
			t.Fatalf("Release() error: %v", err)
		}
		if !bytes.Equal(data, []byte("abc")) {
			t.Errorf("Release() bundle = %q, want %q", data, "abc")
		}
		if rel.Digest != in.Digest || rel.Description != in.Description || rel.Size != in.Size || rel.Seq != got.Seq {
			t.Errorf("Release() = %+v, want fields of %+v with Seq %d", rel, in, got.Seq)
		}
		if !rel.PublishedAt.Equal(in.PublishedAt) {
			t.Errorf("Release() PublishedAt = %v, want %v", rel.PublishedAt, in.PublishedAt)
		}
	})

	t.Run("duplicate version conflicts", func(t *testing.T) {
		s := newStore(t)
		in := Release(t, "lime", "planner", "1.0.0")
		if _, err := s.PutRelease(t.Context(), in, []byte("a")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutRelease(t.Context(), in, []byte("b")); !errors.Is(err, registry.ErrVersionConflict) {
			t.Errorf("second PutRelease() error = %v, want ErrVersionConflict", err)
		}
	})

	t.Run("lower version conflicts", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", "1.1.0"), []byte("a")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", "1.0.9"), []byte("b")); !errors.Is(err, registry.ErrVersionConflict) {
			t.Errorf("PutRelease(1.0.9 after 1.1.0) error = %v, want ErrVersionConflict", err)
		}
		if _, err := s.PutRelease(t.Context(), Release(t, "lime", "other", "1.0.0"), []byte("c")); err != nil {
			t.Errorf("PutRelease(another package) error: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newStore(t)
		in := Release(t, "lime", "planner", "1.0.0")
		if _, _, err := s.Release(t.Context(), in.ID, in.Version); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Release() error = %v, want ErrNotFound", err)
		}
		if _, err := s.Releases(t.Context(), in.ID); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Releases() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("releases sorted by version numerically", func(t *testing.T) {
		s := newStore(t)
		for _, v := range []string{"1.2.0", "1.9.0", "1.10.0"} {
			if _, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", v), []byte(v)); err != nil {
				t.Fatal(err)
			}
		}
		rels, err := s.Releases(t.Context(), Release(t, "lime", "planner", "1.0.0").ID)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rels {
			got = append(got, r.Version.String())
		}
		want := []string{"1.2.0", "1.9.0", "1.10.0"}
		if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Errorf("Releases() versions = %v, want %v", got, want)
		}
	})

	t.Run("packages lists latest per package of one team", func(t *testing.T) {
		s := newStore(t)
		for _, r := range []registry.Release{
			Release(t, "lime", "reviewer", "1.0.0"),
			Release(t, "lime", "planner", "1.9.0"),
			Release(t, "lime", "planner", "1.10.0"),
			Release(t, "other", "planner", "9.0.0"),
		} {
			if _, err := s.PutRelease(t.Context(), r, []byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		rels, err := s.Packages(t.Context(), "lime")
		if err != nil {
			t.Fatal(err)
		}
		if len(rels) != 2 || rels[0].ID.Name() != "planner" || rels[0].Version.String() != "1.10.0" || rels[1].ID.Name() != "reviewer" {
			t.Errorf("Packages(lime) = %+v, want planner@1.10.0 then reviewer@1.0.0", rels)
		}
	})

	t.Run("changes since seq, team only, limited", func(t *testing.T) {
		s := newStore(t)
		var seqs []int64
		for _, r := range []registry.Release{
			Release(t, "lime", "a", "1.0.0"),
			Release(t, "other", "b", "1.0.0"),
			Release(t, "lime", "c", "1.0.0"),
			Release(t, "lime", "d", "1.0.0"),
		} {
			got, err := s.PutRelease(t.Context(), r, []byte("x"))
			if err != nil {
				t.Fatal(err)
			}
			seqs = append(seqs, got.Seq)
		}
		for i := 1; i < len(seqs); i++ {
			if seqs[i] <= seqs[i-1] {
				t.Fatalf("Seq did not grow: %v", seqs)
			}
		}
		rels, err := s.Changes(t.Context(), "lime", seqs[0], 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(rels) != 1 || rels[0].ID.Name() != "c" {
			t.Errorf("Changes(since first, limit 1) = %+v, want only c", rels)
		}
	})

	t.Run("concurrent puts keep versions increasing", func(t *testing.T) {
		s := newStore(t)
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for i := range 20 {
			wg.Go(func() {
				v := semver.New(1, i, 0).String()
				if _, err := s.PutRelease(t.Context(), Release(t, "lime", "planner", v), []byte(v)); err != nil && !errors.Is(err, registry.ErrVersionConflict) {
					errs <- err
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("concurrent PutRelease() error: %v", err)
		}
		rels, err := s.Releases(t.Context(), Release(t, "lime", "planner", "1.0.0").ID)
		if err != nil || len(rels) == 0 {
			t.Fatalf("after 20 concurrent puts Releases() = %d, %v; want at least one", len(rels), err)
		}
		slices.SortFunc(rels, func(a, b registry.Release) int { return cmp.Compare(a.Seq, b.Seq) })
		for i := 1; i < len(rels); i++ {
			if rels[i].Version.Compare(rels[i-1].Version) <= 0 {
				t.Errorf("release %s (seq %d) stored after %s (seq %d)", rels[i].Version, rels[i].Seq, rels[i-1].Version, rels[i-1].Seq)
			}
		}
	})

	t.Run("every release query reads the publisher back", func(t *testing.T) {
		s := newStore(t)
		dana := registry.Actor{TokenID: "tok-dana", Label: "dana@example.com via loadout login", UserID: "u-dana"}
		unknown := Release(t, "lime", "planner", "1.0.0") // stored without one
		first, err := s.PutRelease(t.Context(), unknown, []byte("a"))
		if err != nil {
			t.Fatal(err)
		}
		known := Release(t, "lime", "planner", "2.0.0")
		known.PublishedBy = dana
		if _, err := s.PutRelease(t.Context(), known, []byte("b")); err != nil {
			t.Fatal(err)
		}
		check := func(what string, got registry.Release) {
			t.Helper()
			want := registry.Actor{}
			if got.Version.Compare(known.Version) == 0 {
				want = dana
			}
			if got.PublishedBy != want {
				t.Errorf("%s %s PublishedBy = %+v, want %+v", what, got.Version, got.PublishedBy, want)
			}
		}
		for _, v := range []semver.Version{unknown.Version, known.Version} {
			got, _, err := s.Release(t.Context(), known.ID, v)
			if err != nil {
				t.Fatal(err)
			}
			check("Release()", got)
		}
		rels, err := s.Releases(t.Context(), known.ID)
		if err != nil || len(rels) != 2 {
			t.Fatalf("Releases() = %+v, %v", rels, err)
		}
		for _, r := range rels {
			check("Releases()", r)
		}
		pkgs, err := s.Packages(t.Context(), "lime")
		if err != nil || len(pkgs) != 1 {
			t.Fatalf("Packages() = %+v, %v", pkgs, err)
		}
		check("Packages()", pkgs[0])
		changes, err := s.Changes(t.Context(), "lime", first.Seq-1, 10)
		if err != nil || len(changes) != 2 {
			t.Fatalf("Changes() = %+v, %v", changes, err)
		}
		for _, r := range changes {
			check("Changes()", r)
		}
	})
}

// RunTokenStoreContract runs the behaviour every registry.TokenStore must have.
func RunTokenStoreContract(t *testing.T, newStore func(t *testing.T) registry.TokenStore) {
	t.Helper()
	hash := func(s string) []byte {
		sum := sha256.Sum256([]byte(s))
		return sum[:]
	}
	token := func(id, team string) registry.Token {
		return registry.Token{ID: id, Team: team, Scope: registry.ScopeRead, Label: "laptop " + id, CreatedAt: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	}

	t.Run("create and look up", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateToken(t.Context(), token("t1", "lime"), hash("secret1")); err != nil {
			t.Fatalf("CreateToken() error: %v", err)
		}
		got, err := s.TokenByHash(t.Context(), hash("secret1"))
		if err != nil {
			t.Fatalf("TokenByHash() error: %v", err)
		}
		want := token("t1", "lime")
		if got.ID != want.ID || got.Team != want.Team || got.Scope != want.Scope || got.Label != want.Label || !got.CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("TokenByHash() = %+v, want %+v", got, want)
		}
		if _, err := s.TokenByHash(t.Context(), hash("wrong")); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("TokenByHash(wrong) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate id or hash conflicts", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateToken(t.Context(), token("t1", "lime"), hash("secret1")); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateToken(t.Context(), token("t2", "lime"), hash("secret1")); !errors.Is(err, registry.ErrExists) {
			t.Errorf("CreateToken(same hash) error = %v, want ErrExists", err)
		}
		if err := s.CreateToken(t.Context(), token("t1", "lime"), hash("secret2")); !errors.Is(err, registry.ErrExists) {
			t.Errorf("CreateToken(same id) error = %v, want ErrExists", err)
		}
	})

	t.Run("list per team and revoke", func(t *testing.T) {
		s := newStore(t)
		for i, tk := range []registry.Token{token("t1", "lime"), token("t2", "other"), token("t3", "lime")} {
			if err := s.CreateToken(t.Context(), tk, hash(tk.ID+string(rune('a'+i)))); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Tokens(t.Context(), "lime")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].ID != "t1" || got[1].ID != "t3" {
			t.Errorf("Tokens(lime) = %+v, want t1 then t3", got)
		}
		if err := s.RevokeToken(t.Context(), "t1"); err != nil {
			t.Fatalf("RevokeToken() error: %v", err)
		}
		if _, err := s.TokenByHash(t.Context(), hash("t1a")); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("revoked token still found: %v", err)
		}
		if err := s.RevokeToken(t.Context(), "t1"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("second RevokeToken() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("user tokens", func(t *testing.T) {
		s := newStore(t)
		userToken := func(id, team, user string) registry.Token {
			tk := token(id, team)
			tk.UserID = user
			return tk
		}
		for _, tk := range []registry.Token{
			userToken("u1", "lime", "alice"),
			userToken("u2", "acme", "alice"),
			userToken("u3", "lime", "bob"),
			token("ci", "lime"),
		} {
			if err := s.CreateToken(t.Context(), tk, hash(tk.ID)); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.TokenByHash(t.Context(), hash("u1"))
		if err != nil || got.UserID != "alice" || got.CreatedBy != "" {
			t.Fatalf("TokenByHash(u1) = %+v, %v; want UserID alice", got, err)
		}
		made := token("made", "lime")
		made.CreatedBy = "alice"
		made.ExpiresAt = time.Date(2026, 12, 27, 12, 0, 0, 0, time.UTC)
		if err := s.CreateToken(t.Context(), made, hash("made")); err != nil {
			t.Fatal(err)
		}
		if got, err := s.TokenByHash(t.Context(), hash("made")); err != nil || got.CreatedBy != "alice" || got.UserID != "" || !got.ExpiresAt.Equal(made.ExpiresAt) {
			t.Errorf("TokenByHash(made) = %+v, %v; want CreatedBy alice without UserID, with its expiry", got, err)
		}
		if got, err := s.TokenByHash(t.Context(), hash("ci")); err != nil || got.CreatedBy != "" || !got.ExpiresAt.IsZero() {
			t.Errorf("TokenByHash(ci) = %+v, %v; want no creator and no expiry", got, err)
		}
		if n, err := s.RevokeUserTokens(t.Context(), "alice", "lime"); err != nil || n != 1 {
			t.Errorf("RevokeUserTokens(alice, lime) = %d, %v; want 1", n, err)
		}
		if _, err := s.TokenByHash(t.Context(), hash("u2")); err != nil {
			t.Errorf("alice's acme token was revoked with her lime one: %v", err)
		}
		if n, err := s.RevokeUserTokens(t.Context(), "alice", ""); err != nil || n != 1 {
			t.Errorf("RevokeUserTokens(alice, all) = %d, %v; want 1", n, err)
		}
		if n, err := s.RevokeUserTokens(t.Context(), "", ""); err != nil || n != 0 {
			t.Errorf("RevokeUserTokens(no user) = %d, %v; want 0, never the tokens without a user", n, err)
		}
		for _, id := range []string{"u3", "ci", "made"} {
			if _, err := s.TokenByHash(t.Context(), hash(id)); err != nil {
				t.Errorf("token %s was revoked with alice's: %v", id, err)
			}
		}
	})
}

// RunNamespaceStoreContract runs the behaviour every registry.NamespaceStore
// must have.
func RunNamespaceStoreContract(t *testing.T, newStore func(t *testing.T) registry.NamespaceStore) {
	t.Helper()
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	t.Run("create and look up", func(t *testing.T) {
		s := newStore(t)
		in := []registry.Namespace{
			{Name: "acme", Kind: registry.KindProject, CreatedAt: at},
			{Name: "lime", Parent: "acme", Kind: registry.KindTeam, CreatedAt: at},
		}
		for _, ns := range in {
			if err := s.CreateNamespace(t.Context(), ns); err != nil {
				t.Fatalf("CreateNamespace(%s) error: %v", ns.Name, err)
			}
		}
		for _, want := range in {
			got, err := s.Namespace(t.Context(), want.Name)
			if err != nil {
				t.Fatalf("Namespace(%s) error: %v", want.Name, err)
			}
			if got.Name != want.Name || got.Parent != want.Parent || got.Kind != want.Kind || !got.CreatedAt.Equal(want.CreatedAt) {
				t.Errorf("Namespace(%s) = %+v, want %+v", want.Name, got, want)
			}
		}
		if _, err := s.Namespace(t.Context(), "ghost"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Namespace(ghost) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		s := newStore(t)
		ns := registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: at}
		if err := s.CreateNamespace(t.Context(), ns); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateNamespace(t.Context(), ns); !errors.Is(err, registry.ErrExists) {
			t.Errorf("second CreateNamespace() error = %v, want ErrExists", err)
		}
	})

	t.Run("list sorted by name", func(t *testing.T) {
		s := newStore(t)
		if got, err := s.Namespaces(t.Context()); err != nil || len(got) != 0 {
			t.Fatalf("Namespaces() on an empty store = %v, %v; want none", got, err)
		}
		for _, name := range []string{"lime", "acme", "koala"} {
			if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: name, Kind: registry.KindTeam, CreatedAt: at}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Namespaces(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, ns := range got {
			names = append(names, ns.Name)
		}
		if want := []string{"acme", "koala", "lime"}; !slices.Equal(names, want) {
			t.Errorf("Namespaces() = %v, want %v", names, want)
		}
	})

	t.Run("require review", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if ns, err := s.Namespace(t.Context(), "lime"); err != nil || ns.RequireReview {
			t.Fatalf("new namespace = %+v, %v; want review not required", ns, err)
		}
		if err := s.SetRequireReview(t.Context(), "lime", true); err != nil {
			t.Fatal(err)
		}
		if ns, err := s.Namespace(t.Context(), "lime"); err != nil || !ns.RequireReview {
			t.Errorf("after SetRequireReview(true) = %+v, %v", ns, err)
		}
		if err := s.SetRequireReview(t.Context(), "ghost", true); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("SetRequireReview(ghost) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("wiki switch", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if ns, err := s.Namespace(t.Context(), "lime"); err != nil || ns.Wiki {
			t.Fatalf("new namespace = %+v, %v; want the wiki off", ns, err)
		}
		for _, on := range []bool{true, false, true} {
			if err := s.SetWiki(t.Context(), "lime", on); err != nil {
				t.Fatal(err)
			}
			if ns, err := s.Namespace(t.Context(), "lime"); err != nil || ns.Wiki != on || ns.RequireReview {
				t.Errorf("after SetWiki(%v) = %+v, %v", on, ns, err)
			}
		}
		if nss, err := s.Namespaces(t.Context()); err != nil || len(nss) != 1 || !nss[0].Wiki {
			t.Errorf("Namespaces() = %+v, %v; want lime with its wiki on", nss, err)
		}
		if err := s.SetWiki(t.Context(), "ghost", true); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("SetWiki(ghost) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("course setting", func(t *testing.T) {
		s := newStore(t)
		course, other := Release(t, "lime", "lime-course", "1.0.0").ID, Release(t, "acme", "onboarding", "1.0.0").ID
		if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if ns, err := s.Namespace(t.Context(), "lime"); err != nil || !ns.Course.IsZero() {
			t.Fatalf("new namespace = %+v, %v; want no course", ns, err)
		}
		for _, id := range []pack.ID{course, other, {}} {
			if err := s.SetCourse(t.Context(), "lime", id); err != nil {
				t.Fatal(err)
			}
			if ns, err := s.Namespace(t.Context(), "lime"); err != nil || ns.Course != id || ns.Wiki {
				t.Errorf("after SetCourse(%v) = %+v, %v", id, ns, err)
			}
		}
		if err := s.SetCourse(t.Context(), "lime", course); err != nil {
			t.Fatal(err)
		}
		if nss, err := s.Namespaces(t.Context()); err != nil || len(nss) != 1 || nss[0].Course != course {
			t.Errorf("Namespaces() = %+v, %v; want lime with its course", nss, err)
		}
		if err := s.SetCourse(t.Context(), "ghost", course); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("SetCourse(ghost) error = %v, want ErrNotFound", err)
		}
		if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "acme", Kind: registry.KindProject, CreatedAt: at, Course: other}); err != nil {
			t.Fatal(err)
		}
		if ns, err := s.Namespace(t.Context(), "acme"); err != nil || ns.Course != other {
			t.Errorf("created with a course = %+v, %v", ns, err)
		}
	})

	t.Run("model tiers", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if got, err := s.ModelTiers(t.Context(), "lime"); err != nil || !got.Empty() {
			t.Fatalf("ModelTiers() of a new namespace = %+v, %v; want empty", got.Cells(), err)
		}
		first, err := tier.New(map[tier.Key]tier.Choice{
			{Tool: "copilot", Hint: agent.ModelDeep}: {Model: "Claude Sonnet 5", Effort: tier.EffortHigh},
			{Tool: "copilot", Hint: agent.ModelFast}: {Model: "Claude Haiku 4.5"},
			{Tool: "claude", Hint: agent.ModelDeep}:  {Effort: tier.EffortMax},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetModelTiers(t.Context(), "lime", first); err != nil {
			t.Fatal(err)
		}
		got, err := s.ModelTiers(t.Context(), "lime")
		if err != nil || got.Digest() != first.Digest() {
			t.Errorf("ModelTiers() = %+v, %v; want %+v", got.Cells(), err, first.Cells())
		}
		// A new table replaces every cell.
		second, err := tier.New(map[tier.Key]tier.Choice{{Tool: "claude", Hint: agent.ModelFast}: {Model: "haiku"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetModelTiers(t.Context(), "lime", second); err != nil {
			t.Fatal(err)
		}
		if got, err := s.ModelTiers(t.Context(), "lime"); err != nil || got.Digest() != second.Digest() {
			t.Errorf("after a replace ModelTiers() = %+v, %v; want %+v", got.Cells(), err, second.Cells())
		}
		if err := s.SetModelTiers(t.Context(), "lime", tier.Table{}); err != nil {
			t.Fatal(err)
		}
		if got, err := s.ModelTiers(t.Context(), "lime"); err != nil || !got.Empty() {
			t.Errorf("after clearing ModelTiers() = %+v, %v; want empty", got.Cells(), err)
		}
		if err := s.SetModelTiers(t.Context(), "ghost", first); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("SetModelTiers(ghost) error = %v, want ErrNotFound", err)
		}
		if _, err := s.ModelTiers(t.Context(), "ghost"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("ModelTiers(ghost) error = %v, want ErrNotFound", err)
		}
		// Deleting the namespace takes its cells, so a namespace made again
		// with the name starts empty.
		if err := s.SetModelTiers(t.Context(), "lime", first); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteNamespace(t.Context(), "lime"); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if got, err := s.ModelTiers(t.Context(), "lime"); err != nil || !got.Empty() {
			t.Errorf("ModelTiers() after delete and create = %+v, %v; want empty", got.Cells(), err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		s := newStore(t)
		for _, ns := range []registry.Namespace{
			{Name: "acme", Kind: registry.KindProject, CreatedAt: at},
			{Name: "lime", Parent: "acme", Kind: registry.KindTeam, CreatedAt: at},
		} {
			if err := s.CreateNamespace(t.Context(), ns); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.DeleteNamespace(t.Context(), "acme"); !errors.Is(err, registry.ErrNotEmpty) {
			t.Errorf("DeleteNamespace(with a child) error = %v, want ErrNotEmpty", err)
		}
		if err := s.DeleteNamespace(t.Context(), "lime"); err != nil {
			t.Fatalf("DeleteNamespace(lime) error: %v", err)
		}
		if err := s.DeleteNamespace(t.Context(), "acme"); err != nil {
			t.Fatalf("DeleteNamespace(acme) after its child error: %v", err)
		}
		if err := s.DeleteNamespace(t.Context(), "acme"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("second DeleteNamespace() error = %v, want ErrNotFound", err)
		}
	})
}
