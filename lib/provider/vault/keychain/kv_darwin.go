//go:build darwin && cgo

package keychain

import (
	"fmt"

	gokeychain "github.com/keybase/go-keychain"
)

// Low-level keychain key-value operations, usable beyond vault.Store
// (e.g., for storing CA certs). darwin+cgo path; CLI fallback in kv.go.

// GetRaw reads a raw string value from the keychain.
//
// ServiceDev routes to the file-backed dev vault (dev_file.go) — no
// keychain involvement, no prompts. ServiceProd uses the Security
// framework directly.
//
// No TrimSpace here — the Security framework returns exact stored bytes.
// The CLI counterpart in kv.go trims because `security -w` appends a
// trailing newline. Round-trip via SetRaw→GetRaw is bytewise identical
// on either backend.
func GetRaw(service, account string) (string, error) {
	if service == ServiceDev {
		return devVaultGetRaw(account)
	}
	data, err := gokeychain.GetGenericPassword(service, account, "", "")
	if err != nil {
		return "", fmt.Errorf("keychain: %s/%s: %w", service, account, err)
	}
	if data == nil {
		return "", fmt.Errorf("keychain: not found %s/%s", service, account)
	}
	return string(data), nil
}

// SetRaw writes a raw string value.
//
// ServiceDev routes to the file-backed dev vault (no keychain at all,
// no prompts). ServiceProd uses the legacy SecAccess path with an
// ACL pinned to the current process's designated requirement; reads
// from any non-matching binary prompt.
func SetRaw(service, account, value string) error {
	if service == ServiceDev {
		return devVaultSetRaw(account, value)
	}
	return setGenericPassword(service, account, []byte(value), true)
}

// DeleteRaw removes a raw key/value entry. Idempotent — returns nil if
// the entry doesn't exist, matching the CLI fallback's semantics so
// callers don't have to distinguish "didn't exist" from "deleted".
//
// ServiceDev routes to the file-backed dev vault. ServiceProd uses
// the legacy SecAccess path.
func DeleteRaw(service, account string) error {
	if service == ServiceDev {
		return devVaultDeleteRaw(account)
	}
	err := gokeychain.DeleteGenericPasswordItem(service, account)
	if err == gokeychain.ErrorItemNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("keychain delete: %s/%s: %w", service, account, err)
	}
	return nil
}
