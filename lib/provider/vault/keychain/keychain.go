//go:build !darwin || !cgo

// Fallback Store implementation that shells out to the macOS `security`
// CLI. Active when CGo is disabled or on non-darwin platforms; the
// primary darwin+cgo backend lives in keychain_darwin.go.
//
// This path does not write keychain ACLs (security CLI runs as a
// separate process, so any ACL would gate `security` itself, not
// charon). It exists for hermetic CI / non-darwin development only.

package keychain

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/xianxu/nous/lib/provider/vault"
)

// Store implements vault.Store using the macOS Keychain via the `security` CLI.
type Store struct {
	service string
}

// InspectACL is unimplemented on the non-CGo fallback path. The
// `security` CLI does not expose ACLs in a parseable form; the audit
// tool requires the darwin+cgo build to inspect ACLs.
func (s *Store) InspectACL(account string) (aclCount, appCount int, err error) {
	return 0, 0, fmt.Errorf("InspectACL requires darwin+cgo")
}

// InspectACLDetailed mirrors the darwin+cgo signature for build
// compatibility; same unsupported error.
func (s *Store) InspectACLDetailed(account string) (aclCount, appCount int, drs []string, err error) {
	return 0, 0, nil, fmt.Errorf("InspectACLDetailed requires darwin+cgo")
}

// ErrSigningKeyNotFound mirrors the darwin+cgo sentinel so callers
// can branch on it on either build path. The fallback never returns
// it (it always returns the unsupported error), but having the
// sentinel keeps imports consistent.
var ErrSigningKeyNotFound = fmt.Errorf("signing key not found")

// InspectSigningKeyACL is unimplemented on the non-CGo fallback path.
func InspectSigningKeyACL(label string) (aclCount, appCount int, err error) {
	return 0, 0, fmt.Errorf("InspectSigningKeyACL requires darwin+cgo")
}

// InspectSigningKeyACLDetailed mirrors the darwin+cgo signature for
// build compatibility; same unsupported error.
func InspectSigningKeyACLDetailed(label string) (aclCount, appCount int, drs []string, err error) {
	return 0, 0, nil, fmt.Errorf("InspectSigningKeyACLDetailed requires darwin+cgo")
}

func New() *Store {
	return &Store{service: ResolveServiceName()}
}

// NewWithService builds a Store bound to an explicit service
// namespace. Used by the security audit tool.
func NewWithService(service string) *Store {
	return &Store{service: service}
}

func (s *Store) Get(provider, account string) (*vault.Credential, error) {
	if s.service == ServiceDev {
		return devVaultGet(provider, account)
	}
	key := keyName(provider, account)
	out, err := exec.Command("security", "find-generic-password",
		"-s", s.service,
		"-a", key,
		"-w",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("credential not found for %s: %w", key, err)
	}

	var sc storedCredential
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &sc); err != nil {
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

	// Delete existing entry if present (security CLI errors on duplicate).
	_ = exec.Command("security", "delete-generic-password",
		"-s", s.service,
		"-a", key,
	).Run()

	return exec.Command("security", "add-generic-password",
		"-s", s.service,
		"-a", key,
		"-w", string(data),
		"-U",
	).Run()
}

func (s *Store) Delete(provider, account string) error {
	if s.service == ServiceDev {
		return devVaultDelete(provider, account)
	}
	key := keyName(provider, account)
	return exec.Command("security", "delete-generic-password",
		"-s", s.service,
		"-a", key,
	).Run()
}

// List returns full credentials (minus AccessToken). Matches the
// cross-backend List contract (memory, devFile, keychain darwin+cgo,
// this CLI fallback) — callers can filter on CredType/AdminKey/
// Catalog without an extra Get. Entries that fail to load are
// skipped silently.
func (s *Store) List() ([]*vault.Credential, error) {
	if s.service == ServiceDev {
		return devVaultList()
	}
	out, err := exec.Command("security", "dump-keychain").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to dump keychain: %w", err)
	}

	var creds []*vault.Credential
	var currentService, currentAccount string

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"svce"<blob>=`) {
			currentService = extractQuotedValue(line)
		}
		if strings.HasPrefix(line, `"acct"<blob>=`) {
			currentAccount = extractQuotedValue(line)
		}
		if currentService == s.service && currentAccount != "" {
			parts := strings.SplitN(currentAccount, ":", 2)
			// Skip internal namespaces (e.g. "_ca:cert" — CA storage, not a credential).
			if len(parts) == 2 && !strings.HasPrefix(parts[0], "_") {
				full, err := s.Get(parts[0], parts[1])
				if err == nil {
					full.AccessToken = "" // strip per List contract
					creds = append(creds, full)
				}
			}
			currentService = ""
			currentAccount = ""
		}
	}

	return creds, nil
}

func extractQuotedValue(line string) string {
	// Format: "key"<blob>="value" or "key"<blob>=0xHEX "value"
	idx := strings.Index(line, "=")
	if idx < 0 {
		return ""
	}
	val := strings.TrimSpace(line[idx+1:])
	// Handle quoted value.
	if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
		return val[1 : len(val)-1]
	}
	// Handle hex + quoted: 0xABCD "value"
	if qIdx := strings.Index(val, `"`); qIdx >= 0 {
		end := strings.LastIndex(val, `"`)
		if end > qIdx {
			return val[qIdx+1 : end]
		}
	}
	return val
}
