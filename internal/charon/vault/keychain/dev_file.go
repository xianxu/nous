// File-backed vault for dev mode (ServiceDev). Bypasses the macOS
// Keychain entirely so dev iteration doesn't accumulate keychain
// permission prompts, and so users aren't trained to click
// [Always Allow] on every rebuild.
//
// Only ServiceDev routes here — production (signed binary, ServiceProd)
// keeps using the Keychain Services API with codesign-DR ACLs. The
// dispatch sits at the top of each Store method and raw helper.
//
// File layout: a single JSON document at devVaultPath() containing
// both the typed Credential entries (keyed by `<provider>:<account>`)
// and the raw key/value entries used by AdminKeyStore + the proxy CA
// (keyed by account name, since service is always ServiceDev here).
//
// Concurrency: single-process serialization via sync.Mutex; cross-
// process serialization via unix flock on a separate lock file
// (dev-vault.json.lock). Writes always go load → mutate → save under
// exclusive flock so two charon instances running concurrently in
// dev (e.g. `charon serve` + `charon auth`) don't lose each other's
// updates. Reads don't take the flock — the atomic-rename save
// pattern means readers either see the pre-update or post-update
// file, never partial.
//
// Why a separate lock file: the data file is replaced via atomic
// rename (write tmp, rename onto path), which severs any flock held
// on the old inode. A standalone lock-file inode persists across
// renames so cross-process writers serialize correctly.
//
// File mode 0600. Atomic writes via temp-file + rename.
//
// No build tag here — pure Go, usable from any platform's keychain
// package code path.

package keychain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/xianxu/nous/internal/charon/vault"
)

const devVaultEnvVar = "CHARON_DEV_VAULT_PATH"

// devVaultPath returns the path to the dev vault file. Override via
// the CHARON_DEV_VAULT_PATH env var (used by tests).
func devVaultPath() string {
	if p := os.Getenv(devVaultEnvVar); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to /tmp; the dev vault is best-effort and the
		// alternative would be panicking on a misconfigured system.
		return "/tmp/charon-dev-vault.json"
	}
	return filepath.Join(home, ".local", "share", "charon", "dev-vault.json")
}

// devVaultState is the on-disk JSON shape. Single struct so the
// whole dev vault round-trips through one ReadFile/WriteFile pair.
type devVaultState struct {
	Version     int                          `json:"version"`
	Credentials map[string]*vault.Credential `json:"credentials"` // keyed by "<provider>:<account>"
	Raw         map[string]string            `json:"raw"`         // keyed by account (service implied = ServiceDev)
}

// devVaultMu serializes intra-process access. Writes additionally
// take a unix flock on the lock file for cross-process safety.
var devVaultMu sync.Mutex

// devVaultLockPath returns the path to the lock file. A separate
// inode from the data file so the atomic-rename save doesn't
// invalidate active locks.
func devVaultLockPath() string {
	return devVaultPath() + ".lock"
}

// withDevVaultWriteLock takes the cross-process write lock, runs fn
// while holding it, and releases on close. Caller is the in-process
// devVaultMu holder so this is the second of two locks.
//
// flock blocks until the previous holder releases — fine for dev
// iteration where contention is rare and brief. The lock file is
// created on demand and never deleted (orphaned files don't cause
// issues; flock works on the inode).
func withDevVaultWriteLock(fn func() error) error {
	path := devVaultLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("dev vault lock mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("dev vault lock open: %w", err)
	}
	defer f.Close() // also releases the flock per close(2) semantics
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("dev vault flock: %w", err)
	}
	return fn()
}

// loadDevVault returns a fresh state if the file doesn't exist; an
// error only when the file exists but can't be parsed (corrupt JSON,
// permissions issue). The empty maps avoid nil-map writes downstream.
func loadDevVault() (*devVaultState, error) {
	data, err := os.ReadFile(devVaultPath())
	if errors.Is(err, os.ErrNotExist) {
		return &devVaultState{
			Version:     1,
			Credentials: map[string]*vault.Credential{},
			Raw:         map[string]string{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dev vault read: %w", err)
	}
	var s devVaultState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("dev vault parse: %w", err)
	}
	if s.Credentials == nil {
		s.Credentials = map[string]*vault.Credential{}
	}
	if s.Raw == nil {
		s.Raw = map[string]string{}
	}
	return &s, nil
}

// saveDevVault writes the state atomically (tempfile + rename) at
// mode 0600. Creates parent dirs on first write.
func saveDevVault(s *devVaultState) error {
	s.Version = 1
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("dev vault marshal: %w", err)
	}
	path := devVaultPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("dev vault mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("dev vault write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("dev vault rename: %w", err)
	}
	return nil
}

// ── vault.Store operations (typed Credential) ────────────────────────

func devVaultGet(provider, account string) (*vault.Credential, error) {
	devVaultMu.Lock()
	defer devVaultMu.Unlock()
	s, err := loadDevVault()
	if err != nil {
		return nil, err
	}
	key := keyName(provider, account)
	c, ok := s.Credentials[key]
	if !ok {
		return nil, fmt.Errorf("credential not found for %s", key)
	}
	cp := *c
	return &cp, nil
}

func devVaultSet(c *vault.Credential) error {
	devVaultMu.Lock()
	defer devVaultMu.Unlock()
	return withDevVaultWriteLock(func() error {
		s, err := loadDevVault()
		if err != nil {
			return err
		}
		key := keyName(c.Provider, c.Account)
		cp := *c
		s.Credentials[key] = &cp
		return saveDevVault(s)
	})
}

func devVaultDelete(provider, account string) error {
	devVaultMu.Lock()
	defer devVaultMu.Unlock()
	return withDevVaultWriteLock(func() error {
		s, err := loadDevVault()
		if err != nil {
			return err
		}
		delete(s.Credentials, keyName(provider, account))
		return saveDevVault(s)
	})
}

func devVaultList() ([]*vault.Credential, error) {
	devVaultMu.Lock()
	defer devVaultMu.Unlock()
	s, err := loadDevVault()
	if err != nil {
		return nil, err
	}
	result := make([]*vault.Credential, 0, len(s.Credentials))
	for _, c := range s.Credentials {
		cp := *c
		// Strip access token for List per existing convention —
		// callers that need the full credential use Get explicitly.
		cp.AccessToken = ""
		result = append(result, &cp)
	}
	return result, nil
}

// ── Raw key/value operations (admin key, meta, _ca:*) ────────────────

func devVaultGetRaw(account string) (string, error) {
	devVaultMu.Lock()
	defer devVaultMu.Unlock()
	s, err := loadDevVault()
	if err != nil {
		return "", err
	}
	v, ok := s.Raw[account]
	if !ok {
		return "", fmt.Errorf("dev vault: not found %s", account)
	}
	return v, nil
}

func devVaultSetRaw(account, value string) error {
	devVaultMu.Lock()
	defer devVaultMu.Unlock()
	return withDevVaultWriteLock(func() error {
		s, err := loadDevVault()
		if err != nil {
			return err
		}
		s.Raw[account] = value
		return saveDevVault(s)
	})
}

func devVaultDeleteRaw(account string) error {
	devVaultMu.Lock()
	defer devVaultMu.Unlock()
	return withDevVaultWriteLock(func() error {
		s, err := loadDevVault()
		if err != nil {
			return err
		}
		delete(s.Raw, account)
		return saveDevVault(s)
	})
}
