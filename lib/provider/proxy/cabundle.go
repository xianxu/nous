package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BuildCABundle creates a combined PEM file with system root CAs + Charon CA
// in a temp directory. Returns the path to the bundle file.
// The bundle is ephemeral — regenerated each time the proxy starts.
func BuildCABundle(charonCAPEM []byte) (bundlePath string, cleanup func(), err error) {
	// Use a fixed temp dir so `charon run` reuses it instead of accumulating dirs.
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("charon-ca-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(dir) }

	// Write charon CA cert (needed for NODE_EXTRA_CA_CERTS which is additive).
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, charonCAPEM, 0644); err != nil {
		cleanup()
		return "", nil, err
	}

	// Build combined bundle.
	bundlePath = filepath.Join(dir, "ca-bundle.pem")

	systemPEM, sysErr := loadSystemCAs()
	if sysErr != nil {
		systemPEM = nil
	}

	var bundle []byte
	if len(systemPEM) > 0 {
		bundle = append(bundle, systemPEM...)
		if !strings.HasSuffix(string(systemPEM), "\n") {
			bundle = append(bundle, '\n')
		}
	}
	bundle = append(bundle, charonCAPEM...)

	if err := os.WriteFile(bundlePath, bundle, 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return bundlePath, cleanup, nil
}

// CAPathFromBundle returns the ca.pem path in the same directory as the bundle.
func CAPathFromBundle(bundlePath string) string {
	return filepath.Join(filepath.Dir(bundlePath), "ca.pem")
}

func loadSystemCAs() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("security", "find-certificate", "-a", "-p",
			"/System/Library/Keychains/SystemRootCertificates.keychain").Output()
	default:
		for _, path := range []string{
			"/etc/ssl/certs/ca-certificates.crt",
			"/etc/pki/tls/certs/ca-bundle.crt",
			"/etc/ssl/ca-bundle.pem",
		} {
			if data, err := os.ReadFile(path); err == nil {
				return data, nil
			}
		}
		return nil, os.ErrNotExist
	}
}
