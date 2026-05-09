package security

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
)

// SelfInfo describes the running nous-security binary — used for the
// pre-flight transparency block and for deciding whether the auto-revoke
// flow is safe to offer.
type SelfInfo struct {
	ExecPath   string // absolute path to the running executable
	BundleID   string // CFBundleIdentifier if running inside a .app, else ""
	BundlePath string // path to the .app directory if applicable
	SHA256     string // hex digest of the executable
	Version    string // VCS revision from build info, or "(devel)"
}

// LoadSelfInfo populates a SelfInfo for the current process. Errors
// reading the executable are surfaced; missing bundle metadata is
// reported as empty strings (running outside a .app is a supported,
// audit-degraded mode).
func LoadSelfInfo() (SelfInfo, error) {
	info := SelfInfo{Version: vcsRevision()}

	exe, err := os.Executable()
	if err != nil {
		return info, err
	}
	info.ExecPath = exe

	sum, err := sha256OfFile(exe)
	if err != nil {
		return info, err
	}
	info.SHA256 = sum

	info.BundlePath, info.BundleID = detectBundle(exe)
	return info, nil
}

func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// detectBundle walks up from the executable to find the enclosing
// `.app/Contents/MacOS/<binary>` layout. Returns ("", "") if not in a
// bundle.
func detectBundle(exe string) (bundlePath, bundleID string) {
	dir := filepath.Dir(exe) // .../Contents/MacOS
	if filepath.Base(dir) != "MacOS" {
		return "", ""
	}
	contents := filepath.Dir(dir)
	if filepath.Base(contents) != "Contents" {
		return "", ""
	}
	app := filepath.Dir(contents)
	if filepath.Ext(app) != ".app" {
		return "", ""
	}
	plist := filepath.Join(contents, "Info.plist")
	id, err := readBundleID(plist)
	if err != nil {
		return app, ""
	}
	return app, id
}

// readBundleID extracts CFBundleIdentifier from an Info.plist. We avoid
// pulling in a plist library for this one field — the regex is robust
// enough for plists we generate ourselves and aborts cleanly on
// unfamiliar shapes.
var bundleIDRe = regexp.MustCompile(
	`(?s)<key>CFBundleIdentifier</key>\s*<string>([^<]+)</string>`)

func readBundleID(plistPath string) (string, error) {
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return "", err
	}
	m := bundleIDRe.FindSubmatch(data)
	if m == nil {
		return "", os.ErrNotExist
	}
	return string(m[1]), nil
}

func vcsRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown)"
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return "(devel)"
}
