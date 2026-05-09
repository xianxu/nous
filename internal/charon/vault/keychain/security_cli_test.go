//go:build !darwin || !cgo

package keychain

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// These tests verify that the macOS `security` CLI supports the subcommands
// and flags that the CLI fallback in keychain.go depends on. No keychain
// data is read or written. They run on macOS only (skipped on non-darwin
// fallback hosts), and only when the CLI fallback is the active backend
// (build tag !darwin || !cgo); the primary darwin+cgo backend doesn't use
// the security CLI so these contract checks aren't relevant there.

func skipIfNotMacOS(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("skipping: macOS-only test")
	}
}

func TestSecurityCLIExists(t *testing.T) {
	skipIfNotMacOS(t)
	path, err := exec.LookPath("security")
	if err != nil {
		t.Fatalf("security CLI not found: %v", err)
	}
	if path == "" {
		t.Fatal("security CLI path is empty")
	}
}

func TestSecurityHelpContainsSubcommands(t *testing.T) {
	skipIfNotMacOS(t)

	// `security help` (no args) prints the list of subcommands to stderr.
	out, _ := exec.Command("security", "help").CombinedOutput()
	help := string(out)

	required := []string{
		"find-generic-password",
		"add-generic-password",
		"delete-generic-password",
		"dump-keychain",
	}
	for _, cmd := range required {
		if !strings.Contains(help, cmd) {
			t.Errorf("security help missing subcommand %q", cmd)
		}
	}
}

func TestSecurityFindGenericPasswordFlags(t *testing.T) {
	skipIfNotMacOS(t)

	// Run with -h to get usage. security uses exit code 1 for help.
	out, _ := exec.Command("security", "find-generic-password", "-h").CombinedOutput()
	usage := string(out)

	// We use: -s (service), -a (account), -w (print password only)
	for _, flag := range []string{"-s", "-a", "-w"} {
		if !strings.Contains(usage, flag) {
			t.Errorf("find-generic-password usage missing flag %q\nusage: %s", flag, usage)
		}
	}
}

func TestSecurityAddGenericPasswordFlags(t *testing.T) {
	skipIfNotMacOS(t)

	out, _ := exec.Command("security", "add-generic-password", "-h").CombinedOutput()
	usage := string(out)

	// We use: -s (service), -a (account), -w (password data), -U (update if exists)
	for _, flag := range []string{"-s", "-a", "-w", "-U"} {
		if !strings.Contains(usage, flag) {
			t.Errorf("add-generic-password usage missing flag %q\nusage: %s", flag, usage)
		}
	}
}

func TestSecurityDeleteGenericPasswordFlags(t *testing.T) {
	skipIfNotMacOS(t)

	out, _ := exec.Command("security", "delete-generic-password", "-h").CombinedOutput()
	usage := string(out)

	// We use: -s (service), -a (account)
	for _, flag := range []string{"-s", "-a"} {
		if !strings.Contains(usage, flag) {
			t.Errorf("delete-generic-password usage missing flag %q\nusage: %s", flag, usage)
		}
	}
}
