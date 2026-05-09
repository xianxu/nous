package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	label    = "com.charon.proxy"
	plistTpl = `<?xml version="1.0" encoding="UTF-8"?>
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
</dict>
</plist>
`
)

type launchdManager struct{}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "charon.log")
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

	path := plistPath()
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
	}{
		Label:   label,
		Binary:  absBinary,
		Args:    args,
		LogPath: logPath(),
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
	path := plistPath()
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("service not installed — run 'charon service install' first")
	}
	return exec.Command("launchctl", "start", label).Run()
}

func (l *launchdManager) Stop() error {
	return exec.Command("launchctl", "stop", label).Run()
}

func (l *launchdManager) Uninstall() error {
	path := plistPath()

	// Unload first (ignore error if not loaded).
	_ = exec.Command("launchctl", "unload", path).Run()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist: %w", err)
	}
	return nil
}

func (l *launchdManager) Status() (string, error) {
	out, err := exec.Command("launchctl", "list", label).Output()
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
	if strings.Contains(output, label) {
		return "installed (not running)", nil
	}
	return "installed", nil
}
