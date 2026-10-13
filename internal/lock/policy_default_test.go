package lock

import "testing"

// A new install follows its registry. Edited files are never overwritten
// (the update is held back), so auto is safe as the default.
func TestDefaultPolicyIsAuto(t *testing.T) {
	if DefaultPolicy != PolicyAuto {
		t.Errorf("DefaultPolicy = %q, want %q", DefaultPolicy, PolicyAuto)
	}
	if !DefaultPolicy.Valid() {
		t.Errorf("DefaultPolicy %q is not a valid policy", DefaultPolicy)
	}
}
