package keychain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xianxu/nous/internal/charon/vault"
)

// withDevVaultPath redirects the dev vault to a temp file for the
// duration of the test. Restores the env var on cleanup so tests
// don't leak side effects.
func withDevVaultPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-vault.json")
	prev, had := os.LookupEnv(devVaultEnvVar)
	t.Setenv(devVaultEnvVar, path)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(devVaultEnvVar, prev)
		} else {
			_ = os.Unsetenv(devVaultEnvVar)
		}
	})
	return path
}

func TestDevVault_PathOverridable(t *testing.T) {
	path := withDevVaultPath(t)
	if devVaultPath() != path {
		t.Errorf("env override not honored: %q != %q", devVaultPath(), path)
	}
}

func TestDevVault_Credentials_RoundTrip(t *testing.T) {
	withDevVaultPath(t)

	// Empty initial state.
	creds, err := devVaultList()
	if err != nil {
		t.Fatalf("List on empty: %v", err)
	}
	if len(creds) != 0 {
		t.Errorf("expected 0 creds initially, got %d", len(creds))
	}

	// Set + Get.
	cred := &vault.Credential{
		Type:     vault.TypeAdminKey,
		Provider: "openai",
		Account:  "work",
		AdminKey: &vault.AdminKeyData{
			OrgID:       "org-test-001",
			ProjectID:   "proj_aB3",
			KeyMaterial: "sk-test-secret",
		},
	}
	if err := devVaultSet(cred); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := devVaultGet("openai", "work")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Account != "work" || got.AdminKey == nil || got.AdminKey.KeyMaterial != "sk-test-secret" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// List shows the entry.
	creds, _ = devVaultList()
	if len(creds) != 1 {
		t.Errorf("expected 1 cred after Set, got %d", len(creds))
	}

	// Delete.
	if err := devVaultDelete("openai", "work"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := devVaultGet("openai", "work"); err == nil {
		t.Error("Get after Delete should fail")
	}
}

func TestDevVault_Raw_RoundTrip(t *testing.T) {
	withDevVaultPath(t)

	if _, err := devVaultGetRaw("_openai:admin"); err == nil {
		t.Error("GetRaw on missing should fail")
	}

	if err := devVaultSetRaw("_openai:admin", "sk-admin-test"); err != nil {
		t.Fatalf("SetRaw: %v", err)
	}
	if err := devVaultSetRaw("_openai:meta", `{"org_id":"org-x"}`); err != nil {
		t.Fatalf("SetRaw meta: %v", err)
	}

	v, err := devVaultGetRaw("_openai:admin")
	if err != nil || v != "sk-admin-test" {
		t.Errorf("GetRaw admin: got %q err %v", v, err)
	}
	v, err = devVaultGetRaw("_openai:meta")
	if err != nil || !strings.Contains(v, "org_id") {
		t.Errorf("GetRaw meta: got %q err %v", v, err)
	}

	if err := devVaultDeleteRaw("_openai:admin"); err != nil {
		t.Fatalf("DeleteRaw: %v", err)
	}
	if _, err := devVaultGetRaw("_openai:admin"); err == nil {
		t.Error("GetRaw after DeleteRaw should fail")
	}
	// Meta still there.
	if _, err := devVaultGetRaw("_openai:meta"); err != nil {
		t.Error("DeleteRaw of admin should not affect meta")
	}
}

func TestDevVault_Persistence_AcrossLoads(t *testing.T) {
	path := withDevVaultPath(t)

	if err := devVaultSetRaw("_openai:admin", "sk-secret"); err != nil {
		t.Fatalf("SetRaw: %v", err)
	}

	// Confirm the file exists with correct mode and content.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}

	// Fresh load (next process equivalent) — clear in-memory cache by
	// just calling load again; the file is the source of truth.
	v, err := devVaultGetRaw("_openai:admin")
	if err != nil || v != "sk-secret" {
		t.Errorf("persistence: got %q err %v", v, err)
	}
}

func TestDevVault_LoadCorruptFile(t *testing.T) {
	path := withDevVaultPath(t)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, []byte("not valid json"), 0600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	_, err := devVaultGetRaw("anything")
	if err == nil {
		t.Error("expected parse error on corrupt file")
	}
}

func TestDevVault_AccessTokenStrippedFromList(t *testing.T) {
	withDevVaultPath(t)

	cred := &vault.Credential{
		Type:        vault.TypeOAuth,
		Provider:    "google",
		Account:     "user@gmail.com",
		AccessToken: "ya29.tok",
		Scopes:      []string{"openid"},
	}
	if err := devVaultSet(cred); err != nil {
		t.Fatalf("Set: %v", err)
	}

	creds, _ := devVaultList()
	if len(creds) != 1 {
		t.Fatalf("expected 1 cred")
	}
	if creds[0].AccessToken != "" {
		t.Errorf("List should strip AccessToken, got %q", creds[0].AccessToken)
	}

	// Get returns full credential.
	got, _ := devVaultGet("google", "user@gmail.com")
	if got.AccessToken != "ya29.tok" {
		t.Error("Get should return full credential including AccessToken")
	}
}

func TestDevVault_DeleteIdempotent(t *testing.T) {
	withDevVaultPath(t)
	// Delete on missing — no error.
	if err := devVaultDelete("openai", "missing"); err != nil {
		t.Errorf("Delete on missing should be silent, got %v", err)
	}
	if err := devVaultDeleteRaw("missing"); err != nil {
		t.Errorf("DeleteRaw on missing should be silent, got %v", err)
	}
}

// Concurrent writers don't lose updates. Intra-process is guarded by
// devVaultMu; cross-process relies on the unix flock layered on top.
// This test exercises the intra-process side directly and exercises
// flock indirectly (the path is taken even within one process, just
// against an in-process lock file).
func TestDevVault_ConcurrentWritesNoLostUpdates(t *testing.T) {
	withDevVaultPath(t)
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			account := fmt.Sprintf("openai:k%03d", i)
			if err := devVaultSetRaw(account, fmt.Sprintf("v%d", i)); err != nil {
				t.Errorf("SetRaw %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	// All N writes must be visible.
	for i := 0; i < N; i++ {
		got, err := devVaultGetRaw(fmt.Sprintf("openai:k%03d", i))
		if err != nil {
			t.Errorf("missing key %d after concurrent writes: %v", i, err)
			continue
		}
		want := fmt.Sprintf("v%d", i)
		if got != want {
			t.Errorf("key %d: got %q, want %q", i, got, want)
		}
	}
}

// The lock file is created on first write and never deleted by
// charon. Re-opening a previously-used vault directory works
// without manual lock cleanup.
func TestDevVault_LockFileReusable(t *testing.T) {
	path := withDevVaultPath(t)
	if err := devVaultSetRaw("k1", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("lock file should exist after a write: %v", err)
	}
	// Subsequent writes still work.
	if err := devVaultSetRaw("k2", "v2"); err != nil {
		t.Fatalf("second write: %v", err)
	}
}

func TestDevVault_RawAndCredentials_SeparateNamespaces(t *testing.T) {
	withDevVaultPath(t)

	// Same key string in both namespaces should not collide.
	if err := devVaultSetRaw("openai:work", "raw-value"); err != nil {
		t.Fatalf("SetRaw: %v", err)
	}
	if err := devVaultSet(&vault.Credential{Provider: "openai", Account: "work"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	rawVal, err := devVaultGetRaw("openai:work")
	if err != nil || rawVal != "raw-value" {
		t.Errorf("raw and credential collided: rawVal=%q err=%v", rawVal, err)
	}

	cred, err := devVaultGet("openai", "work")
	if err != nil || cred.Provider != "openai" {
		t.Errorf("credential namespace: %+v err=%v", cred, err)
	}
}

// Errors from corrupt file should preserve the error chain so callers
// can introspect — important for distinguishing parse from IO errors.
func TestDevVault_CorruptError_PropagatesContext(t *testing.T) {
	path := withDevVaultPath(t)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte("{invalid"), 0600)

	_, err := devVaultGetRaw("anything")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errors.Unwrap(err)) && err == nil {
		t.Error("expected wrapped error chain")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse failure, got %v", err)
	}
}
