// Package service manages OS-level service registration for charon.
// Platform-specific implementations handle launchd (macOS), systemd (Linux), etc.
package service

import (
	"fmt"
	"runtime"
)

// Manager handles service install/uninstall/start/stop/status for the current OS.
type Manager interface {
	Install(binary string, args []string) error
	Uninstall() error
	Start() error
	Stop() error
	Status() (string, error) // returns human-readable status
}

// New returns the service manager for the current OS.
func New() (Manager, error) {
	switch runtime.GOOS {
	case "darwin":
		return &launchdManager{}, nil
	default:
		return nil, fmt.Errorf("service management not yet supported on %s", runtime.GOOS)
	}
}
