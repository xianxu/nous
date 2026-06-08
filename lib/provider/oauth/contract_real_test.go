//go:build conformance

// This is the GROUNDING run (the nous#42 two-step grounding discipline applied
// to OAuth). It runs the SAME contract body (runOAuthContract, contract_test.go)
// against REAL Google's token endpoint to certify the fake hasn't drifted on the
// Refresh / CheckHealth surface. Build-tagged `conformance` so it never runs in
// normal CI — invoke it manually, ~monthly or on suspected drift:
//
//	go test -tags conformance ./lib/provider/oauth/ -run Contract_Real -v
//
// GROUNDING BOUNDARY (workshop/targets/oauth-credential-lifecycle.md — the
// transition table's grounding column IS this boundary):
//
//	GROUNDED here:  Expired→Active (Refresh) + the CheckHealth read.
//	NOT grounded:   - the consent leg (Auth) — interactive, non-headless;
//	                - Revoke — destructive (would invalidate the grounding
//	                  refresh token, breaking every subsequent run);
//	                - the provider-autonomous →Dead edge — we can't make Google
//	                  kill a token on demand without a destructive action.
//	These are fake-only (fake_test.go) / documented-manual. Don't claim coverage
//	the mechanism can't deliver.
//
// ZERO-CONFIG: a throwaway test-account refresh token resolves from Keychain
// `nous-oauth-conformance-google` (override via $OAUTH_GOOGLE_REFRESH_TOKEN).
// The developer's real account is NOT on this path. SKIPS (never fails for
// creds) if absent.
//
// ONE-TIME SETUP (token stored in Keychain — never committed):
//
//	security add-generic-password -s nous-oauth-conformance-google \
//	  -a <test-account-email> -w <google-oauth-refresh-token>
//
// If a subtest FAILS, the fake has drifted from real Google: fix fake.go / the
// pure core (not the test) and re-certify.

package oauth

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

func TestContract_RealGoogle(t *testing.T) {
	rt := os.Getenv("OAUTH_GOOGLE_REFRESH_TOKEN")
	if rt == "" {
		rt = keychainSecret(ConformanceKeychainService)
	}
	if rt == "" {
		t.Skip("no Google conformance refresh token " +
			"(Keychain " + ConformanceKeychainService + " or $OAUTH_GOOGLE_REFRESH_TOKEN); " +
			"provision with: go run ./cmd/oauth-conformance-provision")
	}

	gp, err := NewGoogleProvider()
	if err != nil {
		t.Fatalf("NewGoogleProvider: %v", err)
	}
	cred := &vault.Credential{
		Type:         vault.TypeOAuth,
		Provider:     "google",
		Account:      "conformance@grounding", // preserved across refresh; not asserted for correctness
		AccessToken:  "stale",
		RefreshToken: rt,
		Expiry:       time.Now().Add(-time.Hour),
		Scopes:       []string{"openid"},
	}
	runOAuthContract(t, gp, cred)
}

// keychainSecret reads a macOS Keychain generic-password secret. Copied from
// lib/gh's conformance helper — that one is private to package gh's build-tagged
// test file and not importable, so this is a deliberate ~5-line cross-package
// duplication (cheaper than exporting test-only plumbing).
func keychainSecret(service string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
