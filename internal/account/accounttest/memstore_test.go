package accounttest

import (
	"testing"

	"github.com/petervlad/loadout/internal/registry/registrytest"
)

func TestMemStoreContract(t *testing.T) {
	newBackend := func(*testing.T) Backend {
		ns := registrytest.NewMemStore()
		m := NewMemStore(ns)
		return Backend{Store: m, Namespaces: ns}
	}
	RunStoreContract(t, newBackend)
	RunCountStoreContract(t, newBackend)
}
