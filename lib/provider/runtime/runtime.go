// Package runtime tracks the ephemeral state of a running charon
// proxy on disk so other CLI invocations can discover where it's
// listening without requiring `--addr` on every command.
//
// `charon serve` writes a small JSON file at startup with the
// resolved listen address; other commands (`manifest`, `status`,
// `run`, `gcp setup`, …) read it as a fallback when the user
// hasn't explicitly passed `--addr`. The proxy clears the file on
// graceful shutdown; stale files from crashes are tolerated — the
// `proxy.running` healthz probe in `charon manifest` will report
// `false`, and the next `charon serve` will overwrite.
//
// Dev/prod isolation: the file path is namespaced by
// keychain.ResolveServiceName so an unsigned dev binary and a
// signed prod binary keep their own runtime files. They can't
// share a port anyway (binding fails), so each namespace tracks
// its own proxy independently.
package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xianxu/nous/lib/provider/vault/keychain"
)

// Info is the on-disk shape. PID and StartedAt are diagnostic only —
// callers shouldn't depend on them. Addr is the load-bearing field.
type Info struct {
	Addr      string    `json:"addr"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// Path returns the runtime file path for the current binary's
// namespace. Override-able via CHARON_RUNTIME_PATH for tests.
func Path() string {
	if p := os.Getenv("CHARON_RUNTIME_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to /tmp; runtime tracking is best-effort.
		return filepath.Join("/tmp", "charon-runtime.json")
	}
	dir := filepath.Join(home, ".local", "share", "charon")
	name := "runtime.json"
	if keychain.ResolveServiceName() == keychain.ServiceDev {
		name = "runtime-dev.json"
	}
	return filepath.Join(dir, name)
}

// Write persists the runtime info atomically (tempfile + rename).
// Creates the parent directory on first write; mode 0600 since the
// file's content reveals where charon is listening.
func Write(addr string) error {
	info := Info{
		Addr:      addr,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("runtime marshal: %w", err)
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("runtime mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("runtime write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("runtime rename: %w", err)
	}
	return nil
}

// Read returns the persisted runtime info, or (nil, nil) when the
// file doesn't exist (no proxy running per this binary's namespace).
// Other read errors (corrupt JSON, permission denied) are returned
// to the caller.
func Read() (*Info, error) {
	data, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runtime read: %w", err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("runtime parse: %w", err)
	}
	return &info, nil
}

// Remove deletes the runtime file. Idempotent — missing file is not
// an error. Called by `charon serve` on graceful shutdown.
func Remove() error {
	err := os.Remove(Path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("runtime remove: %w", err)
	}
	return nil
}
