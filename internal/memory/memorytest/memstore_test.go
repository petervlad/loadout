package memorytest

import (
	"testing"

	"github.com/petervlad/loadout/internal/memory"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) memory.Store { return NewMemStore() })
	RunTokenStoreContract(t, func(*testing.T) memory.TokenStore { return NewMemTokenStore() })
	RunSwitchContract(t, func(*testing.T) memory.SwitchStore { return NewMemSwitchStore() })
}
