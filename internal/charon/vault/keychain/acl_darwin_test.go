//go:build darwin && cgo && integration

// Integration tests for the ACL'd write path. Hit the real macOS Keychain
// — run with: go test -tags integration ./internal/vault/keychain/
//
// We use a dedicated test service name (`charon-acl-test`) so test entries
// do not pollute the user's real ServiceProd namespace, which holds OAuth
// refresh tokens and the proxy CA private key.

package keychain

import (
	"testing"

	gokeychain "github.com/keybase/go-keychain"
)

const aclTestService = "charon-acl-test"

func aclCleanup(account string) {
	// Use our deleteGenericPassword helper (with the -25244 fallback)
	// rather than gokeychain.DeleteGenericPasswordItem so cleanup is
	// robust against entries left by previous test runs whose access
	// objects don't match the current process.
	_ = deleteGenericPassword(aclTestService, account)
}

func TestACL_WriteAndReadBack(t *testing.T) {
	const account = "acl-test:write-readback"
	defer aclCleanup(account)

	payload := []byte(`{"hello":"acl"}`)
	if err := setGenericPassword(aclTestService, account, payload, true); err != nil {
		t.Fatalf("setGenericPassword(withACL=true) failed: %v", err)
	}

	got, err := gokeychain.GetGenericPassword(aclTestService, account, "", "")
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("read-back mismatch: got %q want %q", got, payload)
	}
}

// TestACL_AtomicUpsert verifies that re-writing an existing ACL'd entry
// goes through SecItemUpdate (in-place value swap) rather than
// delete-then-add. This matters for token rotation: the M2 review noted
// that delete-then-add briefly drops the ACL, which would let a racing
// reader slip in. The atomic update path is exercised here implicitly —
// if we mistakenly fell back to delete+add, the second call's add would
// either fail with errSecDuplicateItem or succeed with a fresh ACL,
// which is fine for this test, but the path under exercise is the
// SecItemUpdate branch in charon_set_generic_password.
func TestACL_AtomicUpsert(t *testing.T) {
	const account = "acl-test:upsert"
	defer aclCleanup(account)

	if err := setGenericPassword(aclTestService, account, []byte("v1"), true); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if err := setGenericPassword(aclTestService, account, []byte("v2"), true); err != nil {
		t.Fatalf("second write (update) failed: %v", err)
	}

	got, err := gokeychain.GetGenericPassword(aclTestService, account, "", "")
	if err != nil {
		t.Fatalf("read-back after update failed: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("expected v2 after update, got %q", got)
	}
}

// TestACL_NoACLPath verifies the with_acl=false branch: write without an
// ACL, read back, succeeds same as the ACL'd path. This is the path
// taken by ServiceDev entries.
func TestACL_NoACLPath(t *testing.T) {
	const account = "acl-test:no-acl"
	defer aclCleanup(account)

	if err := setGenericPassword(aclTestService, account, []byte("plain"), false); err != nil {
		t.Fatalf("setGenericPassword(withACL=false) failed: %v", err)
	}
	got, err := gokeychain.GetGenericPassword(aclTestService, account, "", "")
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	if string(got) != "plain" {
		t.Fatalf("got %q want %q", got, "plain")
	}
}

// TestACL_ActuallyAttachesACL is the regression test that catches the
// "kSecAttrAccess silently dropped by SecItemAdd" bug we hit before
// switching to SecKeychainAddGenericPassword + SecKeychainItemSetAccess.
// Round-trip data tests pass on either path; only an ACL-state inspection
// distinguishes them.
//
// Asserts: a fresh entry written with withACL=true has at least one
// trusted application on its SecAccess. The default ACL from
// SecKeychainAddGenericPassword (no SetAccess called) would also produce
// trusted_apps>0, so this isn't strictly proof that *our* SecAccess
// landed — but the previous bug produced trusted_apps == 0 (no ACL at
// all), so this catches the actual regression.
func TestACL_ActuallyAttachesACL(t *testing.T) {
	const account = "acl-test:attach-check"
	defer aclCleanup(account)

	if err := setGenericPassword(aclTestService, account, []byte("acl-payload"), true); err != nil {
		t.Fatalf("setGenericPassword(withACL=true) failed: %v", err)
	}

	aclCount, appCount, err := inspectGenericPasswordACL(aclTestService, account)
	if err != nil {
		t.Fatalf("inspectGenericPasswordACL failed: %v", err)
	}
	if aclCount == 0 {
		t.Fatalf("entry has no SecAccess attached (regression: SecItemAdd silently dropped kSecAttrAccess)")
	}
	if appCount == 0 {
		t.Fatalf("entry's SecAccess has zero trusted applications — would prompt every read forever")
	}
	t.Logf("ACL inspection: %d ACL entries, %d trusted apps total", aclCount, appCount)
}

// TestACL_AtomicUpsert_PreservesACL verifies the SecKeychainItemModifyContent
// path: after creating an ACL'd entry, an update of its data leaves the
// ACL intact. This is what makes token rotation safe — the ACL doesn't
// briefly drop between writes.
func TestACL_AtomicUpsert_PreservesACL(t *testing.T) {
	const account = "acl-test:upsert-preserves"
	defer aclCleanup(account)

	// First write creates with ACL.
	if err := setGenericPassword(aclTestService, account, []byte("v1"), true); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	ac1, app1, err := inspectGenericPasswordACL(aclTestService, account)
	if err != nil {
		t.Fatalf("inspect after first write: %v", err)
	}

	// Second write goes through the SecKeychainItemModifyContent branch
	// (existing item found). Should preserve ACL.
	if err := setGenericPassword(aclTestService, account, []byte("v2"), true); err != nil {
		t.Fatalf("second write (update) failed: %v", err)
	}
	ac2, app2, err := inspectGenericPasswordACL(aclTestService, account)
	if err != nil {
		t.Fatalf("inspect after second write: %v", err)
	}

	if ac2 != ac1 || app2 != app1 {
		t.Fatalf("update changed ACL shape: was (%d,%d), now (%d,%d)",
			ac1, app1, ac2, app2)
	}

	got, err := gokeychain.GetGenericPassword(aclTestService, account, "", "")
	if err != nil {
		t.Fatalf("read-back after update failed: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("expected v2 after update, got %q", got)
	}
}
