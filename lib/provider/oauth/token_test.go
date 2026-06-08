package oauth

import (
	"testing"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

func TestParseIDToken_EmailAndVerified(t *testing.T) {
	email, verified, err := parseIDToken(mintIDToken("a@b.com", true))
	if err != nil || email != "a@b.com" || !verified {
		t.Fatalf("got (%q,%v,%v), want (a@b.com,true,nil)", email, verified, err)
	}
}

func TestParseIDToken_Unverified(t *testing.T) {
	email, verified, err := parseIDToken(mintIDToken("a@b.com", false))
	if err != nil || email != "a@b.com" || verified {
		t.Fatalf("got (%q,%v,%v), want (a@b.com,false,nil)", email, verified, err)
	}
}

func TestParseIDToken_Errors(t *testing.T) {
	for _, tok := range []string{"", "not-a-jwt", "header.!!!.sig"} {
		if _, _, err := parseIDToken(tok); err == nil {
			t.Errorf("parseIDToken(%q): expected error", tok)
		}
	}
}

func TestCredentialFromToken_RejectsUnverified(t *testing.T) {
	now := time.Unix(1000, 0)
	tok := tokenResponse{AccessToken: "at", RefreshToken: "rt", IDToken: mintIDToken("a@b.com", false), ExpiresIn: 3600, Scope: "openid"}
	if _, err := credentialFromToken(tok, now); err == nil {
		t.Fatal("expected rejection of unverified email")
	}
}

func TestCredentialFromToken_Shape(t *testing.T) {
	now := time.Unix(1000, 0)
	tok := tokenResponse{AccessToken: "at", RefreshToken: "rt", IDToken: mintIDToken("a@b.com", true), ExpiresIn: 3600, Scope: "openid email"}
	c, err := credentialFromToken(tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "google" || c.Account != "a@b.com" || c.AccessToken != "at" || c.RefreshToken != "rt" {
		t.Fatalf("bad cred: %+v", c)
	}
	if !c.Expiry.Equal(now.Add(3600 * time.Second)) {
		t.Fatalf("bad expiry: %v", c.Expiry)
	}
	if len(c.Scopes) != 2 || c.Scopes[0] != "openid" {
		t.Fatalf("bad scopes: %v", c.Scopes)
	}
}

func TestApplyRefresh_RotationSidecarsIdentity(t *testing.T) {
	now := time.Unix(2000, 0)
	old := &vault.Credential{
		Type: vault.TypeOAuth, Provider: "google", Account: "a@b.com",
		RefreshToken: "old", Scopes: []string{"openid"},
		GCP: &vault.GCPData{ProjectID: "p"},
	}

	// no new refresh token in response → keep old; sidecars + identity preserved
	got := applyRefresh(old, tokenResponse{AccessToken: "new", ExpiresIn: 3600}, now)
	if got.RefreshToken != "old" || got.AccessToken != "new" {
		t.Fatalf("rotation wrong: %+v", got)
	}
	if got.GCP == nil || got.GCP.ProjectID != "p" {
		t.Fatalf("sidecar dropped: %+v", got.GCP)
	}
	if got.Type != vault.TypeOAuth || got.Provider != "google" || got.Account != "a@b.com" {
		t.Fatalf("identity not preserved: %+v", got)
	}
	if !got.Expiry.Equal(now.Add(3600 * time.Second)) {
		t.Fatalf("bad expiry: %v", got.Expiry)
	}
	// no tok.Scope → default to old scopes
	if len(got.Scopes) != 1 || got.Scopes[0] != "openid" {
		t.Fatalf("scopes should default to old: %v", got.Scopes)
	}

	// response carries a new refresh token → adopt it; tok.Scope → override
	got2 := applyRefresh(old, tokenResponse{AccessToken: "new", RefreshToken: "rotated", ExpiresIn: 3600, Scope: "openid email"}, now)
	if got2.RefreshToken != "rotated" {
		t.Fatalf("expected rotated refresh token, got %q", got2.RefreshToken)
	}
	if len(got2.Scopes) != 2 {
		t.Fatalf("expected scope override, got %v", got2.Scopes)
	}
}
