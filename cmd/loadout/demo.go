package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/demo"
	"github.com/petervlad/loadout/internal/device"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
	"github.com/petervlad/loadout/internal/web"
)

// demoSeeder builds the demo's services over a data folder, on a clock the
// seed moves, and writes the demo world through them.
type demoSeeder struct {
	log *slog.Logger
	now func() time.Time
}

func (s demoSeeder) Marked(ctx context.Context, dataDir string) (bool, error) {
	return sqlitestore.DemoMarked(ctx, filepath.Join(dataDir, dbFile))
}

func (s demoSeeder) Seed(ctx context.Context, dataDir string) (err error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dataDir, err)
	}
	store, err := sqlitestore.Open(ctx, filepath.Join(dataDir, dbFile), s.log)
	if err != nil {
		return err
	}
	closers := &closeList{store}
	defer func() {
		err = errors.Join(err, closers.Close())
		if err != nil {
			removeDemoFiles(dataDir)
		}
	}()
	mem, err := sqlitestore.OpenMemory(ctx, filepath.Join(dataDir, memoryDBFile), s.log)
	if err != nil {
		return err
	}
	closers.add(mem)

	end := s.now()
	clock := demo.NewClock(end)
	now := clock.Now
	roles := account.NewRoles(store, store)
	mems, err := memoryServicesAt(mem, store, roles, now, s.log)
	if err != nil {
		return err
	}
	svc := registry.NewService(store, store, store, now, s.log, registry.WithMemberships(roles), registry.WithOwnerHints(roles))
	props := registry.NewProposalService(store, store, now, s.log, registry.WithAccess(svc), registry.WithOwners(roles), registry.WithNamespaces(store))
	wikis := registry.NewWikiService(store, store, now, s.log)
	dels := registry.NewDeletionService(store, store, now, s.log, registry.WithDeletionAccess(svc), registry.WithDeletionOwners(roles))
	accounts := account.NewService(store, svc, roles, device.New(now), account.Options{Wiki: wikis, MemoryRemover: mems.Service, Deletions: dels, Now: now, Log: s.log})
	err = demo.Seed(ctx, demo.World{Clock: clock, Registry: svc, Proposals: props, Deletions: dels, Accounts: accounts, Memory: mems.Service}, end)
	if err != nil {
		return err
	}
	// Last, so a seed that stopped half way is not taken for a finished one.
	return store.MarkDemo(ctx)
}

// webDemo is what the pages show of a demo registry, or nil outside one.
func webDemo(on bool) *web.Demo {
	if !on {
		return nil
	}
	d := &web.Demo{Password: demo.Password}
	for _, a := range demo.Accounts() {
		d.Accounts = append(d.Accounts, web.DemoAccount{Email: a.Email, Role: a.Role})
	}
	return d
}

// checkDemoFolder refuses to serve a demo from a registry the seed did not
// make: its password is public.
func checkDemoFolder(ctx context.Context, dataDir string) error {
	marked, err := sqlitestore.DemoMarked(ctx, filepath.Join(dataDir, dbFile))
	if err != nil {
		return err
	}
	if !marked {
		return fmt.Errorf("LOADOUT_DEMO=1 serves only a folder that loadout demo seed made, and %s is not one: use an empty folder", dataDir)
	}
	return nil
}

// removeDemoFiles clears what a seed that failed left, so the folder is empty
// again. The folder was empty when the seed began, which is the only time it
// runs.
func removeDemoFiles(dataDir string) {
	for _, db := range []string{dbFile, memoryDBFile} {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(filepath.Join(dataDir, db+suffix))
		}
	}
}
