package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
{{- if .EnvPath}}
    <key>EnvironmentVariables</key>
    <dict>
        <!-- launchd's default PATH lacks /opt/homebrew/bin; without it
             git can't find git-remote-gcrypt, gnupg, etc. Needed by the
             unified nous serve (which runs brainsync as a goroutine)
             and by brain-sync standalone. -->
        <key>PATH</key>
        <string>{{.EnvPath}}</string>
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
	LogName string // basename in ~/Library/Logs/
	EnvPath string // optional PATH override; empty → omit EnvironmentVariables block
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

	data := struct {
		Label   string
		Binary  string
		Args    []string
		LogPath string
		EnvPath string
	}{
		Label:   l.Label,
		Binary:  absBinary,
		Args:    args,
		LogPath: l.logPath(),
		EnvPath: l.EnvPath,
	}
	if err := tmpl.Execute(f, data); err != nil {
		return err
	}

	// Load the service.
	if err := exec.Command("launchctl", "load", path).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
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
				pid := strings.TrimSpace(parts[1])
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
