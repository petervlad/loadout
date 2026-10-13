package registry_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

func TestServiceDeletePackage(t *testing.T) {
	var logs bytes.Buffer
	store := registrytest.NewMemStore()
	svc := registry.NewService(store, store, store, time.Now, slog.New(slog.NewTextHandler(&logs, nil)))
	rel := registrytest.Release(t, "lime", "planner", "0.1.0")
	if _, err := store.PutRelease(t.Context(), rel, []byte("b")); err != nil {
		t.Fatal(err)
	}
	missing, err := pack.NewID("lime", "nope")
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{"footprint of a missing package", func() error { _, err := svc.PackageFootprint(t.Context(), missing); return err }},
		{"delete of a missing package", func() error { _, err := svc.DeletePackage(t.Context(), missing); return err }},
	} {
		if err := tt.run(); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("%s error = %v, want ErrNotFound", tt.name, err)
		}
	}
	if strings.Contains(logs.String(), "package deleted") {
		t.Errorf("a failed delete was logged as done:\n%s", logs.String())
	}

	fp, err := svc.PackageFootprint(t.Context(), rel.ID)
	if err != nil || len(fp.Releases) != 1 {
		t.Fatalf("PackageFootprint() = %+v, %v; want one release", fp, err)
	}
	if _, err := svc.DeletePackage(t.Context(), rel.ID); err != nil {
		t.Fatalf("DeletePackage() error: %v", err)
	}
	if got := logs.String(); !strings.Contains(got, "package deleted") || !strings.Contains(got, "package=lime/planner") || !strings.Contains(got, "releases=1") {
		t.Errorf("log = %q, want the id and the counts of the deleted package", got)
	}
}
