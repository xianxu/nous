package oauth

import (
	"strings"
	"testing"
	"time"
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

// TestFake_DenyConsent: the consent leg returns access_denied (NoGrant→NoGrant),
// but the authorization request is still built and recorded.
func TestFake_DenyConsent(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid", AuthURL: "https://auth.example"})
	f.SetAuthEmail("u@x.com", true)
	f.DenyConsent(true)

	if _, err := f.Auth("", []string{"openid"}, nil, false); err == nil {
		t.Fatal("expected consent-denied error")
	}
	if u := f.LastAuthURL(); !strings.Contains(u, "response_type=code") {
		t.Fatalf("auth URL not recorded on denial: %q", u)
	}
}

// TestFake_UnverifiedEmail: Auth shaping rejects email_verified==false via the
// shared credentialFromToken guard (a payload concern below the per-provider seam).
func TestFake_UnverifiedEmail(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	f.SetAuthEmail("u@x.com", false)
	if _, err := f.Auth("", []string{"openid"}, nil, false); err == nil {
		t.Fatal("expected rejection of unverified email")
	}
}

// TestFake_RevokeGrant: the issuer kills the grant underneath us
// (Active/Expired→Dead) — observed on the next Refresh as invalid_grant, read by
// CheckHealth as NeedsReauth.
func TestFake_RevokeGrant(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	cred := f.SeedAccount("u@x.com", []string{"openid"})
	f.RevokeGrant("u@x.com")

	if _, err := f.Refresh(cred); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("expected invalid_grant, got %v", err)
	}
	if got := f.CheckHealth(cred); got != HealthNeedsReauth {
		t.Fatalf("CheckHealth = %v, want NeedsReauth", got)
	}
}

// TestFake_Transient: a transient refresh failure is Unknown, not Dead — the
// state belief is unchanged (don't penalize the operator for infra blips).
func TestFake_Transient(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	cred := f.SeedAccount("u@x.com", []string{"openid"})
	f.Transient(true)

	if _, err := f.Refresh(cred); err == nil {
		t.Fatal("expected transient refresh error")
	}
	if got := f.CheckHealth(cred); got != HealthUnknown {
		t.Fatalf("CheckHealth = %v, want Unknown", got)
	}
}

// TestFake_DowngradeScope: an admin trims a granted scope; the refreshed
// credential's scope set shrinks (one-shot).
func TestFake_DowngradeScope(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	cred := f.SeedAccount("u@x.com", []string{"openid", "https://www.googleapis.com/auth/gmail.readonly"})
	f.DowngradeScope("u@x.com", []string{"openid"})

	refreshed, err := f.Refresh(cred)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(refreshed.Scopes) != 1 || refreshed.Scopes[0] != "openid" {
		t.Fatalf("expected downgraded scopes [openid], got %v", refreshed.Scopes)
	}
	// one-shot: a second refresh keeps the (now reduced) scopes, no further trim
	again, err := f.Refresh(refreshed)
	if err != nil || len(again.Scopes) != 1 {
		t.Fatalf("downgrade should be one-shot: %v / %v", err, again.Scopes)
	}
}

// TestFake_WrongAccount: consent resolves to a different identity than requested
// (Auth still succeeds; the credential's account differs).
func TestFake_WrongAccount(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	f.SetAuthEmail("real@x.com", true)
	f.WrongAccount("other@y.com")

	cred, err := f.Auth("real@x.com", []string{"openid"}, nil, false)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if cred.Account != "other@y.com" {
		t.Fatalf("expected authenticated account other@y.com, got %q", cred.Account)
	}
}

// TestFake_ExpiryThenRefresh: Active→Expired (clock) then Expired→Active (Refresh).
func TestFake_ExpiryThenRefresh(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	clock := time.Unix(10000, 0)
	f.SetClock(func() time.Time { return clock })

	cred := f.SeedAccount("u@x.com", []string{"openid"})
	if !cred.IsExpiredAt(clock) {
		t.Fatal("seeded credential should be expired")
	}
	refreshed, err := f.Refresh(cred)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.IsExpiredAt(clock) {
		t.Fatalf("refreshed credential should not be expired (expiry=%v)", refreshed.Expiry)
	}
}

// TestFake_AuthURLModeled: the consent leg is modeled, not faked away — the fake
// builds the real authorization request even though it skips the browser.
func TestFake_AuthURLModeled(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid", AuthURL: "https://accounts.google.com/o/oauth2/auth"})
	f.SetAuthEmail("u@x.com", true)

	if _, err := f.Auth("", []string{"https://www.googleapis.com/auth/gmail.readonly"}, nil, false); err != nil {
		t.Fatalf("Auth: %v", err)
	}
	u := f.LastAuthURL()
	for _, want := range []string{"response_type=code", "redirect_uri=", "gmail.readonly", "client_id=cid"} {
		if !strings.Contains(u, want) {
			t.Errorf("auth URL missing %q: %s", want, u)
		}
	}
}

// TestFake_RotateRefreshTokens: with always-rotate on (Microsoft-like), each
// Refresh issues a new refresh token and the old one becomes single-use-dead.
func TestFake_RotateRefreshTokens(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	f.SetRotateRefreshTokens(true)
	cred := f.SeedAccount("u@x.com", []string{"openid"})
	oldRT := cred.RefreshToken

	refreshed, err := f.Refresh(cred)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.RefreshToken == oldRT || refreshed.RefreshToken == "" {
		t.Fatalf("expected rotated refresh token, got %q (old %q)", refreshed.RefreshToken, oldRT)
	}
	// old refresh token is now invalid (single-use rotation)
	if _, err := f.Refresh(cred); err == nil {
		t.Fatal("expected old refresh token to be invalid after rotation")
	}
	// the rotated token still works
	if _, err := f.Refresh(refreshed); err != nil {
		t.Fatalf("rotated refresh token should refresh: %v", err)
	}
}

// TestFake_RevokeFails: the Revoke request itself surfaces a network-shaped error.
func TestFake_RevokeFails(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid"})
	cred := f.SeedAccount("u@x.com", []string{"openid"})
	f.RevokeFails(true)
	if err := f.Revoke(cred.RefreshToken); err == nil {
		t.Fatal("expected Revoke to surface the network error")
	}
}
