package identity

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// redirectConfig points $XDG_CONFIG_HOME at a tempdir and (on macOS,
// where os.UserConfigDir reads $HOME/Library/Application Support) also
// overrides $HOME so PrimaryStatePath lands inside the tempdir. Returns
// the tempdir for assertions.
func redirectConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Linux path: os.UserConfigDir honors $XDG_CONFIG_HOME.
	t.Setenv("XDG_CONFIG_HOME", dir)
	// macOS path: $HOME/Library/Application Support — we override HOME
	// so the state file lands in the tempdir instead of the real one.
	t.Setenv("HOME", dir)
	return dir
}

func TestPrimary_ErrorWhenNoStateAndZeroSecretKeys(t *testing.T) {
	redirectConfig(t)
	homedir := shortTempHome(t)
	t.Setenv("GNUPGHOME", homedir)
	if err := os.Chmod(homedir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Primary()
	if !errors.Is(err, ErrPrimaryUnset) {
		t.Fatalf("want ErrPrimaryUnset, got %v", err)
	}
}

func TestPrimary_OneSecretKeyIsImplicitPrimary(t *testing.T) {
	redirectConfig(t)
	_, fp := setupGPGHome(t)
	got, err := Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if !strings.EqualFold(got.Fingerprint, fp) {
		t.Errorf("Primary fp = %s, want %s", got.Fingerprint, fp)
	}
}

func TestSetPrimary_PersistsAndReadsBack(t *testing.T) {
	cfgDir := redirectConfig(t)
	_, fp := setupGPGHome(t)

	if err := SetPrimary(fp); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}
	statePath := filepath.Join(cfgDir, "nous", "primary-identity")
	// Try both potential locations (Linux: $XDG_CONFIG_HOME/nous/...;
	// macOS: $HOME/Library/Application Support/nous/...). Either may
	// have been used depending on os.UserConfigDir's behavior.
	if _, err := os.Stat(statePath); err != nil {
		statePath = filepath.Join(cfgDir, "Library", "Application Support", "nous", "primary-identity")
		if _, err := os.Stat(statePath); err != nil {
			t.Fatalf("state file not written under %s", cfgDir)
		}
	}

	got, err := Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if !strings.EqualFold(got.Fingerprint, fp) {
		t.Errorf("Primary fp = %s, want %s", got.Fingerprint, fp)
	}
}

func TestSetPrimary_RefusesShortFingerprint(t *testing.T) {
	redirectConfig(t)
	setupGPGHome(t)
	err := SetPrimary("DEADBEEF")
	if err == nil || !strings.Contains(err.Error(), "40-char") {
		t.Errorf("want 40-char error, got %v", err)
	}
}

func TestSetPrimary_RefusesUnknownFingerprint(t *testing.T) {
	redirectConfig(t)
	setupGPGHome(t)
	err := SetPrimary("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err == nil || !strings.Contains(err.Error(), "no secret key") {
		t.Errorf("want no-secret-key error, got %v", err)
	}
}

func TestPrimary_StaleStateReturnsStale(t *testing.T) {
	redirectConfig(t)
	_, fp := setupGPGHome(t)

	if err := SetPrimary(fp); err != nil {
		t.Fatal(err)
	}
	// Delete the key from the keyring; state file remains.
	homedir := os.Getenv("GNUPGHOME")
	if err := os.RemoveAll(filepath.Join(homedir, "private-keys-v1.d")); err != nil {
		t.Fatal(err)
	}
	// gpg caches; restart agent so the secret-key disappearance is observed.
	// (shortTempHome's cleanup will kill the agent; for the live test we
	// just call List which re-invokes gpg.)
	_, err := Primary()
	if !errors.Is(err, ErrPrimaryStale) {
		t.Errorf("want ErrPrimaryStale, got %v", err)
	}
}

func TestIsPrimary_FalseWhenUnset(t *testing.T) {
	redirectConfig(t)
	homedir := shortTempHome(t)
	t.Setenv("GNUPGHOME", homedir)
	if err := os.Chmod(homedir, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := IsPrimary("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if got {
		t.Errorf("want false (unset), got true")
	}
}

func TestClearPrimary_RemovesStateFile(t *testing.T) {
	redirectConfig(t)
	_, fp := setupGPGHome(t)
	if err := SetPrimary(fp); err != nil {
		t.Fatal(err)
	}
	if err := ClearPrimary(); err != nil {
		t.Fatalf("ClearPrimary: %v", err)
	}
	if err := ClearPrimary(); err != nil {
		t.Errorf("idempotent clear failed: %v", err)
	}
}
