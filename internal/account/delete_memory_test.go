package account_test

import (
	"context"
	"errors"
	"testing"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/memory/memorytest"
	"github.com/petervlad/loadout/internal/registry"
)

type removerFunc func(ctx context.Context, ns string) (memory.Removed, error)

func (r removerFunc) RemoveNamespace(ctx context.Context, ns string) (memory.Removed, error) {
	return r(ctx, ns)
}

// TestDeleteNamespaceRemovesItsMemory: a team deleted and made again under the
// same name starts with memory off and no entries.
func TestDeleteNamespaceRemovesItsMemory(t *testing.T) {
	var svc *memory.Service
	f := newFixtureWith(t, func(o *account.Options) {
		o.MemoryRemover = removerFunc(func(ctx context.Context, ns string) (memory.Removed, error) {
			return svc.RemoveNamespace(ctx, ns)
		})
	})
	store, switches := memorytest.NewMemStore(), memorytest.NewMemSwitchStore()
	var err error
	if svc, err = memory.NewService(store, switches, f.roles, f.clock.now, nil); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := f.reg.CreateNamespace(ctx, "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.CreateNamespace(ctx, "gone", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	if err := switches.Set(ctx, "gone", true, "u1", f.clock.now()); err != nil {
		t.Fatal(err)
	}
	d, err := memory.NewDraft("gone", memory.TypeReference, "Host", "desc", "body", nil)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.Insert(ctx, d, memory.Caller{UserID: "u1", Label: "u1@example.com"}, f.clock.now())
	if err != nil {
		t.Fatal(err)
	}

	if err := f.svc.DeleteNamespace(ctx, account.Operator(), "gone"); err != nil {
		t.Fatalf("DeleteNamespace() error: %v", err)
	}
	if _, err := f.reg.CreateNamespace(ctx, "gone", "acme", registry.KindTeam); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if sp, _ := switches.Get(ctx, "gone"); sp.Enabled {
		t.Error("a recreated team has memory on")
	}
	if _, err := store.Get(ctx, e.ID); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("an entry of the deleted team is still there: %v", err)
	}
}

// TestDeleteNamespaceKeepsMemoryWhenRefused: a delete the registry refuses
// (the namespace still holds a team) removes no memory.
func TestDeleteNamespaceKeepsMemoryWhenRefused(t *testing.T) {
	calls := 0
	f := newFixtureWith(t, func(o *account.Options) {
		o.MemoryRemover = removerFunc(func(context.Context, string) (memory.Removed, error) {
			calls++
			return memory.Removed{}, nil
		})
	})
	ctx := t.Context()
	if _, err := f.reg.CreateNamespace(ctx, "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reg.CreateNamespace(ctx, "web", "acme", registry.KindTeam); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteNamespace(ctx, account.Operator(), "acme"); err == nil {
		t.Fatal("deleted a project that holds a team")
	}
	if calls != 0 {
		t.Errorf("memory removed %d times for a delete that failed", calls)
	}
}

// TestDeleteNamespaceMemoryFailure: the namespace is gone, and the error says
// its memory is not.
func TestDeleteNamespaceMemoryFailure(t *testing.T) {
	boom := errors.New("disk full")
	f := newFixtureWith(t, func(o *account.Options) {
		o.MemoryRemover = removerFunc(func(context.Context, string) (memory.Removed, error) {
			return memory.Removed{}, boom
		})
	})
	ctx := t.Context()
	if _, err := f.reg.CreateNamespace(ctx, "acme", "", registry.KindProject); err != nil {
		t.Fatal(err)
	}
	err := f.svc.DeleteNamespace(ctx, account.Operator(), "acme")
	if !errors.Is(err, boom) {
		t.Errorf("DeleteNamespace() error = %v, want the removal failure", err)
	}
}
