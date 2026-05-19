package brainsync

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Rescan-signal touch-file plumbing.
//
// `nous brain` cobra commands touch this file in a PostRunE hook so
// the running `nous serve` daemon's auto-discovery loop (when in
// auto mode) can react sub-second instead of waiting up to 60s for
// the next periodic rescan. The signal flow is intentionally one-way
// and file-backed:
//
//   write side (any `nous brain` invocation)   read side (serve)
//   ──────────────────────────────────────     ─────────────────
//   TouchRescanSignal()                        fsnotify on the path
//        ↓                                          ↑
//        └── os.Chtimes / create-empty ─────────────┘
//
// File-backed because:
//   - Survives serve restarts (next serve picks up where the last
//     left off; no PID handshake needed).
//   - Operator can `touch ~/Library/Caches/nous/brainsync-rescan`
//     manually to force a rescan — useful if a manual .brain/config.md
//     edit needs to be picked up before the 60s tick.
//   - No socket protocol additions, no SIGUSR1 wiring.

// RescanSignalPath returns the absolute path of the rescan-signal
// touch-file. Lives under the user's cache directory (XDG_CACHE_HOME
// on Linux, ~/Library/Caches on macOS).
func RescanSignalPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	return filepath.Join(cache, "nous", "brainsync-rescan"), nil
}

// TouchRescanSignal updates the mtime of the rescan-signal file
// (creating it if absent), waking any serve daemon watching it via
// fsnotify. Idempotent; safe to call concurrently. Errors only on
// genuinely-broken filesystems — callers typically log + ignore so
// a transient I/O blip doesn't block the operator's `nous brain`
// command.
func TouchRescanSignal() error {
	path, err := RescanSignalPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	// Create-if-missing, then refresh mtime to trigger a fsnotify
	// WRITE event even if the file already existed.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	_ = f.Close()
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		return fmt.Errorf("chtimes %s: %w", path, err)
	}
	return nil
}

// EnsureRescanSignal makes sure the signal file exists so fsnotify
// has something to watch. No-op if it already exists. Called by
// runWithAutoDiscovery during setup before adding the fsnotify watch.
func EnsureRescanSignal() (string, error) {
	path, err := RescanSignalPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	_ = f.Close()
	return path, nil
}
