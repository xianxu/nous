package oauth

import (
	"testing"
)

// TestFake_AuthRefreshRevokeRoundTrip walks the happy-path lifecycle of the S
// machine: NoGrant→Active (Auth), Expired→Active (Refresh), the CheckHealth
// read, and any→NoGrant (Revoke) followed by the Dead classification.
func TestFake_AuthRefreshRevokeRoundTrip(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid", AuthURL: "https://auth.example", DefaultScopes: []string{"openid"}})
	f.SetAuthEmail("user@example.com", true)

	cred, err := f.Auth("", []string{"https://www.googleapis.com/auth/gmail.readonly"}, nil, false)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if cred.Account != "user@example.com" || cred.RefreshToken == "" {
		t.Fatalf("bad cred: %+v", cred)
	}
	if cred.Provider != "google" {
		t.Fatalf("provider = %q, want google", cred.Provider)
	}

	refreshed, err := f.Refresh(cred)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.AccessToken == cred.AccessToken {
		t.Fatal("refresh should rotate the access token")
	}
	if refreshed.Account != "user@example.com" {
		t.Fatalf("refresh dropped account: %+v", refreshed)
	}

	if got := f.CheckHealth(refreshed); got != HealthHealthy {
		t.Fatalf("CheckHealth = %v, want Healthy", got)
	}

	if err := f.Revoke(refreshed.RefreshToken); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// After revoke the grant is gone: refresh fails, health reads NeedsReauth.
	if _, err := f.Refresh(refreshed); err == nil {
		t.Fatal("expected Refresh to fail after Revoke")
	}
	if got := f.CheckHealth(refreshed); got != HealthNeedsReauth {
		t.Fatalf("CheckHealth after revoke = %v, want NeedsReauth", got)
	}
}
