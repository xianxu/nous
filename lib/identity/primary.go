package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Primary identity — which of the local secret keys is "the operator"
// for nous purposes. Distinct from "has a secret half on this
// machine" (which can be many keys: primary + throwaway test keys +
// work key + ...). Used by:
//
//   - lib/brain.Annotator        — (self) vs (local secret) labeling
//   - brain recipient remove      — self-removal safeguard fires only on
//                                  primary (so removing a throwaway
//                                  doesn't trigger the REMOVE-SELF
//                                  ceremony)
//   - future surfaces             — signing identity for outgoing
//                                  commits, OAuth account picking, etc.
//
// Storage: a single-line file at $UserConfigDir/nous/primary-identity
// containing the full 40-char fingerprint. Not encrypted (a
// fingerprint is public information); not synced (machine-specific
// per gpg keyring layout).
//
// Resolution order in Primary():
//
//   1. State file present → that fingerprint.
//   2. Exactly one secret key in keyring → that key (implicit; state
//      file not written automatically).
//   3. ErrPrimaryUnset.
//
// The brain-recipient heuristic (private brain → primary) is applied
// in lib/brain (which already imports lib/identity) so this package
// stays free of brain dependencies. `nous identity primary` (the
// CLI) is the canonical place that performs heuristic resolution and
// persists the result.

// ErrPrimaryUnset is returned by Primary when no primary identity can
// be determined: no state file, and the keyring has 0 or 2+ secret
// keys (so the implicit fallback doesn't fire).
var ErrPrimaryUnset = errors.New("primary identity unset (see `nous identity primary`)")

// ErrPrimaryStale signals that the state file points to a fingerprint
// no longer present in the keyring (key deleted/replaced). The caller
// should re-resolve — either via `nous identity primary <fp>` or by
// clearing the state file.
var ErrPrimaryStale = errors.New("primary identity points to a key no longer in the keyring")

// PrimaryStatePath returns the absolute path to the primary-identity
// state file. Exposed so tests can override via $XDG_CONFIG_HOME and
// so `nous identity primary` can print where state lives.
func PrimaryStatePath() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(cfgDir, "nous", "primary-identity"), nil
}

// Primary returns the operator's primary identity key, resolved per
// the ordering documented at the package level.
func Primary() (Key, error) {
	stored, err := readPrimaryFile()
	if err != nil {
		return Key{}, err
	}
	if stored != "" {
		// Validate the stored fp still exists in the keyring.
		keys, err := List()
		if err != nil {
			return Key{}, err
		}
		for _, k := range keys {
			if strings.EqualFold(k.Fingerprint, stored) {
				return k, nil
			}
		}
		return Key{}, ErrPrimaryStale
	}
	// No state file. Implicit "exactly one secret key" fallback.
	keys, err := List()
	if err != nil {
		return Key{}, err
	}
	if len(keys) == 1 {
		return keys[0], nil
	}
	return Key{}, ErrPrimaryUnset
}

// IsPrimary reports whether the given fingerprint matches the primary
// identity. Returns (false, nil) when primary is unset — callers must
// treat that as "unknown", not "definitively not primary".
func IsPrimary(fp string) (bool, error) {
	p, err := Primary()
	if errors.Is(err, ErrPrimaryUnset) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(p.Fingerprint, fp), nil
}

// SetPrimary writes the state file pointing at fp. Validates that fp
// is the full 40-char form AND a secret key on this machine — refuses
// to record a key the operator can't actually use.
func SetPrimary(fp string) error {
	fp = strings.ToUpper(strings.TrimSpace(fp))
	if len(fp) != 40 {
		return fmt.Errorf("fingerprint must be the full 40-char form (got %d chars)", len(fp))
	}
	keys, err := List()
	if err != nil {
		return fmt.Errorf("list secret keys: %w", err)
	}
	var found bool
	for _, k := range keys {
		if strings.EqualFold(k.Fingerprint, fp) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no secret key with fingerprint %s on this machine", fp)
	}
	path, err := PrimaryStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, []byte(fp+"\n"), 0o644)
}

// ClearPrimary removes the state file. No-op if absent.
func ClearPrimary() error {
	path, err := PrimaryStatePath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readPrimaryFile() (string, error) {
	path, err := PrimaryStatePath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	fp := strings.ToUpper(strings.TrimSpace(string(data)))
	if len(fp) != 40 {
		return "", fmt.Errorf("%s: expected 40-char fingerprint, got %d chars", path, len(fp))
	}
	return fp, nil
}
