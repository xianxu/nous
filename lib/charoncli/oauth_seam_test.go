package charoncli

import (
	"context"
	"testing"

	"github.com/xianxu/nous/lib/provider/oauth"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

// TestTokenSupplierFromVault_RefreshesViaFake is the payoff of the nous#44
// migration: charon's GCP token-supply path now runs hermetically through the
// oauth.Fake (no Google, no browser). It exercises the real consumer logic
// (read → detect expiry → refresh via the port → persist) against the fake.
func TestTokenSupplierFromVault_RefreshesViaFake(t *testing.T) {
	v := memory.New()
	f := oauth.NewFake(oauth.Conf{ClientID: "cid"})

	cred := f.SeedAccount("u@x.com", []string{"openid"}) // seeded already-expired
	if err := v.Set(cred); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	supplier := tokenSupplierFromVault(v, f, "google", "u@x.com")
	tok, err := supplier(context.Background())
	if err != nil {
		t.Fatalf("supplier: %v", err)
	}
	if tok == "" || tok == cred.AccessToken {
		t.Fatalf("expected a refreshed access token, got %q (was %q)", tok, cred.AccessToken)
	}

	// The refreshed credential was persisted back to the vault and is no
	// longer expired — the full read/refresh/persist loop ran against the fake.
	stored, err := v.Get("google", "u@x.com")
	if err != nil {
		t.Fatalf("vault.Get: %v", err)
	}
	if stored.AccessToken != tok {
		t.Fatalf("vault not updated: stored %q, returned %q", stored.AccessToken, tok)
	}
	if stored.IsExpired() {
		t.Fatal("persisted credential should not be expired after refresh")
	}
}
