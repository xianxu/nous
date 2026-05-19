package codesign

import "testing"

// TestIsSigned_RespectsCheckOverride verifies the test-override path
// callers depend on: setting codesign.Check changes what IsSigned
// reports. Mirrors the pattern lib/provider/vault/keychain/service_test.go
// uses to exercise signed-vs-unsigned routing without depending on the
// test binary's actual signing state.
func TestIsSigned_RespectsCheckOverride(t *testing.T) {
	orig := Check
	t.Cleanup(func() { Check = orig })

	Check = func() bool { return true }
	if !IsSigned() {
		t.Error("Check=true ⇒ IsSigned should be true")
	}

	Check = func() bool { return false }
	if IsSigned() {
		t.Error("Check=false ⇒ IsSigned should be false")
	}
}
