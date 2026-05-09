// Package keychain implements vault.Store using the OS keychain.
//
// On darwin with CGo enabled the backend lives in keychain_darwin.go and
// calls the macOS Security framework directly. On other platforms (or
// `CGO_ENABLED=0`) the fallback in keychain.go shells out to the macOS
// `security` CLI; that path is intended for hermetic CI / non-darwin
// development and does not support keychain ACLs.
package keychain

import (
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

// keyName builds the per-entry account key. Entries are stored as one
// row per (provider, account); the account attribute is rendered as
// `<provider>:<account>` so a single service name covers all
// credentials.
func keyName(provider, account string) string {
	return provider + ":" + account
}

// storedCredential is the JSON blob persisted in keychain. The shape
// mirrors vault.Credential including the post-#13 Type discriminator
// and AdminKey/Catalog payloads. Pre-#13 entries omit Type and the new
// payloads; they round-trip unchanged because Type defaults to "" which
// vault.Credential.CredType() normalizes to TypeOAuth.
type storedCredential struct {
	Type         string              `json:"type,omitempty"`
	Provider     string              `json:"provider"`
	Account      string              `json:"account"`
	AccessToken  string              `json:"access_token,omitempty"`
	RefreshToken string              `json:"refresh_token,omitempty"`
	Expiry       time.Time           `json:"expiry,omitempty"`
	Scopes       []string            `json:"scopes,omitempty"`
	AdminKey     *vault.AdminKeyData `json:"admin_key,omitempty"`
	Catalog      *vault.CatalogData  `json:"catalog,omitempty"`
	GCP          *vault.GCPData      `json:"gcp,omitempty"`
	AIStudio     *vault.AIStudioData `json:"aistudio,omitempty"`
}

func fromCredential(c *vault.Credential) storedCredential {
	return storedCredential{
		Type:         c.Type,
		Provider:     c.Provider,
		Account:      c.Account,
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		Expiry:       c.Expiry,
		Scopes:       c.Scopes,
		AdminKey:     c.AdminKey,
		Catalog:      c.Catalog,
		GCP:          c.GCP,
		AIStudio:     c.AIStudio,
	}
}

func (sc storedCredential) toCredential() *vault.Credential {
	return &vault.Credential{
		Type:         sc.Type,
		Provider:     sc.Provider,
		Account:      sc.Account,
		AccessToken:  sc.AccessToken,
		RefreshToken: sc.RefreshToken,
		Expiry:       sc.Expiry,
		Scopes:       sc.Scopes,
		AdminKey:     sc.AdminKey,
		Catalog:      sc.Catalog,
		GCP:          sc.GCP,
		AIStudio:     sc.AIStudio,
	}
}
