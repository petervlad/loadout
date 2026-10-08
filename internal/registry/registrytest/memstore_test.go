package registrytest

import (
	"testing"

	"github.com/petervlad/loadout/internal/registry"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) registry.Store { return NewMemStore() })
	RunTokenStoreContract(t, func(*testing.T) registry.TokenStore { return NewMemStore() })
	RunNamespaceStoreContract(t, func(*testing.T) registry.NamespaceStore { return NewMemStore() })
	RunProposalStoreContract(t, func(*testing.T) ProposalBackend { return NewMemStore() })
	RunCountStoreContract(t, func(*testing.T) CountBackend { return NewMemStore() })
	RunWikiStoreContract(t, func(*testing.T) WikiBackend { return NewMemStore() })
}
