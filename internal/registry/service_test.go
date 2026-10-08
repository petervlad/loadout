package registry_test

import (
	"crypto/sha256"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

func newService(t *testing.T) *registry.Service {
	t.Helper()
	return newServiceOn(t, registrytest.NewMemStore())
}

func newServiceOn(t *testing.T, store *registrytest.MemStore) *registry.Service {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	return registry.NewService(store, store, store, now, slog.New(slog.DiscardHandler))
}

func hashOf(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func bundleFor(t *testing.T, team, version string) []byte {
	t.Helper()
	return bundleNamed(t, team, "planner", version)
}

func bundleNamed(t *testing.T, team, name, version string) []byte {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + team + "/" + name + "\nkind: agent\nversion: " + version + "\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: " + name + "\ndescription: d\n---\nprompt\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := bundle.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func principal(t *testing.T, s *registry.Service, team string, scope registry.TokenScope) registry.Principal {
	t.Helper()
	secret, _, err := s.CreateToken(t.Context(), team, scope, "test")
	if err != nil {
		t.Fatal(err)
	}
	pr, err := s.Authenticate(t.Context(), secret)
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestTokens(t *testing.T) {
	s := newService(t)
	secret, tok, err := s.CreateToken(t.Context(), "lime", registry.ScopePublish, "ci")
	if err != nil {
		t.Fatalf("CreateToken() error: %v", err)
	}
	if !strings.HasPrefix(secret, registry.TokenPrefix) || len(secret) < 40 {
		t.Errorf("secret = %q, want a long %s token", secret, registry.TokenPrefix)
	}
	pr, err := s.Authenticate(t.Context(), secret)
	if err != nil || pr.Team != "lime" || pr.Scope != registry.ScopePublish || pr.TokenID != tok.ID {
		t.Errorf("Authenticate() = %+v, %v; want lime publish %s", pr, err, tok.ID)
	}
	for _, bad := range []string{"", "ldt_wrong", "nope"} {
		if _, err := s.Authenticate(t.Context(), bad); !errors.Is(err, registry.ErrUnauthorized) {
			t.Errorf("Authenticate(%q) error = %v, want ErrUnauthorized", bad, err)
		}
	}
	if _, _, err := s.CreateToken(t.Context(), "Lime Team", registry.ScopeRead, ""); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("CreateToken(bad team) error = %v, want ErrInvalid", err)
	}
}

func TestPublishAndFetch(t *testing.T) {
	s := newService(t)
	pub := principal(t, s, "lime", registry.ScopePublish)
	read := principal(t, s, "lime", registry.ScopeRead)

	rel, err := s.Publish(t.Context(), pub, bundleFor(t, "lime", "1.0.0"))
	if err != nil {
		t.Fatalf("Publish() error: %v", err)
	}
	if _, err := s.Publish(t.Context(), pub, bundleFor(t, "lime", "1.1.0")); err != nil {
		t.Fatalf("Publish(1.1.0) error: %v", err)
	}

	got, data, err := s.Fetch(t.Context(), read, rel.ID, "latest")
	if err != nil {
		t.Fatalf("Fetch(latest) error: %v", err)
	}
	if got.Version.String() != "1.1.0" {
		t.Errorf("Fetch(latest) version = %s, want 1.1.0", got.Version)
	}
	p, err := bundle.Decode(data)
	if err != nil || p.Digest() != got.Digest {
		t.Errorf("fetched bundle digest = %v (%v), want %s", p, err, got.Digest)
	}
	if got, _, err := s.Fetch(t.Context(), read, rel.ID, "1.0.0"); err != nil || got.Version.String() != "1.0.0" {
		t.Errorf("Fetch(1.0.0) = %v, %v", got.Version, err)
	}
}

func TestPublishRecordsThePublisher(t *testing.T) {
	s := newService(t)
	hostMade := principal(t, s, "lime", registry.ScopePublish)
	secret, _, err := s.CreateMachineToken(t.Context(), "lime", registry.ScopePublish, "ci", "u-dana")
	if err != nil {
		t.Fatal(err)
	}
	machine, err := s.Authenticate(t.Context(), secret)
	if err != nil {
		t.Fatal(err)
	}
	dana := registry.Principal{TokenID: "tok-dana", Team: "lime", Scope: registry.ScopePublish, Label: "dana@example.com", UserID: "u-dana"}

	tests := []struct {
		name    string
		pr      registry.Principal
		version string
		want    registry.Actor
	}{
		{"a person", dana, "1.0.0", registry.Actor{TokenID: "tok-dana", Label: "dana@example.com", UserID: "u-dana"}},
		{"a machine token counts as its maker", machine, "1.1.0", registry.Actor{TokenID: machine.TokenID, Label: "ci", UserID: "u-dana"}},
		{"a host-made token by id and label", hostMade, "1.2.0", registry.Actor{TokenID: hostMade.TokenID, Label: "test"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := s.Publish(t.Context(), tc.pr, bundleFor(t, "lime", tc.version))
			if err != nil {
				t.Fatal(err)
			}
			if rel.PublishedBy != tc.want {
				t.Errorf("Publish() PublishedBy = %+v, want %+v", rel.PublishedBy, tc.want)
			}
			got, _, err := s.Fetch(t.Context(), tc.pr, rel.ID, tc.version)
			if err != nil || got.PublishedBy != tc.want {
				t.Errorf("Fetch() PublishedBy = %+v, %v; want %+v", got.PublishedBy, err, tc.want)
			}
		})
	}
}

func TestPublishRules(t *testing.T) {
	s := newService(t)
	pub := principal(t, s, "lime", registry.ScopePublish)
	if _, err := s.Publish(t.Context(), pub, bundleFor(t, "lime", "1.2.0")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		pr      registry.Principal
		data    []byte
		wantErr error
	}{
		{"same version", pub, bundleFor(t, "lime", "1.2.0"), registry.ErrVersionConflict},
		{"older version", pub, bundleFor(t, "lime", "1.1.9"), registry.ErrVersionConflict},
		{"read token", principal(t, s, "lime", registry.ScopeRead), bundleFor(t, "lime", "2.0.0"), registry.ErrForbidden},
		{"other team", pub, bundleFor(t, "other", "1.0.0"), registry.ErrForbidden},
		{"garbage", pub, []byte("not a bundle"), registry.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Publish(t.Context(), tc.pr, tc.data); !errors.Is(err, tc.wantErr) {
				t.Errorf("Publish() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestReadIsTeamScoped(t *testing.T) {
	s := newService(t)
	pub := principal(t, s, "lime", registry.ScopePublish)
	rel, err := s.Publish(t.Context(), pub, bundleFor(t, "lime", "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	outsider := principal(t, s, "other", registry.ScopeRead)
	if _, _, err := s.Fetch(t.Context(), outsider, rel.ID, "latest"); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("Fetch by other team error = %v, want ErrForbidden", err)
	}
	if rels, err := s.Packages(t.Context(), outsider); err != nil || len(rels) != 0 {
		t.Errorf("Packages for other team = %v, %v; want none", rels, err)
	}
	if rels, err := s.Changes(t.Context(), pub, 0); err != nil || len(rels) != 1 {
		t.Errorf("Changes(0) = %v, %v; want 1", rels, err)
	}
}

func TestFetchErrors(t *testing.T) {
	s := newService(t)
	read := principal(t, s, "lime", registry.ScopeRead)
	id, err := pack.NewID("lime", "ghost")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Fetch(t.Context(), read, id, "latest"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Fetch(unknown) error = %v, want ErrNotFound", err)
	}
	if _, _, err := s.Fetch(t.Context(), read, id, "one"); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Fetch(bad version) error = %v, want ErrInvalid", err)
	}
}

func TestRevokeToken(t *testing.T) {
	s := newService(t)
	secret, tok, err := s.CreateToken(t.Context(), "lime", registry.ScopeRead, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if toks, err := s.Tokens(t.Context(), "lime"); err != nil || len(toks) != 1 {
		t.Fatalf("Tokens() = %v, %v; want 1", toks, err)
	}
	if err := s.RevokeToken(t.Context(), tok.ID); err != nil {
		t.Fatalf("RevokeToken() error: %v", err)
	}
	if _, err := s.Authenticate(t.Context(), secret); !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("revoked token still authenticates: %v", err)
	}
	if err := s.RevokeToken(t.Context(), tok.ID); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("second RevokeToken() error = %v, want ErrNotFound", err)
	}
}
