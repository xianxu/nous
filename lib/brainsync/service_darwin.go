//go:build darwin

package brainsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	serviceLabel = "com.xianxu.brain-sync"
	plistTpl     = `<?xml version="1.0" encoding="UTF-8"?>
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
    <key>EnvironmentVariables</key>
    <dict>
        <!-- launchd's default PATH lacks /opt/homebrew/bin; without it
             git can't find git-remote-gcrypt, gnupg, etc. -->
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
        <key>HOME</key>
        <string>{{.Home}}</string>
    </dict>
</dict>
</plist>
`
)

type launchdServiceManager struct{}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
}

func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "brain-sync.log")
}

func (m *launchdServiceManager) Install(binary string, args []string) error {
	tpl, err := template.New("plist").Parse(plistTpl)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath()), 0o755); err != nil {
		return err
	}
	f, err := os.Create(plistPath())
	if err != nil {
		return err
	}
	defer f.Close()
	home, _ := os.UserHomeDir()
	return tpl.Execute(f, struct {
		Label, Binary, LogPath, Home string
		Args                         []string
	}{serviceLabel, binary, logPath(), home, args})
}

func (m *launchdServiceManager) Uninstall() error {
	_ = m.Stop()
	if err := os.Remove(plistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *launchdServiceManager) Start() error {
	_, err := exec.Command("launchctl", "load", plistPath()).CombinedOutput()
	return err
}

func (m *launchdServiceManager) Stop() error {
	_, _ = exec.Command("launchctl", "unload", plistPath()).CombinedOutput()
	return nil
}

func (m *launchdServiceManager) Status() (string, error) {
	if _, err := os.Stat(plistPath()); os.IsNotExist(err) {
		return "not installed", nil
	}
	out, err := exec.Command("launchctl", "list", serviceLabel).CombinedOutput()
	if err != nil {
		// launchctl exits non-zero when the service isn't loaded; treat as
		// "installed but stopped" rather than an error.
		return "installed (not running)", nil
	}
	return strings.TrimSpace(string(out)), nil
}
