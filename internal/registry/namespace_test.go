package registry_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

// tree builds acme (project) > lime, koala (teams) > lime-ml (team).
func tree(t *testing.T, s *registry.Service) {
	t.Helper()
	for _, ns := range []struct {
		name, parent string
		kind         registry.NamespaceKind
	}{
		{"acme", "", registry.KindProject},
		{"lime", "acme", registry.KindTeam},
		{"koala", "acme", registry.KindTeam},
		{"lime-ml", "lime", registry.KindTeam},
	} {
		if _, err := s.CreateNamespace(t.Context(), ns.name, ns.parent, ns.kind); err != nil {
			t.Fatalf("CreateNamespace(%s) error: %v", ns.name, err)
		}
	}
}

func TestPrincipalAccess(t *testing.T) {
	layers := []registry.Layer{
		{Namespace: "lime", Kind: registry.KindTeam, Depth: 0},
		{Namespace: "acme", Kind: registry.KindProject, Depth: 1},
	}
	reader := registry.Principal{Team: "lime", Scope: registry.ScopeRead, Layers: layers}
	publisher := registry.Principal{Team: "lime", Scope: registry.ScopePublish, Layers: layers}
	legacy := registry.Principal{Team: "lime", Scope: registry.ScopePublish}
	noScope := registry.Principal{Team: "lime", Layers: layers}

	tests := []struct {
		name        string
		pr          registry.Principal
		ns          string
		wantRead    bool
		wantPublish bool
		wantDepth   int
	}{
		{"reader own", reader, "lime", true, false, 0},
		{"reader parent", reader, "acme", true, false, 1},
		{"reader sibling", reader, "koala", false, false, 0},
		{"publisher own", publisher, "lime", true, true, 0},
		{"publisher parent", publisher, "acme", true, false, 1},
		{"publisher child", publisher, "lime-ml", false, false, 0},
		{"no layers means own only", legacy, "lime", true, true, 0},
		{"no layers reads no parent", legacy, "acme", false, false, 0},
		{"invalid scope", noScope, "lime", false, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pr.CanRead(tc.ns); got != tc.wantRead {
				t.Errorf("CanRead(%s) = %v, want %v", tc.ns, got, tc.wantRead)
			}
			if got := tc.pr.CanPublish(tc.ns); got != tc.wantPublish {
				t.Errorf("CanPublish(%s) = %v, want %v", tc.ns, got, tc.wantPublish)
			}
			if d, ok := tc.pr.Depth(tc.ns); ok && d != tc.wantDepth {
				t.Errorf("Depth(%s) = %d, want %d", tc.ns, d, tc.wantDepth)
			}
		})
	}
}

func TestCreateNamespaceRules(t *testing.T) {
	s := newService(t)
	tree(t, s)
	tests := []struct {
		name, ns, parent string
		kind             registry.NamespaceKind
		wantErr          error
	}{
		{"duplicate", "lime", "", registry.KindTeam, registry.ErrExists},
		{"missing parent", "x", "ghost", registry.KindTeam, registry.ErrInvalid},
		{"own parent", "self", "self", registry.KindTeam, registry.ErrInvalid},
		{"bad name", "Lime Team", "", registry.KindTeam, registry.ErrInvalid},
		{"bad kind", "x", "", "org", registry.ErrInvalid},
		{"reserved for the built-in skills", "loadout", "", registry.KindProject, registry.ErrInvalid},
		{"reserved for their page", "skills", "acme", registry.KindTeam, registry.ErrInvalid},
		{"fourth layer is fine", "lime-ml-cv", "lime-ml", registry.KindTeam, nil},
		{"fifth layer is too deep", "too-deep", "lime-ml-cv", registry.KindTeam, registry.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.CreateNamespace(t.Context(), tc.ns, tc.parent, tc.kind)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("CreateNamespace(%s, %s) error: %v", tc.ns, tc.parent, err)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("CreateNamespace(%s, %s) error = %v, want %v", tc.ns, tc.parent, err, tc.wantErr)
			}
		})
	}
}

func TestCreateTokenCreatesARootTeam(t *testing.T) {
	s := newService(t)
	pr := principal(t, s, "solo", registry.ScopeRead)
	nss, err := s.Namespaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(nss) != 1 || nss[0].Name != "solo" || nss[0].Parent != "" || nss[0].Kind != registry.KindTeam {
		t.Errorf("Namespaces() = %+v, want the root team solo", nss)
	}
	// A second token for the same namespace keeps it.
	principal(t, s, "solo", registry.ScopePublish)
	if layers, err := s.Layers(t.Context(), pr); err != nil || len(layers) != 1 {
		t.Errorf("Layers() = %+v, %v; want solo only", layers, err)
	}
}

func TestLegacyTokenWithoutNamespace(t *testing.T) {
	store := registrytest.NewMemStore()
	s := newServiceOn(t, store)
	if err := store.CreateToken(t.Context(), registry.Token{ID: "old", Team: "lime", Scope: registry.ScopeRead}, hashOf(registry.TokenPrefix+"old")); err != nil {
		t.Fatal(err)
	}
	pr, err := s.Authenticate(t.Context(), registry.TokenPrefix+"old")
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if want := []registry.Layer{{Namespace: "lime", Kind: registry.KindTeam}}; !slices.Equal(pr.Layers, want) {
		t.Errorf("Layers = %+v, want %+v", pr.Layers, want)
	}
}

func TestLayers(t *testing.T) {
	s := newService(t)
	tree(t, s)
	pr := principal(t, s, "lime-ml", registry.ScopeRead)
	got, err := s.Layers(t.Context(), pr)
	if err != nil {
		t.Fatal(err)
	}
	want := []registry.Layer{
		{Namespace: "lime-ml", Kind: registry.KindTeam, Depth: 0},
		{Namespace: "lime", Kind: registry.KindTeam, Depth: 1},
		{Namespace: "acme", Kind: registry.KindProject, Depth: 2},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Layers() = %+v, want %+v", got, want)
	}
}

func TestReadAndPublishAcrossLayers(t *testing.T) {
	s := newService(t)
	tree(t, s)
	acmePub := principal(t, s, "acme", registry.ScopePublish)
	limePub := principal(t, s, "lime", registry.ScopePublish)
	limeRead := principal(t, s, "lime", registry.ScopeRead)
	koalaRead := principal(t, s, "koala", registry.ScopeRead)

	for _, b := range [][]byte{
		bundleNamed(t, "acme", "planner", "1.0.0"),
		bundleNamed(t, "acme", "standards", "1.0.0"),
	} {
		if _, err := s.Publish(t.Context(), acmePub, b); err != nil {
			t.Fatalf("Publish(acme) error: %v", err)
		}
	}
	if _, err := s.Publish(t.Context(), limePub, bundleNamed(t, "lime", "planner", "1.0.0")); err != nil {
		t.Fatalf("Publish(lime) error: %v", err)
	}
	if _, err := s.Publish(t.Context(), limePub, bundleNamed(t, "acme", "sneaky", "1.0.0")); !errors.Is(err, registry.ErrForbidden) {
		t.Errorf("lime publishing to acme error = %v, want ErrForbidden", err)
	}

	id := func(ns, name string) pack.ID {
		id, err := pack.NewID(ns, name)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	tests := []struct {
		name    string
		pr      registry.Principal
		id      pack.ID
		wantErr error
	}{
		{"team reads its own", limeRead, id("lime", "planner"), nil},
		{"team reads its project", limeRead, id("acme", "standards"), nil},
		{"sibling team is hidden", koalaRead, id("lime", "planner"), registry.ErrForbidden},
		{"project does not read its teams", acmePub, id("lime", "planner"), registry.ErrForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.Fetch(t.Context(), tc.pr, tc.id, "latest"); !errors.Is(err, tc.wantErr) {
				t.Errorf("Fetch(%s) error = %v, want %v", tc.id, err, tc.wantErr)
			}
			if _, err := s.Releases(t.Context(), tc.pr, tc.id); !errors.Is(err, tc.wantErr) {
				t.Errorf("Releases(%s) error = %v, want %v", tc.id, err, tc.wantErr)
			}
		})
	}

	rels, err := s.Packages(t.Context(), limeRead)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rels {
		got = append(got, r.ID.String())
	}
	if want := []string{"lime/planner", "acme/planner", "acme/standards"}; !slices.Equal(got, want) {
		t.Errorf("Packages(lime) = %v, want %v (closest layer first)", got, want)
	}
	if rels, err := s.Packages(t.Context(), koalaRead); err != nil || len(rels) != 2 {
		t.Errorf("Packages(koala) = %v, %v; want the two acme packages", rels, err)
	}
}

func TestChangesMergeLayersBySeq(t *testing.T) {
	s := newService(t)
	tree(t, s)
	acmePub := principal(t, s, "acme", registry.ScopePublish)
	limePub := principal(t, s, "lime", registry.ScopePublish)
	koalaPub := principal(t, s, "koala", registry.ScopePublish)

	publish := func(pr registry.Principal, ns, name string) int64 {
		t.Helper()
		rel, err := s.Publish(t.Context(), pr, bundleNamed(t, ns, name, "1.0.0"))
		if err != nil {
			t.Fatal(err)
		}
		return rel.Seq
	}
	first := publish(limePub, "lime", "a")
	publish(acmePub, "acme", "b")
	publish(koalaPub, "koala", "hidden")
	publish(limePub, "lime", "c")
	publish(acmePub, "acme", "d")

	rels, err := s.Changes(t.Context(), limePub, first)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i, r := range rels {
		got = append(got, r.ID.String())
		if i > 0 && r.Seq <= rels[i-1].Seq {
			t.Errorf("Changes() not in Seq order: %d after %d", r.Seq, rels[i-1].Seq)
		}
	}
	if want := []string{"acme/b", "lime/c", "acme/d"}; !slices.Equal(got, want) {
		t.Errorf("Changes(lime, since first) = %v, want %v", got, want)
	}
}
