//go:build darwin && cgo

// Primary darwin Store implementation: direct calls to the macOS Security
// framework. Replaces the `security` CLI shell-out (kept as fallback in
// keychain.go for !cgo / non-darwin builds).
//
// Get / List use github.com/keybase/go-keychain wrappers.
// Set + Delete go through acl_darwin.go's CGo helpers directly:
//   - Set attaches a SecAccess (ACL) to ServiceProd entries (keybase
//     doesn't expose SecAccess construction) and uses SecItemUpdate
//     for atomic upserts, preserving the ACL across token rotation.
//   - Delete tries SecItemDelete first and falls back to the legacy
//     SecKeychainItemDelete pair on errSecInvalidOwnerEdit (-25244),
//     which the modern API surfaces for items whose access object is
//     owned by another process — even without an explicit ACL.

package keychain

import (
	"encoding/json"
	"fmt"
	"strings"

	gokeychain "github.com/keybase/go-keychain"
	"github.com/xianxu/nous/lib/provider/vault"
)

// Store implements vault.Store via the macOS Security framework.
//
// service is the keychain service-name namespace, snapshotted from
// ResolveServiceName at construction. ServiceProd for a signed binary,
// ServiceDev for unsigned/dev — see service.go.
type Store struct {
	service string
}

func New() *Store {
	return &Store{service: ResolveServiceName()}
}

// NewWithService builds a Store bound to an explicit service
// namespace. Used by the security audit tool to inspect both
// `charon` and `charon-dev` namespaces from outside the running
// charon binary's own identity.
func NewWithService(service string) *Store {
	return &Store{service: service}
}

func (s *Store) Get(provider, account string) (*vault.Credential, error) {
	if s.service == ServiceDev {
		return devVaultGet(provider, account)
	}
	key := keyName(provider, account)
	data, err := gokeychain.GetGenericPassword(s.service, key, "", "")
	if err != nil {
		return nil, fmt.Errorf("keychain Get %s: %w", key, err)
	}
	if data == nil {
		return nil, fmt.Errorf("credential not found for %s", key)
	}
	var sc storedCredential
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("corrupt credential for %s: %w", key, err)
	}
	return sc.toCredential(), nil
}

func (s *Store) Set(cred *vault.Credential) error {
	if s.service == ServiceDev {
		return devVaultSet(cred)
	}
	data, err := json.Marshal(fromCredential(cred))
	if err != nil {
		return err
	}
	key := keyName(cred.Provider, cred.Account)
	// ServiceProd path — SecAccess pinned to current process's designated
	// requirement via setGenericPassword(withACL=true). Atomic upsert
	// preserves the ACL across token rotation.
	return setGenericPassword(s.service, key, data, true)
}

func (s *Store) Delete(provider, account string) error {
	if s.service == ServiceDev {
		return devVaultDelete(provider, account)
	}
	return deleteGenericPassword(s.service, keyName(provider, account))
}

// List returns full credentials (minus AccessToken — transient,
// stripped per the cross-backend contract). Skips entries that fail
// to load individually (corrupt JSON, ACL denial) rather than
// failing the whole call.
//
// All vault.Store backends (memory, devFile, keychain prod) return
// the same shape from List: full Credential structs with AccessToken
// blanked. Callers can filter by CredType / AdminKey / Catalog
// without an extra Get round-trip. The cost is N keychain reads per
// List on the prod path — typical user has <20 entries and reads
// from charon's own ACL'd entries are silent (M4 ACL pinned to the
// running binary's DR).
func (s *Store) List() ([]*vault.Credential, error) {
	if s.service == ServiceDev {
		return devVaultList()
	}
	accounts, err := gokeychain.GetGenericPasswordAccounts(s.service)
	if err != nil {
		return nil, fmt.Errorf("keychain List: %w", err)
	}
	creds := make([]*vault.Credential, 0, len(accounts))
	for _, key := range accounts {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		// Skip internal namespaces (e.g. "_ca:cert" — CA storage).
		if strings.HasPrefix(parts[0], "_") {
			continue
		}
		full, err := s.Get(parts[0], parts[1])
		if err != nil {
			// Corrupt entry or ACL denial — skip rather than fail
			// the whole List. The entry is invisible to callers,
			// matching the "skip internal namespaces" pattern above.
			continue
		}
		full.AccessToken = "" // strip per the List contract
		creds = append(creds, full)
	}
	return creds, nil
}

