//go:build !darwin || !cgo

package keychain

import (
	"fmt"
	"os/exec"
	"strings"
)

// Low-level keychain key-value operations, usable beyond vault.Store
// (e.g., for storing CA certs). CLI fallback path; the darwin+cgo
// counterpart lives in kv_darwin.go.

// GetRaw reads a raw string value. ServiceDev routes to the file-
// backed dev vault (no security CLI dependency, no Keychain prompts);
// matches the kv_darwin path's behavior so dev iteration is identical
// across darwin+cgo, darwin without cgo, and non-darwin builds.
func GetRaw(service, account string) (string, error) {
	if service == ServiceDev {
		return devVaultGetRaw(account)
	}
	out, err := exec.Command("security", "find-generic-password",
		"-s", service,
		"-a", account,
		"-w",
	).Output()
	if err != nil {
		return "", fmt.Errorf("keychain: not found %s/%s: %w", service, account, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SetRaw writes a raw string value. ServiceDev routes to the dev
// file vault (see GetRaw).
//
// The -U flag on add-generic-password handles both create and update
// atomically on the prod path.
func SetRaw(service, account, value string) error {
	if service == ServiceDev {
		return devVaultSetRaw(account, value)
	}
	return exec.Command("security", "add-generic-password",
		"-s", service,
		"-a", account,
		"-w", value,
		"-U",
	).Run()
}

// DeleteRaw removes a raw key/value entry. ServiceDev routes to the
// dev file vault. ServiceProd: idempotent — returns nil if the entry
// doesn't exist (`security delete-generic-password` exits with status
// 44 / errSecItemNotFound when missing).
func DeleteRaw(service, account string) error {
	if service == ServiceDev {
		return devVaultDeleteRaw(account)
	}
	cmd := exec.Command("security", "delete-generic-password",
		"-s", service,
		"-a", account,
	)
	if err := cmd.Run(); err != nil {
		// errSecItemNotFound → idempotent success. Anything else (locked
		// keychain, ACL denial, etc.) bubbles up so callers see real
		// failures.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 44 {
			return nil
		}
		return fmt.Errorf("keychain delete %s/%s: %w", service, account, err)
	}
	return nil
}
