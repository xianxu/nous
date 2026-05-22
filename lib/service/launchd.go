package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

const plistTpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
{{- range .Args}}
        <string>{{.}}</string>
{{- end}}
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
{{- if .EnvKeys}}
    <key>EnvironmentVariables</key>
    <dict>
{{- range .EnvKeys}}
        <key>{{.}}</key>
        <string>{{index $.Env .}}</string>
{{- end}}
    </dict>
{{- end}}
</dict>
</plist>
`

// launchdManager handles one launchd service identified by Label.
// Pre-nous#14 the package was charon-only and hardcoded
// "com.charon.proxy"; nous#16 M4 parameterizes it so the same code
// path serves both the legacy two-service install (charon proxy +
// brain-sync as separate plists) and the unified `nous serve` plist
// (com.xianxu.nous, M5).
type launchdManager struct {
	Label   string
	LogName string            // basename in ~/Library/Logs/
	Env     map[string]string // EnvironmentVariables block; empty/nil → omit
}

func (l *launchdManager) plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", l.Label+".plist")
}

func (l *launchdManager) logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", l.LogName)
}

func (l *launchdManager) Install(binary string, args []string) error {
	// Resolve to absolute path.
	absBinary, err := filepath.Abs(binary)
	if err != nil {
		return fmt.Errorf("failed to resolve binary path: %w", err)
	}

	// Render plist.
	tmpl, err := template.New("plist").Parse(plistTpl)
	if err != nil {
		return err
	}

	path := l.plistPath()
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create plist at %s: %w", path, err)
	}
	defer f.Close()

	// Sorted env keys so the rendered plist is deterministic across
	// runs (helpful for diffing the plist + cache-friendliness).
	envKeys := make([]string, 0, len(l.Env))
	for k := range l.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	data := struct {
		Label   string
		Binary  string
		Args    []string
		LogPath string
		Env     map[string]string
		EnvKeys []string
	}{
		Label:   l.Label,
		Binary:  absBinary,
		Args:    args,
		LogPath: l.logPath(),
		Env:     l.Env,
		EnvKeys: envKeys,
	}
	if err := tmpl.Execute(f, data); err != nil {
		return err
	}

	// Load the service.
	if err := exec.Command("launchctl", "load", path).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
	}

	// Force-start the agent. On modern macOS (Big Sur+) `launchctl
	// load` doesn't reliably honor RunAtLoad for user-level
	// LaunchAgents — the job stays in "speculative" state and
	// never actually spawns until something triggers it (login,
	// manual `start`, etc.). Empirically observed via `launchctl
	// print` showing `runs = 0, pended nondemand spawn =
	// speculative, last exit code = (never exited)` after a clean
	// install. Explicit kickstart bypasses that throttle.
	//
	// kickstart errors are non-fatal: the load succeeded, so the
	// plist is in place; worst case the operator triggers a start
	// manually. We surface the error if any so the install output
	// flags the partial success.
	uid := os.Getuid()
	target := fmt.Sprintf("gui/%d/%s", uid, l.Label)
	if err := exec.Command("launchctl", "kickstart", "-k", target).Run(); err != nil {
		return fmt.Errorf("launchctl kickstart %s: %w (plist loaded, but daemon didn't start)", target, err)
	}

	return nil
}

func (l *launchdManager) Start() error {
	path := l.plistPath()
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("service %s not installed — run install first", l.Label)
	}
	return exec.Command("launchctl", "start", l.Label).Run()
}

func (l *launchdManager) Stop() error {
	return exec.Command("launchctl", "stop", l.Label).Run()
}

func (l *launchdManager) Uninstall() error {
	path := l.plistPath()

	// Unload first (ignore error if not loaded).
	_ = exec.Command("launchctl", "unload", path).Run()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist: %w", err)
	}
	return nil
}

func (l *launchdManager) Status() (string, error) {
	out, err := exec.Command("launchctl", "list", l.Label).Output()
	if err != nil {
		return "not installed", nil
	}

	output := string(out)
	// Parse PID from launchctl list output.
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "PID") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				// launchctl list emits a plist-ish dict: `"PID" = 1234;`.
				// Strip whitespace plus the trailing `;` (and any quotes
				// for value-side quoting, though PID is unquoted in
				// practice) so the displayed status is a bare integer.
				pid := strings.Trim(strings.TrimSpace(parts[1]), `;" `)
				if pid != "0" && pid != "" {
					return fmt.Sprintf("running (PID %s)", pid), nil
				}
			}
		}
	}

	// Check if it's in the list at all.
	if strings.Contains(output, l.Label) {
		return "installed (not running)", nil
	}
	return "installed", nil
}
