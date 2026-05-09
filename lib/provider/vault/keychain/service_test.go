package keychain

import "testing"

// withSignatureCheck swaps the package-level signatureCheck for the
// duration of the test, restoring it on cleanup. Ensures parallel tests
// don't observe leaked overrides.
func withSignatureCheck(t *testing.T, fn func() bool) {
	t.Helper()
	orig := signatureCheck
	t.Cleanup(func() { signatureCheck = orig })
	signatureCheck = fn
}

func TestResolveServiceName_signed(t *testing.T) {
	withSignatureCheck(t, func() bool { return true })
	if got := ResolveServiceName(); got != ServiceProd {
		t.Errorf("signed: got %q, want %q", got, ServiceProd)
	}
}

func TestResolveServiceName_unsigned(t *testing.T) {
	withSignatureCheck(t, func() bool { return false })
	if got := ResolveServiceName(); got != ServiceDev {
		t.Errorf("unsigned: got %q, want %q", got, ServiceDev)
	}
}

// TestStoreSnapshotsServiceAtNew verifies that Store captures the
// resolved service name once at construction. A later signatureCheck
// flip must not move the existing Store between namespaces — that
// would silently re-route reads/writes mid-process.
func TestStoreSnapshotsServiceAtNew(t *testing.T) {
	withSignatureCheck(t, func() bool { return false })
	devStore := New()

	withSignatureCheck(t, func() bool { return true })
	prodStore := New()

	if devStore.service != ServiceDev {
		t.Errorf("devStore.service = %q, want %q", devStore.service, ServiceDev)
	}
	if prodStore.service != ServiceProd {
		t.Errorf("prodStore.service = %q, want %q", prodStore.service, ServiceProd)
	}
}
