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
	RunPackageDeleteContract(t, func(*testing.T) DeleteBackend { return NewMemStore() })
	RunDeletionContract(t, func(*testing.T) DeletionBackend { return NewMemStore() })
	RunCountStoreContract(t, func(*testing.T) CountBackend { return NewMemStore() })
	RunWikiStoreContract(t, func(*testing.T) WikiBackend { return NewMemStore() })
}
