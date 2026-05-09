//go:build integration

// These tests hit the real macOS Keychain.
// Run with: go test -tags integration ./internal/vault/keychain/
//
// They create and delete entries under the "charon" service name.

package keychain

import (
	"strings"
	"testing"

	"github.com/xianxu/nous/lib/provider/vault"
)

const testProvider = "charon-test"
const testAccount = "integration-test@example.com"

func cleanup(s *Store) {
	_ = s.Delete(testProvider, testAccount)
}

func TestKeychainSetAndGet(t *testing.T) {
	s := New()
	defer cleanup(s)

	err := s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "test-access-token",
		Scopes:      []string{"email", "profile"},
	})
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	cred, err := s.Get(testProvider, testAccount)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if cred.Provider != testProvider {
		t.Errorf("Provider = %q, want %q", cred.Provider, testProvider)
	}
	if cred.Account != testAccount {
		t.Errorf("Account = %q, want %q", cred.Account, testAccount)
	}
	if cred.AccessToken != "test-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "test-access-token")
	}
	if len(cred.Scopes) != 2 || cred.Scopes[0] != "email" {
		t.Errorf("Scopes = %v, want [email profile]", cred.Scopes)
	}
}

func TestKeychainOverwrite(t *testing.T) {
	s := New()
	defer cleanup(s)

	_ = s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "old-token",
	})
	_ = s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "new-token",
	})

	cred, err := s.Get(testProvider, testAccount)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if cred.AccessToken != "new-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "new-token")
	}
}

func TestKeychainDelete(t *testing.T) {
	s := New()
	defer cleanup(s)

	_ = s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "to-delete",
	})

	if err := s.Delete(testProvider, testAccount); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err := s.Get(testProvider, testAccount)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestKeychainGetNotFound(t *testing.T) {
	s := New()
	_, err := s.Get("nonexistent", "nobody@example.com")
	if err == nil {
		t.Error("expected error for missing credential")
	}
}

func TestKeychainList(t *testing.T) {
	s := New()
	defer cleanup(s)

	// Set two credentials with different shapes so the List output
	// contract can be checked for payload preservation across types.
	_ = s.Set(&vault.Credential{
		Provider:    testProvider,
		Account:     testAccount,
		AccessToken: "list-test",
		Scopes:      []string{"openid", "email"},
	})
	adminAccount := testAccount + "-adminkey"
	_ = s.Set(&vault.Credential{
		Type:     vault.TypeAdminKey,
		Provider: testProvider,
		Account:  adminAccount,
		AdminKey: &vault.AdminKeyData{
			OrgID: "org-list-test", ProjectID: "proj_list_test",
			KeyID: "svc_list_test", KeyMaterial: "sk-list-test",
		},
	})
	defer s.Delete(testProvider, adminAccount)

	creds, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	var found, foundAdmin bool
	for _, c := range creds {
		if strings.HasPrefix(c.Provider, "_") {
			t.Errorf("List returned internal entry: %s/%s", c.Provider, c.Account)
		}
		if c.Provider != testProvider {
			continue
		}
		// Cross-backend contract: AccessToken stripped on List output.
		if c.AccessToken != "" {
			t.Errorf("List should strip AccessToken on %s/%s, got %q", c.Provider, c.Account, c.AccessToken)
		}
		switch c.Account {
		case testAccount:
			found = true
			if len(c.Scopes) != 2 {
				t.Errorf("OAuth Scopes lost in List for %s: %+v", c.Account, c.Scopes)
			}
		case adminAccount:
			foundAdmin = true
			if c.CredType() != vault.TypeAdminKey {
				t.Errorf("admin-key Type lost in List: %q", c.CredType())
			}
			if c.AdminKey == nil || c.AdminKey.KeyMaterial != "sk-list-test" {
				t.Errorf("AdminKey payload lost in List: %+v", c.AdminKey)
			}
		}
	}
	if !found {
		t.Errorf("List did not include OAuth-shaped test credential")
	}
	if !foundAdmin {
		t.Errorf("List did not include admin-key-shaped test credential")
	}
}
