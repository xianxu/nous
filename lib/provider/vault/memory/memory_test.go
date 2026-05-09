package memory

import (
	"testing"

	"github.com/xianxu/nous/lib/provider/vault"
)

func TestStoreGetSetDelete(t *testing.T) {
	s := New()

	// Set.
	err := s.Set(&vault.Credential{
		Provider:    "google",
		Account:     "user@gmail.com",
		AccessToken: "tok-123",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get.
	cred, err := s.Get("google", "user@gmail.com")
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "tok-123" {
		t.Errorf("got token %q, want %q", cred.AccessToken, "tok-123")
	}

	// List (should not include access tokens).
	creds, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
	if creds[0].AccessToken != "" {
		t.Error("List should not return access tokens")
	}

	// Delete.
	if err := s.Delete("google", "user@gmail.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("google", "user@gmail.com"); err == nil {
		t.Error("expected error after delete")
	}
}

func TestGetNotFound(t *testing.T) {
	s := New()
	_, err := s.Get("nope", "nope")
	if err == nil {
		t.Error("expected error for missing credential")
	}
}

// Cross-backend List contract: returns full credentials with
// AccessToken stripped — Type / AdminKey / Catalog payloads MUST be
// preserved so callers can filter by them without an extra Get.
// Same invariant holds for keychain prod (List does Get-each-entry
// internally) and devFile.
func TestList_FullPayloadMinusAccessToken(t *testing.T) {
	s := New()

	_ = s.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "image-gen",
		AdminKey: &vault.AdminKeyData{
			OrgID: "org-X", ProjectID: "proj_Y", ProjectName: "prod",
			KeyID: "svc_Z", KeyMaterial: "sk-test",
		},
		AccessToken: "should-be-stripped",
	})
	_ = s.Set(&vault.Credential{
		Type: vault.TypeOAuth, Provider: "google", Account: "user@gmail.com",
		AccessToken: "ya29.tok", RefreshToken: "1//rfsh",
		Scopes: []string{"openid", "email"},
	})

	creds, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("expected 2 creds, got %d", len(creds))
	}

	for _, c := range creds {
		if c.AccessToken != "" {
			t.Errorf("List should strip AccessToken on %s/%s, got %q",
				c.Provider, c.Account, c.AccessToken)
		}
		switch c.Provider {
		case "openai":
			if c.CredType() != vault.TypeAdminKey {
				t.Errorf("openai cred type = %q, want admin-key (Type field lost in List)", c.CredType())
			}
			if c.AdminKey == nil || c.AdminKey.KeyMaterial != "sk-test" {
				t.Errorf("AdminKey payload lost in List: %+v", c.AdminKey)
			}
		case "google":
			if c.CredType() != vault.TypeOAuth {
				t.Errorf("google cred type = %q, want oauth", c.CredType())
			}
			if c.RefreshToken != "1//rfsh" {
				t.Errorf("RefreshToken lost in List: %q", c.RefreshToken)
			}
			if len(c.Scopes) != 2 {
				t.Errorf("Scopes lost in List: %+v", c.Scopes)
			}
		}
	}
}
