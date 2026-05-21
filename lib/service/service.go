// Package service manages OS-level service registration for nous-substrate
// daemons. Platform-specific implementations handle launchd (macOS),
// systemd (Linux), etc.
//
// History: nous-era inherits the charon-era one-manager-per-package
// model but parameterizes the launchd Label so the same code path
// installs the legacy `charon serve` plist (com.charon.proxy) and the
// unified `nous serve` plist (com.xianxu.nous, nous#16 M4+).
package service

import (
	"fmt"
	"runtime"
)

// Manager handles service install/uninstall/start/stop/status for one
// launchd label on the current OS.
type Manager interface {
	Install(binary string, args []string) error
	Uninstall() error
	Start() error
	Stop() error
	Status() (string, error) // returns human-readable status
}

// New returns the legacy charon-proxy service manager. Kept for
// backward compatibility with cmd/nous/service.go's existing call
// sites; new callers should prefer NewLabeled or one of the named
// constructors (NewUnified) that documents the label at the call
// site.
func New() (Manager, error) {
	return NewLabeled("com.charon.proxy", "charon.log", nil)
}

// NewLabeled returns a launchd manager keyed by `label` (full
// reverse-DNS form, e.g. "com.xianxu.nous"). logName is the basename
// in ~/Library/Logs/ (e.g. "nous.log"). env is the
// EnvironmentVariables block emitted into the plist — nil/empty
// omits the block entirely. Use the named constructors below in
// most call sites; this is the escape hatch for unusual setups.
func NewLabeled(label, logName string, env map[string]string) (Manager, error) {
	switch runtime.GOOS {
	case "darwin":
		return &launchdManager{Label: label, LogName: logName, Env: env}, nil
	default:
		return nil, fmt.Errorf("service management not yet supported on %s", runtime.GOOS)
	}
}

// NewUnified returns the manager for the unified `nous serve` daemon
// (one process running both proxy + brain-sync goroutines). Label:
// com.42shots.nous (matching the 42shots-product reverse-DNS
// namespace). Env:
//   - PATH includes the Homebrew prefix so the brain-sync goroutine
//     can find git-remote-gcrypt, gpg, etc.
//   - WORKSPACE_ROOT is set by the install command (cmd/nous/
//     service.go::runServiceInstall) per-operator. Not set here
//     because the resolution needs to happen at install time, in
//     the operator's interactive context (where ~/repo symlinks
//     resolve correctly). See nous workspace.Root + the symlink
//     handling for context.
func NewUnified() (Manager, error) {
	// PATH mirrors lib/brainsync/service_darwin.go's launchd template
	// — operators on Apple Silicon need /opt/homebrew/bin/, Intel Macs
	// need /usr/local/bin/, both standard for direct interactive
	// shells but absent from launchd's default minimal PATH.
	return NewLabeled(
		"com.42shots.nous",
		"nous.log",
		map[string]string{
			"PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		},
	)
}

// NewUnifiedWithEnv is like NewUnified but accepts additional env
// vars (merged on top of the defaults). Used by service install to
// persist the operator's resolved WORKSPACE_ROOT.
func NewUnifiedWithEnv(extra map[string]string) (Manager, error) {
	mgr, err := NewUnified()
	if err != nil {
		return nil, err
	}
	if lm, ok := mgr.(*launchdManager); ok {
		for k, v := range extra {
			lm.Env[k] = v
		}
	}
	return mgr, nil
}
