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
//	                - Revoke — Google: destructive (would invalidate the
//	                  grounding token); Microsoft: no per-token revoke endpoint
//	                  AT ALL (ErrRevokeUnsupported) — ungroundable by mechanism;
//	                - the provider-autonomous →Dead edge — we can't make the
//	                  issuer kill a token on demand without a destructive action.
//	These are fake-only (fake_test.go) / documented-manual. Don't claim coverage
//	the mechanism can't deliver.
//
// MICROSOFT ROTATION WRINKLE (the n=2 grounding finding): Microsoft refresh
// tokens are SINGLE-USE — every Refresh rotates them. The shared contract body
// (Refresh, then CheckHealth which itself Refreshes) therefore burns the seeded
// token and leaves the only live token in CheckHealth's discarded rotation. So
// the Microsoft test wraps the provider in rtCapture (records every rotation)
// and persists the final live token back to Keychain — making the grounding
// repeatable. Google's token is reusable, so its test deliberately does NOT
// persist (probe semantics, invariant 1). Same S contract, opposite persistence
// discipline: that asymmetry IS the cross-provider evidence.
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

// keychainStore upserts a Keychain generic-password secret (the conformance
// test's write-back path for Microsoft's single-use refresh tokens). Best-effort:
// errors are returned for the caller to log, not fatal.
func keychainStore(service, account, secret string) error {
	return exec.Command("security", "add-generic-password", "-U",
		"-s", service, "-a", account, "-w", secret).Run()
}

// rtCapture is a Provider decorator that records the most recent non-empty
// refresh token returned by Refresh — including the rotation buried inside
// CheckHealth, because it routes CheckHealth through its own Refresh via the
// shared checkHealth helper. It lets the Microsoft grounding test persist the
// final live (rotated) token back to Keychain, surviving single-use rotation.
type rtCapture struct {
	inner Provider
	last  string
}

func (c *rtCapture) Auth(account string, scopes, existingScopes []string, forceFresh bool) (*vault.Credential, error) {
	return c.inner.Auth(account, scopes, existingScopes, forceFresh)
}

func (c *rtCapture) Refresh(cred *vault.Credential) (*vault.Credential, error) {
	fresh, err := c.inner.Refresh(cred)
	if err == nil && fresh.RefreshToken != "" {
		c.last = fresh.RefreshToken
	}
	return fresh, err
}

func (c *rtCapture) Revoke(refreshToken string) error { return c.inner.Revoke(refreshToken) }

func (c *rtCapture) CheckHealth(cred *vault.Credential) HealthState {
	return checkHealth(c.Refresh, cred) // route through our Refresh so the probe's rotation is captured
}

func TestContract_RealMicrosoft(t *testing.T) {
	clientID := os.Getenv("MICROSOFT_CLIENT_ID")
	tenant := os.Getenv("MICROSOFT_TENANT_ID")
	if clientID == "" || tenant == "" {
		t.Skip("no Microsoft client/tenant ($MICROSOFT_CLIENT_ID / $MICROSOFT_TENANT_ID); " +
			"create a public-client Entra app, then provision with: " +
			"MICROSOFT_CLIENT_ID=… MICROSOFT_TENANT_ID=… go run ./cmd/oauth-conformance-provision -provider microsoft")
	}
	rt := os.Getenv("OAUTH_MICROSOFT_REFRESH_TOKEN")
	if rt == "" {
		rt = keychainSecret(ConformanceKeychainServiceMicrosoft)
	}
	if rt == "" {
		t.Skip("no Microsoft conformance refresh token " +
			"(Keychain " + ConformanceKeychainServiceMicrosoft + " or $OAUTH_MICROSOFT_REFRESH_TOKEN); " +
			"provision with: go run ./cmd/oauth-conformance-provision -provider microsoft")
	}

	c := &rtCapture{inner: NewMicrosoftProvider(clientID, tenant)}
	cred := &vault.Credential{
		Type:         vault.TypeOAuth,
		Provider:     "microsoft",
		Account:      ConformanceAccountMicrosoft, // preserved across refresh; not asserted for correctness
		AccessToken:  "stale",
		RefreshToken: rt,
		Expiry:       time.Now().Add(-time.Hour),
		Scopes:       []string{"openid", "profile", "offline_access"},
	}

	// Persist the final live (rotated) token back to Keychain so re-runs work
	// despite Microsoft's single-use rotation. Always attempt it (even on a
	// failing body) since the seed token is consumed regardless. The account
	// label matches the provisioner's store (ConformanceAccountMicrosoft) so
	// `-U` updates the one item in place — no duplicate/stale entry.
	defer func() {
		if c.last != "" {
			if err := keychainStore(ConformanceKeychainServiceMicrosoft, ConformanceAccountMicrosoft, c.last); err != nil {
				t.Logf("warning: failed to persist rotated refresh token back to Keychain: %v", err)
			} else {
				t.Logf("persisted rotated refresh token back to Keychain %s (single-use rotation)", ConformanceKeychainServiceMicrosoft)
			}
		}
	}()

	// MS-specific pre-assertion: a refresh rotates the token (always-rotate —
	// Google's contract can't assert this). Re-seed cred to the live token so the
	// shared body runs against it, not the now-dead seed.
	fresh, err := c.Refresh(cred)
	if err != nil {
		t.Fatalf("Refresh (rotation check): %v", err)
	}
	if fresh.RefreshToken == "" || fresh.RefreshToken == cred.RefreshToken {
		t.Fatalf("Microsoft should rotate the refresh token on every use: got %q (seed %q)", fresh.RefreshToken, cred.RefreshToken)
	}
	cred.RefreshToken = fresh.RefreshToken

	runOAuthContract(t, c, cred)
}

// TestRTCapture_CapturesLastRotation pins the rotated-token-capture linchpin
// HERMETICALLY (against the fake with always-rotate, no real Microsoft): the
// decorator must capture the rotation buried inside CheckHealth's internal
// Refresh, not just the direct Refresh. If it didn't, the only live token after
// a grounding run would be lost in the discarded probe and re-runs would fail.
// Tagged `conformance` because rtCapture lives in this file, but needs no creds
// or network — runs under `go test -tags conformance`.
func TestRTCapture_CapturesLastRotation(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	f.SetRotateRefreshTokens(true) // Microsoft-like single-use rotation
	seed := f.SeedAccount("u@x.com", []string{"openid"})
	c := &rtCapture{inner: f}

	fresh, err := c.Refresh(seed)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if c.last == "" || c.last != fresh.RefreshToken {
		t.Fatalf("after Refresh, c.last=%q, want the rotated token %q", c.last, fresh.RefreshToken)
	}
	afterRefresh := c.last

	// CheckHealth runs an internal Refresh through c.Refresh → another rotation
	// that rtCapture must capture.
	if got := c.CheckHealth(fresh); got != HealthHealthy {
		t.Fatalf("CheckHealth = %v, want Healthy", got)
	}
	if c.last == "" || c.last == afterRefresh {
		t.Fatalf("CheckHealth's internal rotation not captured: c.last=%q (unchanged from %q)", c.last, afterRefresh)
	}
}
