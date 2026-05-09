package security

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// PreflightOptions toggles which lines the transparency block prints.
// True => the corresponding action is on this run's plan.
type PreflightOptions struct {
	WillReadTCC      bool // M4
	WillCheckCharon  bool // M5
	WillPromptRevoke bool // M6
}

// PrintPreflight prints the transparency block describing what the tool
// will and will not do. Always run before any privileged action.
func PrintPreflight(w io.Writer, self SelfInfo, opts PreflightOptions) {
	fmt.Fprintf(w, "Charon Security Audit  rev=%s\n", self.Version)
	fmt.Fprintf(w, "  binary: %s\n", self.ExecPath)
	fmt.Fprintf(w, "  sha256: %s\n", self.SHA256)
	if self.BundleID != "" {
		fmt.Fprintf(w, "  bundle: %s (%s)\n", self.BundleID, self.BundlePath)
	} else {
		fmt.Fprintf(w, "  bundle: (none — running outside .app; auto-revoke disabled)\n")
	}
	fmt.Fprintf(w, "  source: cmd/charon-security/, internal/security/\n\n")

	fmt.Fprintf(w, "This tool will:\n")
	fmt.Fprintf(w, "  - Run `csrutil status` and `sudo -nv` (read-only)\n")
	fmt.Fprintf(w, "  - Enumerate ~/Library/LaunchAgents, /Library/LaunchAgents,\n")
	fmt.Fprintf(w, "    /Library/LaunchDaemons (read-only)\n")
	fmt.Fprintf(w, "  - Detect installed terminals/IDEs via the filesystem and Spotlight\n")
	fmt.Fprintf(w, "  - Run `codesign -d --entitlements -` on detected terminals (read-only)\n")
	if opts.WillReadTCC {
		fmt.Fprintf(w, "  - Read /Library/Application Support/com.apple.TCC/TCC.db (requires FDA)\n")
		fmt.Fprintf(w, "  - Read ~/Library/Application Support/com.apple.TCC/TCC.db (requires FDA)\n")
	}
	if opts.WillCheckCharon {
		fmt.Fprintf(w, "  - Inspect charon's keychain entries' ACLs (read-only, may prompt once)\n")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "This tool will NOT:\n")
	fmt.Fprintf(w, "  - Modify any TCC grants without explicit confirmation\n")
	fmt.Fprintf(w, "  - Make any network requests\n")
	fmt.Fprintf(w, "  - Persist data outside this run\n")
	if opts.WillPromptRevoke && self.BundleID != "" {
		fmt.Fprintf(w, "\nAt the end, you'll be prompted to revoke this tool's Full Disk Access\n")
		fmt.Fprintf(w, "(via `tccutil reset SystemPolicyAllFiles %s`).\n", self.BundleID)
	}
	fmt.Fprintln(w)
}

// ConfirmDefaultDeny reads from /dev/tty (so piping doesn't auto-yes) and
// returns true only when the user types y/yes (case-insensitive).
//
// Falls back to stdin if /dev/tty isn't available (CI, redirected
// streams) — in that case the strict "must be y/yes" rule still applies.
func ConfirmDefaultDeny(prompt string) bool {
	return readConfirm(prompt+" [y/N]: ", false)
}

// ConfirmDefaultYes is the inverse: blank/Enter means yes; only an
// explicit n/no returns false.
func ConfirmDefaultYes(prompt string) bool {
	return readConfirm(prompt+" [Y/n]: ", true)
}

func readConfirm(prompt string, defaultYes bool) bool {
	src, closer, isTTY := openTTY()
	defer closer()
	// Without an interactive source we can't ask — return the default so
	// non-interactive runs follow the safe path the caller picked.
	if !isTTY {
		return defaultYes
	}

	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(src).ReadString('\n')
	if err != nil && line == "" {
		// Trailing EOF (pipe closed mid-prompt). Print a newline to keep
		// subsequent output on its own line, then fall back to default.
		fmt.Fprintln(os.Stderr)
		return defaultYes
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "":
		return defaultYes
	default:
		return false
	}
}

// openTTY tries /dev/tty first so pipelines (`make security | tee log`)
// still let the user respond. The third return signals whether we found
// an interactive source; when false the caller should not block on input.
func openTTY() (io.Reader, func(), bool) {
	if tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0); err == nil {
		return tty, func() { _ = tty.Close() }, true
	}
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		return os.Stdin, func() {}, true
	}
	return os.Stdin, func() {}, false
}

// IsInteractive reports whether confirm prompts will actually read from
// a human. Callers use this to skip interactive walks (visual-mode TCC
// audit, etc.) when running under pipes, CI, or `< /dev/null`.
func IsInteractive() bool {
	_, closer, ok := openTTY()
	closer()
	return ok
}
