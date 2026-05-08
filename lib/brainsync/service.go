package brainsync

import (
	"fmt"
	"runtime"
)

// ServiceManager handles install/uninstall/start/stop/status for brain-sync
// as an OS-native service. Platform-specific implementations behind build tags.
type ServiceManager interface {
	Install(binary string, args []string) error
	Uninstall() error
	Start() error
	Stop() error
	Status() (string, error)
}

// NewServiceManager returns the service manager for the current OS.
func NewServiceManager() (ServiceManager, error) {
	switch runtime.GOOS {
	case "darwin":
		return &launchdServiceManager{}, nil
	default:
		return nil, fmt.Errorf("service mgmt not supported on %s yet", runtime.GOOS)
	}
}
