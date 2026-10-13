package registry_test

import (
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

const devLoopYAML = `start: plan
slots:
  plan: {brief: Plan it., writes: plan.md, reads: [ticket], verdicts: [READY]}
  dev: {brief: Build it., writes: dev.md, reads: [plan.md], verdicts: [DONE]}
edges:
  - {from: plan, on: READY, to: dev, via: gate}
  - {from: dev, on: DONE, to: end, via: gate}
`

// renamed is devLoopYAML with the dev slot renamed to build.
var renamed = strings.NewReplacer("dev", "build").Replace(devLoopYAML)

func pipelineBundle(t *testing.T, id, version, body string) []byte {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + id + "\nkind: pipeline\nversion: " + version + "\ndescription: d\n"),
		pack.PipelineFile: []byte(body),
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

func binding(uses string, bind string) string {
	return "uses: " + uses + "\nbind:\n" + bind + "work_folder: explorations/\n"
}

type needsFixture struct {
	svc   *registry.Service
	props *registry.ProposalService
	acme  registry.Principal
	lime  registry.Principal
}

func newNeedsFixture(t *testing.T) needsFixture {
	t.Helper()
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	log := slog.New(slog.DiscardHandler)
	svc := registry.NewService(store, store, store, now, log)
	for _, ns := range []struct {
		name, parent string
		kind         registry.NamespaceKind
	}{{"acme", "", registry.KindProject}, {"lime", "acme", registry.KindTeam}, {"pd", "acme", registry.KindTeam}} {
		if _, err := svc.CreateNamespace(t.Context(), ns.name, ns.parent, ns.kind); err != nil {
			t.Fatal(err)
		}
	}
	f := needsFixture{svc: svc, props: registry.NewProposalService(store, store, now, log, registry.WithNamespaces(store))}
	f.acme = principal(t, svc, "acme", registry.ScopePublish)
	f.lime = principal(t, svc, "lime", registry.ScopePublish)
	pd := principal(t, svc, "pd", registry.ScopePublish)
	for _, pub := range []struct {
		pr   registry.Principal
		team string
		name string
	}{{f.lime, "lime", "lime-planner"}, {f.lime, "lime", "lime-dev"}, {f.acme, "acme", "shared-dev"}, {pd, "pd", "pd-dev"}} {
		if _, err := svc.Publish(t.Context(), pub.pr, bundleNamed(t, pub.team, pub.name, "1.0.0")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Publish(t.Context(), f.acme, pipelineBundle(t, "acme/dev-loop", "1.0.0", devLoopYAML)); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPublishChecksNeeds(t *testing.T) {
	f := newNeedsFixture(t)
	ok := binding("acme/dev-loop@1", "  plan: lime-planner\n  dev: lime-dev\n")
	tests := []struct {
		name string
		id   string
		body string
		want string // empty: accepted
	}{
		{"agent from the project", "lime/shared", binding("acme/dev-loop@1", "  plan: lime-planner\n  dev: shared-dev\n"), ""},
		{"unbound slot", "lime/a", binding("acme/dev-loop@1", "  plan: lime-planner\n  dev: \"\"\n"), "slot dev has no agent"},
		{"agent in no layer", "lime/b", binding("acme/dev-loop@1", "  plan: lime-planner\n  dev: nobody\n"), "The agent nobody is not published"},
		{"agent in a sibling team", "lime/c", binding("acme/dev-loop@1", "  plan: lime-planner\n  dev: pd-dev\n"), "The agent pd-dev is not published"},
		{"template outside the view", "lime/d", binding("pd/dev-loop@1", "  plan: lime-planner\n  dev: lime-dev\n"), "pd is not one of the layers lime sees (lime, acme)"},
		{"missing major", "lime/e", binding("acme/dev-loop@2", "  plan: lime-planner\n  dev: lime-dev\n"), "no release with major 2"},
		{"uses a binding", "lime/f", binding("lime/dev-loop@1", "  plan: lime-planner\n  dev: lime-dev\n"), "is not a template"},
		{"conductor named like an agent", "lime/dev", ok, "also one of the agents it binds"},
	}
	// The filled binding is accepted, and the next cases build on it.
	if _, err := f.svc.Publish(t.Context(), f.lime, pipelineBundle(t, "lime/dev-loop", "1.0.0", ok)); err != nil {
		t.Fatalf("publishing the filled binding: %v", err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.Publish(t.Context(), f.lime, pipelineBundle(t, tc.id, "1.0.0", tc.body))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Publish() error: %v", err)
				}
				return
			}
			if !errors.Is(err, registry.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Publish() = %v, want ErrInvalid containing %q", err, tc.want)
			}
		})
	}
}

func TestPublishRefusesConductorNamedLikeAnAgent(t *testing.T) {
	f := newNeedsFixture(t)
	if _, err := f.svc.Publish(t.Context(), f.lime, bundleNamed(t, "lime", "lime-flow", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	body := binding("acme/dev-loop@1", "  plan: lime-planner\n  dev: lime-flow\n")
	_, err := f.svc.Publish(t.Context(), f.lime, pipelineBundle(t, "lime/flow", "1.0.0", body))
	if !errors.Is(err, registry.ErrInvalid) || !strings.Contains(err.Error(), "also one of the agents it binds") {
		t.Errorf("Publish() = %v, want the conductor name refused", err)
	}
}

func TestPublishListsEveryProblem(t *testing.T) {
	f := newNeedsFixture(t)
	_, err := f.svc.Publish(t.Context(), f.lime, pipelineBundle(t, "lime/x", "1.0.0", binding("acme/dev-loop@1", "  plan: ghost\n  dev: nobody\n")))
	if err == nil || !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "nobody") {
		t.Errorf("Publish() = %v, want both missing agents named", err)
	}
}

func TestPublishChecksCompatibleInsideAMajor(t *testing.T) {
	f := newNeedsFixture(t)
	minor := strings.Replace(devLoopYAML, "Build it.", "Build it well.", 1)
	if _, err := f.svc.Publish(t.Context(), f.acme, pipelineBundle(t, "acme/dev-loop", "1.1.0", minor)); err != nil {
		t.Errorf("Publish(1.1.0 changes a brief) error: %v", err)
	}
	_, err := f.svc.Publish(t.Context(), f.acme, pipelineBundle(t, "acme/dev-loop", "1.2.0", renamed))
	if !errors.Is(err, registry.ErrInvalid) || !strings.Contains(err.Error(), "publish 2.0.0") {
		t.Fatalf("Publish(1.2.0 renames a slot) = %v, want ErrInvalid asking for 2.0.0", err)
	}
	if _, err := f.svc.Publish(t.Context(), f.acme, pipelineBundle(t, "acme/dev-loop", "2.0.0", renamed)); err != nil {
		t.Errorf("Publish(2.0.0) error: %v", err)
	}
}

func TestProposeChecksNeeds(t *testing.T) {
	f := newNeedsFixture(t)
	bad := pipelineBundle(t, "lime/dev-loop", "1.0.0", binding("acme/dev-loop@1", "  plan: lime-planner\n  dev: \"\"\n"))
	_, err := f.props.Propose(t.Context(), f.lime, bad, twoPrompts)
	if !errors.Is(err, registry.ErrInvalid) || !strings.Contains(err.Error(), "slot dev has no agent") {
		t.Errorf("Propose(unbound) = %v, want ErrInvalid naming the slot", err)
	}
	good := pipelineBundle(t, "lime/dev-loop", "1.0.0", binding("acme/dev-loop@1", "  plan: lime-planner\n  dev: lime-dev\n"))
	if _, err := f.props.Propose(t.Context(), f.lime, good, twoPrompts); err != nil {
		t.Errorf("Propose(filled) error: %v", err)
	}
}

func TestProposeWithoutNamespacesRefusesWhatItCannotCheck(t *testing.T) {
	store := registrytest.NewMemStore()
	now := func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	props := registry.NewProposalService(store, store, now, slog.New(slog.DiscardHandler))
	svc := registry.NewService(store, store, store, now, slog.New(slog.DiscardHandler))
	pr := principal(t, svc, "lime", registry.ScopePublish)
	_, err := props.Propose(t.Context(), pr, pipelineBundle(t, "lime/dev-loop", "1.0.0", binding("acme/dev-loop@1", "  plan: a\n  dev: b\n")), twoPrompts)
	if err == nil || errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Propose() = %v, want a plain error (not the author's fault)", err)
	}
}
