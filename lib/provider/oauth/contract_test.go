package oauth

import (
	"testing"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

// runOAuthContract is the dual-backend contract: it bisimulates the
// oauth-credential-lifecycle S machine against whichever Provider backend is
// passed — the Fake here (always), real Google in contract_real_test.go
// (build-tagged grounding). Each assertion below is one S transition or read.
//
// It covers only the consumer-driven, groundable edges. The provider-autonomous
// edges (Active/Expired→Dead, scope downgrade) and the consent + Revoke legs are
// fake-only / manual per the grounding boundary (see the target's transition
// table) and live in fake_test.go, not here — grounding them would require
// making real Google kill a token on demand (impossible non-destructively) or a
// headless consent click (impossible).
//
// `cred` is a seeded credential in the Expired state, ready to refresh.
func runOAuthContract(t *testing.T, p Provider, cred *vault.Credential) {
	t.Helper()

	// Expired → Active: Refresh yields a fresh access token with a future
	// expiry, preserving the account (and any sidecar).
	fresh, err := p.Refresh(cred)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if fresh.AccessToken == "" {
		t.Fatal("Refresh returned an empty access token")
	}
	if fresh.Account != cred.Account {
		t.Errorf("Refresh changed account: %q → %q", cred.Account, fresh.Account)
	}
	if !fresh.Expiry.After(time.Now()) {
		t.Errorf("refreshed credential is not in the future: expiry=%v", fresh.Expiry)
	}
	if cred.GCP != nil && (fresh.GCP == nil || fresh.GCP.ProjectID != cred.GCP.ProjectID) {
		t.Errorf("Refresh dropped the GCP sidecar: %+v", fresh.GCP)
	}

	// CheckHealth read: a valid grant reads Healthy.
	if got := p.CheckHealth(fresh); got != HealthHealthy {
		t.Errorf("CheckHealth(valid) = %v, want Healthy", got)
	}
	// nil / no-refresh-token reads NeedsReauth (the account is unusable).
	if got := p.CheckHealth(nil); got != HealthNeedsReauth {
		t.Errorf("CheckHealth(nil) = %v, want NeedsReauth", got)
	}
	if got := p.CheckHealth(&vault.Credential{Provider: "google", Account: "x"}); got != HealthNeedsReauth {
		t.Errorf("CheckHealth(no refresh token) = %v, want NeedsReauth", got)
	}
}

// TestContract_Fake runs the contract against the in-memory fake. Always on.
func TestContract_Fake(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid", DefaultScopes: []string{"openid"}})
	cred := f.SeedAccount("user@example.com", []string{"openid", "email"})
	cred.GCP = &vault.GCPData{ProjectID: "proj-x"} // exercise the sidecar invariant
	runOAuthContract(t, f, cred)
}
